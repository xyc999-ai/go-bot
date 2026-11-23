package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"go-bot/harness/agent"
	"go-bot/harness/assembly"
	"go-bot/harness/schedule"
	sessionformat "go-bot/harness/session"
	"go-bot/harness/sessionsearch"
	"go-bot/harness/skills"
	"go-bot/harness/tools"
	"go-bot/internal/agentfs"
	"go-bot/internal/executor"
	"go-bot/internal/hostruntime"
	"go-bot/internal/modelsettings"
	"go-bot/internal/proactive"
	"go-bot/providers/model/mainmodel"
)

type backgroundTestBuilder struct{ root string }

func (b backgroundTestBuilder) BuildNative(_ context.Context, id assembly.NativeRuntimeIdentity, _ string) (assembly.Resources, error) {
	return assembly.Resources{Files: agentfs.ContainerView{DefaultWorkdir: id.Workspace}, Shell: backgroundTestShell{}, Skills: &skills.Registry{DSHHome: filepath.Join(b.root, ".dsh")}, DefaultWorkdir: id.Workspace}, nil
}

type backgroundTestShell struct{}

func (backgroundTestShell) Run(context.Context, string, string, int) (tools.ShellResult, error) {
	return tools.ShellResult{}, errors.New("test must not invoke a real shell")
}

type backgroundFixture struct {
	b         *backgroundService
	native    *hostruntime.NativeHost
	history   *sessionsearch.Index
	models    *hostModels
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan error
	closeOnce sync.Once
}

func newBackgroundFixture(t *testing.T, root, main, endpoint string, prepareMain ...bool) *backgroundFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	claims := &sessionformat.ProcessClaims{}
	store := func(cwd string) sessionformat.FileStore {
		return sessionformat.FileStore{Root: sessionLogRoot(root), ProjectCWD: cwd, Claim: claims.Acquire}
	}
	native := &hostruntime.NativeHost{UserID: "test", Mode: "host"}
	native.PrepareMain = func(ctx context.Context, id, locale string) error {
		return ensureMainJournal(ctx, root, id, locale, claims.Acquire)
	}
	native.WorkspaceForSession = func(id string) string { header, _ := findHostSession(ctx, root, id); return header.CWD }
	native.Archived = func(string) (bool, error) { return false, nil }
	native.ReadTitle = func(string) string { return "Conversation" }
	native.ReadSession = func(ctx context.Context, id string) (sessionformat.PhysicalArtifact, error) {
		header, err := findHostSession(ctx, root, id)
		if err != nil {
			return sessionformat.PhysicalArtifact{}, err
		}
		return store(header.CWD).ReadArtifact(ctx, id)
	}
	native.ReadSessionHeader = func(ctx context.Context, id string) (sessionformat.Header, error) {
		return findHostSession(ctx, root, id)
	}
	native.SessionLogInfo = func(id string) (os.FileInfo, error) {
		header, err := findHostSession(ctx, root, id)
		if err != nil {
			return nil, err
		}
		path, err := store(header.CWD).Path(id)
		if err != nil {
			return nil, err
		}
		return os.Stat(path)
	}
	native.List = func(ctx context.Context, cursor string, limit int) (sessionformat.SessionListPage, error) {
		return listHostSessions(ctx, root, cursor, limit)
	}
	var err error
	native.Schedule, err = schedule.NewHostService(schedule.FileStore{Path: filepath.Join(root, ".dsh", "storages", "schedule.json")}, native.PrepareSchedule, native.DeliverSchedule)
	if err != nil {
		t.Fatal(err)
	}
	if err := native.Schedule.Start(ctx); err != nil {
		t.Fatal(err)
	}
	models := &hostModels{base: mainmodel.Model{Transport: http.DefaultTransport}, snapshot: modelsettings.Snapshot{Revision: 1, Catalog: modelsettings.Catalog{DefaultEndpointID: "test", Endpoints: map[string]modelsettings.Endpoint{"test": {Name: "Test", APIKey: "local-test-key", BaseURL: endpoint, Protocol: modelsettings.Responses, Models: []string{"test-model"}}}}}}
	if err := models.loadReservations(filepath.Join(root, ".dsh", "storages", "scheduled-models")); err != nil {
		t.Fatal(err)
	}
	history, err := sessionsearch.Open(ctx, filepath.Join(root, "index.sqlite"), "test", sessionsearch.Source{List: native.List, Snapshot: historySnapshot(native), Title: native.ReadTitle})
	if err != nil {
		t.Fatal(err)
	}
	var b *backgroundService
	build := func(id, cwd, preset string) (*hostruntime.NativeRuntime, error) {
		return &hostruntime.NativeRuntime{
			Factory: assembly.NativeFactory{Storage: executor.NativeFileStorage{Store: store(cwd), SessionID: id}, Builder: backgroundTestBuilder{root: root}, Model: models.base,
				ResolveTurnModel: func(context.Context) (agent.TurnModel, error) { return models.backgroundRoute(main) }, ResolveSelectedModel: models.selected,
				Provider: "test", ModelID: "test-model", SystemPrompt: assembly.DefaultCoreSystemPrompt, Schedule: native.Schedule, SessionSearch: history, Limits: agent.Limits{MaxSteps: 4, MaxToolCalls: 8, MaxModelCalls: 8}},
			Identity: assembly.NativeRuntimeIdentity{UserID: "test", SessionID: id, Workspace: cwd, AgentPreset: preset}, HostOwnsSchedule: true,
			UserMessageAccepted: func() {
				if b != nil {
					_ = b.changed()
				}
			},
			TurnCommitted: func(events []sessionformat.Event) {
				if b != nil {
					b.observeTurn(events)
				}
			},
		}, nil
	}
	native.Build = func(id string, resume bool) (*hostruntime.NativeRuntime, error) {
		cwd := defaultSessionWorkspace(root, id)
		if resume {
			header, err := findHostSession(ctx, root, id)
			if err != nil {
				return nil, err
			}
			cwd = header.CWD
		}
		return build(id, cwd, "")
	}
	b, err = newBackgroundService(root, native, models, history, store, build)
	if err != nil {
		t.Fatal(err)
	}
	native.MainReady = b.mainReady
	if len(prepareMain) == 0 || prepareMain[0] {
		if err := native.EnsureMainSession(ctx, main, "zh-CN"); err != nil {
			t.Fatal(err)
		}
	}
	fixture := &backgroundFixture{b: b, native: native, history: history, models: models, ctx: ctx, cancel: cancel, done: make(chan error, 1)}
	go func() { fixture.done <- b.service.Run(ctx) }()
	t.Cleanup(func() { fixture.close(t) })
	return fixture
}

func (f *backgroundFixture) close(t *testing.T) {
	f.closeOnce.Do(func() {
		f.cancel()
		if err := <-f.done; err != nil {
			t.Error(err)
		}
		stop, end := context.WithTimeout(context.Background(), 5*time.Second)
		defer end()
		_ = f.native.Close(stop)
		if f.b.live != nil {
			_ = f.b.live.Close()
		}
		_ = f.history.Close()
	})
}

func waitBackground(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal("background condition did not settle")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBackgroundHostRunsQuietThenTimedNotificationWithStandardContext(t *testing.T) {
	var backgroundCalls atomic.Int64
	var mu sync.Mutex
	var requests [][]byte
	allowNotify := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Tools []struct {
				Name string `json:"name"`
			}
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		isBackground := false
		for _, tool := range req.Tools {
			isBackground = isBackground || tool.Name == "structured_output"
		}
		var output []any
		if isBackground {
			count := backgroundCalls.Add(1)
			mu.Lock()
			requests = append(requests, append([]byte(nil), body...))
			mu.Unlock()
			next := time.Now().Add(24 * time.Hour)
			var notify *proactive.Notification
			if count == 1 {
				next = time.Now().Add(150 * time.Millisecond)
			} else {
				select {
				case <-allowNotify:
				case <-r.Context().Done():
					return
				}
				notify = &proactive.Notification{Message: "AI 行业报告有一项已核实的新信息。", Evidence: []proactive.Evidence{}}
			}
			args, _ := json.Marshal(proactive.Result{WorkSummary: "Checked the user's current research context.", NextCheck: proactive.Check{At: next.Format(time.RFC3339Nano), Reason: "Review new published information."}, Notify: notify})
			output = []any{map[string]any{"type": "function_call", "id": fmt.Sprint("out-", count), "call_id": fmt.Sprint("call-", count), "name": "structured_output", "arguments": string(args)}}
		} else {
			output = []any{map[string]any{"type": "message", "id": uuid.NewString(), "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": "这是已经核实的信息。"}}}}
		}
		payload, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"id": uuid.NewString(), "status": "completed", "output": output}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", payload)
	}))
	defer backend.Close()
	root, main := t.TempDir(), uuid.NewString()
	f := newBackgroundFixture(t, root, main, backend.URL)
	defer func() {
		if t.Failed() {
			t.Logf("proactive status=%+v calls=%d", f.b.service.Status(), backgroundCalls.Load())
			state := f.b.service.Snapshot()
			store := f.b.store(defaultSessionWorkspace(root, state.SessionID))
			if artifact, err := store.ReadArtifact(context.Background(), state.SessionID); err == nil {
				for _, event := range artifact.Events {
					if event.Type == "turn/end" {
						t.Logf("turn failure: %s", event.Data)
					}
				}
			}
		}
	}()
	runtime, err := f.native.Activate(f.ctx, main, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.SubmitText(f.ctx, "我正在研究 AI 行业报告。"); err != nil {
		t.Fatal(err)
	}
	waitBackground(t, func() bool {
		events, _ := runtime.DurableSessionEvents()
		for _, e := range events {
			if e.Type == "turn/end" {
				return true
			}
		}
		return false
	})
	out := httptest.NewRecorder()
	f.b.handler(out, httptest.NewRequest(http.MethodPut, proactivePath, strings.NewReader(`{"enabled":true,"mainSessionId":"`+main+`"}`)))
	if out.Code != 204 {
		t.Fatalf("configure: %d %s", out.Code, out.Body.String())
	}
	waitBackground(t, func() bool { return f.b.service.Snapshot().ResultTurn == 1 })
	before, _ := runtime.DurableSessionEvents()
	for _, event := range before {
		if event.Type == "user/message" && strings.Contains(string(event.Data), "agent-message") {
			t.Fatal("quiet background result was forwarded")
		}
	}
	close(allowNotify)
	waitBackground(t, func() bool {
		state := f.b.service.Snapshot()
		return state.ResultTurn == 2 && state.DeliveryMessageID == ""
	})
	waitBackground(t, func() bool {
		events, _ := runtime.DurableSessionEvents()
		n := 0
		for _, e := range events {
			if e.Type == "turn/end" {
				n++
			}
		}
		return n == 2
	})
	state := f.b.service.Snapshot()
	if state.PendingChange {
		t.Fatal("main Agent's proactive reply woke the background Agent again")
	}
	page, err := listHostSessions(f.ctx, root, "", 100)
	if err != nil || len(page.Sessions) != 1 || page.Sessions[0].ID != main {
		t.Fatalf("internal Session leaked into list: %+v %v", page, err)
	}
	if _, err := findHostSession(f.ctx, root, state.SessionID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("internal Session is publicly addressable: %v", err)
	}
	activity, err := f.history.RecentActivity(f.ctx, main, time.Now())
	if err != nil || len(activity.Sessions) != 1 {
		t.Fatalf("activity: %+v %v", activity, err)
	}
	mu.Lock()
	captured := append([][]byte(nil), requests...)
	mu.Unlock()
	if len(captured) != 2 {
		t.Fatalf("background calls=%d", len(captured))
	}
	historicalTime := time.UnixMilli(activity.Sessions[0].Messages[0].Timestamp).UTC().Format("2006-01-02 15:04:05.000 MST")
	for _, raw := range captured {
		if !strings.Contains(string(raw), "Time sampled while preparing turn") || !strings.Contains(string(raw), "我正在研究 AI 行业报告") {
			t.Fatalf("missing standard time or real activity context: %s", raw)
		}
		if !strings.Contains(string(raw), "historical times in UTC") || !strings.Contains(string(raw), historicalTime) {
			t.Fatalf("historical dialogue lacks readable, labelled times: %s", raw)
		}
		for _, name := range []string{`"name":"ask_user_question"`, `"name":"create_goal"`, `"name":"schedule_create"`, `"name":"send_message"`} {
			if strings.Contains(string(raw), name) {
				t.Fatalf("unavailable interactive/background tool leaked: %s", name)
			}
		}
	}
	events, _ := runtime.DurableSessionEvents()
	id := ""
	for _, event := range events {
		if event.Type == "user/message" {
			var message struct {
				ID     string
				Source struct{ Kind string }
			}
			_ = json.Unmarshal(event.Data, &message)
			if message.Source.Kind == "agent-message" {
				id = message.ID
			}
		}
	}
	if id == "" {
		t.Fatal("notification was not recorded as an internal user/message")
	}
	completed, err := f.b.readResult(f.ctx, state, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.b.deliver(f.ctx, state, completed, id); err != nil {
		t.Fatal(err)
	}
	// A repeated I/O receipt reuses the same ID after the input was claimed.
	final, _ := runtime.DurableSessionEvents()
	relays := 0
	for _, event := range final {
		if event.Type == "user/message" && strings.Contains(string(event.Data), `"kind":"agent-message"`) {
			relays++
		}
	}
	if relays != 1 {
		t.Fatalf("notification was admitted %d times", relays)
	}
	f.close(t)
	restored := newBackgroundFixture(t, root, main, backend.URL, false)
	response := httptest.NewRecorder()
	restored.b.handler(response, httptest.NewRequest(http.MethodPut, proactivePath, strings.NewReader(`{"enabled":true,"mainSessionId":"`+main+`"}`)))
	if response.Code != 204 {
		t.Fatalf("restore configuration: %d %s", response.Code, response.Body.String())
	}
	waitBackground(t, func() bool { return restored.b.service.Status().Phase == "waiting" })
	after := restored.b.service.Snapshot()
	if !restored.native.IsMainSession(main) || after.SessionID != state.SessionID || after.ResultTurn != 2 || after.NextCheck == nil || after.NextCheck.At != state.NextCheck.At || after.DeliveryMessageID != "" || backgroundCalls.Load() != 2 {
		t.Fatalf("restart lost committed state or repeated work: %+v", after)
	}
}
