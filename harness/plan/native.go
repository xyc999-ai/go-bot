package plan

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"go-bot/harness/session"
)

// NativeService stores only the released v4 plan/mode fact in the Session.
// An in-flight selection is process-local until the next accepted step, like
// the original plan-mode controller's pending intent.
type NativeService struct {
	mu      sync.Mutex
	session *sessionformat.LiveSession
	pending *bool
}

func NewNative(record *sessionformat.LiveSession) (*NativeService, error) {
	if record == nil {
		return nil, errors.New("plan mode requires a live Session v4")
	}
	if _, err := record.Plan(); err != nil {
		return nil, err
	}
	return &NativeService{session: record}, nil
}

func (s *NativeService) View() (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.session.Plan()
	if err != nil {
		return View{}, err
	}
	view := View{Active: state.Active}
	if s.pending != nil {
		selected := *s.pending
		view.Pending = &selected
	} else if state.Pending && state.Wanted != nil {
		selected := *state.Wanted
		view.Pending = &selected
	}
	return view, nil
}

func (s *NativeService) Queue(active bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.session.Plan()
	if err != nil {
		return err
	}
	if state.Active == active {
		s.pending = nil
		return nil
	}
	s.pending = &active
	return nil
}

// SetNow commits a between-turn selection. The caller must establish an idle
// Agent boundary; a running turn uses Queue and ApplyPending instead.
func (s *NativeService) SetNow(ctx context.Context, active bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.appendLocked(ctx, active); err != nil {
		return err
	}
	s.pending = nil
	return nil
}

// ApplyPending runs after step/start but before the system prompt and request
// are assembled, so the logged mode and model-visible guidance agree.
func (s *NativeService) ApplyPending(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return nil
	}
	if err := s.appendLocked(ctx, *s.pending); err != nil {
		return err
	}
	s.pending = nil
	return nil
}

func (s *NativeService) appendLocked(ctx context.Context, active bool) error {
	state, err := s.session.Plan()
	if err != nil {
		return err
	}
	if state.Active == active {
		return nil
	}
	data, err := json.Marshal(map[string]bool{"active": active})
	if err != nil {
		return err
	}
	if _, err := s.session.Append(sessionformat.EventInput{Type: "plan/mode", Data: data}); err != nil {
		return err
	}
	return s.session.Flush(ctx)
}
