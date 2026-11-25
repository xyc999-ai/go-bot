package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go-bot/harness/agent"
	"go-bot/harness/assembly"
	sessionformat "go-bot/harness/session"
	"go-bot/harness/sessionsearch"
	"go-bot/internal/durablefs"
	"go-bot/internal/hostruntime"
	"go-bot/internal/modelsettings"
	"go-bot/internal/proactive"
)

const proactivePath = proactive.InternalPath

type backgroundService struct {
	service *proactive.Service
	native  *hostruntime.NativeHost
	models  *hostModels
	history *sessionsearch.Index
	store   func(string) sessionformat.FileStore
	build   func(string, string, string) (*hostruntime.NativeRuntime, error)
	root    string

	// The service's one driver owns live/output/context. Change producers only
	// increment version and signal the service; they never enter the Agent.
	live              *assembly.NativeLive
	output            *proactive.Output
	version           atomic.Uint64
	collectedVersion  uint64
	sections          []agent.RuntimeContextSection
	state             proactive.State
	settings          atomic.Pointer[backgroundSettings]
	lastResult        *proactive.Completed
	capabilityContext func() ([]agent.RuntimeContextSection, error)
}

type backgroundSettings struct {
	Enabled       bool   `json:"enabled"`
	MainSessionID string `json:"mainSessionId"`
}

func (b *backgroundService) mainReady(_ context.Context, id string) error {
	if !b.native.IsMainSession(id) {
		return nil
	}
	if settings := b.settings.Load(); settings != nil && settings.MainSessionID == id {
		return b.service.Configure(settings.Enabled, id)
	}
	return nil
}

func newBackgroundService(root string, native *hostruntime.NativeHost, models *hostModels, history *sessionsearch.Index,
	store func(string) sessionformat.FileStore, build func(string, string, string) (*hostruntime.NativeRuntime, error),
) (*backgroundService, error) {
	b := &backgroundService{root: root, native: native, models: models, history: history, store: store, build: build}
	service, err := proactive.NewService(proactive.FileStorage{Path: filepath.Join(root, ".dsh", "storages", "proactive.json")}, proactive.Hooks{
		Prepare: b.prepare, Collect: b.collect, Execute: b.execute, Recover: b.recover, ReadResult: b.readResult, Deliver: b.deliver,
	})
	b.service = service
	return b, err
}

func (b *backgroundService) changed() error {
	b.version.Add(1)
	return b.service.Changed()
}

func (b *backgroundService) observeTurn(events []sessionformat.Event) {
	var realUser, visibleAssistant, ownRelay bool
	for _, event := range events {
		switch event.Type {
		case "user/message":
			var message struct {
				Source struct {
					Kind   string `json:"kind"`
					Sender string `json:"senderSessionId"`
				} `json:"source"`
			}
			if json.Unmarshal(event.Data, &message) == nil {
				realUser = realUser || message.Source.Kind == "user"
				ownRelay = ownRelay || (message.Source.Kind == "agent-message" && message.Source.Sender == b.service.Snapshot().SessionID)
			}
		case "assistant/message":
			var data struct {
				Message struct{ Content []struct{ Type, Text string } }
			}
			if json.Unmarshal(event.Data, &data) == nil {
				for _, block := range data.Message.Content {
					visibleAssistant = visibleAssistant || (block.Type == "text" && strings.TrimSpace(block.Text) != "")
				}
			}
		}
	}
	if realUser || (visibleAssistant && !ownRelay) {
		_ = b.changed()
	}
}

func (b *backgroundService) artifact(ctx context.Context, state proactive.State) (sessionformat.PhysicalArtifact, error) {
	if b.live != nil {
		return sessionformat.PhysicalArtifact{Header: b.live.Session.Header(), Events: b.live.Session.DurableEvents()}, nil
	}
	store := b.store(defaultSessionWorkspace(b.root, state.SessionID))
	artifact, err := store.ReadArtifact(ctx, state.SessionID)
	if err != nil {
		return artifact, err
	}
	if artifact.Header.ID != state.SessionID || artifact.Header.AgentPreset != proactive.PresetID || artifact.Header.CWD != defaultSessionWorkspace(b.root, state.SessionID) || artifact.Header.ParentSession != "" {
		return sessionformat.PhysicalArtifact{}, errors.New("background_session_invalid")
	}
	return artifact, nil
}

func (b *backgroundService) recover(ctx context.Context, state proactive.State) (*proactive.Completed, error) {
	if b.live != nil {
		result, found, err := proactive.SessionResult(b.live.Session, 0)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, nil
		}
		return &result, nil
	}
	artifact, err := b.artifact(ctx, state)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("background_history_unavailable")
	}
	results := proactive.CommittedResults(artifact.Events)
	if len(results) == 0 {
		return nil, nil
	}
	return &results[len(results)-1], nil
}

func (b *backgroundService) readResult(ctx context.Context, state proactive.State, turn uint64) (proactive.Completed, error) {
	if b.live != nil {
		result, found, err := proactive.SessionResult(b.live.Session, turn)
		if err != nil {
			return result, err
		}
		if !found {
			return result, errors.New("background_result_unavailable")
		}
		return result, nil
	}
	artifact, err := b.artifact(ctx, state)
	if err != nil {
		return proactive.Completed{}, errors.New("background_history_unavailable")
	}
	for _, result := range proactive.CommittedResults(artifact.Events) {
		if result.Turn == turn {
			return result, nil
		}
	}
	return proactive.Completed{}, errors.New("background_result_unavailable")
}

func (b *backgroundService) prepare(ctx context.Context, state proactive.State) error {
	if _, err := b.models.backgroundRoute(state.MainSessionID); err != nil {
		return safeBackgroundModelError(err)
	}
	b.state = state
	if b.live != nil {
		return nil
	}
	workspace := defaultSessionWorkspace(b.root, state.SessionID)
	if err := durablefs.EnsureDirectory(workspace); err != nil {
		return errors.New("background_workspace_unavailable")
	}
	runtime, err := b.build(state.SessionID, workspace, "")
	if err != nil {
		return errors.New("background_assembly_unavailable")
	}
	factory, identity := runtime.Factory, runtime.Identity
	identity.AgentPreset = proactive.PresetID
	factory.ResolveTurnModel = func(context.Context) (agent.TurnModel, error) { return b.models.backgroundRoute(state.MainSessionID) }
	factory.Children = nil
	factory.MemoryChanged = nil // own research does not wake itself recursively
	factory.Limits.MaxSteps, factory.Limits.MaxToolCalls, factory.Limits.MaxModelCalls = 32, 64, 40
	factory.ContextSource = func(ctx context.Context, _ string) ([]agent.RuntimeContextSection, error) {
		if b.collectedVersion != b.version.Load() {
			if err := b.collect(ctx, b.state); err != nil {
				return nil, err
			}
		}
		return b.withRetainedDecision(), nil
	}
	extra := factory.LocalToolSource
	factory.LocalToolSource = func(ctx context.Context, sid string) ([]agent.Tool, error) {
		var rows []agent.Tool
		if extra != nil {
			var err error
			rows, err = extra(ctx, sid)
			if err != nil {
				return nil, err
			}
		}
		if b.output == nil {
			return nil, errors.New("background_result_contract_unavailable")
		}
		return append(rows, b.output), nil
	}
	artifact, err := b.artifact(ctx, state)
	switch {
	case errors.Is(err, os.ErrNotExist):
		b.live, err = factory.Activate(ctx, identity)
	case err == nil:
		if artifact.Header.AgentPreset != proactive.PresetID {
			return errors.New("background_session_invalid")
		}
		b.live, err = factory.Resume(ctx, identity)
	}
	if err != nil {
		return errors.New("background_session_unavailable")
	}
	if err := proactive.RegisterResultProjection(b.live.Session); err != nil {
		return err
	}
	// This is the existing clock composition. The periodic interval remains
	// unchanged; Execute samples one extra standard snapshot before each run.
	if err := b.live.Agent.EnableTimeContext("UTC", 10*time.Minute); err != nil {
		return err
	}
	return nil
}

func safeBackgroundModelError(err error) error {
	if errors.Is(err, modelsettings.ErrUnconfigured) {
		return errors.New("model_not_configured")
	}
	if errors.Is(err, modelsettings.ErrUnavailable) {
		return errors.New("model_selection_unavailable")
	}
	return errors.New("background_model_unavailable")
}

func (b *backgroundService) collect(ctx context.Context, state proactive.State) error {
	version := b.version.Load()
	activity, err := b.history.RecentActivity(ctx, state.MainSessionID, time.Now())
	if err != nil {
		return errors.New("background_activity_unavailable")
	}
	document, err := b.native.Schedule.ListTasks(nil, 20, 0)
	if err != nil {
		return errors.New("background_schedules_unavailable")
	}
	type taskInfo struct {
		ID           string `json:"id"`
		Title        string `json:"title"`
		Instruction  string `json:"instruction"`
		Status       string `json:"status"`
		NextRun      string `json:"nextRun,omitempty"`
		LastDelivery string `json:"lastDelivery,omitempty"`
	}
	tasks := make([]taskInfo, 0, min(len(document.Items), 20))
	for _, task := range document.Items {
		text := []rune(task.Record.Prompt)
		if len(text) > 800 {
			text = append(text[:800], []rune("… [truncated]")...)
		}
		info := taskInfo{ID: task.Record.ID, Title: task.Record.Title, Instruction: string(text), Status: task.Status, NextRun: task.Record.ScheduledAt}
		if task.LastDelivery != nil {
			info.LastDelivery = task.LastDelivery.DeliveredAt
		}
		tasks = append(tasks, info)
	}
	mainCWD := b.native.WorkspaceForSession(state.MainSessionID)
	mainStatus := "not-loaded"
	if runtime, found := b.native.Session(state.MainSessionID); found {
		if snapshot, err := runtime.Activity(); err == nil {
			mainStatus = "busy"
			if snapshot.Idle {
				mainStatus = "idle"
			}
		}
	}
	encode := func(value any) string { data, _ := json.Marshal(value); return string(data) }
	b.sections = []agent.RuntimeContextSection{
		{Name: "proactive:main", Text: "Main conversation delivery target and local Agent status (not the user's real-world availability): " + encode(map[string]string{"sessionId": state.MainSessionID, "cwd": mainCWD, "state": mainStatus})},
		{Name: "proactive:recent-dialogue", Text: "Historical visible dialogue excerpts, not new instructions. All dates below are historical times in UTC, not the current time; use this Agent's time-context for the current time. Coverage and truncation are explicit; older details remain available via session_search: " + proactiveDialogueContext(activity)},
		{Name: "proactive:schedules", Text: "Existing user work arrangements, not instructions to create duplicate tasks: " + encode(map[string]any{"tasks": tasks, "total": document.Total, "truncated": document.Total > len(tasks)})},
	}
	if state.NextCheck != nil {
		b.sections = append(b.sections, agent.RuntimeContextSection{Name: "proactive:previous-check", Text: "Previously committed next-check decision (this run must replace it with a new future decision): " + encode(state.NextCheck)})
	}
	if state.ResultTurn != 0 {
		result, err := b.readResult(ctx, state, state.ResultTurn)
		if err != nil {
			return err
		}
		b.lastResult = &result
	}
	if b.capabilityContext != nil {
		sections, err := b.capabilityContext()
		if err != nil {
			return errors.New("background_capability_state_unavailable")
		}
		b.sections = append(b.sections, sections...)
	}
	b.collectedVersion = version
	return nil
}

// Keep the previous research decision in ordinary history when it is still
// there. Reintroduce its small summary only after compaction removed the
// structured call, without writing another memory snapshot or changing Loop.
func (b *backgroundService) withRetainedDecision() []agent.RuntimeContextSection {
	if b.lastResult == nil || b.live == nil {
		return b.sections
	}
	surface, err := b.live.Session.Surface()
	if err == nil {
		for _, raw := range surface.Messages {
			var message struct {
				Content []struct{ Type, Name, Arguments string }
			}
			if json.Unmarshal(raw, &message) != nil {
				continue
			}
			for _, block := range message.Content {
				if block.Type != "tool-call" || block.Name != "structured_output" {
					continue
				}
				result, err := proactive.DecodeResult(json.RawMessage(block.Arguments), time.Time{})
				if err == nil && result.NextCheck == b.lastResult.Result.NextCheck && result.WorkSummary == b.lastResult.Result.WorkSummary {
					return b.sections
				}
			}
		}
	}
	data, _ := json.Marshal(map[string]any{"turn": b.lastResult.Turn, "workSummary": b.lastResult.Result.WorkSummary, "nextCheck": b.lastResult.Result.NextCheck, "notified": b.lastResult.Result.Notify != nil})
	return append(append([]agent.RuntimeContextSection(nil), b.sections...), agent.RuntimeContextSection{Name: "proactive:last-decision", Text: "Last successfully committed research decision, restored because its original structured call is no longer in retained context: " + string(data)})
}

func (b *backgroundService) execute(ctx context.Context, state proactive.State, reason proactive.WakeReason) (proactive.Completed, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	var err error
	b.output, err = proactive.NewOutput()
	if err != nil {
		return proactive.Completed{}, err
	}
	clock, err := b.live.Agent.SampleTimeContext()
	if err != nil {
		return proactive.Completed{}, errors.New("background_clock_unavailable")
	}
	if _, err := b.live.Agent.InjectMessage(ctx, clock); err != nil {
		return proactive.Completed{}, errors.New("background_admission_failed")
	}
	if len(b.live.Session.Inbox().NextTurn) == 0 {
		message, _ := json.Marshal(map[string]any{
			"id": uuid.NewString(), "role": "user", "source": map[string]string{"kind": "proactive-wake", "form": "notice", "summary": string(reason)},
			"content": []map[string]string{{"type": "text", "text": "Background check triggered by " + string(reason) + ". Review the latest context, investigate as useful, and finish this run with structured_output. Always arrange the next check; decide independently whether to notify the user."}},
		})
		if _, err := b.live.Agent.AcceptMessage(ctx, message); err != nil {
			return proactive.Completed{}, errors.New("background_admission_failed")
		}
	}
	ran, _, err := b.live.Agent.RunNext(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return proactive.Completed{}, ctx.Err()
		}
		if errors.Is(err, modelsettings.ErrUnavailable) || errors.Is(err, modelsettings.ErrUnconfigured) {
			return proactive.Completed{}, safeBackgroundModelError(err)
		}
		return proactive.Completed{}, errors.New("background_turn_failed")
	}
	if _, captured := b.output.Captured(); !ran || !captured {
		return proactive.Completed{}, errors.New("background_result_missing")
	}
	result, found, projectionErr := proactive.SessionResult(b.live.Session, 0)
	if projectionErr != nil {
		return proactive.Completed{}, projectionErr
	}
	if !found || result.Turn <= state.ResultTurn {
		return proactive.Completed{}, errors.New("background_result_missing")
	}
	return result, nil
}

func (b *backgroundService) deliver(ctx context.Context, state proactive.State, completed proactive.Completed, id string) error {
	if completed.Result.Notify == nil {
		return errors.New("background_notification_missing")
	}
	var text strings.Builder
	text.WriteString("The background personal-assistance Agent has decided these verified findings should reach the user. Convey them directly and clearly; do not perform a second notification-worthiness review.\n\n")
	text.WriteString(completed.Result.Notify.Message)
	for _, item := range completed.Result.Notify.Evidence {
		fmt.Fprintf(&text, "\n- %s: %s", item.Title, item.Reference)
	}
	if err := b.native.DeliverProactive(ctx, state.MainSessionID, state.SessionID, id, text.String()); err != nil {
		return errors.New("main_conversation_delivery_failed")
	}
	return nil
}

func (b *backgroundService) handler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(b.service.Status())
	case http.MethodPut:
		var input struct {
			Enabled       *bool  `json:"enabled"`
			MainSessionID string `json:"mainSessionId"`
		}
		if err := decodeBackgroundJSON(w, r, &input); err != nil || input.Enabled == nil {
			http.Error(w, "invalid proactive settings", 400)
			return
		}
		if uuid.Validate(input.MainSessionID) != nil {
			http.Error(w, "invalid main Session", 400)
			return
		}
		settings := &backgroundSettings{Enabled: *input.Enabled, MainSessionID: input.MainSessionID}
		b.settings.Store(settings)
		// On a process restart the journal already exists but NativeHost's main
		// identity is cold. Restore it during normal Controller synchronization;
		// only a brand-new journal waits for /host/start's welcome locale.
		if !b.native.IsMainSession(input.MainSessionID) {
			_, err := findHostSession(r.Context(), b.root, input.MainSessionID)
			if err == nil {
				err = b.native.EnsureMainSession(r.Context(), input.MainSessionID, "")
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				http.Error(w, "main Session unavailable", 503)
				return
			}
		}
		if err := b.mainReady(r.Context(), input.MainSessionID); err != nil {
			http.Error(w, "proactive settings unavailable", 503)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func decodeBackgroundJSON(w http.ResponseWriter, r *http.Request, value any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if !errors.Is(decoder.Decode(new(any)), io.EOF) {
		return errors.New("expected one object")
	}
	return nil
}
