package sessionformat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type GoalRef struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
}

type GoalBlockReason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type GoalSnapshot struct {
	ID            string           `json:"id"`
	Revision      uint64           `json:"revision"`
	Objective     string           `json:"objective"`
	Phase         string           `json:"phase"`
	MaxGoalRounds uint64           `json:"maxGoalRounds"`
	BlockedReason *GoalBlockReason `json:"blockedReason,omitempty"`
}

// GoalChange is exactly the released goal/change payload. Clear carries a
// tombstone instead of a snapshot; activation is never stored in this event.
type GoalChange struct {
	Kind          string        `json:"kind"`
	Version       int           `json:"version"`
	Operation     string        `json:"operation"`
	Goal          *GoalSnapshot `json:"goal,omitempty"`
	RoundsStarted uint64        `json:"roundsStarted,omitempty"`
	CreatedAt     int64         `json:"createdAt,omitempty"`
	UpdatedAt     int64         `json:"updatedAt,omitempty"`
	Cleared       *GoalRef      `json:"cleared,omitempty"`
	ClearedAt     int64         `json:"clearedAt,omitempty"`
}

func (c GoalChange) MarshalJSON() ([]byte, error) {
	if c.Operation == "clear" {
		return json.Marshal(map[string]any{
			"kind": c.Kind, "version": c.Version, "operation": c.Operation,
			"cleared": c.Cleared, "clearedAt": c.ClearedAt,
		})
	}
	return json.Marshal(map[string]any{
		"kind": c.Kind, "version": c.Version, "operation": c.Operation,
		"goal": c.Goal, "roundsStarted": c.RoundsStarted,
		"createdAt": c.CreatedAt, "updatedAt": c.UpdatedAt,
	})
}

type GoalProjection struct {
	Goal          *GoalSnapshot
	RoundsStarted uint64
	CreatedAt     int64
	UpdatedAt     int64
	LastRef       *GoalRef
}

var goalBlockCode = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

func goalFields(raw json.RawMessage, expected ...string) (map[string]json.RawMessage, error) {
	fields, err := objectValue(raw)
	if err != nil || len(fields) != len(expected) {
		return nil, errors.New("goal change has an invalid object shape")
	}
	for _, key := range expected {
		if _, ok := fields[key]; !ok {
			return nil, fmt.Errorf("goal change lacks %s", key)
		}
	}
	return fields, nil
}

func goalString(raw json.RawMessage, field string, normalized bool) (string, error) {
	var value string
	if json.Unmarshal(raw, &value) != nil || value == "" || normalized && (strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value) {
		return "", fmt.Errorf("goal change %s is invalid", field)
	}
	return value, nil
}

func goalCount(raw json.RawMessage, field string, positive bool) (uint64, error) {
	value, err := safeCount(raw)
	if err != nil || positive && value == 0 {
		return 0, fmt.Errorf("goal change %s is not a valid safe integer", field)
	}
	return value, nil
}

func decodeGoalRef(raw json.RawMessage) (GoalRef, error) {
	fields, err := goalFields(raw, "id", "revision")
	if err != nil {
		return GoalRef{}, err
	}
	id, err := goalString(fields["id"], "ref.id", false)
	if err != nil {
		return GoalRef{}, err
	}
	revision, err := goalCount(fields["revision"], "ref.revision", true)
	return GoalRef{ID: id, Revision: revision}, err
}

func decodeGoalSnapshot(raw json.RawMessage) (GoalSnapshot, error) {
	fields, err := objectValue(raw)
	if err != nil {
		return GoalSnapshot{}, errors.New("goal change goal must be an object")
	}
	var phase string
	if json.Unmarshal(fields["phase"], &phase) != nil {
		return GoalSnapshot{}, errors.New("goal change goal.phase is invalid")
	}
	keys := []string{"id", "revision", "objective", "phase", "maxGoalRounds"}
	switch phase {
	case "active", "paused", "complete":
	case "blocked":
		keys = append(keys, "blockedReason")
	default:
		return GoalSnapshot{}, errors.New("goal change goal.phase is invalid")
	}
	fields, err = goalFields(raw, keys...)
	if err != nil {
		return GoalSnapshot{}, err
	}
	id, err := goalString(fields["id"], "goal.id", false)
	if err != nil {
		return GoalSnapshot{}, err
	}
	revision, err := goalCount(fields["revision"], "goal.revision", true)
	if err != nil {
		return GoalSnapshot{}, err
	}
	objective, err := goalString(fields["objective"], "goal.objective", true)
	if err != nil {
		return GoalSnapshot{}, err
	}
	maximum, err := goalCount(fields["maxGoalRounds"], "goal.maxGoalRounds", true)
	if err != nil {
		return GoalSnapshot{}, err
	}
	goal := GoalSnapshot{ID: id, Revision: revision, Objective: objective, Phase: phase, MaxGoalRounds: maximum}
	if phase == "blocked" {
		blocked, err := goalFields(fields["blockedReason"], "code", "message")
		if err != nil {
			return GoalSnapshot{}, err
		}
		code, err := goalString(blocked["code"], "goal.blockedReason.code", false)
		if err != nil || !goalBlockCode.MatchString(code) {
			return GoalSnapshot{}, errors.New("goal change blocked reason code must be lower-kebab-case")
		}
		message, err := goalString(blocked["message"], "goal.blockedReason.message", true)
		if err != nil {
			return GoalSnapshot{}, err
		}
		goal.BlockedReason = &GoalBlockReason{Code: code, Message: message}
	}
	return goal, nil
}

// DecodeGoalChange rejects same-named but differently shaped payloads, not
// merely unknown physical event types. Numbers use the original safe-integer
// range and all snapshot/tombstone fields are required exactly.
func DecodeGoalChange(raw json.RawMessage) (GoalChange, error) {
	fields, err := objectValue(raw)
	if err != nil {
		return GoalChange{}, errors.New("goal/change requires object data")
	}
	kind, err := goalString(fields["kind"], "kind", false)
	if err != nil || kind != "goal/change" {
		return GoalChange{}, errors.New("goal/change has invalid kind")
	}
	version, err := goalCount(fields["version"], "version", true)
	if err != nil || version != 1 {
		return GoalChange{}, errors.New("unsupported goal change version")
	}
	operation, err := goalString(fields["operation"], "operation", false)
	if err != nil {
		return GoalChange{}, err
	}
	change := GoalChange{Kind: kind, Version: 1, Operation: operation}
	if operation == "clear" {
		fields, err = goalFields(raw, "kind", "version", "operation", "cleared", "clearedAt")
		if err != nil {
			return GoalChange{}, err
		}
		ref, err := decodeGoalRef(fields["cleared"])
		if err != nil {
			return GoalChange{}, err
		}
		at, err := goalCount(fields["clearedAt"], "clearedAt", false)
		if err != nil {
			return GoalChange{}, err
		}
		change.Cleared, change.ClearedAt = &ref, int64(at)
		return change, nil
	}
	switch operation {
	case "create", "edit", "pause", "resume", "complete", "block":
	default:
		return GoalChange{}, errors.New("goal change operation is invalid")
	}
	fields, err = goalFields(raw, "kind", "version", "operation", "goal", "roundsStarted", "createdAt", "updatedAt")
	if err != nil {
		return GoalChange{}, err
	}
	goal, err := decodeGoalSnapshot(fields["goal"])
	if err != nil {
		return GoalChange{}, err
	}
	rounds, err := goalCount(fields["roundsStarted"], "roundsStarted", false)
	if err != nil {
		return GoalChange{}, err
	}
	created, err := goalCount(fields["createdAt"], "createdAt", false)
	if err != nil {
		return GoalChange{}, err
	}
	updated, err := goalCount(fields["updatedAt"], "updatedAt", false)
	if err != nil || updated < created {
		return GoalChange{}, errors.New("goal change updatedAt precedes createdAt")
	}
	change.Goal, change.RoundsStarted, change.CreatedAt, change.UpdatedAt = &goal, rounds, int64(created), int64(updated)
	return change, nil
}

func sameGoalDefinition(a, b GoalSnapshot) bool {
	return a.Objective == b.Objective && a.MaxGoalRounds == b.MaxGoalRounds
}

func sameBlockedReason(a, b *GoalBlockReason) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func applyGoalChange(state *GoalProjection, seen map[string]bool, change GoalChange) error {
	if change.Operation == "clear" {
		if state.Goal == nil || change.Cleared.ID != state.Goal.ID || change.Cleared.Revision != state.Goal.Revision+1 || change.ClearedAt < state.UpdatedAt {
			return errors.New("goal clear must tombstone the next revision of the current goal")
		}
		ref := *change.Cleared
		*state = GoalProjection{LastRef: &ref}
		return nil
	}
	next := *change.Goal
	if change.Operation == "create" {
		if next.Revision != 1 || next.Phase != "active" || change.RoundsStarted != 0 || state.Goal != nil && state.Goal.Phase != "complete" || seen[next.ID] {
			return errors.New("goal create requires a fresh active revision-one goal with zero rounds")
		}
		seen[next.ID] = true
	} else {
		if state.Goal == nil {
			return fmt.Errorf("goal %s requires a current goal", change.Operation)
		}
		current := *state.Goal
		if next.ID != current.ID || next.Revision != current.Revision+1 || change.CreatedAt != state.CreatedAt || change.UpdatedAt < state.UpdatedAt || change.RoundsStarted != state.RoundsStarted {
			return fmt.Errorf("goal %s does not advance the current revision and preserve counters", change.Operation)
		}
		switch change.Operation {
		case "edit":
			if next.Phase != current.Phase || !sameBlockedReason(next.BlockedReason, current.BlockedReason) {
				return errors.New("goal edit cannot change phase or blocked reason")
			}
		case "pause":
			if !sameGoalDefinition(next, current) || current.Phase != "active" || next.Phase != "paused" {
				return errors.New("goal pause has an invalid phase transition")
			}
		case "resume":
			if !sameGoalDefinition(next, current) || current.Phase == "complete" || next.Phase != "active" || state.RoundsStarted >= next.MaxGoalRounds {
				return errors.New("goal resume has an invalid phase transition or exhausted round budget")
			}
		case "complete":
			if !sameGoalDefinition(next, current) || current.Phase == "complete" || next.Phase != "complete" {
				return errors.New("goal complete has an invalid phase transition")
			}
		case "block":
			if !sameGoalDefinition(next, current) || current.Phase != "active" || next.Phase != "blocked" {
				return errors.New("goal block has an invalid phase transition")
			}
		}
	}
	copyGoal := next
	if next.BlockedReason != nil {
		copyReason := *next.BlockedReason
		copyGoal.BlockedReason = &copyReason
	}
	ref := GoalRef{ID: next.ID, Revision: next.Revision}
	state.Goal, state.RoundsStarted, state.CreatedAt, state.UpdatedAt, state.LastRef = &copyGoal, change.RoundsStarted, change.CreatedAt, change.UpdatedAt, &ref
	return nil
}

// FoldGoal reconstructs only original v4 goal facts. A goal-sourced user
// message advances rounds only when it was actually admitted to the surface;
// queued inbox messages and process-local activation never change this fold.
type goalFoldState struct {
	GoalProjection
	seen map[string]bool
}

func nextGoal(state goalFoldState, event Event) (goalFoldState, error) {
	switch event.Type {
	case "goal/change":
		change, err := DecodeGoalChange(event.Data)
		if err == nil && change.Operation == "create" {
			copied := make(map[string]bool, len(state.seen)+1)
			for id, present := range state.seen {
				copied[id] = present
			}
			state.seen = copied
		}
		if err == nil {
			err = applyGoalChange(&state.GoalProjection, state.seen, change)
		}
		if err != nil {
			return goalFoldState{}, fmt.Errorf("goal/change at seq %d: %w", event.Seq, err)
		}
	case "user/message":
		fields, err := objectValue(event.Data)
		if err != nil {
			return goalFoldState{}, fmt.Errorf("goal user/message at seq %d: %w", event.Seq, err)
		}
		source, err := objectValue(fields["source"])
		if err != nil {
			return state, nil // message row validation owns malformed non-goal sources
		}
		if !bytes.Equal(source["kind"], []byte(`"goal"`)) {
			return state, nil
		}
		id, err := goalString(source["goalId"], "source.goalId", false)
		if err != nil {
			return goalFoldState{}, err
		}
		revision, err := goalCount(source["revision"], "source.revision", true)
		if err != nil {
			return goalFoldState{}, err
		}
		round, err := goalCount(source["round"], "source.round", true)
		if err != nil {
			return goalFoldState{}, err
		}
		if state.Goal == nil || state.Goal.Phase != "active" || id != state.Goal.ID || revision != state.Goal.Revision || round != state.RoundsStarted+1 || round > state.Goal.MaxGoalRounds {
			return goalFoldState{}, fmt.Errorf("goal round at seq %d is not the next admitted round", event.Seq)
		}
		state.RoundsStarted = round
	}
	return state, nil
}

func FoldGoal(events []Event) (GoalProjection, error) {
	state := goalFoldState{seen: make(map[string]bool)}
	for _, event := range events {
		var err error
		state, err = nextGoal(state, event)
		if err != nil {
			return GoalProjection{}, err
		}
	}
	return cloneGoalProjection(state.GoalProjection), nil
}

func cloneGoalProjection(state GoalProjection) GoalProjection {
	if state.Goal != nil {
		goal := *state.Goal
		if goal.BlockedReason != nil {
			reason := *goal.BlockedReason
			goal.BlockedReason = &reason
		}
		state.Goal = &goal
	}
	if state.LastRef != nil {
		ref := *state.LastRef
		state.LastRef = &ref
	}
	return state
}

var goalDefinition = ProjectionDefinition[goalFoldState]{Key: "goal", Version: 1,
	Init: func(Header, uint64) goalFoldState { return goalFoldState{seen: make(map[string]bool)} }, Apply: nextGoal,
}

func (s *LiveSession) Goal() (GoalProjection, error) {
	return ReadProjection(ProjectionHandle[goalFoldState]{s, &goalDefinition}, LiveCut, func(state goalFoldState) GoalProjection { return cloneGoalProjection(state.GoalProjection) })
}

func isGoalSourcedMessage(event Event) bool {
	if event.Type != "user/message" {
		return false
	}
	fields, err := objectValue(event.Data)
	if err != nil {
		return false
	}
	source, err := objectValue(fields["source"])
	return err == nil && bytes.Equal(source["kind"], []byte(`"goal"`))
}
