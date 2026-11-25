package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go-bot/harness/agent"
	"go-bot/harness/goal"
	"go-bot/harness/session"
)

type NativeGetGoal struct {
	Goals   *goal.NativeService
	Session *sessionformat.LiveSession
}

type NativeCreateGoal struct {
	Goals   *goal.NativeService
	Session *sessionformat.LiveSession
}

type NativeUpdateGoal struct {
	Goals   *goal.NativeService
	Session *sessionformat.LiveSession
}

type createGoalArgs struct {
	Objective     string `json:"objective"`
	MaxGoalRounds int    `json:"max_goal_rounds,omitempty"`
}

type updateGoalArgs struct {
	GoalID        string `json:"goal_id"`
	Revision      int    `json:"revision"`
	Action        string `json:"action"`
	Objective     string `json:"objective,omitempty"`
	MaxGoalRounds int    `json:"max_goal_rounds,omitempty"`
	BlockedReason string `json:"blocked_reason,omitempty"`
}

func nativeGoalExecution(ctx context.Context, session *sessionformat.LiveSession) (directHuman bool, sources []sessionformat.GoalRef, rounds []uint64, err error) {
	turn, _, ok := agent.ToolPositionFromContext(ctx)
	if !ok || session == nil {
		return false, nil, nil, errors.New("goal tool requires an active native Agent turn")
	}
	boundary := session.TurnBoundary(sessionformat.LiveCut)
	if !boundary.Open || boundary.Turn != turn {
		return false, nil, nil, errors.New("goal tool requires a durable open turn")
	}
	directHuman = boundary.DirectHuman && session.Header().Origin != "subagent"
	for _, source := range boundary.GoalRounds {
		sources = append(sources, source.GoalRef)
		rounds = append(rounds, source.Round)
	}
	return directHuman, sources, rounds, nil
}

func nativeGoalResult(view goal.NativeView, found bool) (string, error) {
	if !found {
		return `{"goal":null}`, nil
	}
	value := struct {
		Goal struct {
			ID            string                         `json:"id"`
			Revision      uint64                         `json:"revision"`
			Objective     string                         `json:"objective"`
			Phase         string                         `json:"phase"`
			RoundsStarted uint64                         `json:"roundsStarted"`
			MaxGoalRounds uint64                         `json:"maxGoalRounds"`
			BlockedReason *sessionformat.GoalBlockReason `json:"blockedReason,omitempty"`
		} `json:"goal"`
		Activation string `json:"activation"`
	}{Activation: view.Activation}
	value.Goal.ID = view.Goal.ID
	value.Goal.Revision = view.Goal.Revision
	value.Goal.Objective = view.Goal.Objective
	value.Goal.Phase = view.Goal.Phase
	value.Goal.RoundsStarted = view.RoundsStarted
	value.Goal.MaxGoalRounds = view.Goal.MaxGoalRounds
	value.Goal.BlockedReason = view.Goal.BlockedReason
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func (t NativeGetGoal) Spec() agent.ToolSpec {
	return agent.ToolSpec{
		Name: "get_goal", Description: "Read the current session goal, including the id and revision that update_goal requires.",
		Parameters: json.RawMessage(`{"type":"object","properties":{}}`), ParallelSafe: true,
	}
}

func (t NativeGetGoal) Validate(raw json.RawMessage) error {
	if t.Goals == nil || t.Session == nil {
		return errors.New("native goal service is unavailable")
	}
	var args struct{}
	if err := decodeGoalArgs(raw, &args); err != nil {
		return err
	}
	return nil
}

func (t NativeGetGoal) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	if err := t.Validate(raw); err != nil {
		return "", err
	}
	if _, _, _, err := nativeGoalExecution(ctx, t.Session); err != nil {
		return "", err
	}
	view, found, err := t.Goals.Get()
	if err != nil {
		return "", err
	}
	return nativeGoalResult(view, found)
}

func (t NativeCreateGoal) Spec() agent.ToolSpec {
	return agent.ToolSpec{
		Name: "create_goal", Description: "Create a persisted goal for a long-running direct human objective; do not use for one-turn work.",
		Parameters: json.RawMessage(`{"type":"object","properties":{"objective":{"type":"string","minLength":1},"max_goal_rounds":{"type":"integer","minimum":1}},"required":["objective"]}`),
	}
}

func (t NativeCreateGoal) Validate(raw json.RawMessage) error {
	if t.Goals == nil || t.Session == nil {
		return errors.New("native goal service is unavailable")
	}
	var args createGoalArgs
	if err := decodeGoalArgs(raw, &args); err != nil {
		return err
	}
	if strings.TrimSpace(args.Objective) == "" || args.MaxGoalRounds < 0 || uint64(args.MaxGoalRounds) > 1<<53-1 {
		return errors.New("invalid goal objective or round limit")
	}
	return nil
}

func (t NativeCreateGoal) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	if err := t.Validate(raw); err != nil {
		return "", err
	}
	human, _, _, err := nativeGoalExecution(ctx, t.Session)
	if err != nil {
		return "", err
	}
	if !human {
		return "", errors.New("create_goal requires a direct human turn on a top-level agent")
	}
	var args createGoalArgs
	if err := decodeGoalArgs(raw, &args); err != nil {
		return "", err
	}
	view, err := t.Goals.Create(ctx, args.Objective, uint64(args.MaxGoalRounds))
	if err != nil {
		return "", err
	}
	return nativeGoalResult(view, true)
}

func (t NativeUpdateGoal) Spec() agent.ToolSpec {
	return agent.ToolSpec{
		Name: "update_goal", Description: "Update the exact current goal revision. Edit, pause, and resume require a direct human turn; complete and blocked also work in the exact current goal round.",
		Parameters: json.RawMessage(`{"type":"object","properties":{"goal_id":{"type":"string","minLength":1},"revision":{"type":"integer","minimum":1},"action":{"type":"string","enum":["edit","pause","resume","complete","blocked"]},"objective":{"type":"string"},"max_goal_rounds":{"type":"integer","minimum":0},"blocked_reason":{"type":"string"}},"required":["goal_id","revision","action"]}`),
	}
}

func (t NativeUpdateGoal) Validate(raw json.RawMessage) error {
	if t.Goals == nil || t.Session == nil {
		return errors.New("native goal service is unavailable")
	}
	var args updateGoalArgs
	if err := decodeGoalArgs(raw, &args); err != nil {
		return err
	}
	if args.GoalID == "" || strings.TrimSpace(args.GoalID) != args.GoalID || args.Revision < 1 ||
		uint64(args.Revision) > 1<<53-1 || args.MaxGoalRounds < 0 || uint64(args.MaxGoalRounds) > 1<<53-1 {
		return errors.New("invalid goal ID, revision, or round limit")
	}
	switch args.Action {
	case "edit":
		if args.BlockedReason != "" || args.Objective == "" && args.MaxGoalRounds == 0 {
			return errors.New("edit requires an objective or round limit, without blocked_reason")
		}
	case "pause", "resume", "complete":
		if args.Objective != "" || args.MaxGoalRounds != 0 || args.BlockedReason != "" {
			return errors.New("unexpected fields for goal phase change")
		}
	case "blocked":
		if args.Objective != "" || args.MaxGoalRounds != 0 || strings.TrimSpace(args.BlockedReason) == "" {
			return errors.New("blocked requires only a concrete blocked_reason")
		}
	default:
		return errors.New("unknown goal action")
	}
	return nil
}

func (t NativeUpdateGoal) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	if err := t.Validate(raw); err != nil {
		return "", err
	}
	human, sources, rounds, err := nativeGoalExecution(ctx, t.Session)
	if err != nil {
		return "", err
	}
	var args updateGoalArgs
	if err := decodeGoalArgs(raw, &args); err != nil {
		return "", err
	}
	ref := sessionformat.GoalRef{ID: args.GoalID, Revision: uint64(args.Revision)}
	current, found, err := t.Goals.Get()
	if err != nil {
		return "", err
	}
	if !found || current.Goal.ID != ref.ID || current.Goal.Revision != ref.Revision {
		return "", goal.ErrStaleRevision
	}
	if args.Action == "edit" || args.Action == "pause" || args.Action == "resume" {
		if !human {
			return "", errors.New("this goal operation requires a direct human turn")
		}
	} else if !human {
		matching := false
		for i, source := range sources {
			if source == ref && rounds[i] == current.RoundsStarted {
				matching = true
				break
			}
		}
		if !matching {
			return "", errors.New("completion requires a human turn or the exact current goal round")
		}
		if args.Action == "blocked" && current.RoundsStarted < 3 {
			return "", fmt.Errorf("blocked requires three admitted goal rounds; current round is %d", current.RoundsStarted)
		}
	}
	var updated goal.NativeView
	switch args.Action {
	case "edit":
		var objective *string
		var maxRounds *uint64
		if args.Objective != "" {
			objective = &args.Objective
		}
		if args.MaxGoalRounds != 0 {
			limit := uint64(args.MaxGoalRounds)
			maxRounds = &limit
		}
		updated, err = t.Goals.Edit(ctx, ref, objective, maxRounds)
	case "pause":
		updated, err = t.Goals.Pause(ctx, ref)
	case "resume":
		if current.Goal.Phase == "paused" {
			return "", errors.New("the model cannot resume a paused goal; the user must resume it")
		}
		updated, err = t.Goals.Resume(ctx, ref)
	case "complete":
		updated, err = t.Goals.Complete(ctx, ref)
	case "blocked":
		updated, err = t.Goals.Block(ctx, ref, sessionformat.GoalBlockReason{Code: "model-reported", Message: strings.TrimSpace(args.BlockedReason)})
	}
	if err != nil {
		return "", err
	}
	return nativeGoalResult(updated, true)
}
