package proactive

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

const mainID = "00000000-0000-4000-8000-000000000001"

type memoryStorage struct {
	mu   sync.Mutex
	data []byte
}

func (m *memoryStorage) Load() (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var state State
	if len(m.data) == 0 {
		return state, nil
	}
	err := json.Unmarshal(m.data, &state)
	return state, err
}
func (m *memoryStorage) Save(state State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var err error
	m.data, err = json.Marshal(state)
	return err
}

func testHooks() Hooks {
	return Hooks{
		Prepare: func(context.Context, State) error { return nil },
		Collect: func(context.Context, State) error { return nil },
		Execute: func(context.Context, State, WakeReason) (Completed, error) {
			result, _ := DecodeResult(json.RawMessage(validResult), time.Time{})
			return Completed{Turn: 1, CompletedAt: time.Now().UnixMilli(), Result: result}, nil
		},
		ReadResult: func(context.Context, State, uint64) (Completed, error) {
			return Completed{}, errors.New("no pending result")
		},
		Deliver: func(context.Context, State, Completed, string) error {
			return errors.New("quiet result must not deliver")
		},
	}
}

func TestDisabledServiceDoesNotPrepareOrExecuteOnStartupOrChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hooks := testHooks()
		calls := 0
		hooks.Prepare = func(context.Context, State) error { calls++; return nil }
		hooks.Execute = func(context.Context, State, WakeReason) (Completed, error) {
			calls++
			return Completed{}, errors.New("disabled Agent must not run")
		}
		service, err := NewService(&memoryStorage{}, hooks)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- service.Run(ctx) }()
		if err := service.Configure(false, mainID); err != nil {
			t.Fatal(err)
		}
		if err := service.Changed(); err != nil {
			t.Fatal(err)
		}
		if err := service.ModelChanged(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(24 * time.Hour)
		synctest.Wait()
		status := service.Status()
		if calls != 0 || status.Enabled || status.LastRunStartedAt != 0 {
			t.Fatalf("disabled service ran: calls=%d status=%+v", calls, status)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestAnyRunConsumesExistingChangesButKeepsLaterChanges(t *testing.T) {
	for _, trigger := range []WakeReason{InitialWake, TimedWake, ChangeWake, RecoverWake, EnabledWake} {
		for _, newChange := range []string{"none", "during-collection", "after-collection"} {
			t.Run(string(trigger)+"/"+newChange, func(t *testing.T) {
				now := time.Date(2026, 11, 21, 10, 15, 0, 0, time.UTC)
				hooks := testHooks()
				service, err := NewService(&memoryStorage{}, hooks)
				if err != nil {
					t.Fatal(err)
				}
				service.now = func() time.Time { return now }
				if err := service.Configure(true, mainID); err != nil {
					t.Fatal(err)
				}
				if err := service.Changed(); err != nil {
					t.Fatal(err)
				}
				lastChange := service.Snapshot().LastChangeAt
				if newChange == "during-collection" {
					service.hooks.Collect = func(context.Context, State) error {
						// The same millisecond still represents a new boundary.
						return service.Changed()
					}
				}
				service.hooks.Execute = func(ctx context.Context, state State, reason WakeReason) (Completed, error) {
					if reason != trigger {
						t.Fatalf("trigger changed: %s", reason)
					}
					if newChange == "after-collection" {
						now = now.Add(5 * time.Minute)
						if err := service.Changed(); err != nil {
							t.Fatal(err)
						}
					}
					return hooks.Execute(ctx, state, reason)
				}
				if err := service.runOnce(t.Context(), trigger); err != nil {
					t.Fatal(err)
				}
				state := service.Snapshot()
				if state.PendingChange != (newChange != "none") {
					t.Fatalf("pending=%v for %s", state.PendingChange, newChange)
				}
				if newChange == "none" && state.LastChangeAt != lastChange {
					t.Fatal("a run reset the quiet window without new information")
				}
				if newChange == "after-collection" && !state.ChangeEligibleAt().Equal(time.Date(2026, 11, 21, 10, 45, 0, 0, time.UTC)) {
					t.Fatalf("latest run baseline was not used: %s", state.ChangeEligibleAt())
				}
			})
		}
	}
}

func TestReenableChecksImmediatelyButHostSyncAndRestartKeepAITimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		storage := &memoryStorage{}
		past := time.Now().Add(-time.Minute).UnixMilli()
		if err := storage.Save(State{SessionID: mainID, MainSessionID: mainID, NextCheck: &Check{At: time.Now().Add(time.Hour).Format(time.RFC3339), Reason: "Existing AI decision"}, LastRunStartedAt: past, LastCompletedAt: past, ResultTurn: 1}); err != nil {
			t.Fatal(err)
		}
		calls := 0
		hooks := testHooks()
		hooks.Execute = func(_ context.Context, state State, reason WakeReason) (Completed, error) {
			if reason != EnabledWake {
				t.Fatalf("reenabling must provide its own wake: %s", reason)
			}
			calls++
			return Completed{Turn: state.ResultTurn + 1, CompletedAt: time.Now().UnixMilli(), Result: Result{WorkSummary: "Rechecked after enable", NextCheck: Check{At: time.Now().Add(time.Hour).Format(time.RFC3339), Reason: "Updated AI decision"}}}, nil
		}
		start := func() (*Service, context.CancelFunc, <-chan error) {
			service, err := NewService(storage, hooks)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- service.Run(ctx) }()
			if err := service.Configure(true, mainID); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			return service, cancel, done
		}
		service, cancel, done := start()
		if calls != 0 {
			t.Fatal("initial Host synchronization ignored the saved timer")
		}
		for want := 1; want <= 2; want++ {
			if err := service.Configure(false, mainID); err != nil {
				t.Fatal(err)
			}
			if err := service.Configure(true, mainID); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			if calls != want {
				t.Fatalf("reenable calls=%d, want=%d", calls, want)
			}
			if err := service.Configure(true, mainID); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			if calls != want {
				t.Fatal("duplicate configuration triggered another turn")
			}
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		_, cancel, done = start()
		defer cancel()
		if calls != 2 {
			t.Fatal("Host restart ran ahead of the persisted AI timer")
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestFailedCollectionDoesNotConsumeChanges(t *testing.T) {
	service, err := NewService(&memoryStorage{}, testHooks())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Configure(true, mainID); err != nil {
		t.Fatal(err)
	}
	if err := service.Changed(); err != nil {
		t.Fatal(err)
	}
	service.hooks.Collect = func(context.Context, State) error { return errors.New("activity source unavailable") }
	if err := service.runOnce(t.Context(), TimedWake); err == nil {
		t.Fatal("failed collection became a successful check")
	}
	if !service.Snapshot().PendingChange {
		t.Fatal("failed collection consumed pending information")
	}
}

func TestServiceTimerRunsIndependentlyAndDoesNotReplayConsumedWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		var calls int
		hooks := testHooks()
		hooks.Execute = func(_ context.Context, _ State, reason WakeReason) (Completed, error) {
			calls++
			next := time.Now().Add(time.Hour)
			if calls == 1 {
				if reason != InitialWake {
					t.Fatalf("first reason=%s", reason)
				}
				next = start.Add(15 * time.Minute)
			} else if calls == 2 && reason != TimedWake {
				t.Fatalf("AI timer did not beat the window: %s", reason)
			} else if calls == 3 && reason != ChangeWake {
				t.Fatalf("new information did not use its window: %s", reason)
			}
			return Completed{Turn: uint64(calls), CompletedAt: time.Now().UnixMilli(), Result: Result{WorkSummary: "Checked", NextCheck: Check{At: next.Format(time.RFC3339), Reason: "AI selected"}}}, nil
		}
		service, err := NewService(&memoryStorage{}, hooks)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- service.Run(ctx) }()
		if err := service.Configure(true, mainID); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if calls != 1 {
			t.Fatalf("initial calls=%d", calls)
		}
		time.Sleep(8 * time.Minute)
		if err := service.Changed(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(7 * time.Minute)
		synctest.Wait()
		if calls != 2 || service.Snapshot().PendingChange {
			t.Fatalf("timed run did not consume old change: calls=%d state=%+v", calls, service.Snapshot())
		}
		time.Sleep(15 * time.Minute)
		synctest.Wait()
		if calls != 2 {
			t.Fatal("old 10:30 window replayed consumed changes")
		}
		if err := service.Changed(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(15 * time.Minute)
		synctest.Wait()
		if calls != 3 {
			t.Fatalf("new change should run at 10:45: %d", calls)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestDisablingCancelsOnlyBackgroundAndFailedRunDoesNotSpin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hooks := testHooks()
		calls := 0
		hooks.Execute = func(ctx context.Context, _ State, _ WakeReason) (Completed, error) {
			calls++
			if calls == 1 {
				<-ctx.Done()
				return Completed{}, ctx.Err()
			}
			return Completed{}, errors.New("background turn did not submit structured_output")
		}
		service, err := NewService(&memoryStorage{}, hooks)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- service.Run(ctx) }()
		_ = service.Configure(true, mainID)
		synctest.Wait()
		if !service.Status().Enabled || service.Status().Phase != "running" {
			t.Fatalf("status=%+v", service.Status())
		}
		_ = service.Configure(false, mainID)
		synctest.Wait()
		if service.Status().Phase != "disabled" || service.Status().Error != "" {
			t.Fatalf("user stop was recorded as a failure: %+v", service.Status())
		}
		_ = service.Configure(true, mainID)
		synctest.Wait()
		if calls != 2 || service.Status().Phase != "error" {
			t.Fatalf("status=%+v calls=%d", service.Status(), calls)
		}
		time.Sleep(time.Hour)
		synctest.Wait()
		if calls != 2 {
			t.Fatal("failed initial turn spun through repeated model calls")
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestPendingNotificationRecoversSameMessageIDAndQuietResultNeverDelivers(t *testing.T) {
	store := FileStorage{Path: filepath.Join(t.TempDir(), "storages", "proactive.json")}
	hooks := testHooks()
	service, err := NewService(store, hooks)
	if err != nil {
		t.Fatal(err)
	}
	_ = service.Configure(true, mainID)
	if err := service.runOnce(t.Context(), InitialWake); err != nil {
		t.Fatal(err)
	}
	if service.Snapshot().DeliveryMessageID != "" {
		t.Fatal("quiet result manufactured a delivery")
	}
	result, _ := DecodeResult(json.RawMessage(validResult), time.Time{})
	result.Notify = &Notification{Message: "Verified update", Evidence: []Evidence{}}
	completed := Completed{Turn: 2, CompletedAt: time.Now().UnixMilli(), Result: result}
	if err := service.apply(completed); err != nil {
		t.Fatal(err)
	}
	before := service.Snapshot()
	if before.DeliveryMessageID == "" {
		t.Fatal("notification has no recovery identity")
	}
	hooks.ReadResult = func(_ context.Context, state State, turn uint64) (Completed, error) {
		if turn != 2 || state.SessionID != before.SessionID {
			t.Fatal("wrong background result reference")
		}
		return completed, nil
	}
	hooks.Deliver = func(_ context.Context, state State, got Completed, id string) error {
		if id != before.DeliveryMessageID || state.MainSessionID != mainID || got.Result.Notify.Message != "Verified update" {
			t.Fatal("delivery reference changed across restart")
		}
		return nil
	}
	restored, err := NewService(store, hooks)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Configure(true, mainID); err != nil {
		t.Fatal(err)
	}
	if err := restored.deliver(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := store.Load()
	if err != nil || after.DeliveryMessageID != "" || after.ResultTurn != 2 || after.NextCheck == nil {
		t.Fatalf("restored projection=%+v error=%v", after, err)
	}
	if err := restored.apply(completed); err != nil || restored.Snapshot().DeliveryMessageID != "" {
		t.Fatal("replaying a settled result manufactured another notification")
	}
}

func TestDisablingCancelsInFlightDeliveryWithoutCancellingItsDurableResult(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		hooks := testHooks()
		completed := Completed{Turn: 1, CompletedAt: time.Now().UnixMilli(), Result: Result{WorkSummary: "Checked", NextCheck: Check{At: time.Now().Add(time.Hour).Format(time.RFC3339), Reason: "Next update"}, Notify: &Notification{Message: "Verified finding", Evidence: []Evidence{}}}}
		hooks.ReadResult = func(context.Context, State, uint64) (Completed, error) { return completed, nil }
		hooks.Deliver = func(ctx context.Context, _ State, _ Completed, _ string) error {
			calls++
			if calls == 1 {
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		}
		service, err := NewService(&memoryStorage{}, hooks)
		if err != nil {
			t.Fatal(err)
		}
		_ = service.Configure(true, mainID)
		if err := service.apply(completed); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- service.Run(ctx) }()
		synctest.Wait()
		if calls != 1 {
			t.Fatal("notification delivery did not start")
		}
		_ = service.Configure(false, mainID)
		synctest.Wait()
		if service.Status().Phase != "disabled" || service.Snapshot().LastError != "" || service.Snapshot().DeliveryMessageID == "" {
			t.Fatalf("stop lost the committed receipt or created an error: %+v", service.Snapshot())
		}
		_ = service.Configure(true, mainID)
		synctest.Wait()
		if calls != 2 || service.Snapshot().DeliveryMessageID != "" {
			t.Fatal("reenabling did not finish the same durable notification")
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}
