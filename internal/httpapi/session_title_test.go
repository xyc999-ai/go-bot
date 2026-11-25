package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go-bot/harness/agent"
	"go-bot/harness/assembly"
	"go-bot/harness/session"
	"go-bot/harness/skills"
	"go-bot/harness/tools"
	"go-bot/internal/agentfs"
	"go-bot/internal/executor"
	"go-bot/internal/hostruntime"
	"go-bot/internal/modeldata"
)

type titleHTTPModel struct{}

func (titleHTTPModel) Complete(context.Context, []agent.Message, []agent.ToolSpec) (modeldata.AssistantMessage, error) {
	return modeldata.AssistantMessage{Content: "Done."}, nil
}

type titleHTTPBuilder struct{ root string }

func (b titleHTTPBuilder) BuildNative(context.Context, assembly.NativeRuntimeIdentity, string) (assembly.Resources, error) {
	return assembly.Resources{Files: agentfs.ContainerView{DefaultWorkdir: b.root}, Shell: titleHTTPBuilder{}, Skills: &skills.Registry{}}, nil
}
func (titleHTTPBuilder) Run(context.Context, string, string, int) (tools.ShellResult, error) {
	return tools.ShellResult{}, errors.New("unexpected shell call")
}

func TestSessionTitleHTTPPersistsColdRenameAndPublishesToHostStream(t *testing.T) {
	root := t.TempDir()
	store := sessionformat.FileStore{Root: filepath.Join(root, "sessions"), ProjectCWD: root, Claim: func(context.Context, string) (func(), error) { return func() {}, nil }}
	host := &hostruntime.NativeHost{UserID: "title-qa", Mode: "docker"}
	host.Build = func(id string, _ bool) (*hostruntime.NativeRuntime, error) {
		return &hostruntime.NativeRuntime{
			Factory:        assembly.NativeFactory{Storage: executor.NativeFileStorage{Store: store, SessionID: id}, Builder: titleHTTPBuilder{root}, Model: titleHTTPModel{}, Provider: "p", ModelID: "m", SystemPrompt: "Help.", Limits: agent.Limits{MaxSteps: 2, MaxToolCalls: 2}},
			Identity:       assembly.NativeRuntimeIdentity{UserID: host.UserID, SessionID: id, Workspace: root},
			TitleGenerator: func(context.Context, agent.ModelRoute, string) (string, error) { return "Generated report title", nil },
		}, nil
	}
	defer host.Close(context.Background())
	reader := &sessionformat.FileHistoryReader{}
	host.ReadHistory = func(ctx context.Context, id string, q sessionformat.HistoryRequest) (sessionformat.HistoryWindow, error) {
		return reader.Read(ctx, store, id, q)
	}
	id := uuid.NewString()
	worker, err := host.Activate(t.Context(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = worker.SubmitText(t.Context(), "Create a report about AI"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		window, err := host.SessionHistoryWindow(t.Context(), id, sessionformat.HistoryRequest{Kind: sessionformat.HistoryState})
		if err != nil {
			t.Fatal(err)
		}
		if string(window.Views["title"]) == `"Generated report title"` {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("title generation did not persist")
		}
		time.Sleep(time.Millisecond)
	}
	if err := host.StopSession(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewNativeHostHTTP(host, NativeWorkerHTTPOptions{}))
	defer server.Close()
	put := func(title string) *httptest.ResponseRecorder {
		out := httptest.NewRecorder()
		data, _ := json.Marshal(map[string]string{"title": title})
		NewNativeHostHTTP(host, NativeWorkerHTTPOptions{}).ServeHTTP(out, httptest.NewRequest(http.MethodPut, "/v1/sessions/"+id+"/title", strings.NewReader(string(data))))
		return out
	}
	if out := put("  A\nchosen title "); out.Code != 200 || !strings.Contains(out.Body.String(), "A chosen title") {
		t.Fatal(out.Code, out.Body.String())
	}
	if out := put(" \n\t"); out.Code != 400 {
		t.Fatal("empty title accepted", out.Code)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/host/stream?cursors="+`%7B%22`+id+`%22%3Anull%7D`, nil)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	seen := false
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		var event struct {
			SessionID, Type string
			Data            struct {
				Type string
				Data struct{ Title string }
			}
		}
		if json.Unmarshal([]byte(data), &event) == nil && event.SessionID == id && event.Type == "session" && event.Data.Type == "session/title" && event.Data.Data.Title == "A chosen title" {
			seen = true
			break
		}
	}
	if !seen {
		t.Fatal("renamed title missing from Host SSE", scanner.Err())
	}
	response.Body.Close()
	cancel()
	window, err := host.SessionHistoryWindow(t.Context(), id, sessionformat.HistoryRequest{Kind: sessionformat.HistoryState})
	if err != nil || string(window.Views["title"]) != `"A chosen title"` {
		t.Fatal("rename lost after cold activation", window.Views, err)
	}
}
