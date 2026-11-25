package hostruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"go-bot/harness/agent"
	"go-bot/harness/assembly"
	"go-bot/harness/goal"
	"go-bot/harness/interaction"
	"go-bot/harness/jobs"
	"go-bot/harness/permission"
	"go-bot/harness/plan"
	"go-bot/harness/session"
	"go-bot/harness/tools"
	"go-bot/harness/workflow"
	"go-bot/internal/modelsettings"
)

var ErrNativeTurnNotFound = errors.New("native turn ID does not belong to this Session")
var ErrNativeTurnFinished = errors.New("native turn already finished")
var ErrNativeTurnNotRunning = errors.New("native turn is not running")
var ErrNativeCompactionBusy = errors.New("native Agent is not idle for manual compaction")

// NativeRuntime owns one v4 top-level Agent inside its worker process. The
// caller activates it before exposing the worker's HTTP listener; admission
// appends to the Agent's own Session and the driver executes turns afterward.
type NativeRuntime struct {
	Factory  assembly.NativeFactory
	Identity assembly.NativeRuntimeIdentity
	// HostOwnsSchedule leaves the shared Schedule lifecycle to NativeHost;
	// this Session still receives the same top-level Schedule tools.
	HostOwnsSchedule bool
	// WorkspaceRoot is the worker-visible mounted directory used by the
	// readiness round trip. Empty defaults to Identity.Workspace.
	WorkspaceRoot string
	// These Host observers receive committed dialogue/admission, not Agent
	// execution middleware. They cannot change turn/step or tool decisions.
	UserMessageAccepted func()
	TurnCommitted       func([]sessionformat.Event)

	approvalAudience      func() bool
	mu                    sync.Mutex
	stopMu                sync.Mutex
	live                  *assembly.NativeLive
	children              *nativeChildManager
	wake                  chan struct{}
	drain                 chan struct{}
	done                  chan struct{}
	cancel                context.CancelFunc
	activeCancel          context.CancelCauseFunc
	compactDone           chan struct{}
	compactCancel         context.CancelFunc
	stopping              bool
	lastErr               error
	fatalErr              error
	closeOnce             sync.Once
	closeError            error
	activatedAt           time.Time
	startDone             chan struct{}
	startCancel           context.CancelFunc
	sessionCreation       *assembly.NativeSessionOptions
	timedQuestions        []interaction.Pending
	timedQuestionsThrough int64
}

type NativeSetup func(context.Context, *assembly.NativeLive) error

func (r *NativeRuntime) Start(ctx context.Context, resume bool) error {
	return r.start(ctx, resume, nil, nil, nil)
}

func (r *NativeRuntime) StartWithSetup(ctx context.Context, resume bool, setup NativeSetup) error {
	return r.start(ctx, resume, nil, nil, setup)
}

func (r *NativeRuntime) start(ctx context.Context, resume bool, sessionReady func(*sessionformat.LiveSession), readable func(*assembly.NativeLive), setup NativeSetup) (err error) {
	ctx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	if r.live != nil || r.stopping || r.startDone != nil {
		r.mu.Unlock()
		cancel()
		return errors.New("native worker already started")
	}
	r.startDone, r.startCancel = make(chan struct{}), cancel
	r.mu.Unlock()
	var prepared *assembly.NativeLive
	defer func() {
		cancel()
		if err != nil && prepared != nil {
			cleanup := prepared.Close()
			if cleanup != nil && !errors.Is(err, cleanup) {
				err = errors.Join(err, cleanup)
			}
			if cleanup != nil {
				// Retain ownership if cleanup was not confirmed. Host must not
				// permit a second same-id lifetime over these resources.
				r.mu.Lock()
				r.live, r.fatalErr, r.stopping = prepared, err, true
				r.mu.Unlock()
			} else {
				r.mu.Lock()
				if r.live == prepared {
					r.live = nil
				}
				r.mu.Unlock()
			}
		}
		r.mu.Lock()
		r.startCancel = nil
		close(r.startDone)
		r.mu.Unlock()
	}()
	_, hasChildStorage := r.Factory.Storage.(assembly.NativeChildStorageFactory)
	r.Factory.Children = nil
	if hasChildStorage {
		r.Factory.Children = r
	}
	factory := r.Factory
	ready := factory.SessionReady
	factory.SessionReady = func(s *sessionformat.LiveSession) {
		if sessionReady != nil {
			sessionReady(s)
		}
		if ready != nil {
			ready(s)
		}
	}
	beforePublish := factory.BeforePublish
	factory.BeforePublish = func(ctx context.Context, live *assembly.NativeLive) error {
		prepared = live
		if readable != nil {
			readable(live)
		}
		if beforePublish != nil {
			if err := beforePublish(ctx, live); err != nil {
				return err
			}
		}
		if setup != nil {
			if err := setup(ctx, live); err != nil {
				return err
			}
		}
		return ctx.Err()
	}
	var live *assembly.NativeLive
	if resume {
		live, err = factory.Resume(ctx, r.Identity)
	} else {
		options := r.sessionCreation
		r.sessionCreation = nil
		if options == nil {
			live, err = factory.Activate(ctx, r.Identity)
		} else {
			live, err = factory.ActivateWithSessionOptions(ctx, r.Identity, *options)
		}
	}
	if err != nil {
		return err
	}
	// Schedule delivery can synchronously reenter admission during Start.
	// It may enqueue into the real Session, but no driver runs until commit.
	r.mu.Lock()
	if err := ctx.Err(); err != nil || r.stopping {
		r.mu.Unlock()
		if err != nil {
			return err
		}
		return context.Canceled
	}
	r.live = live
	r.mu.Unlock()
	if r.Factory.Schedule != nil && !r.HostOwnsSchedule {
		if err := r.Factory.Schedule.Start(ctx); err != nil {
			r.Factory.Schedule.Close()
			return fmt.Errorf("start native Schedule: %w", err)
		}
	}
	r.mu.Lock()
	if err := ctx.Err(); err != nil || r.stopping {
		r.mu.Unlock()
		if r.Factory.Schedule != nil && !r.HostOwnsSchedule {
			r.Factory.Schedule.Close()
		}
		if err != nil {
			return err
		}
		return context.Canceled
	}
	r.activatedAt = time.Now().UTC()
	if hasChildStorage {
		r.children = newNativeChildManager(r.Factory, r.Identity, live, r.kick)
	}
	if live.Questions != nil {
		live.Questions.Enable()
		live.Questions.SetApprovalAudience(r.approvalAudience)
	}
	r.wake = make(chan struct{}, 1)
	r.drain = make(chan struct{})
	r.done = make(chan struct{})
	var runCtx context.Context
	runCtx, r.cancel = context.WithCancel(context.Background())
	r.startCancel = nil // Publication detaches the creation-only signal.
	go r.drive(runCtx)
	if live.Resource.Jobs != nil {
		go r.watchJobs(runCtx, live.Resource.Jobs)
	}
	// A resumed journal can have an interrupted turn even with an empty inbox.
	// Wake once so RunNext repairs that tail before this worker becomes idle.
	r.kick() // Also observes input accepted by creation listeners before start.
	r.mu.Unlock()
	return nil
}

func (r *NativeRuntime) Activity() (sessionformat.ActivitySnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil {
		return sessionformat.ActivitySnapshot{}, errors.New("native worker is not ready")
	}
	snapshot := r.live.Session.DurableActivity()
	if r.activatedAt.After(snapshot.Since) {
		snapshot.Since = r.activatedAt
	}
	if r.activeCancel != nil || r.compactDone != nil || r.stopping || r.fatalErr != nil {
		snapshot.Idle = false
	}
	if r.live.Resource.Jobs != nil {
		active, since := r.live.Resource.Jobs.Activity()
		if active {
			snapshot.Idle = false
		}
		if since.After(snapshot.Since) {
			snapshot.Since = since
		}
	}
	if r.children != nil {
		active, since := r.children.Activity()
		if active {
			snapshot.Idle = false
		}
		if since.After(snapshot.Since) {
			snapshot.Since = since
		}
	}
	return snapshot, nil
}

// A visible background Job is a producer-owned user message, just as in
// DeepSeek's tool-jobs plugin. Delivery is independent of the turn driver so
// a settlement during a model call can enter the next step of that turn.
func (r *NativeRuntime) watchJobs(ctx context.Context, registry *jobs.Registry) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.done:
			return
		case <-r.drain:
			return
		case <-registry.SettledWake():
		}
		if err := registry.DeliverSettled(func(job jobs.Snapshot) error {
			return r.deliverJobCompletion(ctx, job)
		}); err != nil {
			r.mu.Lock()
			r.lastErr, r.fatalErr = err, err
			r.mu.Unlock()
			r.Abort()
			return
		}
	}
}

func (r *NativeRuntime) deliverJobCompletion(ctx context.Context, job jobs.Snapshot) error {
	message, err := nativeJobNoticeMessage(job)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping || r.fatalErr != nil || r.live == nil || ctx.Err() != nil {
		return nil // Disposal drops a producer notice with its worker.
	}
	if r.activeCancel != nil {
		_, err = r.live.Agent.InjectMessage(ctx, message)
	} else {
		_, err = r.live.Agent.AcceptMessage(ctx, message)
	}
	if err != nil {
		return fmt.Errorf("persist background Job %s completion: %w", job.ID, err)
	}
	// Also close the race where the active turn settled before this injection.
	r.kick()
	return nil
}

func nativeJobNoticeMessage(job jobs.Snapshot) ([]byte, error) {
	status := fmt.Sprintf("[status: %s]", job.Status)
	content := fmt.Sprintf("background job %s (%s: %s) finished %s. Read its output with job_output.", job.ID, job.Kind, job.Label, status)
	summary := nativeBoundContextSummary(fmt.Sprintf("%s %s %s", job.Kind, job.Label, status))
	return json.Marshal(map[string]any{
		"id": uuid.NewString(), "role": "user",
		"source":  map[string]any{"kind": "tool-jobs", "form": "notice", "summary": summary},
		"content": []map[string]any{{"type": "text", "text": content}},
	})
}

// DeepSeek bounds producer-owned notice summaries to 120 characters. Keep
// unbounded Job labels out of the collapsed context row.
func nativeBoundContextSummary(summary string) string {
	const maxChars = 120
	chars := []rune(summary)
	if len(chars) <= maxChars {
		return summary
	}
	return string(chars[:maxChars-1]) + "…"
}

func (r *NativeRuntime) kick() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *NativeRuntime) SubmitText(ctx context.Context, content string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil || r.stopping || r.fatalErr != nil {
		return "", errors.New("native worker is not ready")
	}
	select {
	case <-r.done:
		return "", errors.New("native worker has stopped")
	default:
	}
	id, err := r.live.Agent.AcceptText(ctx, content)
	if err != nil {
		return "", err
	}
	if r.UserMessageAccepted != nil {
		r.UserMessageAccepted()
	}
	r.kick()
	return id, nil
}

func (r *NativeRuntime) SubmitMessage(ctx context.Context, message []byte) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil || r.stopping || r.fatalErr != nil {
		return "", errors.New("native worker is not ready")
	}
	select {
	case <-r.done:
		return "", errors.New("native worker has stopped")
	default:
	}
	id, err := r.live.Agent.AcceptMessage(ctx, message)
	if err != nil {
		return "", err
	}
	var source struct{ Source struct{ Kind string } }
	if r.UserMessageAccepted != nil && json.Unmarshal(message, &source) == nil && source.Source.Kind == "user" {
		r.UserMessageAccepted()
	}
	r.kick()
	return id, nil
}

func (r *NativeRuntime) UpdateQueuedMessage(ctx context.Context, id string, steer bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil || r.stopping || r.fatalErr != nil {
		return errors.New("native worker is not ready")
	}
	flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return r.live.Agent.UpdateQueuedMessage(flushCtx, id, steer)
}

func (r *NativeRuntime) DurableSessionEvents() ([]sessionformat.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil {
		return nil, errors.New("native worker is not ready")
	}
	return r.live.Session.DurableEvents(), nil
}

// DurableSessionSuffix returns an atomic committed view for the history index.
func (r *NativeRuntime) DurableSessionSuffix(next int) ([]sessionformat.Event, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil {
		return nil, false, errors.New("native worker is not ready")
	}
	if int64(next) > r.live.Session.DurableCursor()+1 {
		return r.live.Session.DurableEvents(), true, nil
	}
	events, err := r.live.Session.DurableEventsFrom(next)
	return events, false, err
}

// CompactNow reserves an idle Agent before its first await. Submissions may
// still join the inbox while the summary model runs, but the driver cannot
// claim them until the closed compaction transaction has been flushed.
func (r *NativeRuntime) CompactNow(ctx context.Context) (bool, error) {
	r.mu.Lock()
	if r.live == nil || r.stopping || r.fatalErr != nil {
		r.mu.Unlock()
		return false, errors.New("native worker is not ready")
	}
	if r.activeCancel != nil || r.compactDone != nil {
		r.mu.Unlock()
		return false, ErrNativeCompactionBusy
	}
	inbox := r.live.Session.Inbox()
	if len(inbox.NextTurn) != 0 || len(inbox.NextStep) != 0 {
		r.mu.Unlock()
		return false, ErrNativeCompactionBusy
	}
	done := make(chan struct{})
	compactCtx, cancel := context.WithCancel(ctx)
	r.compactDone, r.compactCancel = done, cancel
	live := r.live
	r.mu.Unlock()
	compacted, err := live.Agent.CompactNow(compactCtx)
	cancel()
	r.mu.Lock()
	r.compactDone, r.compactCancel = nil, nil
	close(done)
	if errors.Is(err, sessionformat.ErrV4UncertainWrite) {
		r.fatalErr = err
	}
	r.mu.Unlock()
	r.kick()
	return compacted, err
}

// DeliverSchedule is the in-container Schedule service's persistence boundary:
// it admits a producer-owned followup to this Agent's v4 inbox before the
// Schedule task can advance its receipt and target.
func (r *NativeRuntime) DeliverSchedule(ctx context.Context, content string) (string, error) {
	message, err := json.Marshal(map[string]any{
		"id": uuid.NewString(), "role": "user", "source": map[string]any{"kind": "schedule"},
		"content": []map[string]any{{"type": "text", "text": content}},
	})
	if err != nil {
		return "", err
	}
	return r.SubmitMessage(ctx, message)
}

func (r *NativeRuntime) GetPlan() (plan.View, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil {
		return plan.View{}, errors.New("native worker is not ready")
	}
	return r.live.Plan.View()
}

func (r *NativeRuntime) GetGoal() (goal.NativeView, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil || r.live.Goals == nil {
		return goal.NativeView{}, false, errors.New("native worker is not ready")
	}
	return r.live.Goals.Get()
}

func (r *NativeRuntime) Usage() (sessionformat.UsageSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil {
		return sessionformat.UsageSnapshot{}, errors.New("native worker is not ready")
	}
	return r.live.Session.Usage()
}

func (r *NativeRuntime) ResumeGoal(ctx context.Context, ref sessionformat.GoalRef) (goal.NativeView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil || r.live.Goals == nil || r.stopping || r.fatalErr != nil {
		return goal.NativeView{}, errors.New("native worker is not ready")
	}
	view, err := r.live.Goals.Resume(ctx, ref)
	if err != nil {
		return goal.NativeView{}, err
	}
	r.kick()
	return view, nil
}

func (r *NativeRuntime) SetPlan(ctx context.Context, active bool) (plan.View, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil || r.stopping || r.fatalErr != nil {
		return plan.View{}, errors.New("native worker is not ready")
	}
	var err error
	if r.activeCancel != nil {
		err = r.live.Plan.Queue(active)
	} else {
		err = r.live.Plan.SetNow(ctx, active)
	}
	if err != nil {
		return plan.View{}, err
	}
	return r.live.Plan.View()
}

var ErrPermissionBusy = errors.New("permission cannot change while work is running")

func (r *NativeRuntime) GetPermission() (permission.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil {
		return permission.State{}, errors.New("session is unavailable")
	}
	return permission.ReadSession(r.live.Session), nil
}

func (r *NativeRuntime) SetPermission(ctx context.Context, mode permission.Mode) (permission.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil || r.stopping || r.fatalErr != nil {
		return permission.State{}, errors.New("session is unavailable")
	}
	current := permission.ReadSession(r.live.Session)
	if mode == current.Mode {
		return current, nil
	}
	if r.activeCancel != nil {
		return current, ErrPermissionBusy
	}
	if r.children != nil {
		if active, _ := r.children.Activity(); active {
			return current, ErrPermissionBusy
		}
	}
	if mode != permission.FullAccess {
		if r.live.Resource.ProbeSandbox == nil {
			return current, errors.New("sandbox unavailable on this Host")
		}
		probe := permission.WithScope(ctx, permission.Scope{Mode: mode, Root: r.live.Session.Header().CWD})
		if err := r.live.Resource.ProbeSandbox(probe); err != nil {
			return current, err
		}
	}
	if r.live.Resource.Jobs != nil {
		for _, job := range r.live.Resource.Jobs.List() {
			if job.Status == jobs.Running || job.Status == jobs.Stopping {
				return current, ErrPermissionBusy
			}
		}
	}
	if err := permission.Save(ctx, r.live.Session, mode, false, false); err != nil {
		return current, err
	}
	return permission.ReadSession(r.live.Session), nil
}

func (r *NativeRuntime) AttachApprovalClient() func() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil {
		return func() {}
	}
	return r.live.Questions.AttachApprovalClient()
}

func (r *NativeRuntime) PendingQuestions() ([]interaction.Pending, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil || r.live.Questions == nil {
		return nil, errors.New("native worker is not ready")
	}
	return r.pendingQuestionsLocked(), nil
}

func (r *NativeRuntime) pendingQuestionsLocked() []interaction.Pending {
	cursor := r.live.Session.DurableCursor()
	if r.timedQuestions == nil || cursor != r.timedQuestionsThrough {
		questions, err := interaction.SessionQuestions(r.live.Session)
		if err != nil {
			panic(err)
		}
		r.timedQuestions = append([]interaction.Pending{}, questions...)
		r.timedQuestionsThrough = cursor
	}
	live := r.live.Questions.Pending()
	projected := make([]interaction.Pending, 0, len(r.timedQuestions)+len(live))
	seen := make(map[string]int, len(r.timedQuestions))
	for _, question := range r.timedQuestions {
		if question.State == "continued" {
			seen[question.CallID] = len(projected)
			projected = append(projected, question)
		}
	}
	for _, question := range live {
		if index, ok := seen[question.CallID]; ok {
			projected[index] = question
		} else {
			seen[question.CallID] = len(projected)
			projected = append(projected, question)
		}
	}
	return projected
}

func (r *NativeRuntime) AnswerQuestion(ctx context.Context, callID string, answer interaction.Answer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil || r.live.Questions == nil || r.stopping {
		return interaction.ErrUnavailable
	}
	err := r.live.Questions.Answer(ctx, callID, answer)
	if err == nil || (!errors.Is(err, interaction.ErrNotPending) && !errors.Is(err, interaction.ErrContinued)) {
		return err
	}
	var continued *interaction.Pending
	for _, question := range r.pendingQuestionsLocked() {
		if question.CallID == callID && question.State == "continued" {
			copy := question
			continued = &copy
			break
		}
	}
	if continued == nil {
		return interaction.ErrNotPending
	}
	if err := interaction.ValidateAnswer(continued.Questions, answer); err != nil {
		return err
	}
	inbox := r.live.Session.Inbox()
	for _, queued := range append(inbox.NextStep, inbox.NextTurn...) {
		var message struct {
			Source struct {
				Kind   string `json:"kind"`
				CallID string `json:"callId"`
			} `json:"source"`
		}
		if json.Unmarshal(queued, &message) == nil && message.Source.Kind == "user-question-reply" && message.Source.CallID == callID {
			return interaction.ErrAlreadyAnswered
		}
	}
	content, err := json.Marshal(map[string]any{
		"kind": "answer_to_pending_question", "tool": "ask_user_question", "callId": callID,
		"questions": continued.Questions, "answers": answer.Answers,
	})
	if err != nil {
		return err
	}
	message, err := json.Marshal(map[string]any{
		"id": uuid.NewString(), "role": "user",
		"source":  map[string]any{"kind": "user-question-reply", "callId": callID, "outcome": "answered"},
		"content": []map[string]any{{"type": "text", "text": string(content)}},
	})
	if err != nil {
		return err
	}
	if _, err := r.live.Agent.SteerMessage(ctx, message); err != nil {
		return err
	}
	_ = r.live.Questions.Cancel(ctx, callID) // A timed foreground wait has already returned.
	r.kick()
	return nil
}

func (r *NativeRuntime) CancelQuestion(ctx context.Context, callID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil || r.live.Questions == nil || r.stopping {
		return interaction.ErrUnavailable
	}
	for _, question := range r.pendingQuestionsLocked() {
		if question.CallID == callID && question.State == "continued" {
			return interaction.ErrNotPending // Continued questions stay answerable until a reply is admitted.
		}
	}
	return r.live.Questions.Cancel(ctx, callID)
}

func (r *NativeRuntime) drive(ctx context.Context) {
	defer close(r.done)
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-r.drain:
		}
		for ctx.Err() == nil {
			r.mu.Lock()
			compactDone := r.compactDone
			r.mu.Unlock()
			if compactDone != nil {
				select {
				case <-compactDone:
				case <-ctx.Done():
					return
				}
				continue
			}
			turnCtx := ctx
			cancelTimeout := func() {}
			if duration := r.Factory.Limits.MaxTurnDuration; duration > 0 {
				turnCtx, cancelTimeout = context.WithTimeout(ctx, duration)
			}
			turnCtx, cancelTurn := context.WithCancelCause(turnCtx)
			r.mu.Lock()
			if r.compactDone != nil {
				r.mu.Unlock()
				cancelTurn(nil)
				cancelTimeout()
				continue
			}
			r.activeCancel = cancelTurn
			r.mu.Unlock()
			cursor := int(r.live.Session.DurableCursor() + 1)
			ran, _, err := r.live.Agent.RunNext(turnCtx)
			cancelTurn(nil)
			cancelTimeout()
			r.mu.Lock()
			r.activeCancel = nil
			if errors.Is(err, modelsettings.ErrUnconfigured) || errors.Is(err, modelsettings.ErrUnavailable) {
				r.mu.Unlock()
				break
			}
			if err != nil {
				r.lastErr = err
				if r.live.Goals != nil {
					r.live.Goals.Disarm()
				}
				if errors.Is(err, sessionformat.ErrV4UncertainWrite) {
					r.fatalErr = err
				}
			}
			fatal := r.fatalErr != nil
			r.mu.Unlock()
			if ran && r.TurnCommitted != nil {
				if events, readErr := r.live.Session.DurableEventsFrom(cursor); readErr == nil {
					r.TurnCommitted(events)
				}
			}
			if fatal {
				return
			}
			if err == nil {
				queued, queueErr := r.queueGoalContinuation(ctx)
				if queueErr != nil {
					r.mu.Lock()
					r.lastErr, r.fatalErr = queueErr, queueErr
					r.mu.Unlock()
					return
				}
				if queued {
					continue
				}
			}
			if !ran {
				break
			}
		}
		r.mu.Lock()
		stopping := r.stopping
		r.mu.Unlock()
		if stopping {
			return
		}
	}
}

// queueGoalContinuation runs only after the preceding turn and its log are
// settled. Admission and human submission share r.mu; the NativeLoop claim
// guard then resolves a human message that arrived behind a queued round.
func (r *NativeRuntime) queueGoalContinuation(ctx context.Context) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping || r.live == nil || r.live.Goals == nil || ctx.Err() != nil {
		return false, nil
	}
	inbox := r.live.Session.Inbox()
	if len(inbox.NextTurn) != 0 || len(inbox.NextStep) != 0 {
		return false, nil
	}
	view, found, err := r.live.Goals.Get()
	if err != nil || !found || view.Goal.Phase != "active" || view.Activation != "armed" {
		return false, err
	}
	if err := r.live.Session.Flush(ctx); err != nil {
		r.live.Goals.Disarm()
		return false, err
	}
	ref := sessionformat.GoalRef{ID: view.Goal.ID, Revision: view.Goal.Revision}
	if view.RoundsStarted >= view.Goal.MaxGoalRounds {
		_, err := r.live.Goals.Block(ctx, ref, sessionformat.GoalBlockReason{
			Code: "round-limit", Message: fmt.Sprintf("Goal reached its configured limit of %d rounds.", view.Goal.MaxGoalRounds),
		})
		return false, err
	}
	round := view.RoundsStarted + 1
	quotedObjective, err := json.Marshal(view.Goal.Objective)
	if err != nil {
		return false, err
	}
	prompt := fmt.Sprintf("<goal_round>\nObjective: %s\nRound: %d/%d\n\nContinue working toward the objective in this same session. Treat the current workspace, tool results, and durable session state as authoritative; inspect them instead of assuming earlier narration is still current. Make concrete progress and verify the result. Before claiming completion, gather evidence that the whole objective is achieved, read the current goal, and mark it complete. If work remains, leave the goal active for the next round. Follow the configured goal-tool policy before reporting a blocker.\n</goal_round>", quotedObjective, round, view.Goal.MaxGoalRounds)
	message, err := json.Marshal(map[string]any{
		"id": uuid.NewString(), "role": "user",
		"source":  map[string]any{"kind": "goal", "goalId": view.Goal.ID, "revision": view.Goal.Revision, "round": round},
		"content": []map[string]any{{"type": "text", "text": prompt}},
	})
	if err != nil {
		return false, err
	}
	if _, err := r.live.Agent.AcceptMessage(ctx, message); err != nil {
		if errors.Is(err, sessionformat.ErrV4UncertainWrite) {
			r.live.Goals.Disarm()
			return false, err
		}
		_, blockErr := r.live.Goals.Block(ctx, ref, sessionformat.GoalBlockReason{
			Code: "queue-failed", Message: fmt.Sprintf("Could not queue goal round %d: %v", round, err),
		})
		return false, blockErr
	}
	return true, nil
}

// CancelTurn targets an already opened native turn number. Messages still in
// the inbox have no turn ID; explicit cancellation durably discards them as
// canceled splices before another turn can start.
func (r *NativeRuntime) CancelTurn(ctx context.Context, turnID string) (string, error) {
	turn, err := strconv.ParseUint(turnID, 10, 64)
	if err != nil || turn == 0 {
		return "", ErrNativeTurnNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil {
		return "", errors.New("native worker is not ready")
	}
	started, ended := r.live.Session.TurnStatus(turn, sessionformat.DurableCut)
	if !started {
		return "", ErrNativeTurnNotFound
	}
	if ended {
		return "", ErrNativeTurnFinished
	}
	if r.activeCancel == nil || r.stopping {
		return "", ErrNativeTurnNotRunning
	}
	r.activeCancel(agent.ErrNativeUserCancellation)
	flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := r.live.Agent.CancelPending(flushCtx); err != nil {
		if errors.Is(err, sessionformat.ErrV4UncertainWrite) {
			r.fatalErr = err
			r.stopping = true
			if r.cancel != nil {
				r.cancel()
			}
		}
		return "", err
	}
	return "running", nil
}

// A nil cursor includes event seq 0. Native v4 event seqs are zero-based,
// unlike the previous control API's one-based event cursor.
func (r *NativeRuntime) DurableEventsAfter(after *uint64, limit int) ([]sessionformat.Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil {
		return nil, errors.New("native worker is not ready")
	}
	if limit < 1 || limit > 500 {
		return nil, errors.New("invalid native event page limit")
	}
	return r.live.Session.DurableEventPage(after, limit), nil
}

func (r *NativeRuntime) SubscribeAssistantStream() (<-chan agent.NativeAssistantStreamFrame, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil {
		return nil, nil, errors.New("native worker is not ready")
	}
	frames, cancel := r.live.Agent.SubscribeAssistantStream()
	return frames, cancel, nil
}

func (r *NativeRuntime) SubscribeAssistantStreamSnapshot() (agent.NativeAssistantStreamBaseline, <-chan agent.NativeAssistantStreamFrame, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil {
		return agent.NativeAssistantStreamBaseline{}, nil, nil, errors.New("native worker is not ready")
	}
	baseline, frames, cancel := r.live.Agent.SubscribeAssistantStreamSnapshot()
	return baseline, frames, cancel, nil
}

func (r *NativeRuntime) Done() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.done
}

func (r *NativeRuntime) LastError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastErr
}

func (r *NativeRuntime) TerminationError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fatalErr
}

// Stop rejects new messages, drains every already accepted inbox item, then
// closes the Session. A caller deadline converts the stop to an abort; the
// durable inbox and interrupted-turn repair determine recovery afterward.
func (r *NativeRuntime) Stop(ctx context.Context) error {
	// Preparation/setup may call back into other Go services. Never hold the
	// stop or state mutex while waiting for that creation transaction to drain.
	for {
		r.mu.Lock()
		starting := r.startDone
		if starting != nil {
			select {
			case <-starting:
				starting = nil
			default:
			}
		}
		cancel := r.startCancel
		if starting != nil && cancel != nil {
			r.stopping = true
		}
		r.mu.Unlock()
		if starting == nil {
			break
		}
		if cancel != nil {
			cancel()
		}
		select {
		case <-starting:
		case <-ctx.Done():
			r.Abort()
			return ctx.Err()
		}
	}
	r.stopMu.Lock()
	r.mu.Lock()
	if r.startDone != nil {
		select {
		case <-r.startDone:
		default:
			r.mu.Unlock()
			r.stopMu.Unlock()
			return r.Stop(ctx) // Start was admitted after the barrier check.
		}
	}
	defer r.stopMu.Unlock()
	if r.live == nil {
		attempted := r.startDone != nil
		r.mu.Unlock()
		if attempted {
			return nil // Failed preparation already released its resources.
		}
		return errors.New("native worker is not started")
	}
	if r.done == nil {
		live := r.live
		r.stopping = true
		r.mu.Unlock()
		r.closeOnce.Do(func() { r.closeError = live.Close() })
		return r.closeError
	}
	if !r.stopping {
		r.stopping = true
		if r.live.Questions != nil && r.live.Questions.Disable() && r.activeCancel != nil {
			r.activeCancel(context.Canceled)
		}
		close(r.drain)
		r.kick()
	}
	done, live, children := r.done, r.live, r.children
	r.mu.Unlock()
	if r.Factory.Schedule != nil && !r.HostOwnsSchedule {
		r.Factory.Schedule.Close()
	}
	select {
	case <-done:
	case <-ctx.Done():
		r.Abort()
		return ctx.Err()
	}
	if children != nil {
		if err := children.Close(ctx); err != nil {
			r.Abort()
			return err
		}
	}
	r.closeOnce.Do(func() { r.closeError = live.Close() })
	return r.closeError
}

// Dispose is factory-owner teardown, rather than the UI's draining Stop.
// Upstream clears a ready machine's inbox on disposal, but failed/unpublished
// creation retains accepted input for recovery. Stop still drains every writer
// and resource; cancellation alone never transfers ownership of them.
func (r *NativeRuntime) Dispose(ctx context.Context) error {
	r.Abort()
	r.mu.Lock()
	live, ready, compactDone := r.live, r.done != nil, r.compactDone
	r.mu.Unlock()
	var err error
	if live != nil && ready {
		err = live.Agent.CancelPending(ctx)
	}
	// A manual compaction runs outside the turn driver. Its cancellation must
	// settle before scoped effects or the Session writer can be closed.
	if compactDone != nil {
		select {
		case <-compactDone:
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		}
	}
	return errors.Join(err, r.Stop(ctx))
}

// Abort is for owner loss or process shutdown. It cancels the active turn;
// unlike Stop, it does not consume queued user messages before exiting.
func (r *NativeRuntime) Abort() {
	r.mu.Lock()
	r.stopping = true
	cancel := r.cancel
	startCancel := r.startCancel
	compactCancel := r.compactCancel
	children := r.children
	r.mu.Unlock()
	if startCancel != nil {
		startCancel()
	}
	if cancel != nil {
		cancel()
	}
	if compactCancel != nil {
		compactCancel()
	}
	if children != nil {
		children.CancelAll()
	}
	if r.Factory.Schedule != nil && !r.HostOwnsSchedule {
		r.Factory.Schedule.Close()
	}
}

func (r *NativeRuntime) childCoordinator() (*nativeChildManager, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.children == nil || r.stopping || r.fatalErr != nil {
		return nil, errors.New("native child coordinator is unavailable")
	}
	return r.children, nil
}

func (r *NativeRuntime) SpawnChild(ctx context.Context, parentID, label, prompt string, fork bool) (string, error) {
	return r.SpawnChildAt(ctx, parentID, label, prompt, "", fork)
}

func (r *NativeRuntime) SpawnChildAt(ctx context.Context, parentID, label, prompt, cwd string, fork bool) (string, error) {
	children, err := r.childCoordinator()
	if err != nil {
		return "", err
	}
	return children.SpawnChildAt(ctx, parentID, label, prompt, cwd, fork)
}

func (r *NativeRuntime) SpawnChildWithPreset(ctx context.Context, parentID, label, prompt, cwd, preset string, fork bool) (string, error) {
	children, err := r.childCoordinator()
	if err != nil {
		return "", err
	}
	return children.SpawnChildWithPreset(ctx, parentID, label, prompt, cwd, preset, fork)
}

func (r *NativeRuntime) RunWorkflowChild(ctx context.Context, parentID, prompt string, options workflow.AgentOptions) (workflow.AgentResult, error) {
	return r.RunWorkflowChildObserved(ctx, parentID, prompt, options, nil)
}

func (r *NativeRuntime) RunWorkflowChildObserved(ctx context.Context, parentID, prompt string, options workflow.AgentOptions, started func(string)) (workflow.AgentResult, error) {
	children, err := r.childCoordinator()
	if err != nil {
		return workflow.AgentResult{}, err
	}
	return children.RunWorkflowChildObserved(ctx, parentID, prompt, options, started)
}

func (r *NativeRuntime) SendChildMessage(ctx context.Context, senderID, targetID, content string) (string, error) {
	children, err := r.childCoordinator()
	if err != nil {
		return "", err
	}
	return children.SendChildMessage(ctx, senderID, targetID, content)
}

func (r *NativeRuntime) InterruptChild(ctx context.Context, parentID, childID string) (string, error) {
	children, err := r.childCoordinator()
	if err != nil {
		return "", err
	}
	return children.InterruptChild(ctx, parentID, childID)
}

func (r *NativeRuntime) ListChildren(ctx context.Context, parentID string) ([]tools.ChildStatus, error) {
	children, err := r.childCoordinator()
	if err != nil {
		return nil, err
	}
	return children.ListChildren(ctx, parentID)
}

func (r *NativeRuntime) Close() error {
	return r.Stop(context.Background())
}

func (r *NativeRuntime) HistoryWindow(ctx context.Context, request sessionformat.HistoryRequest) (sessionformat.HistoryWindow, error) {
	r.mu.Lock()
	if r.live == nil {
		r.mu.Unlock()
		return sessionformat.HistoryWindow{}, errors.New("native worker is not ready")
	}
	s := r.live.Session
	r.mu.Unlock()
	return s.HistoryWindow(ctx, request)
}
