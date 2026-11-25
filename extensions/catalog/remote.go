package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Remote is a bounded, process-local cache of public release metadata. It
// owns no user settings and does not run a background refresh loop.
type Remote struct {
	URL     string
	Client  *http.Client
	TTL     time.Duration
	mu      sync.Mutex
	entries []Entry
	etag    string
	checked time.Time
}

func NewRemote(url string) *Remote {
	return &Remote{URL: url, TTL: 5 * time.Minute, Client: &http.Client{Timeout: 10 * time.Second}}
}
func cloneEntries(entries []Entry) []Entry {
	if entries == nil {
		return nil
	}
	raw, _ := json.Marshal(entries)
	var copy []Entry
	_ = json.Unmarshal(raw, &copy)
	return copy
}

// Entries returns the last valid snapshot on a transient failure. A first
// failed fetch is an error, not an empty market. Invalid content never replaces
// a known-good catalog, including when a caller explicitly requests refresh.
func (s *Remote) Entries(ctx context.Context, force bool) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !force && s.entries != nil && time.Since(s.checked) < s.TTL {
		return cloneEntries(s.entries), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err == nil {
		request.Header.Set("Accept", "application/json")
		if force {
			request.Header.Set("Cache-Control", "no-cache")
		}
		if s.etag != "" {
			request.Header.Set("If-None-Match", s.etag)
		}
		var response *http.Response
		response, err = s.Client.Do(request)
		if err == nil {
			defer response.Body.Close()
			switch {
			case response.StatusCode == http.StatusNotModified && s.entries != nil:
				s.checked = time.Now()
				return cloneEntries(s.entries), nil
			case response.StatusCode == http.StatusOK:
				var data []byte
				data, err = io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
				if err == nil && len(data) > 4<<20 {
					err = errors.New("plugin catalog exceeds 4 MiB")
				}
				var entries []Entry
				if err == nil {
					entries, err = Parse(data)
				}
				if err == nil {
					s.entries, s.etag, s.checked = entries, response.Header.Get("ETag"), time.Now()
					return cloneEntries(entries), nil
				}
			default:
				err = fmt.Errorf("plugin catalog returned HTTP %d", response.StatusCode)
			}
		}
	}
	if s.entries != nil {
		s.checked = time.Now()
		return cloneEntries(s.entries), nil
	}
	return nil, fmt.Errorf("plugin_catalog_unavailable: %w", err)
}
