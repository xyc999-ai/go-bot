package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"go-bot/internal/config"
)

func (s *memoryStore) UpdateClientPresence(_ context.Context, id string, u ClientUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[id]; !ok {
		return ErrAccountNotFound
	}
	if s.clients == nil {
		s.clients = map[string]map[string]clientRecord{}
	}
	if s.clients[id] == nil {
		s.clients[id] = map[string]clientRecord{}
	}
	return updateClientRecords(s.clients[id], u)
}
func (s *memoryStore) ClientDevices(_ context.Context, id string, now int64) ([]ClientDevice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[id]; !ok {
		return nil, ErrAccountNotFound
	}
	return publicClients(s.clients[id], now), nil
}
func clientHeader(info ClientIdentity) string {
	data, _ := json.Marshal(info)
	return url.PathEscape(string(data))
}
func TestClientIdentityValidationAndWebPrivacy(t *testing.T) {
	request := httptest.NewRequest("GET", "/v1/host/stream", nil)
	request.Header.Set(ClientHeader, clientHeader(ClientIdentity{ID: "browser-fixture", Type: "web", Platform: "macOS", Model: "Hidden model"}))
	info, err := clientIdentity(request)
	if err != nil || info.Model != "" || info.Platform != "" {
		t.Fatal(info, err)
	}
	for _, raw := range []string{`{"id":"valid-client","type":"web"} {}`, `{`, `{"id":"a","type":"web"}`, `{"id":"valid-client","type":"unknown"}`, `{"id":"valid-client","type":"mobile","model":"bad\nname"}`, `{"id":"valid-client","type":"web","unknown":true}`} {
		request.Header.Set(ClientHeader, url.PathEscape(raw))
		if _, err := clientIdentity(request); err == nil {
			t.Fatal("accepted invalid identity", raw)
		}
	}
}
func TestClientLeasesKeepOtherTabsOnlineAndExpireWithoutAScanner(t *testing.T) {
	records := map[string]clientRecord{}
	info := ClientIdentity{ID: "web-fixture", Type: "web"}
	update := func(id string, now int64, connected bool) {
		t.Helper()
		if err := updateClientRecords(records, ClientUpdate{Identity: info, ConnectionID: id, Now: now, Connected: connected}); err != nil {
			t.Fatal(err)
		}
	}
	update("tab-one", 1000, true)
	update("tab-two", 1100, true)
	update("tab-one", 1200, false)
	if publicClients(records, 1200)[0].Status != "connected" {
		t.Fatal("closing one tab disconnected the browser")
	}
	// A delayed close for the preceding connection cannot remove its reconnect.
	update("reconnected", 1300, true)
	update("tab-two", 1400, false)
	if publicClients(records, 1400)[0].Status != "connected" {
		t.Fatal("old close removed reconnect")
	}
	if publicClients(records, 1300+clientLease.Milliseconds())[0].Status != "disconnected" {
		t.Fatal("stale lease survived")
	}
	update("reconnected", 100000, true)
	update("reconnected", 100001, false)
	if d := publicClients(records, 100001)[0]; d.Status != "disconnected" || d.LastSeenAt != 100001 {
		t.Fatal(d)
	}
}
func TestClientPresenceTracksOnlySuccessfulStreamsAndKeepsChatProjection(t *testing.T) {
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(ClientHeader) != "" {
			t.Error("client metadata was forwarded to Host")
		}
		if r.URL.Query().Get("fail") == "true" {
			http.Error(w, "unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: host\ndata: {\"type\":\"sessions.changed\",\"data\":{}}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer host.Close()
	store := &memoryStore{records: map[string]Account{"alice": {DesiredState: running, HostID: "host-fixture", HostEndpoint: host.URL, HostToken: "fixture"}, "bob": {DesiredState: stopped}}}
	manager := New(config.Default(), nil, store)
	server := httptest.NewServer(manager.Handler())
	defer server.Close()
	open := func(path, id string) *http.Response {
		t.Helper()
		req, _ := http.NewRequestWithContext(t.Context(), "GET", server.URL+path, nil)
		req.Header.Set(UserHeader, "alice")
		req.Header.Set(ClientHeader, clientHeader(ClientIdentity{ID: id, Type: "web"}))
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	failed := open("/v1/host/stream?fail=true", "failed-browser")
	failed.Body.Close()
	if devices, _ := store.ClientDevices(t.Context(), "alice", time.Now().UnixMilli()); len(devices) != 0 {
		t.Fatal("failed stream registered", devices)
	}
	one := open("/v1/host/stream", "browser-one")
	defer one.Body.Close()
	two := open("/v1/host/stream", "browser-one")
	defer two.Body.Close()
	if one.StatusCode != 200 || two.StatusCode != 200 {
		t.Fatal("stream failed")
	}
	devices, _ := store.ClientDevices(t.Context(), "alice", time.Now().UnixMilli())
	if len(devices) != 1 || devices[0].Status != "connected" {
		t.Fatal(devices)
	}
	if other, _ := store.ClientDevices(t.Context(), "bob", time.Now().UnixMilli()); len(other) != 0 {
		t.Fatal("cross-account presence leak")
	}
	one.Body.Close()
	deadline := time.Now().Add(time.Second)
	for {
		store.mu.Lock()
		count := len(store.clients["alice"]["browser-one"].Connections)
		store.mu.Unlock()
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stream close did not release its lease")
		}
		time.Sleep(10 * time.Millisecond)
	}
	devices, _ = store.ClientDevices(t.Context(), "alice", time.Now().UnixMilli())
	if devices[0].Status != "connected" {
		t.Fatal(devices)
	}
	two.Body.Close()
	deadline = time.Now().Add(time.Second)
	for {
		devices, _ = store.ClientDevices(t.Context(), "alice", time.Now().UnixMilli())
		if devices[0].Status == "disconnected" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("last stream close did not disconnect")
		}
		time.Sleep(10 * time.Millisecond)
	}
	req := httptest.NewRequest("GET", "/v1/clients", nil)
	req.Header.Set(UserHeader, "alice")
	out := httptest.NewRecorder()
	manager.Handler().ServeHTTP(out, req)
	var view struct{ Clients []ClientDevice }
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &view) != nil || len(view.Clients) != 1 {
		t.Fatal(out.Code, out.Body)
	}
}
