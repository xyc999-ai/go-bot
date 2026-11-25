package catalog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRemoteRefreshPreservesSelectedReleasesAndLastValidSnapshot(t *testing.T) {
	var version atomic.Int32
	version.Store(1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := version.Load()
		if state == 3 {
			http.Error(w, "offline", 503)
			return
		}
		if state == 4 {
			_, _ = w.Write([]byte(`invalid`))
			return
		}
		etag := `"first"`
		entries := []Entry{reviewed()}
		if state == 2 {
			next := reviewed()
			next.Version = "2.0.0"
			next.Source.Spec = "@fixture/mail@2.0.0"
			next.AssetBase = "https://example.com/mail-v2.0.0/"
			entries = append(entries, next)
			etag = `"second"`
		}
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", etag)
		_ = json.NewEncoder(w).Encode(entries)
	}))
	defer server.Close()
	remote := NewRemote(server.URL)
	first, err := remote.Entries(t.Context(), false)
	if err != nil || len(first) != 1 {
		t.Fatal(first, err)
	}
	first[0].Name = "mutated by caller"
	current, err := remote.Entries(t.Context(), true)
	if err != nil || current[0].Name != "Mail" {
		t.Fatal("304 or detached snapshot failed", current, err)
	}
	version.Store(2)
	current, err = remote.Entries(t.Context(), true)
	if err != nil || len(current) != 2 || Latest(current)[0].Version != "2.0.0" {
		t.Fatal("new release not discovered", current, err)
	}
	for _, failure := range []int32{3, 4} {
		version.Store(failure)
		current, err = remote.Entries(t.Context(), true)
		if err != nil || len(current) != 2 {
			t.Fatal("lost known good market", err)
		}
	}
	empty := NewRemote(server.URL)
	if _, err := empty.Entries(t.Context(), true); err == nil {
		t.Fatal("first fetch failure became an empty market")
	}
}
