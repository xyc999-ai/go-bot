package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go-bot/frontend"
	"go-bot/harness/session"
	"go-bot/internal/config"
	"go-bot/internal/controller"
)

func TestDockerWebChatPreview(t *testing.T) {
	if os.Getenv("RUN_DOCKER_INTEGRATION") != "1" {
		t.Skip("requires the built Host image")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Second)
	defer cancel()
	var modelCalls atomic.Int32
	release := make(chan struct{})
	model := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fixtureTitleResponse(w, r) {
			return
		}
		var request struct{ Tools []struct{ Name string } }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid request", 400)
			return
		}
		tools := map[string]bool{}
		for _, tool := range request.Tools {
			tools[tool.Name] = true
		}
		if tools["generateimage"] || !tools["readimage"] || !tools["present"] {
			t.Error("Host must retain image reading and delivery without registering native generation")
		}
		call := modelCalls.Add(1)
		if call == 1 {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-preview\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"id\":\"preview-reply\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"Preview reply.\"}]}]}}\n\n")
	}))
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	model.Listener = listener
	model.Start()
	defer model.Close()

	c := dockerFixtureConfig()
	c.Model.Main = config.MainModel{Provider: "custom", Name: "preview-fixture", Protocol: "responses",
		BaseURL: fmt.Sprintf("http://host.docker.internal:%d", listener.Addr().(*net.TCPAddr).Port),
		Auth:    "bearer", APIKey: "fixture-model-key", ContextWindow: config.ModelContextWindow}
	c.Web.Provider, c.Web.APIKey = "deepseek", "fixture-search-key"

	manager := newFixtureManager(t, c)
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if err := manager.Delete(cleanup); err != nil {
			t.Error(err)
		}
	}()
	if err := manager.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	files := frontend.Files()
	server := httptest.NewServer(manager.HandlerWithWeb(controller.WebOptions{Files: files}))
	defer server.Close()
	call := func(method, path, body string, want int) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", server.URL)
		req.Header.Set("Content-Type", "application/json")
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if want != 0 && resp.StatusCode != want {
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, data)
		}
		return resp
	}
	read := func(method, path, body string, want int) string {
		t.Helper()
		resp := call(method, path, body, want)
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if files != nil && !strings.Contains(read("GET", "/", "", 200), "/assets/") {
		t.Fatal("Controller entry does not load its embedded resources")
	}
	var main struct {
		MainSessionID string `json:"mainSessionId"`
	}
	if err := json.Unmarshal([]byte(read("POST", "/v1/host/start", `{"welcomeLocale":"en"}`, 200)), &main); err != nil || main.MainSessionID == "" {
		t.Fatalf("main chat: %v", err)
	}
	if greeting := read("GET", "/v1/sessions/"+main.MainSessionID+"/transcript", "", 200); !strings.Contains(greeting, "Hi, I'm Go Bot") {
		t.Fatal("main greeting absent")
	}
	if failure := read("DELETE", "/v1/sessions/"+main.MainSessionID, "", 409); !strings.Contains(failure, "main_session_cannot_archive") {
		t.Fatal("main could be archived")
	}
	var created struct{ SessionID string }
	if err := json.Unmarshal([]byte(read("POST", "/v1/sessions", "", 201)), &created); err != nil || created.SessionID == "" {
		t.Fatalf("missing created session: %v", err)
	}
	sid := created.SessionID
	url := "/v1/sessions/" + sid
	if files != nil && !strings.Contains(read("GET", "/chat/"+sid, "", 200), "Go Bot") {
		t.Fatal("Controller does not serve direct chat links")
	}
	stream := call("GET", url+"/stream", "", 200)
	defer stream.Body.Close()
	read("POST", url+"/messages", `{"content":"Preview title and history"}`, 202)
	if conflict := read("DELETE", url, "", 409); !strings.Contains(conflict, "session_busy") {
		t.Fatalf("expected busy archive conflict, got %s", conflict)
	}
	close(release)
	events := readWebTurn(t, stream.Body)
	stream.Body.Close()
	if len(events) == 0 || events[len(events)-1].Type != "turn/end" {
		t.Fatal("missing terminal SSE event")
	}

	// Naming is independent of the reply turn; wait for its durable result
	// before testing Host replacement, rather than relying on request timing.
	titleDeadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(read("GET", "/v1/sessions", "", 200), `"title":"Test conversation"`) {
		if time.Now().After(titleDeadline) {
			t.Fatal("generated title did not become visible")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := manager.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	read("GET", url+"/events", "", 503)
	if err := manager.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	history := read("GET", url+"/events", "", 200)
	if !strings.Contains(history, "Preview title and history") || !strings.Contains(history, "Preview reply.") ||
		strings.Contains(history, "system/message") || modelCalls.Load() != 1 {
		t.Fatalf("cold history changed or activated the Agent: %s, calls=%d", history, modelCalls.Load())
	}
	read("GET", url, "", 404)
	var page struct {
		Events     []sessionformat.Event
		NextCursor *uint64
	}
	if err := json.Unmarshal([]byte(read("GET", url+"/events?limit=1", "", 200)), &page); err != nil ||
		len(page.Events) != 0 || page.NextCursor == nil || *page.NextCursor != 0 {
		t.Fatalf("filtered cold page did not advance the cursor: %+v %v", page, err)
	}
	cursor := strconv.FormatUint(*page.NextCursor, 10)
	read("GET", url+"/events?after="+cursor, "", 200)
	if list := read("GET", "/v1/sessions", "", 200); !strings.Contains(list, `"title":"Test conversation"`) {
		t.Fatalf("missing durable title: %s", list)
	}

	read("PUT", url+"/title", `{"title":"My report"}`, 200)
	if list := read("GET", "/v1/sessions", "", 200); !strings.Contains(list, `"title":"My report"`) || modelCalls.Load() != 1 {
		t.Fatalf("renaming a cold chat failed or started a model turn: %s", list)
	}

	// Renaming a cold Session activates it. Its empty driver probe may still
	// hold the runtime busy flag after the title has been durably committed.
	// Wait only for that explicit transient conflict; keep all other failures
	// fatal, and keep the active-turn archive rejection asserted above.
	archiveDeadline := time.Now().Add(3 * time.Second)
	for {
		response := call("DELETE", url, "", 0)
		data, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if response.StatusCode == http.StatusNoContent {
			break
		}
		var failure struct {
			Error string `json:"error"`
		}
		if response.StatusCode != http.StatusConflict || json.Unmarshal(data, &failure) != nil ||
			failure.Error != "session_busy" || time.Now().After(archiveDeadline) {
			t.Fatalf("archive idle Session: %d %s", response.StatusCode, data)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	read("DELETE", url, "", 204)
	read("GET", url+"/events", "", 404)
	read("POST", url+"/messages", `{"content":"Must not run"}`, 404)
	if err := manager.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if list := read("GET", "/v1/sessions", "", 200); strings.Contains(list, sid) {
		t.Fatalf("archived session reappeared: %s", list)
	}
	_ = manager.Journal(t, ctx, sid)
	manager.Exec(t, ctx, "sh", "-ec", `test -d "workspaces/$1"; test -f .dsh/sessions/*/"$1"/archived`, "sh", sid)
}
