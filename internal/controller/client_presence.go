package controller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const ClientHeader = "X-Go-Bot-Client"
const clientLease = 90 * time.Second
const clientHeartbeat = 30 * time.Second
const clientsField = "clients"

type ClientIdentity struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Platform string `json:"platform,omitempty"`
	Model    string `json:"model,omitempty"`
}
type ClientDevice struct {
	ClientIdentity
	Status     string `json:"status"`
	LastSeenAt int64  `json:"lastSeenAt"`
}
type clientRecord struct {
	ClientIdentity
	LastSeenAt  int64            `json:"lastSeenAt"`
	Connections map[string]int64 `json:"connections"`
}
type ClientUpdate struct {
	Identity     ClientIdentity
	ConnectionID string
	Now          int64
	Connected    bool
}

var clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,80}$`)

func clientIdentity(r *http.Request) (*ClientIdentity, error) {
	raw := r.Header.Get(ClientHeader)
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 2048 {
		return nil, errors.New("invalid_client_identity")
	}
	data, err := url.PathUnescape(raw)
	if err != nil {
		return nil, errors.New("invalid_client_identity")
	}
	var identity ClientIdentity
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&identity) != nil || decoder.Decode(new(any)) != io.EOF || !clientIDPattern.MatchString(identity.ID) ||
		(identity.Type != "web" && identity.Type != "mobile" && identity.Type != "desktop") ||
		len(identity.Platform) > 32 || len(identity.Model) > 120 ||
		strings.IndexFunc(identity.Platform+identity.Model, unicode.IsControl) >= 0 {
		return nil, errors.New("invalid_client_identity")
	}
	if identity.Type == "web" {
		identity.Platform = ""
		identity.Model = ""
	}
	return &identity, nil
}

func pruneClientConnections(records map[string]clientRecord, now int64) {
	for id, record := range records {
		for connection, expires := range record.Connections {
			if expires <= now {
				delete(record.Connections, connection)
			}
		}
		records[id] = record
	}
}
func updateClientRecords(records map[string]clientRecord, u ClientUpdate) error {
	pruneClientConnections(records, u.Now)
	record, exists := records[u.Identity.ID]
	if !exists && !u.Connected {
		return nil
	}
	if !exists && len(records) >= 50 {
		oldest := ""
		for id, r := range records {
			if len(r.Connections) == 0 && (oldest == "" || r.LastSeenAt < records[oldest].LastSeenAt) {
				oldest = id
			}
		}
		if oldest == "" {
			return errors.New("client_presence_limit")
		}
		delete(records, oldest)
	}
	if u.Connected {
		record.ClientIdentity = u.Identity
		if record.Connections == nil {
			record.Connections = map[string]int64{}
		}
		record.Connections[u.ConnectionID] = u.Now + clientLease.Milliseconds()
	} else {
		delete(record.Connections, u.ConnectionID)
	}
	record.LastSeenAt = max(record.LastSeenAt, u.Now)
	records[u.Identity.ID] = record
	return nil
}
func publicClients(records map[string]clientRecord, now int64) []ClientDevice {
	result := make([]ClientDevice, 0, len(records))
	for _, record := range records {
		status := "disconnected"
		for _, expires := range record.Connections {
			if expires > now {
				status = "connected"
				break
			}
		}
		result = append(result, ClientDevice{ClientIdentity: record.ClientIdentity, Status: status, LastSeenAt: record.LastSeenAt})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Status != result[j].Status {
			return result[i].Status == "connected"
		}
		if result[i].LastSeenAt != result[j].LastSeenAt {
			return result[i].LastSeenAt > result[j].LastSeenAt
		}
		return result[i].ID < result[j].ID
	})
	return result
}
func (s *RedisStore) UpdateClientPresence(ctx context.Context, id string, u ClientUpdate) error {
	key := accountPrefix + id
	return s.watch(ctx, []string{key}, func(tx *redis.Tx) error {
		fields, err := tx.HMGet(ctx, key, "desiredState", clientsField).Result()
		if err != nil {
			return err
		}
		if fields[0] == nil {
			return ErrAccountNotFound
		}
		records := map[string]clientRecord{}
		if fields[1] != nil && json.Unmarshal([]byte(redisString(fields[1])), &records) != nil {
			return errors.New("invalid Redis clients")
		}
		if records == nil {
			records = map[string]clientRecord{}
		}
		if err := updateClientRecords(records, u); err != nil {
			return err
		}
		data, err := json.Marshal(records)
		if err != nil {
			return err
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error { pipe.HSet(ctx, key, clientsField, data); return nil })
		return err
	})
}
func (s *RedisStore) ClientDevices(ctx context.Context, id string, now int64) ([]ClientDevice, error) {
	fields, err := s.client.HMGet(ctx, accountPrefix+id, "desiredState", clientsField).Result()
	if err != nil {
		return nil, err
	}
	if fields[0] == nil {
		return nil, ErrAccountNotFound
	}
	records := map[string]clientRecord{}
	if fields[1] != nil && json.Unmarshal([]byte(redisString(fields[1])), &records) != nil {
		return nil, errors.New("invalid Redis clients")
	}
	return publicClients(records, now), nil
}

// Each accepted stream owns only its renewal loop. Redis leases survive an
// unclean Controller exit; no global scanner or in-memory device registry exists.
func (m *Manager) trackClient(ctx context.Context, user string, identity ClientIdentity) func() {
	lease := uuid.NewString()
	update := func(ctx context.Context, connected bool) error {
		call, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		return m.Store.UpdateClientPresence(call, user, ClientUpdate{Identity: identity, ConnectionID: lease, Now: time.Now().UnixMilli(), Connected: connected})
	}
	if update(ctx, true) != nil {
		return func() {}
	}
	renew, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(clientHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-renew.Done():
				return
			case <-ticker.C:
				_ = update(renew, true)
			}
		}
	}()
	return func() { cancel(); <-done; _ = update(context.Background(), false) }
}
func (m *Manager) clientsHTTP(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		httpError(w, 405, "method_not_allowed")
		return
	}
	devices, err := m.Store.ClientDevices(r.Context(), id, time.Now().UnixMilli())
	if errors.Is(err, ErrAccountNotFound) {
		httpError(w, 404, "account not found")
		return
	}
	if err != nil {
		httpError(w, 503, "clients_unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"clients": devices})
}
