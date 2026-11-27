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

const firecrawlFixture = `{"success":true,"data":{"web":[{"url":"javascript:invalid"},{"url":"https://example.com/report","title":"Fixture","description":"Relevant source text","position":1,"markdown":"Do not forward page content"},{"url":"https://example.com/overflow"}]},"creditsUsed":2}`

func checkFirecrawlRequest(t *testing.T, r *http.Request, count int) {
	t.Helper()
	var body map[string]any
	if r.Method != http.MethodPost || r.URL.Path != "/v2/search" || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&body) != nil {
		t.Error("unexpected Firecrawl search wire request")
		return
	}
	sources, _ := body["sources"].([]any)
	if r.Header.Get("Authorization") != "Bearer fixture-secret" || body["query"] != "fixture query" || body["limit"] != float64(count) || len(sources) != 1 || sources[0] != "web" || body["origin"] != "go-bot" || len(body) != 4 {
		t.Errorf("incorrect Firecrawl search request: %+v", body)
	}
	if strings.Contains(string(mustJSON(t, body)), "fixture-secret") {
		t.Error("key was put into the body")
	}
}

func TestFirecrawlSearchRequestAndToolProjection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkFirecrawlRequest(t, r, 1)
		io.WriteString(w, firecrawlFixture)
	}))
	defer server.Close()
	provider, err := New("firecrawl", web.Options{BaseURL: server.URL + "/v2/search", APIKey: "fixture-secret"})
	if err != nil || provider.Name() != "firecrawl" {
		t.Fatalf("construct Firecrawl search provider: %v", err)
	}
	result, err := provider.Search(t.Context(), web.SearchRequest{Query: " fixture\nquery ", MaxResults: 1})
	if err != nil || len(result.Sources) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	source := result.Sources[0]
	if source.URL != "https://example.com/report" || source.Title == nil || *source.Title != "Fixture" || source.Snippet == nil || *source.Snippet != "Relevant source text" || source.Summary != nil || source.PublishedAt != nil || result.Content != nil {
		t.Fatalf("source normalization lost data: %+v", source)
	}

	tool := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, firecrawlFixture) }))
	defer tool.Close()
	p, _ := New("firecrawl", web.Options{BaseURL: tool.URL, APIKey: "fixture-secret"})
	text, _, err := (tools.WebSearch{Searcher: p}).ExecutePresented(t.Context(), json.RawMessage(`{"queries":["fixture query"]}`))
	if err != nil || !strings.Contains(text, "https://example.com/report") || !strings.Contains(text, "Relevant source text") || !strings.Contains(text, "untrusted") || strings.Contains(text, "Do not forward") {
		t.Fatalf("unexpected tool projection: %s (%v)", text, err)
	}
}

func TestFirecrawlSearchLimitsErrorsAndEmptyResults(t *testing.T) {
	for _, tc := range []struct {
		status      int
		reply, code string
	}{
		{200, `{"success":true,"data":{"web":[]}}`, ""}, {200, `{"success":true,"data":{}}`, ""},
		{200, `{}`, "WEB_PROVIDER_FAILED"}, {200, `{"data":`, "WEB_PROVIDER_FAILED"},
		{401, `{"error":"fixture-secret"}`, "WEB_PROVIDER_AUTH"}, {403, `{"error":"fixture-secret"}`, "WEB_PROVIDER_AUTH"},
		{402, `{"error":"Insufficient credits"}`, "WEB_PROVIDER_QUOTA"}, {429, `{}`, "WEB_PROVIDER_RATE_LIMIT"},
		{408, `{}`, "WEB_PROVIDER_FAILED"}, {500, `{}`, "WEB_PROVIDER_FAILED"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			checkFirecrawlRequest(t, r, 50)
			w.WriteHeader(tc.status)
			io.WriteString(w, tc.reply)
		}))
		p, err := New("firecrawl", web.Options{BaseURL: server.URL + "/v2/search", APIKey: "fixture-secret"})
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

func TestFirecrawlSearchValidationCancellationRedirectAndSizeLimit(t *testing.T) {
	for _, key := range []string{"", " bad", "bad\nkey", strings.Repeat("x", 4097)} {
		if _, err := New("firecrawl", web.Options{APIKey: key}); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
	var calls atomic.Int32
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer empty.Close()
	p, _ := New("firecrawl", web.Options{BaseURL: empty.URL, APIKey: "fixture-secret"})
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
	p, _ = New("firecrawl", web.Options{BaseURL: redirect.URL, APIKey: "fixture-secret"})
	if _, err := p.Search(t.Context(), web.SearchRequest{Query: "fixture"}); err == nil || followed.Load() != 0 {
		t.Fatal("credential redirect was followed")
	}

	for _, size := range []int{4 << 20, (4 << 20) + 1} {
		prefix, suffix := `{"data":{"web":[{"url":"https://example.com/large","description":"`, `"}]}}`
		reply := prefix + strings.Repeat("x", size-len(prefix)-len(suffix)) + suffix
		large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, reply) }))
		p, _ = New("firecrawl", web.Options{BaseURL: large.URL, APIKey: "fixture-secret"})
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
	p, _ = New("firecrawl", web.Options{BaseURL: slow.URL, APIKey: "fixture-secret"})
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

func isWebError(err error, code string) bool {
	failure, ok := err.(*web.Error)
	return ok && failure.Code == code
}
