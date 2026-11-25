package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"go-bot/harness/interaction"
	"go-bot/internal/config"
)

func TestDockerHostStreamMultiplexesNewSessionsAndApprovalReconnect(t *testing.T) {
	if os.Getenv("RUN_DOCKER_INTEGRATION") != "1" {
		t.Skip("requires the built Host image")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 70*time.Second)
	defer cancel()
	model := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fixtureTitleResponse(w, r) {
			return
		}
		body, _ := io.ReadAll(r.Body)
		output := []map[string]any{{"type": "message", "role": "assistant", "content": []map[string]string{{"type": "output_text", "text": "Completed"}}}}
		if !strings.Contains(string(body), "function_call_output") {
			if strings.Contains(string(body), "approval-fixture") {
				output = []map[string]any{webCall("approval-call", "shellexecute", `{"command":"printf approved-once","sandbox_permissions":"danger-full-access","justification":"Verify one-shot execution approval"}`)}
			} else {
				output = []map[string]any{webCall("question-call", "ask_user_question", `{"questions":[{"id":"choice","question":"Choose?","options":[{"label":"A"},{"label":"B"}]}],"timeout":1}`)}
			}
		}
		payload, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"id": "reply", "status": "completed", "output": output}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", payload)
	}))
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	model.Listener = listener
	model.Start()
	defer model.Close()
	c := dockerFixtureConfig()
	c.Model.Main = config.MainModel{APIKey: "fixture", BaseURL: fmt.Sprintf("http://host.docker.internal:%d", listener.Addr().(*net.TCPAddr).Port), Name: "fixture"}
	m := newFixtureManager(t, c)
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if err := m.Delete(cleanup); err != nil {
			t.Error(err)
		}
	}()
	if err := m.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	call := func(method, path, body string, want int) []byte {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != want {
			t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, data)
		}
		return data
	}
	type event struct {
		SessionID string          `json:"sessionId"`
		Type      string          `json:"type"`
		Data      json.RawMessage `json:"data"`
	}
	connect := func() (<-chan event, func()) {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/host/stream", nil)
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			t.Fatalf("Host stream status %d", resp.StatusCode)
		}
		messages := make(chan event, 256)
		go func() {
			defer close(messages)
			scanner := bufio.NewScanner(resp.Body)
			scanner.Buffer(make([]byte, 4096), 2<<20)
			for scanner.Scan() {
				if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
					var e event
					if json.Unmarshal([]byte(data), &e) == nil {
						select {
						case messages <- e:
						case <-ctx.Done():
							return
						}
					}
				}
			}
		}()
		return messages, func() { resp.Body.Close() }
	}
	wait := func(events <-chan event, id, kind string, match func(json.RawMessage) bool) event {
		t.Helper()
		for {
			select {
			case e, ok := <-events:
				if !ok {
					t.Fatal("stream ended before expected event")
				}
				if e.SessionID == id && e.Type == kind && (match == nil || match(e.Data)) {
					return e
				}
			case <-ctx.Done():
				t.Fatalf("waiting for %s %s: %v", id, kind, ctx.Err())
			}
		}
	}
	newSession := func() string {
		var result struct{ SessionID string }
		if err := json.Unmarshal(call("POST", "/v1/sessions", "", 201), &result); err != nil {
			t.Fatal(err)
		}
		return result.SessionID
	}
	pending := func(data json.RawMessage) bool {
		var v struct{ Questions []interaction.Pending }
		_ = json.Unmarshal(data, &v)
		return len(v.Questions) > 0
	}
	// No interactive Host client: a new approval must fail closed rather than
	// silently grant access or wait forever for a client that is not present.
	offline := newSession()
	offlinePath := "/v1/sessions/" + offline
	call("POST", offlinePath+"/permissions", `{"mode":"read-only"}`, 200)
	call("POST", offlinePath+"/messages", `{"content":"approval-fixture"}`, 202)
	for {
		data := call("GET", offlinePath+"/events?limit=200", "", 200)
		if strings.Contains(string(data), `"type":"turn/end"`) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(30 * time.Millisecond):
		}
	}
	if journal := string(m.Journal(t, ctx, offline)); !strings.Contains(journal, `"outcome":"unavailable"`) {
		t.Fatal("offline approval did not report unavailable")
	}
	events, closeStream := connect()
	wait(events, "", "sessions.changed", nil)
	first := newSession()
	path := "/v1/sessions/" + first
	call("POST", path+"/permissions", `{"mode":"read-only"}`, 200)
	call("POST", path+"/messages", `{"content":"approval-fixture"}`, 202)
	e := wait(events, first, "questions.state", pending)
	var state struct{ Questions []interaction.Pending }
	_ = json.Unmarshal(e.Data, &state)
	if state.Questions[0].Approval == nil {
		t.Fatal("no approval payload")
	}
	approvalID := state.Questions[0].CallID
	closeStream()
	// The same pending identity must survive reconnect, with no Session stream.
	events, closeStream = connect()
	defer closeStream()
	e = wait(events, first, "questions.state", pending)
	_ = json.Unmarshal(e.Data, &state)
	if state.Questions[0].CallID != approvalID {
		t.Fatal("reconnect replaced the pending request")
	}
	call("POST", path+"/questions/"+approvalID+"/answer", `{"answers":[{"id":"approval","selected":["Allow once"]}]}`, 204)
	wait(events, first, "session", func(data json.RawMessage) bool { return strings.Contains(string(data), `"type":"turn/end"`) })
	second := newSession()
	call("POST", "/v1/sessions/"+second+"/messages", `{"content":"question-fixture"}`, 202)
	wait(events, second, "questions.state", pending)
	wait(events, second, "questions.state", func(data json.RawMessage) bool { return strings.Contains(string(data), `"state":"continued"`) })
	// Both Sessions used one stream; a newly activated Session was discovered.
	journal := string(m.Journal(t, ctx, first))
	if !strings.Contains(journal, "allowed-once") || !strings.Contains(journal, "approved-once") {
		t.Fatal("approved call did not execute")
	}
}
