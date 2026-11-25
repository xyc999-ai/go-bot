package permission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	session "go-bot/harness/session"
)

type Mode string

const (
	ReadOnly       Mode = "read-only"
	WorkspaceWrite Mode = "workspace-write"
	FullAccess     Mode = "danger-full-access"
)

func (m Mode) Rank() int {
	switch m {
	case ReadOnly:
		return 1
	case WorkspaceWrite:
		return 2
	case FullAccess:
		return 3
	default:
		return 0
	}
}

type State struct {
	Mode     Mode   `json:"mode"`
	Approval string `json:"approval"`
}

func Read(events []session.Event) State {
	state := State{Mode: FullAccess, Approval: "never"}
	for _, event := range events {
		switch event.Type {
		case "sandbox/mode":
			var value struct {
				Mode Mode `json:"mode"`
			}
			if json.Unmarshal(event.Data, &value) == nil && value.Mode.Rank() > 0 {
				state.Mode = value.Mode
			}
		case "approval/policy":
			var value struct {
				Policy string `json:"policy"`
			}
			if json.Unmarshal(event.Data, &value) == nil && (value.Policy == "ask" || value.Policy == "never") {
				state.Approval = value.Policy
			}
		}
	}
	return state
}

// ReadSession reads required incrementally maintained execution policy.
func ReadSession(s *session.LiveSession) State {
	value := s.Permission()
	return State{Mode: Mode(value.Mode), Approval: value.Approval}
}

func Save(ctx context.Context, s *session.LiveSession, mode Mode, delegated bool, initialize bool) error {
	if mode.Rank() == 0 {
		return errors.New("invalid permission mode")
	}
	state := ReadSession(s)
	approval := "ask"
	if mode == FullAccess || delegated {
		approval = "never"
	}
	events := []session.EventInput{}
	appendFact := func(kind string, value map[string]string) {
		data, _ := json.Marshal(value)
		events = append(events, session.EventInput{Type: kind, Data: data})
	}
	knob := func(key, value string) map[string]string {
		fact := map[string]string{key: value}
		if delegated {
			fact["source"] = "delegation"
		}
		return fact
	}
	if !delegated && (initialize || mode != state.Mode || approval != state.Approval) {
		appendFact("permission/preset", map[string]string{"preset": string(mode)})
	}
	if initialize || mode != state.Mode {
		appendFact("sandbox/mode", knob("mode", string(mode)))
	}
	if initialize || approval != state.Approval {
		appendFact("approval/policy", knob("policy", approval))
	}
	if delegated && mode == FullAccess {
		appendFact("permission/preset", map[string]string{"preset": string(mode)})
	}
	for _, event := range events {
		if _, err := s.Append(event); err != nil {
			return err
		}
	}
	if len(events) == 0 {
		return nil
	}
	return s.Flush(ctx)
}

type Scope struct {
	Mode Mode
	Root string
	Ask  bool
}

type scopeKey struct{}

func WithScope(ctx context.Context, scope Scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, scope)
}

func From(ctx context.Context) Scope {
	scope, _ := ctx.Value(scopeKey{}).(Scope)
	return scope
}

func (s Scope) Restricted() bool { return s.Mode == ReadOnly || s.Mode == WorkspaceWrite }

func Guidance(state State, root string) (file, approval string) {
	switch state.Mode {
	case ReadOnly:
		file = "Current DSH file policy: read-only. Any available operation enforced by the DSH file sandbox cannot modify files in the standing mode. Do not refuse a required modification from this policy alone: try an available tool normally and follow any denial and escalation guidance it returns."
	case WorkspaceWrite:
		file = "Current DSH file policy: workspace-write. Any available operation enforced by the DSH file sandbox may modify files under the session workspace: " + strconv.Quote(root) + ". Some platform temporary areas may also be writable."
	case FullAccess:
		file = "Current DSH file policy: danger-full-access. The DSH file sandbox does not restrict file modifications by available operations."
	}
	if state.Approval == "ask" {
		return file, "Approval prompts are enabled in this session: operations that require approval may ask through the configured answerers; without an available answerer, the request fails closed."
	}
	return file, "Approval prompts are disabled in this session: actions that require approval are rejected automatically — do not request sandbox escalation (do not set `sandbox_permissions`)."
}

type Failure struct {
	Code    string
	Message string
}

func (e Failure) Error() string                   { return e.Message }
func (e Failure) ToolErrorInfo() (string, string) { return "SandboxError", e.Code }

func Denial(mode Mode, ask bool, subject string) string {
	message := fmt.Sprintf("[sandbox: file access denied under %s mode]", mode)
	if ask {
		message += fmt.Sprintf("\n[sandbox: escalation available — retry this exact %s once with sandbox_permissions (the narrowest wider mode that suffices) + justification; the approval prompt asks the user]", subject)
	}
	return message
}
