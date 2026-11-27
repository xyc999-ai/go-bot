package webproviders

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go-bot/harness/tools"
	"go-bot/web"
)

const keenableFixture = `{"results":[{"url":"javascript:invalid","title":"Bad"},{"title":"Fixture","url":"https://example.com/report","description":"","snippet":"Relevant page text\nfrom the source"},{"url":"https://example.com/overflow","snippet":"x"}]}`

func checkKeenableRequest(t *testing.T, r *http.Request, path, key string, count int) {
	t.Helper()
	var body map[string]any
	if r.Method != http.MethodPost || r.URL.Path != path || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&body) != nil {
		t.Error("unexpected Keenable search wire request")
		return
	}
	if r.Header.Get("X-Keenable-Title") != "Go Bot" || r.Header.Get("X-API-Key") != key || r.Header.Get("Authorization") != "" {
		t.Errorf("incorrect Keenable headers: title=%q key=%q", r.Header.Get("X-Keenable-Title"), r.Header.Get("X-API-Key"))
	}
	if body["query"] != "fixture query" || body["max_results"] != float64(count) || len(body) != 2 {
		t.Errorf("incorrect Keenable search request: %+v", body)
	}
	if key != "" && strings.Contains(string(mustJSON(t, body)), key) {
		t.Error("key was put into the body")
	}
}

func TestKeenableKeylessSearchRequestAndToolProjection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkKeenableRequest(t, r, "/v1/search/public", "", 1)
		io.WriteString(w, keenableFixture)
	}))
	defer server.Close()
	provider, err := New("keenable", web.Options{BaseURL: server.URL + "/v1/search/public"})
	if err != nil || provider.Name() != "keenable" {
		t.Fatalf("construct keyless Keenable search provider: %v", err)
	}
	result, err := provider.Search(t.Context(), web.SearchRequest{Query: " fixture\nquery ", MaxResults: 1})
	if err != nil || len(result.Sources) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	source := result.Sources[0]
	if source.URL != "https://example.com/report" || source.Title == nil || *source.Title != "Fixture" || source.Snippet != nil || source.Summary == nil || *source.Summary != "Relevant page text\nfrom the source" || source.PublishedAt != nil || result.Content != nil {
		t.Fatalf("source normalization lost data: %+v", source)
	}

	tool := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, keenableFixture) }))
	defer tool.Close()
	p, _ := New("keenable", web.Options{BaseURL: tool.URL})
	text, _, err := (tools.WebSearch{Searcher: p}).ExecutePresented(t.Context(), json.RawMessage(`{"queries":["fixture query"]}`))
	if err != nil || !strings.Contains(text, "https://example.com/report") || !strings.Contains(text, "Relevant page text from the source") || !strings.Contains(text, "untrusted") {
		t.Fatalf("unexpected tool projection: %s (%v)", text, err)
	}
}

func TestKeenableKeyedSearchSendsKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkKeenableRequest(t, r, "/v1/search", "fixture-secret", 10)
		io.WriteString(w, `{"results":[]}`)
	}))
	defer server.Close()
	p, err := New("keenable", web.Options{BaseURL: server.URL + "/v1/search", APIKey: "fixture-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := p.Search(t.Context(), web.SearchRequest{Query: "fixture query"}); err != nil || len(result.Sources) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestKeenableSearchLimitsErrorsAndEmptyResults(t *testing.T) {
	for _, tc := range []struct {
		status      int
		reply, code string
	}{
		{200, `{"results":[]}`, ""},
		{200, `{}`, "WEB_PROVIDER_FAILED"}, {200, `{"results":`, "WEB_PROVIDER_FAILED"},
		{400, `{"detail":"Missing app identifier"}`, "WEB_PROVIDER_FAILED"},
		{401, `{"detail":"fixture-secret"}`, "WEB_PROVIDER_AUTH"}, {403, `{}`, "WEB_PROVIDER_AUTH"},
		{429, `{"detail":"Rate limit exceeded"}`, "WEB_PROVIDER_RATE_LIMIT"},
		{500, `{}`, "WEB_PROVIDER_FAILED"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			checkKeenableRequest(t, r, "/v1/search", "fixture-secret", 50)
			w.WriteHeader(tc.status)
			io.WriteString(w, tc.reply)
		}))
		p, err := New("keenable", web.Options{BaseURL: server.URL + "/v1/search", APIKey: "fixture-secret"})
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		result, err := p.Search(t.Context(), web.SearchRequest{Query: "fixture query", MaxResults: 100})
		server.Close()
		if tc.code == "" {
			if err != nil || len(result.Sources) != 0 {
				t.Fatalf("empty response=%+v err=%v", result, err)
			}
			continue
		}
		failure, ok := err.(*web.Error)
		if !ok || failure.Code != tc.code || strings.Contains(err.Error(), "fixture-secret") {
			t.Fatalf("HTTP %d: error=%v", tc.status, err)
		}
	}
}

func TestKeenableSearchValidationCancellationRedirectAndSizeLimit(t *testing.T) {
	for _, key := range []string{" bad", "bad\nkey", strings.Repeat("x", 4097)} {
		if _, err := New("keenable", web.Options{APIKey: key}); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
	if _, err := New("keenable", web.Options{}); err != nil {
		t.Fatalf("empty key must select the keyless endpoint: %v", err)
	}
	var calls atomic.Int32
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer empty.Close()
	p, _ := New("keenable", web.Options{BaseURL: empty.URL})
	if _, err := p.Search(t.Context(), web.SearchRequest{Query: " \n "}); calls.Load() != 0 || !isWebError(err, "WEB_INVALID_QUERY") {
		t.Fatalf("blank query reached upstream: %v", err)
	}

	var followed atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	p, _ = New("keenable", web.Options{BaseURL: redirect.URL, APIKey: "fixture-secret"})
	if _, err := p.Search(t.Context(), web.SearchRequest{Query: "fixture"}); err == nil || followed.Load() != 0 {
		t.Fatal("credential redirect was followed")
	}

	for _, size := range []int{4 << 20, (4 << 20) + 1} {
		prefix, suffix := `{"results":[{"url":"https://example.com/large","snippet":"`, `"}]}`
		reply := prefix + strings.Repeat("x", size-len(prefix)-len(suffix)) + suffix
		large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, reply) }))
		p, _ = New("keenable", web.Options{BaseURL: large.URL})
		result, err := p.Search(t.Context(), web.SearchRequest{Query: "fixture"})
		large.Close()
		if size > 4<<20 {
			if !isWebError(err, "WEB_PROVIDER_FAILED") {
				t.Fatalf("oversized response accepted: %v", err)
			}
		} else if err != nil || len(result.Sources) != 1 {
			t.Fatalf("response at the size limit rejected: %v", err)
		}
	}

	started := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer slow.Close()
	p, _ = New("keenable", web.Options{BaseURL: slow.URL})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := p.Search(ctx, web.SearchRequest{Query: "fixture"}); result <- err }()
	<-started
	cancel()
	select {
	case err := <-result:
		if failure, ok := err.(*web.Error); !ok || failure.Code != "WEB_ABORTED" {
			t.Fatalf("cancellation=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not cancel")
	}
}

func TestKeenableIsTheOnlyKeyOptionalProvider(t *testing.T) {
	for _, descriptor := range Catalog() {
		if want := descriptor.ID == "keenable"; descriptor.KeyOptional != want || KeyOptional(descriptor.ID) != want {
			t.Errorf("%s: keyOptional=%v, want %v", descriptor.ID, descriptor.KeyOptional, want)
		}
	}
	if KeyOptional("unknown") || Default == "keenable" {
		t.Fatal("unknown provider is key-optional or Keenable became the default")
	}
}
