package goal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go-bot/harness/session"
)

// NativeView joins the durable v4 goal projection with process-local
// continuation activation. Activation is deliberately absent from the log.
type NativeView struct {
	Goal          sessionformat.GoalSnapshot `json:"goal"`
	RoundsStarted uint64                     `json:"roundsStarted"`
	CreatedAt     int64                      `json:"createdAt"`
	UpdatedAt     int64                      `json:"updatedAt"`
	Activation    string                     `json:"activation"`
}

// NativeService owns goal mutations for one live v4 Session. It does not
// migrate, translate, or reopen the old Go goal format.
type NativeService struct {
	mu         sync.Mutex
	session    *sessionformat.LiveSession
	activation string
}

func NewNative(s *sessionformat.LiveSession) (*NativeService, error) {
	if s == nil {
		return nil, errors.New("goal requires a live Session v4")
	}
	if _, err := s.Goal(); err != nil {
		return nil, err
	}
	return &NativeService{session: s, activation: "disarmed"}, nil
}

func (s *NativeService) Get() (NativeView, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked()
}

func (s *NativeService) getLocked() (NativeView, bool, error) {
	state, err := s.session.Goal()
	if err != nil || state.Goal == nil {
		return NativeView{}, false, err
	}
	return NativeView{
		Goal: *state.Goal, RoundsStarted: state.RoundsStarted,
		CreatedAt: state.CreatedAt, UpdatedAt: state.UpdatedAt,
		Activation: s.activation,
	}, true, nil
}

func (s *NativeService) Disarm() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activation = "disarmed"
}

func (s *NativeService) Create(ctx context.Context, objective string, maxRounds uint64) (NativeView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return NativeView{}, errors.New("goal objective is required")
	}
	if maxRounds == 0 {
		maxRounds = DefaultMaxRounds
	}
	if maxRounds > 1<<53-1 {
		return NativeView{}, errors.New("goal round limit exceeds a safe integer")
	}
	current, found, err := s.getLocked()
	if err != nil {
		return NativeView{}, err
	}
	if found && current.Goal.Phase != "complete" {
		return NativeView{}, fmt.Errorf("%w: an unfinished goal already exists", ErrInvalidTransition)
	}
	now := time.Now().UnixMilli()
	change := sessionformat.GoalChange{
		Kind: "goal/change", Version: 1, Operation: "create",
		Goal: &sessionformat.GoalSnapshot{
			ID: "goal-" + uuid.NewString(), Revision: 1,
			Objective: objective, Phase: "active", MaxGoalRounds: maxRounds,
		},
		RoundsStarted: 0, CreatedAt: now, UpdatedAt: now,
	}
	return s.commitLocked(ctx, change, "armed")
}

func (s *NativeService) Edit(ctx context.Context, ref sessionformat.GoalRef, objective *string, maxRounds *uint64) (NativeView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.expectLocked(ref)
	if err != nil {
		return NativeView{}, err
	}
	if objective == nil && maxRounds == nil {
		return NativeView{}, errors.New("goal edit requires an objective or round limit")
	}
	next := current.Goal
	next.Revision++
	if objective != nil {
		next.Objective = strings.TrimSpace(*objective)
		if next.Objective == "" {
			return NativeView{}, errors.New("goal objective is required")
		}
	}
	if maxRounds != nil {
		if *maxRounds == 0 || *maxRounds > 1<<53-1 {
			return NativeView{}, errors.New("goal round limit must be a positive safe integer")
		}
		next.MaxGoalRounds = *maxRounds
	}
	return s.commitCurrentLocked(ctx, current, "edit", next, s.activation)
}

func (s *NativeService) Pause(ctx context.Context, ref sessionformat.GoalRef) (NativeView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.expectLocked(ref)
	if err != nil {
		return NativeView{}, err
	}
	if current.Goal.Phase != "active" {
		return NativeView{}, ErrInvalidTransition
	}
	next := nextNativePhase(current.Goal, "paused")
	return s.commitCurrentLocked(ctx, current, "pause", next, "disarmed")
}

func (s *NativeService) Resume(ctx context.Context, ref sessionformat.GoalRef) (NativeView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.expectLocked(ref)
	if err != nil {
		return NativeView{}, err
	}
	if current.Goal.Phase == "complete" || current.RoundsStarted >= current.Goal.MaxGoalRounds ||
		current.Goal.Phase == "active" && s.activation == "armed" {
		return NativeView{}, ErrInvalidTransition
	}
	next := nextNativePhase(current.Goal, "active")
	return s.commitCurrentLocked(ctx, current, "resume", next, "armed")
}

func (s *NativeService) Complete(ctx context.Context, ref sessionformat.GoalRef) (NativeView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.expectLocked(ref)
	if err != nil {
		return NativeView{}, err
	}
	if current.Goal.Phase == "complete" {
		return NativeView{}, ErrInvalidTransition
	}
	next := nextNativePhase(current.Goal, "complete")
	return s.commitCurrentLocked(ctx, current, "complete", next, "disarmed")
}

func (s *NativeService) Block(ctx context.Context, ref sessionformat.GoalRef, reason sessionformat.GoalBlockReason) (NativeView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.expectLocked(ref)
	if err != nil {
		return NativeView{}, err
	}
	if current.Goal.Phase != "active" || !blockCode.MatchString(reason.Code) ||
		reason.Message == "" || strings.TrimSpace(reason.Message) != reason.Message {
		return NativeView{}, ErrInvalidTransition
	}
	next := nextNativePhase(current.Goal, "blocked")
	next.BlockedReason = &reason
	return s.commitCurrentLocked(ctx, current, "block", next, "disarmed")
}

func (s *NativeService) Clear(ctx context.Context, ref sessionformat.GoalRef) (sessionformat.GoalRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.expectLocked(ref)
	if err != nil {
		return sessionformat.GoalRef{}, err
	}
	tombstone := sessionformat.GoalRef{ID: current.Goal.ID, Revision: current.Goal.Revision + 1}
	now := max(time.Now().UnixMilli(), current.UpdatedAt)
	change := sessionformat.GoalChange{
		Kind: "goal/change", Version: 1, Operation: "clear",
		Cleared: &tombstone, ClearedAt: now,
	}
	if err := s.appendAndFlushLocked(ctx, change); err != nil {
		return sessionformat.GoalRef{}, err
	}
	s.activation = "disarmed"
	return tombstone, nil
}

func nextNativePhase(current sessionformat.GoalSnapshot, phase string) sessionformat.GoalSnapshot {
	return sessionformat.GoalSnapshot{
		ID: current.ID, Revision: current.Revision + 1,
		Objective: current.Objective, Phase: phase,
		MaxGoalRounds: current.MaxGoalRounds,
	}
}

func (s *NativeService) expectLocked(ref sessionformat.GoalRef) (NativeView, error) {
	current, found, err := s.getLocked()
	if err != nil {
		return NativeView{}, err
	}
	if !found {
		return NativeView{}, ErrNotFound
	}
	if ref.ID != current.Goal.ID || ref.Revision != current.Goal.Revision {
		return NativeView{}, ErrStaleRevision
	}
	return current, nil
}

func (s *NativeService) commitCurrentLocked(ctx context.Context, current NativeView, operation string, next sessionformat.GoalSnapshot, activation string) (NativeView, error) {
	change := sessionformat.GoalChange{
		Kind: "goal/change", Version: 1, Operation: operation,
		Goal: &next, RoundsStarted: current.RoundsStarted,
		CreatedAt: current.CreatedAt,
		UpdatedAt: max(time.Now().UnixMilli(), current.UpdatedAt),
	}
	return s.commitLocked(ctx, change, activation)
}

func (s *NativeService) commitLocked(ctx context.Context, change sessionformat.GoalChange, activation string) (NativeView, error) {
	if err := s.appendAndFlushLocked(ctx, change); err != nil {
		return NativeView{}, err
	}
	s.activation = activation
	view, found, err := s.getLocked()
	if err != nil || !found {
		return NativeView{}, fmt.Errorf("goal mutation replay: %w", err)
	}
	return view, nil
}

func (s *NativeService) appendAndFlushLocked(ctx context.Context, change sessionformat.GoalChange) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(change)
	if err != nil {
		return err
	}
	if _, err := s.session.Append(sessionformat.EventInput{Type: "goal/change", Data: data}); err != nil {
		return err
	}
	// A committed mutation must not be reported as absent solely because the
	// tool call was canceled after the event entered the live Session.
	flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.session.Flush(flushCtx); err != nil {
		s.activation = "disarmed"
		return err
	}
	return nil
}
