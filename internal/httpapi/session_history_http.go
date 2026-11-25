package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"sync"

	"go-bot/harness/session"
	"go-bot/internal/hostruntime"
)

type publicEventPage struct {
	Events     []sessionformat.Event `json:"events"`
	NextCursor *uint64               `json:"nextCursor"`
}

func eventPageParams(r *http.Request) (after *uint64, limit int, code string) {
	if values, present := r.URL.Query()["after"]; present {
		value, err := strconv.ParseUint(values[0], 10, 64)
		if err != nil {
			return nil, 0, "invalid_event_cursor"
		}
		after = &value
	}
	limit = 200
	if values, present := r.URL.Query()["limit"]; present {
		value, err := strconv.Atoi(values[0])
		if err != nil || value < 1 || value > 500 {
			return nil, 0, "invalid_event_limit"
		}
		limit = value
	}
	return after, limit, ""
}

func publicEventPageFrom(events []sessionformat.Event) publicEventPage {
	page := publicEventPage{Events: make([]sessionformat.Event, 0, len(events))}
	if len(events) != 0 {
		last := events[len(events)-1].Seq
		page.NextCursor = &last
	}
	for _, event := range events {
		if projected, ok := publicNativeEvent(event); ok {
			page.Events = append(page.Events, projected)
		}
	}
	return page
}

const maxCachedHistoryBytes = 8 << 20

// Retain at most one validated log between pages. Appends and file
// replacements invalidate the read through the file identity and version.
type coldHistoryCache struct {
	mu       sync.Mutex
	id       string
	version  os.FileInfo
	artifact sessionformat.PhysicalArtifact
}

func sameHistoryVersion(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) &&
		a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func (c *coldHistoryCache) load(ctx context.Context, host *hostruntime.NativeHost, id string) (sessionformat.PhysicalArtifact, error) {
	if host.SessionLogInfo == nil {
		return host.SessionArtifact(ctx, id)
	}
	version, err := host.SessionLogInfo(id)
	if err != nil {
		return sessionformat.PhysicalArtifact{}, err
	}
	if version.Size() > maxCachedHistoryBytes {
		return host.SessionArtifact(ctx, id)
	}
	c.mu.Lock()
	if c.id == id && sameHistoryVersion(c.version, version) {
		artifact := c.artifact
		c.mu.Unlock()
		return artifact, nil
	}
	c.mu.Unlock()
	artifact, err := host.SessionArtifact(ctx, id)
	if err != nil {
		return sessionformat.PhysicalArtifact{}, err
	}
	if current, err := host.SessionLogInfo(id); err == nil && sameHistoryVersion(version, current) {
		c.mu.Lock()
		c.id, c.version, c.artifact = id, current, artifact
		c.mu.Unlock()
	}
	return artifact, nil
}

func (c *coldHistoryCache) invalidate(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.id == id {
		c.id, c.version, c.artifact = "", nil, sessionformat.PhysicalArtifact{}
	}
}

func serveColdSessionHistory(w http.ResponseWriter, r *http.Request, host *hostruntime.NativeHost, cache *coldHistoryCache, id string) {
	after, limit, code := eventPageParams(r)
	if code != "" {
		writeSessionJSON(w, http.StatusBadRequest, map[string]string{"error": code})
		return
	}

	if host.ReadHistory != nil {
		value, err := host.SessionHistoryWindow(r.Context(), id, sessionformat.HistoryRequest{Kind: sessionformat.HistoryEvents, After: after, Limit: limit})
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			writeSessionJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent_events_unavailable"})
			return
		}
		writeSessionJSON(w, http.StatusOK, publicEventPageFrom(value.Events))
		return
	}
	if host.ReadSession == nil {
		http.NotFound(w, r)
		return
	}
	artifact, err := cache.load(r.Context(), host, id)
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeSessionJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent_events_unavailable"})
		return
	}

	events := artifact.Events
	if after != nil {
		if *after >= uint64(len(events)) {
			events = nil
		} else {
			events = events[*after+1:]
		}
	}
	writeSessionJSON(w, http.StatusOK, publicEventPageFrom(events[:min(len(events), limit)]))
}

func writeSessionJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
