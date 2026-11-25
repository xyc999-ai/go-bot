package integration

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"go-bot/harness/sessiontitle"
)

// Auxiliary naming must not consume a deterministic fixture's next Agent
// response or enter its tool-call assertions. The actual title service is
// exercised by its own lifecycle, adapter, HTTP and projection tests.
func fixtureTitleResponse(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil {
		return false
	}
	body, err := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		http.Error(w, "could not read fixture request", 400)
		return true
	}
	prefix, _, _ := strings.Cut(sessiontitle.System, "\n")
	if !bytes.Contains(body, []byte(prefix)) {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	if strings.HasSuffix(r.URL.Path, "/messages") {
		_, _ = io.WriteString(w, `{"id":"fixture-title","type":"message","role":"assistant","content":[{"type":"text","text":"Test conversation"}],"stop_reason":"end_turn"}`)
	} else {
		_, _ = io.WriteString(w, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Test conversation"}]}]}`)
	}
	return true
}

func TestFixtureTitleResponsePreservesAgentRequestBody(t *testing.T) {
	body := `{"input":[{"role":"user","content":"hello"}]}`
	r, _ := http.NewRequest("POST", "http://fixture/responses", strings.NewReader(body))
	if fixtureTitleResponse(nil, r) {
		t.Fatal("ordinary request was handled as a title")
	}
	got, _ := io.ReadAll(r.Body)
	if string(got) != body {
		t.Fatal("fixture consumed the Agent request body")
	}
}
