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
	"strings"
	"testing"
	"time"

	"go-bot/internal/config"
)

func TestDockerBrowserSkillAndSessionIsolation(t *testing.T) {
	if os.Getenv("RUN_DOCKER_INTEGRATION") != "1" {
		t.Skip("set RUN_DOCKER_INTEGRATION=1 and HARNESS_TEST_HOST_IMAGE to the built Host image")
	}
	c := config.Default()
	if image := os.Getenv("HARNESS_TEST_HOST_IMAGE"); image != "" {
		c.Runtime.Image = image
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if side := r.URL.Query().Get("side"); side == "first" {
			http.SetCookie(w, &http.Cookie{Name: "fixture", Value: "first-only", Path: "/"})
		}
		_, _ = fmt.Fprint(w, `<h1>Browser fixture</h1><input aria-label="Report name"><button onclick="document.querySelector('h1').textContent=document.querySelector('input').value">Run report</button><a href="/download" download>Download report</a>`)
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="fixture.txt"`)
		_, _ = fmt.Fprint(w, "browser download fixture")
	})
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := fmt.Sprintf("http://host.docker.internal:%d", listener.Addr().(*net.TCPAddr).Port)
	mux.HandleFunc("/responses", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input []struct {
				Role, Type string
				CallID     string `json:"call_id"`
				Content    []struct{ Text string }
				Output     string
			}
			Tools []struct{ Name string }
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, "invalid request", 400)
			return
		}
		tools := map[string]bool{}
		for _, tool := range body.Tools {
			tools[tool.Name] = true
		}
		if tools["generateimage"] || !tools["readimage"] || !tools["present"] || !tools["web_search"] || !tools["skill"] {
			t.Error("expected built-in tools were not registered")
		}
		var userMessage string
		outputs := map[string]string{}
		for _, item := range body.Input {
			if item.Role == "user" && len(item.Content) > 0 &&
				!strings.HasPrefix(item.Content[0].Text, "Time sampled") &&
				!strings.HasPrefix(item.Content[0].Text, "Current runtime context.") {
				userMessage = item.Content[0].Text
			}
			if item.Type == "function_call_output" {
				outputs[item.CallID] = item.Output
			}
		}
		var output map[string]any
		shell := func(id, command string) map[string]any {
			args, _ := json.Marshal(map[string]string{"command": command})
			return webCall(id, "shellexecute", string(args))
		}
		check := func(id, want string) {
			if !strings.Contains(outputs[id], want) {
				t.Errorf("%s output is missing %q: %.1500s", id, want, outputs[id])
			}
		}
		switch userMessage {
		case "Browser first":
			switch {
			case outputs["browser-skill"] == "":
				output = webCall("browser-skill", "skill", `{"name":"agent-browser"}`)
			case outputs["browser-open"] == "":
				check("browser-skill", "agent-browser skills get core")
				output = shell("browser-open", "set -e; test -n \"$AGENT_BROWSER_SOCKET_DIR\"; printf '%s' \"$AGENT_BROWSER_SOCKET_DIR\" > /root/harness/browser-first-socket; agent-browser skills get core > /tmp/browser-core-fixture; grep -q 'agent-browser core' /tmp/browser-core-fixture; agent-browser --session shared open "+origin+"/page?side=first; agent-browser --session shared snapshot")
			case outputs["browser-interact"] == "":
				check("browser-open", "Run report")
				output = shell("browser-interact", `set -e
agent-browser --session shared fill @e2 "Browser report"
agent-browser --session shared click @e3
agent-browser --session shared snapshot
agent-browser --session shared screenshot browser-report.png
agent-browser --session shared download @e4 browser-report.txt
agent-browser --session extra open about:blank
`)
			default:
				check("browser-interact", "Browser report")
				check("browser-interact", "browser-report.txt")
			}
		case "Browser second":
			if outputs["browser-second"] == "" {
				output = shell("browser-second", "set -e; test \"$(cat /root/harness/browser-first-socket)\" != \"$AGENT_BROWSER_SOCKET_DIR\"; agent-browser --session shared open "+origin+"/page?side=second; agent-browser --session shared cookies; agent-browser --session shared snapshot; printf '%s' \"$AGENT_BROWSER_SOCKET_DIR\" > browser-second-socket; touch browser-second-file")
			} else {
				if strings.Contains(outputs["browser-second"], "first-only") {
					t.Error("browser cookies leaked between sessions")
				}
				check("browser-second", "Browser fixture")
			}
		case "Browser after archive":
			if outputs["browser-survives"] == "" {
				output = shell("browser-survives", `set -e
test ! -d "$(cat /root/harness/browser-first-socket)"
agent-browser --session shared snapshot
`)
			} else {
				check("browser-survives", "Browser fixture")
			}
		case "Browser after restart":
			if outputs["browser-cold"] == "" {
				output = shell("browser-cold", `set -e
test -f browser-second-file
test "$(cat browser-second-socket)" != "$AGENT_BROWSER_SOCKET_DIR"
test -z "$(find "$AGENT_BROWSER_SOCKET_DIR" -type s -print -quit)"
printf "fresh browser session"
`)
			} else {
				check("browser-cold", "fresh browser session")
			}
		case "Built-in image generation disabled":
			// The model can read and present plugin-generated images, but no
			// built-in generation tool is advertised, even with saved settings.
		default:
			t.Errorf("unexpected user message %q", userMessage)
		}
		if output == nil {
			output = map[string]any{"type": "message", "id": "done", "role": "assistant",
				"content": []map[string]string{{"type": "output_text", "text": "Done."}}}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		payload, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{
			"id": "fixture", "status": "completed", "output": []map[string]any{output},
			"usage": map[string]int{"input_tokens": 10, "output_tokens": 5},
		}})
		_, _ = fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", payload)
	})
	fixture := httptest.NewUnstartedServer(mux)
	fixture.Listener = listener
	fixture.Start()
	defer fixture.Close()
	c.Model.Main = config.MainModel{Provider: "custom", Name: "test-main", Protocol: "responses",
		BaseURL: origin, Auth: "bearer", APIKey: "fixture-model-key", ContextWindow: config.ModelContextWindow}
	manager := newFixtureManager(t, c)
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
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
	request := func(method, path, body string, status int) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != status {
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
		if after != "" {
			streamPath += "?after=" + after
		}
		body, _ := json.Marshal(map[string]string{"content": text})
		request("POST", path+"/messages", string(body), 202).Body.Close()
		stream := request("GET", streamPath, "", 200)
		defer stream.Body.Close()
		events := readWebTurn(t, stream.Body)
		return fmt.Sprint(events[len(events)-1].Seq)
	}

	const imageSettings = "/v1/settings/image-generation"
	request("PUT", imageSettings, `{"provider":"seedream","model":"ep-browser-fixture","apiKey":"fixture-image-key"}`, 200).Body.Close()
	saved, err := manager.Store.Get(ctx, manager.account)
	if err != nil || saved.ImageGeneration == nil || saved.ImageGeneration.Entries["seedream"].Model != "ep-browser-fixture" {
		t.Fatalf("image settings were not saved in Redis: %v", err)
	}
	request("DELETE", imageSettings+"?provider=seedream", "", 200).Body.Close()

	first, second := newSession(), newSession()
	// Sessions already exist before CLI installation. Installation must prepare
	// software while disabled; activation contributes the Skill without downloading.
	manager.Exec(t, ctx, "sh", "-ec", "! command -v agent-browser")
	request("POST", "/v1/settings/plugins/packages", `{"pluginId":"agent-browser"}`, 200).Body.Close()
	manager.Exec(t, ctx, "sh", "-ec", "agent-browser --version; test ! -e /root/harness/.dsh/plugins/profile/plugins.json")
	request("PUT", "/v1/settings/plugins/packages/%40go-bot%2Fplugin-agent-browser", `{"enabled":true}`, 200).Body.Close()

	send(first, "Browser first", "")
	after := send(second, "Browser second", "")
	for name, signature := range map[string]string{"browser-report.png": "\x89PNG", "browser-report.txt": "browser download fixture"} {
		res := request("GET", first+"/files/content?path="+name, "", 200)
		data, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil || !strings.HasPrefix(string(data), signature) {
			t.Fatalf("download %s: %d bytes, %v", name, len(data), err)
		}
	}
	request("DELETE", first, "", 204).Body.Close()
	after = send(second, "Browser after archive", after)
	if err := manager.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	send(second, "Browser after restart", after)
	send(newSession(), "Built-in image generation disabled", "")
}
