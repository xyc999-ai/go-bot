package assembly

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go-bot/harness/agent"
	"go-bot/harness/agentpreset"
	"go-bot/harness/skills"
	"go-bot/harness/tools"
)

type compositionTool string

func (t compositionTool) Spec() agent.ToolSpec {
	return agent.ToolSpec{Name: string(t), Description: "Test capability", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)}
}
func (compositionTool) Validate(json.RawMessage) error { return nil }
func (compositionTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "done", nil
}

type compositionBuilder struct {
	nativeTestBuilder
	registry *skills.Registry
}

func (b compositionBuilder) BuildNative(ctx context.Context, identity NativeRuntimeIdentity, reason string) (Resources, error) {
	resource, err := b.nativeTestBuilder.BuildNative(ctx, identity, reason)
	resource.Skills = b.registry
	return resource, err
}

func TestCompositionSelectsRealToolsAndAllSkillEntryPoints(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"visible-skill", "hidden-skill"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: Test instructions\n---\nUse the test capability."), 0600); err != nil {
			t.Fatal(err)
		}
	}
	shared := &skills.Registry{CustomDirs: []string{dir}}
	allowTools := []string{"readfile", "skill", "plugin-visible"}
	allowSkills := []string{"visible-skill"}
	presets := agentpreset.New("research-test", nil)
	_, err := presets.Register(agentpreset.Definition{ID: "research-test", Persona: agentpreset.PersonaSpec{Suffix: "Independent research identity."}, Tools: agentpreset.Selection{AllowNames: &allowTools}, Skills: agentpreset.Selection{AllowNames: &allowSkills}})
	if err != nil {
		t.Fatal(err)
	}
	model := &observingModel{}
	factory := NativeFactory{
		Storage: &nativeMemoryStorage{}, Model: model,
		Builder:  compositionBuilder{nativeTestBuilder: nativeTestBuilder{newContainerTestBuilder(t, t.TempDir())}, registry: shared},
		Provider: "p", ModelID: "m", SystemPrompt: "Base system.", Limits: agent.Limits{MaxSteps: 3, MaxToolCalls: 3},
		Presets: presets,
		ToolSource: func(context.Context, string) ([]agent.Tool, error) {
			return []agent.Tool{compositionTool("plugin-visible"), compositionTool("plugin-hidden")}, nil
		},
		PromptSource: func(_ context.Context, input agent.PromptAssemblyInput) (agent.PromptAssembly, error) {
			for _, spec := range input.Tools {
				if spec.Name == "writefile" || spec.Name == "plugin-hidden" {
					t.Fatalf("prompt assembler received a filtered-out tool: %s", spec.Name)
				}
			}
			return agent.PromptAssembly{SystemPrompt: input.SystemPrompt, Tools: input.Tools}, nil
		},
	}
	identity := NativeRuntimeIdentity{UserID: "test", SessionID: "research", Workspace: t.TempDir(), AgentPreset: "research-test"}
	live, err := factory.Activate(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if _, err := live.Agent.RunTextTurn(t.Context(), "Inspect current information"); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, spec := range model.specs[0] {
		names = append(names, spec.Name)
	}
	if !slices.Contains(names, "plugin-visible") || slices.Contains(names, "plugin-hidden") || slices.Contains(names, "writefile") || !strings.Contains(model.prompts[0], "Independent research identity.") {
		t.Fatalf("composition did not reach the real model request: tools=%v prompt=%s", names, model.prompts[0])
	}
	lookup := skills.Lookup{CWD: identity.Workspace, Workspace: identity.Workspace, SessionID: identity.SessionID}
	snapshot, err := live.Resource.Skills.Snapshot(t.Context(), lookup)
	if err != nil || len(snapshot.Entries) != 1 || snapshot.Entries[0].Name != "visible-skill" {
		t.Fatalf("catalog=%+v err=%v", snapshot, err)
	}
	tool := tools.Skill{Registry: live.Resource.Skills, Lookup: func() skills.Lookup { return lookup }}
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"name":"hidden-skill"}`)); err == nil {
		t.Fatal("hidden skill could be loaded outside its catalog")
	}
	if _, found, err := live.Resource.Skills.Find(t.Context(), lookup, "hidden-skill"); err != nil || found {
		t.Fatal("explicit invocation bypassed the Agent view")
	}
	all, err := shared.Snapshot(t.Context(), lookup)
	if err != nil || len(all.Entries) != 2 || shared.Visible != nil {
		t.Fatalf("composition changed the Host registry: %+v %v", all, err)
	}
	unregister, err := presets.Register(agentpreset.Definition{ID: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	unregister()
	if _, err := presets.Resolve(t.Context(), "unused"); err == nil {
		t.Fatal("removed preset silently resolved")
	}
}
