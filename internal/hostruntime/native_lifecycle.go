package hostruntime

import (
	"context"
	"errors"
	"sync"

	"go-bot/harness/assembly"
	"go-bot/harness/interaction"
	"go-bot/harness/session"
)

type hostSessionOperation struct {
	done     chan struct{}
	cancel   context.CancelFunc // Creation only; stop has its caller's budget.
	runtime  *NativeRuntime
	live     *assembly.NativeLive
	session  *sessionformat.LiveSession
	stopping bool
	err      error // Read only after done.
}

type hostHistoryGate struct {
	mu    sync.RWMutex
	users int
}

// AwaitSession waits for an already admitted creation without starting another
// Agent or reading an incompletely prepared journal.
func (h *NativeHost) AwaitSession(ctx context.Context, id string) (*NativeRuntime, error) {
	h.mu.Lock()
	pending := h.operations[id]
	h.mu.Unlock()
	if pending != nil && !pending.stopping {
		select {
		case <-pending.done:
			if pending.err != nil {
				return nil, pending.err
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrHostClosed
	}
	if runtime := h.sessions[id]; runtime != nil {
		return runtime, nil
	}
	return nil, ErrHostSessionAbsent
}

// Only the actual Session file's preparation excludes cold readers. The
// write gate is released as soon as a real Go LiveSession can serve reads,
// before setup or extension callbacks. Other Session IDs never share it.
func (h *NativeHost) historyGate(id string, write bool) func() {
	h.mu.Lock()
	if h.historyLocks == nil {
		h.historyLocks = make(map[string]*hostHistoryGate)
	}
	gate := h.historyLocks[id]
	if gate == nil {
		gate = &hostHistoryGate{}
		h.historyLocks[id] = gate
	}
	gate.users++
	h.mu.Unlock()
	if write {
		gate.mu.Lock()
	} else {
		gate.mu.RLock()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			if write {
				gate.mu.Unlock()
			} else {
				gate.mu.RUnlock()
			}
			h.mu.Lock()
			gate.users--
			if gate.users == 0 {
				delete(h.historyLocks, id)
			}
			h.mu.Unlock()
		})
	}
}

func (h *NativeHost) sessionHistory(id string) *sessionformat.LiveSession {
	h.mu.Lock()
	if operation := h.operations[id]; operation != nil && operation.session != nil {
		s := operation.session
		h.mu.Unlock()
		return s
	}
	if operation := h.operations[id]; operation != nil && operation.live != nil {
		live := operation.live.Session
		h.mu.Unlock()
		return live
	}
	runtime := h.sessions[id]
	h.mu.Unlock()
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.live == nil {
		return nil
	}
	return runtime.live.Session
}

// DurableSessionSuffix includes a prepared Session without publishing its
// Agent or exposing a mutable Session handle to history consumers.
func (h *NativeHost) DurableSessionSuffix(id string, next int) ([]sessionformat.Event, bool, bool, error) {
	live := h.sessionHistory(id)
	if live == nil {
		return nil, false, false, nil
	}
	if int64(next) > live.DurableCursor()+1 {
		return live.DurableEvents(), true, true, nil
	}
	events, err := live.DurableEventsFrom(next)
	return events, false, true, err
}

// SessionArtifact chooses the actual Go fsync watermark during preparation,
// execution and teardown, using disk only when no such owner exists.
func (h *NativeHost) SessionArtifact(ctx context.Context, id string) (artifact sessionformat.PhysicalArtifact, err error) {
	err = h.WithSessionHistory(ctx, id, func() error {
		if live := h.sessionHistory(id); live != nil {
			artifact = sessionformat.PhysicalArtifact{Header: live.Header(), Events: live.DurableEvents(), InheritedEventCount: live.InheritedEventCount()}
			return nil
		}
		if h.ReadSession == nil {
			return errors.New("Session history storage is unavailable")
		}
		artifact, err = h.ReadSession(ctx, id)
		return err
	})
	return artifact, err
}

func (h *NativeHost) finishOperation(id string, operation *hostSessionOperation, err error) {
	h.mu.Lock()
	operation.err = err
	delete(h.operations, id)
	close(operation.done)
	h.mu.Unlock()
}

// SessionHistoryWindow observes the real fsync cut, including unpublished live
// owners, and uses explicit indexed storage only when no live owner exists.
func (h *NativeHost) SessionHistoryWindow(ctx context.Context, id string, request sessionformat.HistoryRequest) (value sessionformat.HistoryWindow, err error) {
	err = h.WithSessionHistory(ctx, id, func() error {
		if live := h.sessionHistory(id); live != nil {
			var readErr error
			value, readErr = live.HistoryWindow(ctx, request)
			return readErr
		}
		if h.ReadHistory != nil {
			var readErr error
			value, readErr = h.ReadHistory(ctx, id, request)
			return readErr
		}
		if h.ReadSession == nil {
			return errors.New("Session history storage is unavailable")
		}
		artifact, readErr := h.ReadSession(ctx, id)
		if readErr != nil {
			return readErr
		}
		value, readErr = sessionformat.HistoryFromArtifact(ctx, artifact, request, func(s *sessionformat.LiveSession) error {
			_, err := interaction.RegisterQuestionProjection(s)
			return err
		})
		return readErr
	})
	return value, err
}

// DurableSessionPage reads an active owner's bounded prefix without reacquiring
// a lifecycle gate already held by history consumers.
func (h *NativeHost) DurableSessionPage(ctx context.Context, id string, request sessionformat.HistoryRequest) (sessionformat.HistoryWindow, bool, error) {
	live := h.sessionHistory(id)
	if live == nil {
		return sessionformat.HistoryWindow{}, false, nil
	}
	value, err := live.HistoryWindow(ctx, request)
	return value, true, err
}
