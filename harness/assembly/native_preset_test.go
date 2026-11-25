package assembly

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"go-bot/harness/agent"
	"go-bot/harness/agentpreset"
	"go-bot/harness/memory"
	"go-bot/harness/schedule"
	"go-bot/harness/session"
	"go-bot/harness/skills"
	"go-bot/internal/modeldata"
)

type presetModelFunc func(context.Context, []agent.Message, []agent.ToolSpec) (modeldata.AssistantMessage, error)

func (f presetModelFunc) Complete(ctx context.Context, m []agent.Message, t []agent.ToolSpec) (modeldata.AssistantMessage, error) {
	return f(ctx, m, t)
}

func TestPresetForkRetainsParentDefinitionAndSpawnCanChooseCurrentDefinition(t *testing.T) {
	f, id, m := presetFixture(t, agentpreset.Definition{ID: "research", Persona: agentpreset.PersonaSpec{Prefix: "Original identity."}})
	f.Storage = &nativeTreeStorage{nativeMemoryStorage: &nativeMemoryStorage{}}
	parent, err := f.Activate(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if _, err := parent.Agent.RunTextTurn(t.Context(), "Root task"); err != nil {
		t.Fatal(err)
	}
	if err := f.Presets.Replace(agentpreset.Definition{ID: "research", Persona: agentpreset.PersonaSpec{Prefix: "Current identity."}}); err != nil {
		t.Fatal(err)
	}
	fork, err := f.ForkChild(t.Context(), id, parent, "fork-retained", "Fork work")
	if err != nil {
		t.Fatal(err)
	}
	defer fork.Close()
	if !fork.Preset.Same(parent.Preset) {
		t.Fatal("fork resolved replacement instead of inheriting parent binding")
	}
	if _, err := fork.Agent.RunTextTurn(t.Context(), "Continue fork"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.prompts[len(m.prompts)-1], "Original identity.") {
		t.Fatal("fork lost original composition")
	}
	spawn, err := f.ActivateChildWithPresetAt(t.Context(), id, parent, "spawn-current", "Independent work", "", "research")
	if err != nil {
		t.Fatal(err)
	}
	defer spawn.Close()
	if spawn.Preset.Same(parent.Preset) {
		t.Fatal("explicit spawn did not resolve the current definition")
	}
	if _, err := spawn.Agent.RunTextTurn(t.Context(), "Independent task"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.prompts[len(m.prompts)-1], "Current identity.") {
		t.Fatal("spawn lost selected composition")
	}
}

func TestScheduleCreationCapturesEffectivePresetAfterBlankSelection(t *testing.T) {
	f, id, _ := presetFixture(t, agentpreset.Definition{ID: "original"})
	if _, err := f.Presets.Register(agentpreset.Definition{ID: "research"}); err != nil {
		t.Fatal(err)
	}
	store := schedule.FileStore{Path: filepath.Join(t.TempDir(), "schedule.json")}
	service, err := schedule.NewHostService(store, func(context.Context, string) error { return nil }, func(context.Context, string, string) (string, error) { return "", nil })
	if err != nil {
		t.Fatal(err)
	}
	service.SetDestinationCreator(func(context.Context, string, schedule.TargetCreate, string) error { return nil })
	if err := service.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	f.Schedule = service
	live, err := f.Activate(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := live.Session.Append(sessionformat.EventInput{Type: "agent-preset/selected", Data: json.RawMessage(`{"agentPreset":"research"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := live.Close(); err != nil {
		t.Fatal(err)
	}
	step := 0
	f.Model = presetModelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (modeldata.AssistantMessage, error) {
		step++
		if step == 1 {
			return modeldata.AssistantMessage{ToolCalls: []modeldata.ToolRequest{{ID: "create", Name: "schedule_create", Arguments: `{"title":"Review","prompt":"Read current notes","rrule":"DTSTART:20990101T090000Z\nRRULE:FREQ=DAILY;COUNT=1","target":"new_once"}`}}}, nil
		}
		return modeldata.AssistantMessage{Content: "Scheduled."}, nil
	})
	live, err = f.Resume(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if live.Session.Header().AgentPreset != "original" || live.Preset.ID() != "research" {
		t.Fatal("creation identity or effective binding lost")
	}
	if _, err := live.Agent.RunTextTurn(t.Context(), "Create the requested task"); err != nil {
		t.Fatal(err)
	}
	document, err := store.Load(t.Context())
	if err != nil || len(document.Tasks) != 1 || document.Tasks[0].Target.Create == nil || document.Tasks[0].Target.Create.AgentPreset != "research" {
		t.Fatalf("schedule captured creation header: %+v %v", document, err)
	}
}

func TestPresetCanSuppressMemoryInjectionAndRuntimeFetchWithoutRemovingMemoryTool(t *testing.T) {
	include := false
	f, id, m := presetFixture(t, agentpreset.Definition{ID: "focused", Persona: agentpreset.PersonaSpec{IncludeRuntimeContext: &include}, PromptSections: agentpreset.Selection{DenySources: []agentpreset.SourceRef{"builtin:memory"}}})
	f.Memory = memory.NewStore(t.TempDir())
	if _, err := f.Memory.Apply("user", []memory.Operation{{Action: "add", Content: "Private saved preference."}}); err != nil {
		t.Fatal(err)
	}
	reads := 0
	f.ContextSource = func(context.Context, string) ([]agent.RuntimeContextSection, error) {
		reads++
		return []agent.RuntimeContextSection{{Name: "test", Text: "runtime background"}}, nil
	}
	live, err := f.Activate(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if _, err := live.Agent.RunTextTurn(t.Context(), "Focused request"); err != nil {
		t.Fatal(err)
	}
	if reads != 0 || strings.Contains(m.prompts[0], "Private saved preference.") {
		t.Fatal("disabled context was fetched or injected")
	}
	found := false
	for _, spec := range m.specs[0] {
		found = found || spec.Name == "memory"
	}
	if !found {
		t.Fatal("memory injection policy removed the memory tool")
	}
	for _, event := range live.Session.DurableEvents() {
		if event.Type == "user/message" && strings.Contains(string(event.Data), "runtime-context") {
			t.Fatal("runtime snapshot persisted despite suppression")
		}
	}
}

type presetSourceTool struct {
	compositionTool
	source string
}

func (t presetSourceTool) ContributionSource() string { return t.source }

func presetFixture(t *testing.T, d agentpreset.Definition) (NativeFactory, NativeRuntimeIdentity, *observingModel) {
	t.Helper()
	r := agentpreset.New(d.ID, nil)
	if _, err := r.Register(d); err != nil {
		t.Fatal(err)
	}
	m := &observingModel{}
	f := NativeFactory{Presets: r, Storage: &nativeMemoryStorage{}, Builder: nativeTestBuilder{newContainerTestBuilder(t, t.TempDir())}, Model: m, Provider: "test", ModelID: "test", SystemPrompt: "Default identity.", Limits: agent.Limits{MaxSteps: 5, MaxToolCalls: 5}}
	return f, NativeRuntimeIdentity{UserID: "alice", SessionID: "preset-session", Workspace: t.TempDir()}, m
}

func TestPresetBindingKeepsDefinitionButRefreshesMemoryAndPluginTools(t *testing.T) {
	f, id, m := presetFixture(t, agentpreset.Definition{ID: "research", Persona: agentpreset.PersonaSpec{Prefix: "Research for {{account.id}}."}, Tools: agentpreset.Selection{DenyNames: []string{"present"}, DenySources: []agentpreset.SourceRef{"plugin:hidden"}}})
	f.Memory = memory.NewStore(t.TempDir())
	if _, err := f.Memory.Apply("user", []memory.Operation{{Action: "add", Content: "Prefers Chinese."}}); err != nil {
		t.Fatal(err)
	}
	pluginName := "plugin-one"
	f.ToolSource = func(context.Context, string) ([]agent.Tool, error) {
		return []agent.Tool{presetSourceTool{compositionTool(pluginName), "plugin:visible"}, presetSourceTool{compositionTool("plugin-hidden"), "plugin:hidden"}}, nil
	}
	live, err := f.Activate(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if live.Preset.ID() != "research" || live.Session.Header().AgentPreset != "research" {
		t.Fatal("default was not resolved before creation")
	}
	if _, err := live.Agent.RunTextTurn(t.Context(), "First request"); err != nil {
		t.Fatal(err)
	}
	if err := f.Presets.Replace(agentpreset.Definition{ID: "research", Persona: agentpreset.PersonaSpec{Prefix: "Replacement identity."}}); err != nil {
		t.Fatal(err)
	}
	pluginName = "plugin-two"
	if _, err := f.Memory.Apply("memory", []memory.Operation{{Action: "add", Content: "The archive is in the new directory."}}); err != nil {
		t.Fatal(err)
	}
	if _, err := live.Agent.RunTextTurn(t.Context(), "Second request"); err != nil {
		t.Fatal(err)
	}
	for i, expected := range []string{"plugin-one", "plugin-two"} {
		var names []string
		for _, spec := range m.specs[i] {
			names = append(names, spec.Name)
		}
		if !slices.Contains(names, expected) || slices.Contains(names, "present") || slices.Contains(names, "plugin-hidden") {
			t.Fatalf("step %d tools=%v", i, names)
		}
		if !strings.Contains(m.prompts[i], "Research for alice.") || !strings.Contains(m.prompts[i], "Prefers Chinese.") || strings.Contains(m.prompts[i], "Replacement identity.") {
			t.Fatal("binding identity or memory was lost", m.prompts[i])
		}
	}
	if !strings.Contains(m.prompts[1], "The archive is in the new directory.") {
		t.Fatal("memory was frozen at binding")
	}
	nextFactory := f
	nextFactory.Storage = &nativeMemoryStorage{}
	other, err := nextFactory.Activate(t.Context(), NativeRuntimeIdentity{UserID: "alice", SessionID: "another", Workspace: id.Workspace})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Agent.RunTextTurn(t.Context(), "New binding"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.prompts[len(m.prompts)-1], "Replacement identity.") {
		t.Fatal("new Agent did not use replacement definition")
	}

}

func TestCompletePresetKeepsToolsButSuppressesAllOtherSystemContributions(t *testing.T) {
	f, id, m := presetFixture(t, agentpreset.Definition{ID: "complete", Persona: agentpreset.PersonaSpec{Prefix: "Only {{account.id}}.", Suffix: "Must not appear", Complete: true}})
	f.Memory = memory.NewStore(t.TempDir())
	if _, err := f.Memory.Apply("user", []memory.Operation{{Action: "add", Content: "Hidden saved preference."}}); err != nil {
		t.Fatal(err)
	}
	f.SystemContextSource = func(context.Context, string) ([]agent.RuntimeContextSection, error) {
		return []agent.RuntimeContextSection{{Name: "plugin:system", Text: "Other system context"}}, nil
	}
	f.PromptSource = func(_ context.Context, input agent.PromptAssemblyInput) (agent.PromptAssembly, error) {
		return agent.PromptAssembly{SystemPrompt: input.SystemPrompt + "\nPlugin instruction", Tools: input.Tools}, nil
	}
	live, err := f.Activate(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	for range 2 {
		if _, err := live.Agent.RunTextTurn(t.Context(), "Request"); err != nil {
			t.Fatal(err)
		}
	}
	for i, prompt := range m.prompts {
		if prompt != "Only alice." {
			t.Fatalf("complete prompt leaked sections: %q", prompt)
		}
		if len(m.specs[i]) == 0 {
			t.Fatal("complete persona removed tools")
		}
	}
}

type localPresetModel struct{ calls int }

func (m *localPresetModel) Complete(_ context.Context, messages []agent.Message, _ []agent.ToolSpec) (modeldata.AssistantMessage, error) {
	m.calls++
	if m.calls == 1 {
		return modeldata.AssistantMessage{ToolCalls: []modeldata.ToolRequest{{ID: "local", Name: "readfile", Arguments: `{}`}}}, nil
	}
	for _, message := range messages {
		if message.Role == "tool" && strings.Contains(message.Content, "done") {
			return modeldata.AssistantMessage{Content: "Local handler ran."}, nil
		}
	}
	return modeldata.AssistantMessage{Content: "Missing local result"}, nil
}

func TestPresetLocalToolsOverrideInheritedAndRemainExecutable(t *testing.T) {
	f, id, _ := presetFixture(t, agentpreset.Definition{ID: "local", Tools: agentpreset.Selection{DenyNames: []string{"readfile"}}, LocalTools: []string{"local-reader"}})
	f.Model = &localPresetModel{}
	f.PresetToolProviders = map[string]NativePresetToolProvider{"local-reader": func(context.Context, NativeRuntimeIdentity, *sessionformat.LiveSession, Resources) ([]agent.Tool, error) {
		return []agent.Tool{compositionTool("readfile")}, nil
	}}
	live, err := f.Activate(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if _, err := live.Agent.RunTextTurn(t.Context(), "Run the local reader"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range live.Session.DurableEvents() {
		if event.Type == "tool/result" {
			var d struct {
				Message struct {
					IsError bool
					Content []struct{ Type, Text string }
				}
			}
			if json.Unmarshal(event.Data, &d) != nil || d.Message.IsError {
				t.Fatalf("local handler failed: %s", event.Data)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("local tool did not execute")
	}
}

func TestSamePresetChildrenHaveIndependentStructuredResultsAndQuestions(t *testing.T) {
	f, id, _ := presetFixture(t, agentpreset.Definition{ID: "shared"})
	f.Storage = &nativeTreeStorage{nativeMemoryStorage: &nativeMemoryStorage{}}
	parent, err := f.Activate(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	f.WorkflowSchema = map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}}
	children := make([]*NativeLive, 2)
	for i, name := range []string{"one", "two"} {
		childFactory := f
		childFactory.Model = presetModelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (modeldata.AssistantMessage, error) {
			return modeldata.AssistantMessage{ToolCalls: []modeldata.ToolRequest{{ID: "result", Name: "structured_output", Arguments: `{"value":"` + name + `"}`}}}, nil
		})
		children[i], err = childFactory.ActivateChild(t.Context(), id, parent, name, "Independent result")
		if err != nil {
			t.Fatal(err)
		}
		defer children[i].Close()
	}
	if children[0].Questions == children[1].Questions || children[0].Structured == children[1].Structured || !children[0].Preset.Same(children[1].Preset) {
		t.Fatal("definition sharing leaked mutable Agent resources")
	}
	var wg sync.WaitGroup
	for _, child := range children {
		wg.Go(func() {
			if _, err := child.Agent.RunTextTurn(t.Context(), "Complete the work"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	for i, name := range []string{"one", "two"} {
		value, ok := children[i].Structured.Captured()
		if !ok || value.(map[string]any)["value"] != name {
			t.Fatalf("crossed child result: %v", value)
		}
	}
}

func TestCompletePresetStaysCompleteAfterCompaction(t *testing.T) {
	f, id, _ := presetFixture(t, agentpreset.Definition{ID: "complete", Persona: agentpreset.PersonaSpec{Prefix: "Only this system.", Complete: true}})
	m := &memoryTurnModel{}
	f.Model = m
	f.Memory = memory.NewStore(t.TempDir())
	live, err := f.Activate(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	for _, prompt := range []string{"Remember the preference. " + strings.Repeat("Context. ", 100), "Continue work."} {
		if _, err := live.Agent.RunTextTurn(t.Context(), prompt); err != nil {
			t.Fatal(err)
		}
	}
	if compacted, err := live.Agent.CompactNow(t.Context()); err != nil || !compacted {
		t.Fatal("compaction did not run", compacted, err)
	}
	if _, err := live.Agent.RunTextTurn(t.Context(), "After compaction"); err != nil {
		t.Fatal(err)
	}
	for _, messages := range m.requests {
		for _, message := range messages {
			if message.Role == "system" && message.Content != "Only this system." {
				t.Fatalf("complete system leaked after refresh: %q", message.Content)
			}
		}
	}
}

func TestPresetSkillSourceViewDoesNotMutateHostRegistry(t *testing.T) {
	f, id, _ := presetFixture(t, agentpreset.Definition{ID: "skills", Skills: agentpreset.Selection{DenySources: []agentpreset.SourceRef{"plugin:hidden"}}})
	shared := &skills.Registry{Providers: func(context.Context, skills.Lookup) ([]skills.Provider, error) {
		var providers []skills.Provider
		for _, name := range []string{"visible", "hidden"} {
			providers = append(providers, skills.Provider{Name: name, Source: "plugin:" + name, List: func(context.Context, skills.Lookup) (skills.ProviderSnapshot, error) {
				return skills.ProviderSnapshot{Complete: true, Entries: []skills.ProviderEntry{{Summary: skills.Summary{Name: name, Description: "Instructions", ModelInvocable: true}, Rank: 1}}}, nil
			}, Load: func(context.Context, skills.Lookup, skills.ProviderEntry) (skills.Loaded, error) {
				return skills.Loaded{Summary: skills.Summary{Name: name}, Value: skills.Value{Name: name, Provider: name, Content: "Instructions"}}, nil
			}})
		}
		return providers, nil
	}}
	f.Builder = compositionBuilder{nativeTestBuilder: nativeTestBuilder{newContainerTestBuilder(t, t.TempDir())}, registry: shared}
	live, err := f.Activate(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	lookup := skills.Lookup{Workspace: id.Workspace, CWD: id.Workspace}
	view, err := live.Resource.Skills.Snapshot(t.Context(), lookup)
	if err != nil || len(view.Entries) != 1 || view.Entries[0].Name != "visible" {
		t.Fatal("source filter not applied", view, err)
	}
	if _, found, err := live.Resource.Skills.Find(t.Context(), lookup, "hidden"); err != nil || found {
		t.Fatal("hidden source directly loadable", err)
	}
	all, err := shared.Snapshot(t.Context(), lookup)
	if err != nil || len(all.Entries) != 2 {
		t.Fatal("shared registry mutated", err)
	}
}
