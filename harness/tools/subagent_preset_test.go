package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type presetChildCoordinator struct {
	selected string
	starts   int
}

func (c *presetChildCoordinator) SpawnChild(context.Context, string, string, string, bool) (string, error) {
	c.starts++
	return "child", nil
}
func (c *presetChildCoordinator) SpawnChildWithPreset(_ context.Context, _, _, _, _, preset string, _ bool) (string, error) {
	c.selected = preset
	c.starts++
	return "child", nil
}
func (*presetChildCoordinator) SendChildMessage(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (*presetChildCoordinator) InterruptChild(context.Context, string, string) (string, error) {
	return "", nil
}
func (*presetChildCoordinator) ListChildren(context.Context, string) ([]ChildStatus, error) {
	return nil, nil
}

func TestSubagentPresetChoiceRequiresDeploymentAllowlistAndSpawn(t *testing.T) {
	c := &presetChildCoordinator{}
	tool := Subagent{Coordinator: c, ParentID: "parent", PresetSelection: SubagentPresetSelection{AgentPreset: "research", AllowedAgentPresets: []string{"research", "standard"}}}
	if !strings.Contains(string(tool.Spec().Parameters), "agent_preset") {
		t.Fatal("configured choices not shown")
	}
	for _, raw := range []string{`{"description":"Default","prompt":"Read"}`, `{"description":"Chosen","prompt":"Read","agent_preset":"standard"}`} {
		if _, err := tool.Execute(t.Context(), json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if c.starts != 2 || c.selected != "standard" {
		t.Fatalf("selection: %+v", c)
	}
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"description":"Other","prompt":"Read","agent_preset":"unlisted"}`)); err == nil || c.starts != 2 {
		t.Fatal("unlisted preset started a child")
	}
	tool.PresetSelection = SubagentPresetSelection{}
	if strings.Contains(string(tool.Spec().Parameters), "agent_preset") {
		t.Fatal("unconfigured choice exposed")
	}
	if err := tool.Validate(json.RawMessage(`{"description":"Other","prompt":"Read","agent_preset":"standard"}`)); err == nil {
		t.Fatal("unconfigured override accepted")
	}
	tool.Fork = true
	tool.PresetSelection = SubagentPresetSelection{AgentPreset: "research"}
	if err := tool.Validate(json.RawMessage(`{"description":"Fork","prompt":"Read"}`)); err == nil {
		t.Fatal("fork independently selected a preset")
	}
}
