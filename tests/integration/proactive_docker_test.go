package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sessionformat "go-bot/harness/session"
	"go-bot/internal/config"
	"go-bot/internal/proactive"
)

func TestDockerProactiveQuietTimedNotifyAndRecovery(t *testing.T) {
	if os.Getenv("RUN_DOCKER_INTEGRATION") != "1" {
		t.Skip("requires built Host image and Docker")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 70*time.Second)
	defer cancel()
	var calls atomic.Int64
	release := make(chan struct{})
	model := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fixtureTitleResponse(w, r) {
			return
		}
		var input struct{ Tools []struct{ Name string } }
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			w.WriteHeader(400)
			return
		}
		background := false
		for _, tool := range input.Tools {
			background = background || tool.Name == "structured_output"
		}
		var output []any
		if background {
			count := calls.Add(1)
			next := time.Now().Add(24 * time.Hour)
			var notify *proactive.Notification
			if count == 1 {
				next = time.Now().Add(3 * time.Second)
			} else {
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				notify = &proactive.Notification{Message: "A verified proactive update is ready for the user.", Evidence: []proactive.Evidence{}}
			}
			args, _ := json.Marshal(proactive.Result{WorkSummary: "Checked available context.", NextCheck: proactive.Check{At: next.Format(time.RFC3339Nano), Reason: "Revisit the expected information."}, Notify: notify})
			output = []any{map[string]any{"type": "function_call", "id": fmt.Sprint("out-", count), "call_id": fmt.Sprint("call-", count), "name": "structured_output", "arguments": string(args)}}
		} else {
			output = []any{map[string]any{"type": "message", "id": "reply", "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": "A verified proactive update."}}}}
		}
		data, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"id": "response", "status": "completed", "output": output}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", data)
	}))
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	model.Listener = listener
	model.Start()
	defer model.Close()
	c := dockerFixtureConfig()
	c.Model.Main = config.MainModel{Provider: "custom", Name: "proactive-fixture", Protocol: "responses", BaseURL: fmt.Sprintf("http://host.docker.internal:%d", listener.Addr().(*net.TCPAddr).Port), Auth: "bearer", APIKey: "fixture-model-key", ContextWindow: config.ModelContextWindow}
	manager := newFixtureManager(t, c)
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		if err := manager.Delete(cleanup); err != nil {
			t.Error(err)
		}
	}()
	if err := manager.Store.SaveProactiveSettings(ctx, manager.account, proactive.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(manager.Handler())
	defer server.Close()
	call := func(method, path, body string, want int) []byte {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("%s %s: %d %s", method, path, response.StatusCode, data)
		}
		return data
	}
	var main struct {
		MainSessionID string `json:"mainSessionId"`
	}
	if err := json.Unmarshal(call("POST", "/v1/host/start", `{"welcomeLocale":"en"}`, 200), &main); err != nil || main.MainSessionID == "" {
		t.Fatal("main identity missing")
	}
	status := func() proactive.Status {
		var state proactive.Status
		data := call("GET", proactive.SettingsPath, "", 200)
		if json.Unmarshal(data, &state) != nil {
			t.Fatalf("invalid status %s", data)
		}
		return state
	}
	wait := func(check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatalf("proactive did not settle: %+v", status())
			case <-time.After(30 * time.Millisecond):
			}
		}
	}
	wait(func() bool { return status().LastCompletedAt != 0 })
	first := status()
	if first.NextCheckAt <= first.LastCompletedAt || first.NextCheckReason == "" {
		t.Fatalf("AI did not choose a future check: %+v", first)
	}
	transcriptPath := "/v1/sessions/" + main.MainSessionID + "/transcript"
	if bytes.Contains(call("GET", transcriptPath, "", 200), []byte("A verified proactive update.")) {
		t.Fatal("quiet result notified the main chat")
	}
	var state proactive.State
	if err := json.Unmarshal(manager.Exec(t, ctx, "cat", "/root/harness/.dsh/storages/proactive.json"), &state); err != nil {
		t.Fatal(err)
	}
	if state.SessionID == "" || state.SessionID == main.MainSessionID {
		t.Fatal("background did not have its own Session")
	}
	if bytes.Contains(call("GET", "/v1/sessions", "", 200), []byte(state.SessionID)) {
		t.Fatal("background Session leaked into public list")
	}
	close(release)
	wait(func() bool { return status().LastCompletedAt > first.LastCompletedAt })
	wait(func() bool {
		return bytes.Contains(call("GET", transcriptPath, "", 200), []byte("A verified proactive update."))
	})
	transcript := call("GET", transcriptPath, "", 200)
	if bytes.Contains(transcript, []byte("agent-message")) || bytes.Contains(transcript, []byte("subagent-settled")) {
		t.Fatal("internal delivery became a user message")
	}
	artifact, err := sessionformat.InspectV4JSONL(bytes.NewReader(manager.Journal(t, ctx, state.SessionID)))
	if err != nil || artifact.Header.AgentPreset != proactive.PresetID || artifact.Header.ParentSession != "" {
		t.Fatalf("background journal=%+v err=%v", artifact.Header, err)
	}
	if results := proactive.CommittedResults(artifact.Events); len(results) != 2 {
		t.Fatalf("background committed %d results", len(results))
	}
	if err := manager.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	call("POST", "/v1/host/start", `{}`, 200)
	last := status()
	if !last.Enabled || last.NextCheckAt == 0 || calls.Load() != 2 {
		t.Fatalf("restart lost timer or replayed an old run: %+v calls=%d", last, calls.Load())
	}
	call("PUT", proactive.SettingsPath, `{"enabled":false}`, 200)
	if final := status(); final.Enabled || final.Phase != "disabled" {
		t.Fatalf("disable did not reach Host: %+v", final)
	}
}
