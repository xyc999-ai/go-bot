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
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"go-bot/harness/session"
	"go-bot/internal/config"
	dockerapi "go-bot/thirdparty/docker"
)

func TestDockerHostConversationRecoveryAndFiles(t *testing.T) {
	if os.Getenv("RUN_DOCKER_INTEGRATION") != "1" {
		t.Skip("set RUN_DOCKER_INTEGRATION=1; requires built Host image")
	}
	var modelCalls atomic.Int32
	model := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		b, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/responses" {
			t.Errorf("unexpected model path %s", r.URL.Path)
		}
		if strings.Contains(string(b), "list_subagent_models") || strings.Contains(string(b), `"max_output_tokens"`) {
			t.Error("removed model options appeared in request")
		}
		modelCalls.Add(1)
		var output []map[string]any
		if !strings.Contains(string(b), "web_search") || !strings.Contains(string(b), `"name":"skill"`) {
			t.Error("search or skill is missing from Host model tools")
		}
		if r.Header.Get("Authorization") != "Bearer fixture-model-key" {
			t.Error("Host did not use its configured model key")
		}
		if !strings.Contains(string(b), "function_call_output") {
			output = []map[string]any{
				webCall("tool-test", "shellexecute", `{"command":"officecli create report.docx && officecli save report.docx && printf harness-output > report.txt"}`),
				webCall("office-docx", "skill", `{"name":"officecli-docx"}`),
				webCall("skill-creator", "skill", `{"name":"skill-creator"}`),
				webCall("unknown-skill", "skill", `{"name":"not-installed"}`),
				webCall("bad-skill-args", "skill", `{"name":9}`),
			}
			if strings.Contains(string(b), "Project fixture skill") {
				output = append(output, webCall("project-skill", "skill", `{"name":"project-fixture"}`))
			}
			if strings.Contains(string(b), "Creator smoke fixture") {
				output = append(output, webCall("created-skill", "skill", `{"name":"creator-smoke"}`))
			}
		} else {
			output = []map[string]any{{"type": "message", "id": "message-test", "role": "assistant", "content": []map[string]string{{"type": "output_text", "text": "Saved report.txt"}}}}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		payload, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"id": "response-test", "status": "completed", "output": output, "usage": map[string]int{"input_tokens": 10, "output_tokens": 5}}})
		fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", payload)
		w.(http.Flusher).Flush()
	}))
	listener, e := net.Listen("tcp", "0.0.0.0:0")
	if e != nil {
		t.Fatal(e)
	}
	model.Listener = listener
	model.Start()
	defer model.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	c := dockerFixtureConfig()
	baseImage := c.Runtime.Image
	c.Runtime.Image = "go-bot/host-upgrade-test:" + uuid.NewString()
	docker := dockerapi.Client{}
	if _, err := docker.Run(ctx, nil, "image", "tag", baseImage, c.Runtime.Image); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		if _, err := docker.Run(cleanup, nil, "image", "rm", c.Runtime.Image); err != nil {
			t.Errorf("remove fixture image: %v", err)
		}
	})
	c.Model.Main = config.MainModel{Provider: "custom", Name: "test-main", Protocol: "responses", BaseURL: fmt.Sprintf("http://host.docker.internal:%d", port), Auth: "bearer", APIKey: "fixture-model-key", ContextWindow: config.ModelContextWindow}
	c.Web.Provider = "deepseek"
	c.Web.APIKey = "fixture-search-key"

	if e := c.Validate(false); e != nil {
		t.Fatal(e)
	}
	manager := newFixtureManager(t, c)
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 25*time.Second)
		defer done()
		if manager.Instance(cleanup) != nil {
			if e := manager.Delete(cleanup); e != nil {
				t.Errorf("cleanup: %v", e)
			}
		}
	}()
	if e = manager.Ensure(ctx); e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(manager.Handler())
	defer server.Close()
	call := func(method, path, body string) (int, []byte) {
		t.Helper()
		req, e := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		res, e := server.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
	if code, body := call("POST", "/v1/host/plugins/prepare", ""); code != 200 {
		t.Fatalf("prepare default plugins: %d %s", code, body)
	}
	status, b := call("POST", "/v1/sessions", "")
	if status != 201 {
		t.Fatalf("create: %d %s", status, b)
	}
	var created map[string]string
	json.Unmarshal(b, &created)
	sid := created["sessionId"]
	if sid == "" {
		t.Fatal("no session ID")
	}
	writeSkill := func(path, content string) {
		t.Helper()
		manager.Exec(t, ctx, "sh", "-ec", `mkdir -p "$(dirname "$1")"; printf '%s' "$2" > "$1"`, "sh", path, content)
	}
	writeSkill("workspaces/"+sid+"/.dsh/skills/project.md", "---\nname: project-fixture\ndescription: Project fixture skill\n---\nLatest project instructions\n")
	writeSkill(".agents/skills/manual/SKILL.md", "---\nname: manual-fixture\ndescription: User invocation fixture\ndisable-model-invocation: true\n---\nExplicit user instructions\n")
	streamReq, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/sessions/"+sid+"/stream", nil)
	streamReq.Header.Set("Origin", server.URL)
	stream, e := server.Client().Do(streamReq)
	if e != nil {
		t.Fatal(e)
	}
	if stream.StatusCode != 200 || !strings.HasPrefix(stream.Header.Get("Content-Type"), "text/event-stream") ||
		stream.Header.Get("Access-Control-Allow-Origin") != "" {
		stream.Body.Close()
		t.Fatalf("same-origin SSE status=%d headers=%v", stream.StatusCode, stream.Header)
	}
	status, b = call("POST", "/v1/sessions/"+sid+"/messages", `{"content":"Create report.txt and /manual-fixture"}`)
	if status != 202 {
		t.Fatalf("message: %d %s", status, b)
	}
	first := readWebTurn(t, stream.Body)
	stream.Body.Close()
	instance := manager.Instance(ctx)
	if instance == nil {
		t.Fatal("Host instance missing")
	}
	assertDockerSkills(t, manager.Journal(t, ctx, sid), true, true)
	status, b = call("GET", "/v1/sessions/"+sid+"/files/content?path=report.txt", "")
	if status != 200 || string(b) != "harness-output" {
		t.Fatalf("tool output: %d %s", status, b)
	}
	status, b = call("GET", "/v1/sessions/"+sid+"/files/content?path=report.docx", "")
	if status != 200 || !strings.HasPrefix(string(b), "PK") {
		t.Fatalf("OfficeCLI did not create a DOCX in the workspace: %d", status)
	}
	if _, e = (dockerapi.Client{}).Run(ctx, nil, "exec", instance.Ref.ID, "sh", "-ec", `
		test -z "$ALIBABA_CLOUD_ACCESS_KEY_SECRET" && test -z "$CONTROLLER_API_TOKEN"
		test "$HOME" = /root && test "$DSH_HOME" = /root/harness/.dsh
		test "$DSH_AGENTS_HOME" = /root/harness/.agents && test "$PWD" = /root/harness
		for bundle in officecli officecli-docx officecli-xlsx officecli-pptx officecli-academic-paper officecli-data-dashboard officecli-financial-model officecli-pitch-deck officecli-word-form morph-ppt morph-ppt-3d; do
		  test -s "$DSH_HOME/plugins/profile/node_modules/@go-bot/plugin-officecli/skills/$bundle/SKILL.md"
		done
		test -s "$DSH_HOME/plugins/profile/node_modules/@go-bot/plugin-officecli/skills/morph-ppt/reference/styles/INDEX.md"
		test -s "$DSH_HOME/skills/skill-creator/agents/openai.yaml"
		test -s "$DSH_HOME/skills/skill-creator/LICENSE.txt"
		python "$DSH_HOME/skills/skill-creator/scripts/quick_validate.py" "$DSH_HOME/skills/skill-creator"
		python "$DSH_HOME/skills/skill-creator/scripts/init_skill.py" creator-smoke
		test -s "$DSH_HOME/skills/creator-smoke/agents/openai.yaml"
	`); e != nil {
		t.Fatalf("unexpected container skills or environment: %v", e)
	}
	manager.Exec(t, ctx, "python", "-c", `from pathlib import Path
p = Path('/root/harness/.dsh/skills/creator-smoke/SKILL.md')
p.write_text('---\nname: creator-smoke\ndescription: Creator smoke fixture\nuser-invocable: true\n---\nGenerated skill instructions\n', encoding='utf-8')`)
	manager.Exec(t, ctx, "python", "/root/harness/.dsh/skills/skill-creator/scripts/quick_validate.py", "/root/harness/.dsh/skills/creator-smoke")
	direct, e := server.Client().Get(instance.Endpoint + "/readyz")
	if e != nil {
		t.Fatal(e)
	}
	direct.Body.Close()
	if direct.StatusCode != http.StatusUnauthorized {
		t.Fatalf("direct Host accepted a request without its internal token: %d", direct.StatusCode)
	}
	status, body := call("POST", "/v1/sessions", "")
	var second struct{ SessionID string }
	if status != 201 || json.Unmarshal(body, &second) != nil || second.SessionID == "" {
		t.Fatalf("create isolated session: %d %s", status, body)
	}
	other := "/v1/sessions/" + second.SessionID
	req, err := http.NewRequestWithContext(ctx, "GET", server.URL+other+"/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	otherStream, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if status, body = call("POST", other+"/messages", `{"content":"Create report.txt and /manual-fixture"}`); status != 202 {
		t.Fatalf("isolated session turn: %d %s", status, body)
	}
	_ = readWebTurn(t, otherStream.Body)
	otherStream.Body.Close()
	assertDockerSkills(t, manager.Journal(t, ctx, second.SessionID), false, true)
	if !strings.Contains(string(manager.Journal(t, ctx, second.SessionID)), "Generated skill instructions") {
		t.Fatal("skill created by the bundled initializer was not discovered and loaded")
	}
	writeSkill(".dsh/skills/deleted-user-skill/SKILL.md", "---\nname: deleted-user-skill\ndescription: Deleted user skill\n---\nremoved\n")
	manager.Exec(t, ctx, "rm", "-r", ".dsh/skills/deleted-user-skill")
	manager.Exec(t, ctx, "rm", "-r", ".dsh/skills/skill-creator")
	beforeStop, e := manager.Store.Get(ctx, manager.account)
	if e != nil || beforeStop.HostEndpoint != instance.Endpoint {
		t.Fatalf("running Host route was not saved: %v", e)
	}
	if status, body := call("POST", "/v1/host/stop", ""); status != http.StatusNoContent {
		t.Fatalf("stop Host: %d %s", status, body)
	}
	afterStop, e := manager.Store.Get(ctx, manager.account)
	if e != nil || afterStop.HostEndpoint != "" || afterStop.HostID != beforeStop.HostID {
		t.Fatalf("stopped Docker Host did not clear its endpoint and keep its identity: %v", e)
	}

	if status, body := call("POST", "/v1/host/start", ""); status != http.StatusOK {
		t.Fatalf("reopen account: %d %s", status, body)
	}
	afterStart, e := manager.Store.Get(ctx, manager.account)
	restarted := manager.Instance(ctx)
	if e != nil || restarted == nil || afterStart.HostEndpoint != restarted.Endpoint ||
		afterStart.HostID != beforeStop.HostID || restarted.Ref != instance.Ref {
		t.Fatalf("restarted Host did not persist its current route and identity: %v", e)
	}
	status, b = call("GET", "/v1/sessions", "")
	if status != 200 || !strings.Contains(string(b), sid) {
		t.Fatalf("history lost after restart: %d %s", status, b)
	}
	// Optional artifact export lets the unmodified upstream TypeScript decoder
	// validate a journal generated by the real Docker Host, not a Go fixture.
	if dir := os.Getenv("HARNESS_VALIDATION_DIR"); dir != "" {
		data := manager.Journal(t, ctx, sid)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "docker-container.v4.jsonl"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	manager.Exec(t, ctx, "sh", "-ec", `test ! -e .dsh/skills/deleted-user-skill/SKILL.md; test ! -e .dsh/skills/skill-creator; test -f .agents/skills/manual/SKILL.md`)

	oldImageID, e := docker.Run(ctx, nil, "container", "inspect", "--format", "{{.Image}}", instance.Ref.ID)
	if e != nil {
		t.Fatal(e)
	}
	stage := t.TempDir()
	recipe := "FROM " + baseImage + "\nLABEL org.opencontainers.image.version=upgrade-test\n"
	if err := os.WriteFile(filepath.Join(stage, "Dockerfile"), []byte(recipe), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := docker.Run(ctx, nil, "build", "--pull=false", "--tag", c.Runtime.Image, stage); err != nil {
		t.Fatal(err)
	}
	if status, body := call("DELETE", "/v1/host/instance", ""); status != http.StatusNoContent {
		t.Fatalf("delete Host: %d %s", status, body)
	}
	if status, body := call("POST", "/v1/host/start", ""); status != http.StatusOK {
		t.Fatalf("create replacement Host: %d %s", status, body)
	}
	replacement := manager.Instance(ctx)
	if replacement == nil || replacement.Ref == instance.Ref || replacement.RunID == beforeStop.HostID {
		t.Fatal("deletion did not create a new Host")
	}
	newImageID, e := docker.Run(ctx, nil, "container", "inspect", "--format", "{{.Image}}", replacement.Ref.ID)
	if e != nil || string(oldImageID) == string(newImageID) {
		t.Fatalf("replacement did not use the updated image: %v", e)
	}
	if status, body := call("GET", "/v1/sessions", ""); status != http.StatusOK || !strings.Contains(string(body), sid) {
		t.Fatalf("replacement did not recover the session: %d %s", status, body)
	}
	if data := manager.Exec(t, ctx, "cat", "workspaces/"+sid+"/report.txt"); string(data) != "harness-output" {
		t.Fatalf("replacement did not recover the workspace: %q", data)
	}
	// A persisted, cold Session resumes on its next message, then file access is live again.
	status, b = call("POST", "/v1/sessions/"+sid+"/messages", `{"content":"Continue"}`)
	if status != 202 {
		t.Fatalf("resume: %d %s", status, b)
	}
	after := strconv.FormatUint(first[len(first)-1].Seq, 10)
	resume, e := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/sessions/"+sid+"/stream?after="+after, nil)
	if e != nil {
		t.Fatal(e)
	}
	resumedStream, e := server.Client().Do(resume)
	if e != nil {
		t.Fatal(e)
	}
	if resumedStream.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resumedStream.Body)
		resumedStream.Body.Close()
		t.Fatalf("open cold replay stream: %d %s", resumedStream.StatusCode, body)
	}
	_ = readWebTurn(t, resumedStream.Body)
	resumedStream.Body.Close()
	status, b = call("GET", "/v1/sessions/"+sid+"/files/content?path=report.txt", "")
	if status != 200 || string(b) != "harness-output" {
		t.Fatal("workspace did not survive")
	}
	manager.Exec(t, ctx, "sh", "-ec", `test ! -e .dsh/skills/deleted-user-skill/SKILL.md; test ! -e .dsh/skills/skill-creator; test -f .agents/skills/manual/SKILL.md`)
	if calls := modelCalls.Load(); calls < 5 {
		t.Fatalf("expected model calls for two new turns and a resumed turn, got %d", calls)
	}
}

func assertDockerSkills(t *testing.T, journal []byte, project, shared bool) {
	t.Helper()
	var catalog string
	var invocation string
	results := make(map[string]webObservation)
	for _, line := range strings.Split(strings.TrimSpace(string(journal)), "\n")[1:] {
		var event sessionformat.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		switch event.Type {
		case "user/message":
			var row struct {
				Source  struct{ Kind string }
				Content []struct{ Text string }
			}
			if err := json.Unmarshal(event.Data, &row); err != nil {
				t.Fatal(err)
			}
			if len(row.Content) == 1 && row.Source.Kind == "skill-catalog" {
				catalog = row.Content[0].Text
			}
			if len(row.Content) == 1 && row.Source.Kind == "skill-invocation" {
				invocation = row.Content[0].Text
			}
		case "tool/result":
			var result webObservation
			if err := json.Unmarshal(event.Data, &result); err != nil {
				t.Fatal(err)
			}
			results[result.Message.ToolCallID] = result
		}
	}
	if !strings.Contains(catalog, "`officecli-docx`") || !strings.Contains(catalog, "`morph-ppt-3d`") || !strings.Contains(catalog, "`skill-creator`") ||
		strings.Contains(catalog, "`project-fixture`") != project || strings.Contains(catalog, "`manual-fixture`") ||
		strings.Contains(invocation, "Explicit user instructions") != shared {
		t.Fatalf("unexpected first skill catalog or invocation: %q / %q", catalog, invocation)
	}
	want := map[string]struct {
		text   string
		failed bool
	}{
		"office-docx":    {`<skill_content name="officecli-docx">`, false},
		"skill-creator":  {`<skill_content name="skill-creator">`, false},
		"unknown-skill":  {`Error: skill "not-installed" is unknown or no longer available`, true},
		"bad-skill-args": {`Error: invalid arguments: "name" must be a string`, true},
	}
	if project {
		want["project-skill"] = struct {
			text   string
			failed bool
		}{"Latest project instructions", false}
	}
	for id, expected := range want {
		actual := results[id]
		if len(actual.Message.Content) != 1 || !strings.Contains(actual.Message.Content[0].Text, expected.text) ||
			actual.Message.IsError != expected.failed || len(actual.Meta) != 0 {
			t.Fatalf("unexpected skill result %s: %+v", id, actual)
		}
	}
	equalWebJSON(t, results["bad-skill-args"].Error, `{"name":"ToolArgsError","code":"INVALID_ARGS"}`)
}
