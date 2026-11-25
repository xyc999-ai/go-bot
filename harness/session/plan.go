package sessionformat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// PlanProjection is the native plan-mode replay value. Active is the last
// logged plan/mode value; Pending is derived from an unsettled or successful
// logged /plan command, not from a second durable mode record.
type PlanProjection struct {
	Active             bool
	Pending            bool
	Wanted             *bool
	ActiveAtLastHeader *bool
}

// DecodePlanMode accepts precisely the released v4 payload shape.
func DecodePlanMode(raw json.RawMessage) (bool, error) {
	fields, err := objectValue(raw)
	if err != nil || len(fields) != 1 {
		return false, errors.New("plan/mode requires exactly one active field")
	}
	active, ok := fields["active"]
	if !ok || !bytes.Equal(active, []byte("true")) && !bytes.Equal(active, []byte("false")) {
		return false, errors.New("plan/mode active must be boolean")
	}
	return bytes.Equal(active, []byte("true")), nil
}

func planCommandSelection(data map[string]json.RawMessage) (id string, wanted bool, applies bool, err error) {
	if !bytes.Equal(data["name"], []byte(`"plan"`)) {
		return "", false, false, nil
	}
	if len(data["args"]) == 0 {
		return "", false, false, nil
	}
	if err := json.Unmarshal(data["commandId"], &id); err != nil || id == "" {
		return "", false, false, errors.New("plan command/run has invalid commandId")
	}
	var args string
	if err := json.Unmarshal(data["args"], &args); err != nil {
		return "", false, false, errors.New("plan command/run args must be text")
	}
	return id, strings.TrimSpace(args) != "off", true, nil
}

// FoldPlan mirrors the current DeepSeek plan projection. A standalone mode
// event is log-only and whole-value replacing; request/header captures the
// mode last exposed to the model for later change narration.
type planFoldState struct {
	PlanProjection
	runningID     string
	runningWanted bool
}

func nextPlan(state planFoldState, event Event) (planFoldState, error) {
	switch event.Type {
	case "plan/mode":
		active, err := DecodePlanMode(event.Data)
		if err != nil {
			return planFoldState{}, fmt.Errorf("plan/mode at seq %d: %w", event.Seq, err)
		}
		state.Active, state.Wanted = active, nil
	case "command/run":
		data, err := objectValue(event.Data)
		if err != nil {
			return planFoldState{}, fmt.Errorf("command/run at seq %d: %w", event.Seq, err)
		}
		id, wanted, applies, err := planCommandSelection(data)
		if err != nil {
			return planFoldState{}, fmt.Errorf("command/run at seq %d: %w", event.Seq, err)
		}
		if applies {
			state.runningID, state.runningWanted = id, wanted
		}
	case "command/done":
		data, err := objectValue(event.Data)
		if err != nil {
			return planFoldState{}, fmt.Errorf("command/done at seq %d: %w", event.Seq, err)
		}
		var id string
		if json.Unmarshal(data["commandId"], &id) == nil && state.runningID != "" && id == state.runningID {
			if bytes.Equal(data["kind"], []byte(`"success"`)) && state.runningWanted != state.Active {
				selected := state.runningWanted
				state.Wanted = &selected
			} else {
				state.Wanted = nil
			}
			state.runningID = ""
		}
	case "request/header":
		active := state.Active
		state.ActiveAtLastHeader = &active
	}
	return state, nil
}
func planView(state planFoldState) PlanProjection {
	view := state.PlanProjection
	target := view.Wanted
	if state.runningID != "" {
		wanted := state.runningWanted
		target = &wanted
	}
	view.Pending = target != nil && *target != view.Active
	if target != nil {
		v := *target
		view.Wanted = &v
	}
	if view.ActiveAtLastHeader != nil {
		v := *view.ActiveAtLastHeader
		view.ActiveAtLastHeader = &v
	}
	return view
}
func FoldPlan(events []Event) (PlanProjection, error) {
	var state planFoldState
	for _, event := range events {
		var err error
		state, err = nextPlan(state, event)
		if err != nil {
			return PlanProjection{}, err
		}
	}
	return planView(state), nil
}

var planDefinition = ProjectionDefinition[planFoldState]{Key: "plan", Version: 1, Init: func(Header, uint64) planFoldState { return planFoldState{} }, Apply: nextPlan}

func (s *LiveSession) Plan() (PlanProjection, error) {
	return ReadProjection(ProjectionHandle[planFoldState]{s, &planDefinition}, LiveCut, planView)
}
