package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go-bot/harness/sessionsearch"
	"go-bot/internal/config"
)

func TestDockerMemoryAndHistoryRebuild(t *testing.T) {
	if os.Getenv("RUN_DOCKER_INTEGRATION") != "1" {
		t.Skip("set RUN_DOCKER_INTEGRATION=1; requires built Host image")
	}
	testMemoryAndHistoryRebuild(t, "", "", "")
}

func TestECINASMemoryAndHistoryRebuild(t *testing.T) {
	profile := os.Getenv("HARNESS_TEST_ECI_PROFILE")
	if profile == "" {
		t.Skip("set HARNESS_TEST_ECI_PROFILE to an ECI NAS profile with the updated Host cache")
	}
	account := os.Getenv("HARNESS_TEST_ECI_ACCOUNT")
	fixtureURL := os.Getenv("HARNESS_TEST_ECI_MODEL_URL")
	if !strings.HasPrefix(account, "fixture-") || fixtureURL == "" {
		t.Fatal("set HARNESS_TEST_ECI_ACCOUNT to a fresh fixture-* account with a NAS directory and HARNESS_TEST_ECI_MODEL_URL to the reachable fixture URL")
	}
	testMemoryAndHistoryRebuild(t, profile, account, fixtureURL)
}

func testMemoryAndHistoryRebuild(t *testing.T, profile, account, fixtureURL string) {
	t.Helper()
	cloud := profile != ""
	c := config.Default()
	limit := 3 * time.Minute
	if cloud {
		var err error
		c, err = config.Load("", profile)
		if err != nil {
			t.Fatal(err)
		}
		if c.Runtime.Provider != "eci" || c.Storage.Type != "nas" || c.Storage.NASMountTarget == "" || !config.ValidImageCacheID(c.Runtime.ECI.ImageCacheID) {
			t.Fatal("the ECI fixture requires HARNESS_RUNTIME=eci, HARNESS_STORAGE=nas, a NAS mount target and the updated Host image cache ID")
		}
		if err := config.ValidateModel("fixture-model-key", fixtureURL, "test-main"); err != nil {
			t.Fatal(err)
		}
		limit = 3*c.Runtime.StartTimeout + time.Minute
	} else if image := os.Getenv("HARNESS_TEST_HOST_IMAGE"); image != "" {
		c.Runtime.Image = image
	}
	ctx, cancel := context.WithTimeout(t.Context(), limit)
	defer cancel()

	var sawMemory atomic.Bool
	var foundHistory atomic.Bool
	var foundAppend atomic.Bool
	var restoredMemory atomic.Bool
	var excludedArchived atomic.Bool
	var isolatedAccount atomic.Bool
	provider := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fixtureTitleResponse(w, r) {
			return
		}
		var body struct {
			Tools []struct{ Name string }
			Input []struct {
				Type    string
				Role    string
				CallID  string `json:"call_id"`
				Content []struct{ Text string }
				Output  string
			}
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil || json.Unmarshal(raw, &body) != nil {
			t.Error("invalid model request")
			http.Error(w, "invalid request", 400)
			return
		}
		declared := map[string]bool{}
		for _, tool := range body.Tools {
			declared[tool.Name] = true
		}
		if !declared["memory"] || !declared["session_search"] {
			t.Error("managed Host did not register both memory tools")
		}
		var systemPrompt strings.Builder
		for _, item := range body.Input {
			if item.Role == "system" || item.Role == "developer" {
				for _, content := range item.Content {
					systemPrompt.WriteString(content.Text)
				}
			}
		}
		prompt := systemPrompt.String()
		if !strings.Contains(prompt, "You have persistent memory across sessions") || !strings.Contains(prompt, "look it up with session_search") {
			t.Error("model request did not explain the available memory capabilities")
		}
		done := map[string]any{"type": "message", "id": "final", "role": "assistant",
			"content": []map[string]string{{"type": "output_text", "text": "Done."}}}
		output := done
		if strings.Contains(string(raw), "Remember that") {
			if strings.Contains(string(raw), "fc-remember") || strings.Contains(string(raw), "function_call_output") {
				sawMemory.Store(strings.Contains(prompt, "User preferences (") && strings.Contains(prompt, "日报用中文"))
			} else {
				output = webCall("remember", "memory", `{"target":"user","operations":[{"action":"add","content":"日报用中文"}]}`)
			}
		}
		if strings.Contains(string(raw), "Search the saved conversation") {
			afterArchive := strings.Contains(string(raw), "after archive")
			otherAccount := strings.Contains(string(raw), "from another account")
			incremental := strings.Contains(string(raw), "for the appended detail")
			callID := "recall"
			arguments := `{"query":"挂载"}`
			if afterArchive {
				callID = "recall-archived"
			}
			if otherAccount {
				callID = "recall-isolated"
			}
			if incremental {
				callID = "recall-appended"
				arguments = `{"query":"归档清单"}`
			}
			var result *sessionsearch.Result
			for _, item := range body.Input {
				if item.Type == "function_call_output" && item.CallID == callID {
					var decoded sessionsearch.Result
					if err := json.Unmarshal([]byte(item.Output), &decoded); err != nil {
						t.Errorf("session_search output = %q", item.Output)
					}
					result = &decoded
				}
			}
			if result == nil {
				output = webCall(callID, "session_search", arguments)
			} else if incremental {
				foundAppend.Store(len(result.Sessions) == 1 && result.Sessions[0].Match != nil &&
					strings.Contains(result.Sessions[0].Match.Content, "归档清单"))
			} else if otherAccount {
				isolatedAccount.Store(result.Mode == "discover" && len(result.Sessions) == 0 &&
					!strings.Contains(prompt, "日报用中文"))
			} else if afterArchive {
				excludedArchived.Store(result.Mode == "discover" && len(result.Sessions) == 0)
			} else {
				foundHistory.Store(len(result.Sessions) == 1 && result.Sessions[0].Match != nil &&
					strings.Contains(result.Sessions[0].Match.Content, "挂载卷"))
				restoredMemory.Store(strings.Contains(prompt, "日报用中文"))
			}
		}
		payload, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{
			"id": "memory-response", "status": "completed", "output": []any{output},
		}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", payload)
	}))
	listen := "0.0.0.0:0"
	if cloud {
		u, _ := url.Parse(fixtureURL)
		if os.Getenv("HARNESS_TEST_ECI_FIXTURE_LISTEN") != "" {
			listen = os.Getenv("HARNESS_TEST_ECI_FIXTURE_LISTEN")
		} else if u.Port() != "" {
			listen = net.JoinHostPort("0.0.0.0", u.Port())
		} else {
			t.Fatal("set HARNESS_TEST_ECI_FIXTURE_LISTEN or include the fixture port in HARNESS_TEST_ECI_MODEL_URL")
		}
	}
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		t.Fatal(err)
	}
	provider.Listener = listener
	provider.Start()
	defer provider.Close()

	if !cloud {
		fixtureURL = "http://host.docker.internal:" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	}
	c.Model.Main = config.MainModel{Provider: "custom", Name: "test-main", Protocol: "responses",
		BaseURL: fixtureURL,
		Auth:    "bearer", APIKey: "fixture-model-key", ContextWindow: config.ModelContextWindow}
	var manager *fixtureManager
	if cloud {
		manager = newFixtureManagerForAccount(t, c, account)
	} else {
		manager = newFixtureManager(t, c)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), c.Runtime.StartTimeout)
		defer stop()
		if err := manager.Delete(cleanup); err != nil {
			t.Errorf("remove fixture Host: %v", err)
		}
	}()
	if err := manager.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(manager.Handler())
	defer server.Close()

	request := func(method, path, body string, code int) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != code {
			defer res.Body.Close()
			data, _ := io.ReadAll(res.Body)
			t.Fatalf("%s %s: %s %s", method, path, res.Status, data)
		}
		return res
	}
	newSession := func() string {
		res := request("POST", "/v1/sessions", "", 201)
		defer res.Body.Close()
		var created struct{ SessionID string }
		if err := json.NewDecoder(res.Body).Decode(&created); err != nil || created.SessionID == "" {
			t.Fatalf("create session: %v", err)
		}
		return "/v1/sessions/" + created.SessionID
	}
	send := func(path, text, after string) string {
		streamPath := path + "/stream"
		body, _ := json.Marshal(map[string]string{"content": text})
		if after != "" {
			streamPath += "?after=" + after
			// The message activates a cold Session before the stream attaches.
			request("POST", path+"/messages", string(body), 202).Body.Close()
		}
		stream := request("GET", streamPath, "", 200)
		defer stream.Body.Close()
		if after == "" {
			request("POST", path+"/messages", string(body), 202).Body.Close()
		}
		events := readWebTurn(t, stream.Body)
		return strconv.FormatUint(events[len(events)-1].Seq, 10)
	}

	history := newSession()
	after := send(history, "Remember that I use a 挂载卷 for reports.", "")
	if !sawMemory.Load() {
		t.Fatal("same-turn request did not receive the updated memory prompt")
	}
	send(newSession(), "Search the saved conversation.", "")
	if !foundHistory.Swap(false) {
		t.Fatal("live Host did not index the committed conversation")
	}
	restoredMemory.Store(false)
	if !cloud {
		if got := manager.Exec(t, ctx, "cat", "/root/harness/.dsh/memories/USER.md"); strings.TrimSpace(string(got)) != "日报用中文" {
			t.Fatalf("saved memory = %q", got)
		}
		manager.Exec(t, ctx, "test", "-s", "/var/lib/go-bot/session-search.sqlite")
	}
	instance, err := manager.Observe(ctx, manager.account)
	if err != nil || instance.HostID == "" {
		t.Fatal("original Host instance missing")
	}
	if err := manager.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if replaced, err := manager.Observe(ctx, manager.account); err != nil || replaced.HostID == "" || replaced.HostID == instance.HostID {
		t.Fatal("test did not replace the Host container")
	}
	send(newSession(), "Search the saved conversation.", "")
	if !foundHistory.Load() {
		t.Fatal("replacement Host did not recall the prior conversation through FTS5")
	}
	if !restoredMemory.Load() {
		t.Fatal("replacement Host did not load the saved long-term memory")
	}
	send(history, "The new document is the 归档清单.", after)
	send(newSession(), "Search the saved conversation for the appended detail.", "")
	if !foundAppend.Load() {
		t.Fatal("resumed session's new committed message was not indexed")
	}
	if cloud {
		return
	}
	other := newFixtureManager(t, c)
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 25*time.Second)
		defer stop()
		if err := other.Delete(cleanup); err != nil {
			t.Errorf("remove second fixture Host: %v", err)
		}
	}()
	if err := other.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	otherServer := httptest.NewServer(other.Handler())
	defer otherServer.Close()
	originalServer := server
	server = otherServer
	send(newSession(), "Search the saved conversation from another account.", "")
	server = originalServer
	if !isolatedAccount.Load() {
		t.Fatal("another account did not receive isolated memory and history")
	}
	request("DELETE", history, "", 204).Body.Close()
	send(newSession(), "Search the saved conversation after archive.", "")
	if !excludedArchived.Load() {
		t.Fatal("search did not return an empty result after archive")
	}
}
