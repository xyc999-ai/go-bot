package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"go-bot/harness/agent"
)

// ChildCoordinator is implemented by the owning worker process. A child is
// another in-process Agent, not a deployment endpoint or a Docker container.
type ChildCoordinator interface {
	SpawnChild(context.Context, string, string, string, bool) (string, error)
	SendChildMessage(context.Context, string, string, string) (string, error)
	InterruptChild(context.Context, string, string) (string, error)
	ListChildren(context.Context, string) ([]ChildStatus, error)
}

// ChildDirectoryCoordinator preserves the provider's optional initial cwd.
// A deployment that cannot isolate per-Agent cwd must not silently ignore it.
type ChildDirectoryCoordinator interface {
	SpawnChildAt(context.Context, string, string, string, string, bool) (string, error)
}

type ChildStatus struct {
	SessionID string `json:"sessionId"`
	Label     string `json:"label"`
	Activity  string `json:"activity"`
}

type Subagent struct {
	Coordinator     ChildCoordinator
	ParentID        string
	Fork            bool
	PresetSelection SubagentPresetSelection
}

type SendMessage struct {
	Coordinator ChildCoordinator
	SenderID    string
}

type InterruptAgent struct {
	Coordinator ChildCoordinator
	ParentID    string
}

type ListAgents struct {
	Coordinator ChildCoordinator
	ParentID    string
}

type subagentArgs struct {
	CWD, Description, Prompt string
	Provider                 string `json:"provider,omitempty"`
	Model                    string `json:"model,omitempty"`
	ReasoningEffort          string `json:"reasoning_effort,omitempty"`
	AgentPreset              string `json:"agent_preset,omitempty"`
}

func (t Subagent) Spec() agent.ToolSpec {
	name := "subagent"
	description := "Delegate a self-contained task to a subagent with a separate context. It shares the workspace and returns an id immediately; the runtime notifies you when it finishes."
	if t.Fork {
		name = "subagent_fork"
		description = "Delegate a task to a subagent seeded with this conversation's completed turns. It does not see the current in-flight turn. The runtime returns its id immediately and notifies you when it finishes."
	}
	parameters := json.RawMessage(`{"type":"object","properties":{"cwd":{"type":"string"},"description":{"type":"string"},"prompt":{"type":"string"}},"required":["description","prompt"]}`)
	if !t.Fork && len(t.PresetSelection.AllowedAgentPresets) > 0 {
		var schema map[string]any
		_ = json.Unmarshal(parameters, &schema)
		schema["properties"].(map[string]any)["agent_preset"] = map[string]any{"type": "string", "enum": t.PresetSelection.AllowedAgentPresets, "description": "Agent preset for this child; omission uses the configured default or inherits the parent."}
		parameters, _ = json.Marshal(schema)
	}
	return agent.ToolSpec{Name: name, Description: description, Parameters: parameters, ParallelSafe: true}
}

func (t Subagent) Validate(raw json.RawMessage) error {
	if t.Coordinator == nil || t.ParentID == "" {
		return errors.New("subagent coordinator is unavailable")
	}
	var request subagentArgs
	if err := decodeGoalArgs(raw, &request); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	_, provided := fields["agent_preset"]
	selected, err := t.PresetSelection.Select(request.AgentPreset, provided, t.Fork)
	if err != nil {
		return err
	}
	if selected != "" {
		if _, ok := t.Coordinator.(ChildPresetCoordinator); !ok {
			return errors.New("child Agent Preset selection is unavailable in this worker")
		}
	}
	if strings.TrimSpace(request.Description) == "" || strings.TrimSpace(request.Prompt) == "" || len(request.Description) > 120 || len(request.Prompt) > 100000 {
		return errors.New("subagent description or prompt is invalid")
	}
	if len(request.CWD) > 4096 || strings.ContainsRune(request.CWD, 0) {
		return errors.New("subagent cwd is invalid")
	}
	if request.CWD != "" {
		if _, ok := t.Coordinator.(ChildDirectoryCoordinator); !ok {
			return errors.New("subagent cwd is unavailable in this worker")
		}
	}
	for _, name := range []string{"provider", "model", "reasoning_effort"} {
		if _, ok := fields[name]; ok {
			return errors.New("subagents inherit model.main; model selection is not accepted")
		}
	}

	return nil
}

func (t Subagent) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	if err := t.Validate(raw); err != nil {
		return "", err
	}
	var request subagentArgs
	_ = json.Unmarshal(raw, &request)
	var id string
	var err error
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	_, provided := fields["agent_preset"]
	selected, _ := t.PresetSelection.Select(request.AgentPreset, provided, t.Fork)
	if selected != "" {
		id, err = t.Coordinator.(ChildPresetCoordinator).SpawnChildWithPreset(ctx, t.ParentID, request.Description, request.Prompt, request.CWD, selected, t.Fork)
	} else if coordinator, ok := t.Coordinator.(ChildDirectoryCoordinator); ok {
		id, err = coordinator.SpawnChildAt(ctx, t.ParentID, request.Description, request.Prompt, request.CWD, t.Fork)
	} else {
		id, err = t.Coordinator.SpawnChild(ctx, t.ParentID, request.Description, request.Prompt, t.Fork)
	}
	if err != nil {
		return "", err
	}
	return `{"kind":"activation","subagentId":` + jsonString(id) + `}`, nil
}

func (t SendMessage) Spec() agent.ToolSpec {
	return agent.ToolSpec{Name: "send_message", Description: "Send a message to a direct continuable child or direct parent. A working agent receives it at its next step; an idle agent starts a new turn. This confirms delivery, not the agent's answer.", Parameters: json.RawMessage(`{"type":"object","properties":{"agent_id":{"type":"string"},"message":{"type":"string"}},"required":["agent_id","message"]}`)}
}

func (t SendMessage) Validate(raw json.RawMessage) error {
	if t.Coordinator == nil || t.SenderID == "" {
		return errors.New("subagent coordinator is unavailable")
	}
	var request struct {
		AgentID string `json:"agent_id"`
		Message string `json:"message"`
	}
	if err := decodeGoalArgs(raw, &request); err != nil {
		return err
	}
	if request.AgentID == "" || strings.TrimSpace(request.Message) == "" || len(request.Message) > 100000 {
		return errors.New("target Agent ID or message is invalid")
	}
	return nil
}

func (t SendMessage) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	if err := t.Validate(raw); err != nil {
		return "", err
	}
	var request struct {
		AgentID string `json:"agent_id"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &request)
	id, err := t.Coordinator.SendChildMessage(ctx, t.SenderID, request.AgentID, request.Message)
	if err != nil {
		return "", err
	}
	return `{"messageId":` + jsonString(id) + `}`, nil
}

func (t InterruptAgent) Spec() agent.ToolSpec {
	return agent.ToolSpec{Name: "interrupt_agent", Description: "Ask a child Agent to stop its current work without waiting for it to stop. A local continuable child can be resumed later with send_message.", Parameters: json.RawMessage(`{"type":"object","properties":{"agent_id":{"type":"string"}},"required":["agent_id"]}`)}
}

func (t InterruptAgent) Validate(raw json.RawMessage) error {
	if t.Coordinator == nil || t.ParentID == "" {
		return errors.New("subagent coordinator is unavailable")
	}
	var request struct {
		AgentID string `json:"agent_id"`
	}
	if err := decodeGoalArgs(raw, &request); err != nil {
		return err
	}
	if request.AgentID == "" {
		return errors.New("child Agent ID is required")
	}
	return nil
}

func (t InterruptAgent) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	if err := t.Validate(raw); err != nil {
		return "", err
	}
	var request struct {
		AgentID string `json:"agent_id"`
	}
	_ = json.Unmarshal(raw, &request)
	_, err := t.Coordinator.InterruptChild(ctx, t.ParentID, request.AgentID)
	if err != nil {
		return "", err
	}
	return `{"accepted":true}`, nil
}

func (t ListAgents) Spec() agent.ToolSpec {
	return agent.ToolSpec{Name: "list_agents", Description: "List subagents you started with their ids, labels, and running or inactive status. Completion is sent as a notice, so there is no need to poll. Descendants scope includes parent and depth.", Parameters: json.RawMessage(`{"type":"object","properties":{"scope":{"type":"string","enum":["children","descendants"]}}}`), ParallelSafe: true}
}

func (t ListAgents) Validate(raw json.RawMessage) error {
	if t.Coordinator == nil || t.ParentID == "" {
		return errors.New("subagent coordinator is unavailable")
	}
	var request struct {
		Scope string `json:"scope"`
	}
	if err := decodeGoalArgs(raw, &request); err != nil {
		return err
	}
	if request.Scope != "" && request.Scope != "children" && request.Scope != "descendants" {
		return errors.New("list_agents scope must be children or descendants")
	}
	return nil
}

func (t ListAgents) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	if err := t.Validate(raw); err != nil {
		return "", err
	}
	children, err := t.Coordinator.ListChildren(ctx, t.ParentID)
	if err != nil {
		return "", err
	}
	var request struct {
		Scope string `json:"scope"`
	}
	_ = json.Unmarshal(raw, &request)
	rows := make([]map[string]any, 0, len(children))
	for _, child := range children {
		status := "inactive"
		if child.Activity == "running" {
			status = "running"
		}
		row := map[string]any{"kind": "child", "id": child.SessionID, "label": child.Label, "status": status}
		if request.Scope == "descendants" {
			row["parent"], row["depth"] = t.ParentID, 1
		}
		rows = append(rows, row)
	}
	encoded, err := json.Marshal(rows)
	return string(encoded), err
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
