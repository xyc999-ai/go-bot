package hostruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-bot/harness/agent"
	"go-bot/harness/agentpreset"
	"go-bot/harness/assembly"
	"go-bot/harness/session"
	"go-bot/internal/modeldata"
)

type presetRuntimeModel struct {
	mu      sync.Mutex
	prompts []string
}

type presetPublishFailureBuilder struct {
	assembly.NativeBuilder
	calls atomic.Int32
}

type presetUncertainStorage struct {
	*nativeRuntimeStore
	fail atomic.Bool
}

type presetUncertainJournal struct {
	sessionformat.EventJournal
	storage *presetUncertainStorage
}

func (j presetUncertainJournal) Append(ctx context.Context, events []sessionformat.Event) error {
	if err := j.EventJournal.Append(ctx, events); err != nil {
		return err
	}
	for _, event := range events {
		if event.Type == "agent-preset/selected" && j.storage.fail.Swap(false) {
			return errors.New("injected uncertain acknowledgement after commit")
		}
	}
	return nil
}

func (s *presetUncertainStorage) CreateNative(ctx context.Context, h sessionformat.Header) (sessionformat.EventJournal, error) {
	j, err := s.nativeRuntimeStore.CreateNative(ctx, h)
	return presetUncertainJournal{j, s}, err
}

func (s *presetUncertainStorage) OpenNative(ctx context.Context) (sessionformat.EventJournal, sessionformat.PhysicalArtifact, error) {
	j, a, err := s.nativeRuntimeStore.OpenNative(ctx)
	return presetUncertainJournal{j, s}, a, err
}

func TestPresetUncertainSelectionCommitStopsAdmissionAndReopensWithoutReplay(t *testing.T) {
	store := &presetUncertainStorage{nativeRuntimeStore: &nativeRuntimeStore{}}
	f := nativeRuntimeTestFactory(t, store.nativeRuntimeStore, &presetRuntimeModel{})
	f.Storage = store
	f.Presets = agentpreset.New(agentpreset.Standard, nil)
	for _, d := range []agentpreset.Definition{{ID: agentpreset.Standard}, {ID: "research"}} {
		if _, err := f.Presets.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	id := assembly.NativeRuntimeIdentity{UserID: "test", SessionID: "uncertain-selection", Workspace: t.TempDir()}
	r := &NativeRuntime{Factory: f, Identity: id}
	if err := r.Start(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	store.fail.Store(true)
	if err := selectIdlePreset(t, r, "research"); !errors.Is(err, sessionformat.ErrV4UncertainWrite) {
		t.Fatalf("uncertain write not propagated: %v", err)
	}
	if _, err := r.SubmitText(t.Context(), "Must not run"); err == nil {
		t.Fatal("accepted input after uncertain selection")
	}
	if err := r.Close(); !errors.Is(err, sessionformat.ErrV4UncertainWrite) {
		t.Fatalf("expected uncertain close: %v", err)
	}
	resumed, err := f.Resume(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	count := 0
	for _, event := range resumed.Session.DurableEvents() {
		if event.Type == "agent-preset/selected" {
			count++
		}
	}
	if resumed.Preset.ID() != "research" || count != 1 {
		t.Fatalf("recovery rewrote identity: %s selections=%d", resumed.Preset.ID(), count)
	}
}

func (b *presetPublishFailureBuilder) BuildNative(ctx context.Context, id assembly.NativeRuntimeIdentity, reason string) (assembly.Resources, error) {
	if b.calls.Add(1) == 3 { // initial activation, detached check, then publication
		return assembly.Resources{}, errors.New("injected publication failure")
	}
	return b.NativeBuilder.BuildNative(ctx, id, reason)
}

func TestPresetCommittedSelectionSurvivesFailedPublication(t *testing.T) {
	store := &nativeRuntimeStore{}
	f := nativeRuntimeTestFactory(t, store, &presetRuntimeModel{})
	f.Presets = agentpreset.New(agentpreset.Standard, nil)
	for _, d := range []agentpreset.Definition{{ID: agentpreset.Standard}, {ID: "research"}} {
		if _, err := f.Presets.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	f.Builder = &presetPublishFailureBuilder{NativeBuilder: f.Builder}
	id := assembly.NativeRuntimeIdentity{UserID: "test", SessionID: "publish-failure", Workspace: t.TempDir()}
	r := &NativeRuntime{Factory: f, Identity: id}
	if err := r.Start(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := selectIdlePreset(t, r, "research"); err == nil {
		t.Fatal("publication failure was ignored")
	}
	if _, err := r.SubmitText(t.Context(), "Must not use the old Agent"); err == nil {
		t.Fatal("old binding remained runnable")
	}
	_, artifact, err := store.OpenNative(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	selected, err := sessionformat.EffectiveAgentPreset(artifact.Header, artifact.Events)
	if err != nil || selected != "research" {
		t.Fatal("committed identity was lost", selected, err)
	}
	resumed, err := f.Resume(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if resumed.Preset.ID() != "research" {
		t.Fatal("recovery fell back to creation preset")
	}
}

func TestRemovedPresetKeepsHistoryReadableButCannotResumeExecution(t *testing.T) {
	host, _ := lifecycleFileHost(t)
	registry := agentpreset.New("research", nil)
	if _, err := registry.Register(agentpreset.Definition{ID: "research"}); err != nil {
		t.Fatal(err)
	}
	build := host.Build
	host.Build = func(id string, resume bool) (*NativeRuntime, error) {
		r, err := build(id, resume)
		if err == nil {
			r.Factory.Presets = registry
		}
		return r, err
	}
	if _, err := host.Activate(t.Context(), "removed-preset", false); err != nil {
		t.Fatal(err)
	}
	if err := host.StopSession(t.Context(), "removed-preset"); err != nil {
		t.Fatal(err)
	}
	if err := registry.Remove("research"); err != nil {
		t.Fatal(err)
	}
	selected, err := host.PresetForSession(t.Context(), "removed-preset")
	if err != nil || selected != "research" {
		t.Fatal("cold history required an executable preset", selected, err)
	}
	if _, err := host.Activate(t.Context(), "removed-preset", true); !errors.Is(err, agentpreset.ErrNotFound) {
		t.Fatalf("removed definition did not block execution: %v", err)
	}
}

func (m *presetRuntimeModel) Complete(_ context.Context, messages []agent.Message, _ []agent.ToolSpec) (modeldata.AssistantMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, message := range messages {
		if message.Role == "system" {
			m.prompts = append(m.prompts, message.Content)
		}
	}
	return modeldata.AssistantMessage{Content: "done"}, nil
}

func selectIdlePreset(t *testing.T, r *NativeRuntime, id string) error {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		err := r.SelectAgentPreset(t.Context(), id)
		if !errors.Is(err, ErrHostSessionBusy) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBlankPresetSelectionPersistsWithoutStartingTurnAndColdResumes(t *testing.T) {
	host, store := lifecycleFileHost(t)
	registry := agentpreset.New(agentpreset.Standard, nil)
	for _, d := range []agentpreset.Definition{{ID: agentpreset.Standard}, {ID: "research", Persona: agentpreset.PersonaSpec{Prefix: "Research identity."}}} {
		if _, err := registry.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	model := &presetRuntimeModel{}
	build := host.Build
	host.Build = func(id string, resume bool) (*NativeRuntime, error) {
		r, e := build(id, resume)
		if e == nil {
			r.Factory.Presets = registry
			r.Factory.Model = model
		}
		return r, e
	}
	seed := []sessionformat.Event{
		{Type: "system/message", Seq: 0, Time: 1, Data: json.RawMessage(`{"message":{"id":"initial","role":"system","source":{"kind":"go-bot/initial-system"},"content":[{"type":"text","text":"Help"}]}}`), SurfaceOp: json.RawMessage(`"append"`)},
		{Type: "assistant/message", Seq: 1, Time: 2, Data: json.RawMessage(`{"stream":[],"message":{"id":"welcome","role":"assistant","source":{"kind":"go-bot/welcome"},"content":[{"type":"text","text":"Welcome"}]}}`), SurfaceOp: json.RawMessage(`"append"`)},
	}
	r, err := host.ActivateWithSessionOptions(t.Context(), "selectable", assembly.NativeSessionOptions{Seed: seed}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = selectIdlePreset(t, r, "research"); err != nil {
		t.Fatal(err)
	}
	if err = selectIdlePreset(t, r, "research"); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.ReadArtifact(t.Context(), "selectable")
	if err != nil {
		t.Fatal(err)
	}
	id, err := sessionformat.EffectiveAgentPreset(artifact.Header, artifact.Events)
	if err != nil || id != "research" || artifact.Header.AgentPreset != agentpreset.Standard {
		t.Fatalf("header/projection: %+v %s %v", artifact.Header, id, err)
	}
	selections := 0
	for _, event := range artifact.Events {
		if event.Type == "agent-preset/selected" {
			selections++
		}
		if event.Type == "turn/start" {
			t.Fatal("selection started a turn")
		}
	}
	if selections != 1 {
		t.Fatalf("selections=%d", selections)
	}
	model.mu.Lock()
	calls := len(model.prompts)
	model.mu.Unlock()
	if calls != 0 {
		t.Fatal("selection invoked the model")
	}
	if err := host.StopSession(t.Context(), "selectable"); err != nil {
		t.Fatal(err)
	}
	r, err = host.Activate(t.Context(), "selectable", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.SubmitText(t.Context(), "First real request"); err != nil {
		t.Fatal(err)
	}
	waitNativeTurn(t, r, 1)
	model.mu.Lock()
	prompt := model.prompts[len(model.prompts)-1]
	model.mu.Unlock()
	if !strings.Contains(prompt, "Research identity.") {
		t.Fatal("cold resume used creation header instead of selected preset", prompt)
	}
	if err = r.SelectAgentPreset(t.Context(), agentpreset.Standard); !errors.Is(err, agentpreset.ErrLocked) {
		t.Fatalf("started session selection: %v", err)
	}
}

func TestFailedPresetPreparationLeavesOldBindingAndJournalUntouched(t *testing.T) {
	host, store := lifecycleFileHost(t)
	registry := agentpreset.New(agentpreset.Standard, nil)
	for _, d := range []agentpreset.Definition{{ID: agentpreset.Standard}, {ID: "broken", LocalTools: []string{"missing-provider"}}} {
		if _, err := registry.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	build := host.Build
	host.Build = func(id string, resume bool) (*NativeRuntime, error) {
		r, e := build(id, resume)
		if e == nil {
			r.Factory.Presets = registry
		}
		return r, e
	}
	r, err := host.Activate(t.Context(), "refused", false)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.ReadArtifact(t.Context(), "refused")
	if err != nil {
		t.Fatal(err)
	}
	if err = selectIdlePreset(t, r, "broken"); err == nil {
		t.Fatal("accepted missing provider")
	}
	after, err := store.ReadArtifact(t.Context(), "refused")
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Events) != len(after.Events) {
		t.Fatal("failed preparation wrote selection facts")
	}
	r.mu.Lock()
	id := r.live.Preset.ID()
	fatal := r.fatalErr
	r.mu.Unlock()
	if id != agentpreset.Standard || fatal != nil {
		t.Fatal("failed preparation destroyed old Agent", id, fatal)
	}
	if _, err = r.SubmitText(t.Context(), "Still usable"); err != nil {
		t.Fatal(err)
	}
	waitNativeTurn(t, r, 1)
}
