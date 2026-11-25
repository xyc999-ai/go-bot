package hostruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"go-bot/harness/agentpreset"
	"go-bot/harness/assembly"
	"go-bot/harness/permission"
	"go-bot/harness/session"
)

func TestNativeFactoryCreatesFromExactSeedAndMetadataUsingActualGoJournal(t *testing.T) {
	host, store := lifecycleFileHost(t)
	runtime, err := host.Build("programmatic", false)
	if err != nil {
		t.Fatal(err)
	}
	cwd, parent, preset := store.ProjectCWD, "parent-metadata", "reporter"
	runtime.Factory.Presets = agentpreset.New(agentpreset.Standard, nil)
	if _, err := runtime.Factory.Presets.Register(agentpreset.Definition{ID: preset}); err != nil {
		t.Fatal(err)
	}
	seeded, count := true, uint64(4)
	seed := []sessionformat.Event{
		{Type: "permission/preset", Seq: 0, Time: 1, Data: json.RawMessage(`{"preset":"read-only"}`)},
		{Type: "sandbox/mode", Seq: 1, Time: 2, Data: json.RawMessage(`{"mode":"read-only"}`)},
		{Type: "approval/policy", Seq: 2, Time: 3, Data: json.RawMessage(`{"policy":"ask"}`)},
		{Type: "plugin:qa/seed", Seq: 3, Time: 4, Ignorable: true, Data: json.RawMessage(`{"fact":"保留此事实"}`)},
	}
	options := assembly.NativeSessionOptions{Meta: &assembly.NativeSessionMeta{CWD: &cwd, ParentSession: &parent, AgentPreset: &preset, IsSeeded: &seeded}, Seed: seed, InheritedEventCount: &count, ParentSource: &assembly.NativeParentSource{SessionID: "copied-source", EndSeq: 4}}
	entered := false
	runtime.Factory.SessionReady = func(s *sessionformat.LiveSession) {
		ctx := t.Context()
		entered = true
		artifact, err := store.ReadArtifact(ctx, s.Header().ID)
		if err != nil || len(artifact.Events) != 5 || artifact.InheritedEventCount != 4 {
			t.Fatal("seed was not fully durable before Session entry", artifact, err)
		}
	}
	runtime.Factory.BeforePublish = func(_ context.Context, live *assembly.NativeLive) error {
		seed[0].Data = json.RawMessage(`{"preset":"danger-full-access"}`)
		preset = "caller-mutated"
		if live.Session.Header().AgentPreset != "reporter" || permission.Read(live.Session.Events()).Mode != permission.ReadOnly {
			return errors.New("caller mutation or default initialization replaced the prepared input")
		}
		return nil
	}
	live, err := runtime.Factory.ActivateWithSessionOptions(t.Context(), runtime.Identity, options)
	if err != nil {
		t.Fatal(err)
	}
	if !entered {
		t.Fatal("actual Session entry hook was skipped")
	}
	header := live.Session.Header()
	if header.CWD != cwd || header.ParentSession != parent || header.Origin != "" || header.AgentPreset != "reporter" {
		t.Fatal("programmatic metadata was reclassified as an internal subagent", header)
	}
	if err := live.Close(); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.ReadArtifact(t.Context(), header.ID)
	if err != nil || len(artifact.Events) != 5 || permission.Read(artifact.Events).Mode != permission.ReadOnly {
		t.Fatal("prepared facts differed after close", artifact, err)
	}
	runtime.Factory.BeforePublish = nil
	resumed, err := runtime.Factory.Resume(t.Context(), runtime.Identity)
	if err != nil {
		t.Fatal("programmatic lineage could not reopen", err)
	}
	if resumed.Session.Header() != header || resumed.Session.InheritedEventCount() != 4 {
		t.Fatal("resume changed creation metadata or inherited cut")
	}
	if err := resumed.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeFactoryRefusesInvalidCreationBeforePublishingAnyJournal(t *testing.T) {
	host, store := lifecycleFileHost(t)
	seeded := true
	for _, scenario := range []struct {
		name    string
		options assembly.NativeSessionOptions
	}{
		{"gap", assembly.NativeSessionOptions{Seed: []sessionformat.Event{{Type: "session/end-seed", Seq: 3, Time: 1, Data: json.RawMessage(`{}`)}}}},
		{"missing-cut", assembly.NativeSessionOptions{Meta: &assembly.NativeSessionMeta{IsSeeded: &seeded}, Seed: []sessionformat.Event{}}},
	} {
		runtime, err := host.Build(scenario.name, false)
		if err != nil {
			t.Fatal(err)
		}
		if live, err := runtime.Factory.ActivateWithSessionOptions(t.Context(), runtime.Identity, scenario.options); err == nil {
			_ = live.Close()
			t.Fatal("invalid creation was accepted", scenario.name)
		}
		path, err := store.Path(scenario.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("rejected creation left a canonical journal", scenario.name, err)
		}
	}
}

func TestNativeFactoryCreatesFromIndependentSubagentOrigin(t *testing.T) {
	host, _ := lifecycleFileHost(t)
	runtime, err := host.Build("origin-metadata", false)
	if err != nil {
		t.Fatal(err)
	}
	origin, parent, depth := "subagent", "metadata-parent", 1
	live, err := runtime.Factory.ActivateWithSessionOptions(t.Context(), runtime.Identity, assembly.NativeSessionOptions{Meta: &assembly.NativeSessionMeta{Origin: &origin, ParentSession: &parent, DelegationDepth: &depth}})
	if err != nil {
		t.Fatal(err)
	}
	header := live.Session.Header()
	if err := live.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := runtime.Factory.Resume(t.Context(), runtime.Identity)
	if err != nil {
		t.Fatal("generic resume incorrectly required an internal child descriptor", err)
	}
	defer resumed.Close()
	if resumed.Session.Header() != header || header.Origin != origin || header.ParentSession != parent || header.DelegationDepth != depth {
		t.Fatal("generic resume altered lineage metadata", resumed.Session.Header())
	}
}

func TestHostProgrammaticCreationUsesActualRuntimeAndReleasesBorrowedInputs(t *testing.T) {
	host, _ := lifecycleFileHost(t)
	preset := "initial-preset"
	registry := agentpreset.New(agentpreset.Standard, nil)
	if _, err := registry.Register(agentpreset.Definition{ID: preset}); err != nil {
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
	options := assembly.NativeSessionOptions{Meta: &assembly.NativeSessionMeta{AgentPreset: &preset}, Seed: []sessionformat.Event{}}
	runtime, err := host.ActivateWithSessionOptions(t.Context(), "programmatic-host", options, func(ctx context.Context, live *assembly.NativeLive) error {
		preset = "late-caller-change"
		if _, visible := host.Session("programmatic-host"); visible {
			return errors.New("Agent published before setup")
		}
		artifact, err := host.SessionArtifact(ctx, "programmatic-host")
		if err != nil || artifact.Header.AgentPreset != "initial-preset" || len(artifact.Events) == 0 || artifact.Events[0].Type != "session/end-seed" {
			return errors.New("explicit empty seed or snapshot metadata lost before setup")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.sessionCreation != nil {
		t.Fatal("runtime retained a second copy of creation history")
	}
	if active, ok := host.Session("programmatic-host"); !ok || active != runtime {
		t.Fatal("programmatic creation bypassed Host ownership")
	}
	if err := host.StopSession(t.Context(), "programmatic-host"); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(t.Context(), "programmatic-host", true); err != nil {
		t.Fatal("programmatic Session could not resume through normal Host admission", err)
	}
}

func TestProgrammaticCreationAndResumeSetupFailurePreservePendingInbox(t *testing.T) {
	host, store := lifecycleFileHost(t)
	seed := []sessionformat.Event{{Type: "agent/inbox/spliced", Seq: 0, Time: 1, Data: json.RawMessage(`{"target":"next-turn","start":0,"inserted":[{"id":"pending-message","role":"user","source":{"kind":"user"},"content":[{"type":"text","text":"do not lose this accepted input"}]}]}`)}}
	veto := errors.New("setup intentionally rejected")
	setup := func(_ context.Context, live *assembly.NativeLive) error {
		if len(live.Session.Inbox().NextTurn) != 1 {
			return errors.New("pending input lost before setup")
		}
		return veto
	}
	if _, err := host.ActivateWithSessionOptions(t.Context(), "pending-failure", assembly.NativeSessionOptions{Seed: seed}, setup); !errors.Is(err, veto) {
		t.Fatal("unexpected creation result", err)
	}
	if _, err := host.ActivateWithSetup(t.Context(), "pending-failure", true, setup); !errors.Is(err, veto) {
		t.Fatal("unexpected resume result", err)
	}
	artifact, err := store.ReadArtifact(t.Context(), "pending-failure")
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := sessionformat.FoldInbox(artifact.Events)
	if err != nil || len(inbox.NextTurn) != 1 {
		t.Fatal("initialization rollback discarded accepted input", inbox, err)
	}
	for _, event := range artifact.Events {
		if event.Type == "turn/start" {
			t.Fatal("failed initialization released queued work into the driver")
		}
	}
}
