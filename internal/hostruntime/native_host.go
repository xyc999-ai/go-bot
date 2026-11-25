package hostruntime

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"sync"
	"time"

	"go-bot/harness/assembly"
	"go-bot/harness/schedule"
	"go-bot/harness/session"
)

var (
	ErrHostClosed          = errors.New("native Host is closed")
	ErrHostSessionActive   = errors.New("native Session is already active")
	ErrHostSessionArchived = errors.New("native Session is archived")
	ErrHostSessionBusy     = errors.New("native Session is busy")
	ErrHostSessionAbsent   = errors.New("native Session is not active")
	hostSessionID          = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// NativeHost owns the top-level Session runtimes of one user. It does not own
// their journals or workspaces: Build binds each Session to its own durable
// storage and initial cwd. Child Agents remain inside their parent's runtime.
// Admission is serialized, while active Session turns run independently.
type NativeHost struct {
	PrepareMain          func(context.Context, string, string) error
	MainReady            func(context.Context, string) error
	mainSessionID        string
	UserID               string
	Mode                 string
	Build                func(sessionID string, resume bool) (*NativeRuntime, error)
	BuildInWorkspace     func(sessionID string, create schedule.TargetCreate) (*NativeRuntime, error)
	List                 func(context.Context, string, int) (sessionformat.SessionListPage, error)
	LatestSession        func(context.Context) (sessionformat.SessionListPage, error)
	ReadSession          func(context.Context, string) (sessionformat.PhysicalArtifact, error)
	ReadHistory          func(context.Context, string, sessionformat.HistoryRequest) (sessionformat.HistoryWindow, error)
	ReadSessionHeader    func(context.Context, string) (sessionformat.Header, error)
	SessionLogInfo       func(string) (os.FileInfo, error)
	WorkspaceForSession  func(string) string
	Archived             func(string) (bool, error)
	SaveArchive          func(context.Context, string) error
	ReadTitle            func(string) string
	SaveTitle            func(string, string) error
	Schedule             *schedule.Service
	StartedAt            time.Time
	ReserveScheduleModel func(string) error
	PluginsChanges       func() (<-chan struct{}, <-chan struct{})
	MCPChanges           func() (<-chan struct{}, <-chan struct{})

	approvalMu      sync.Mutex
	approvalStreams map[string]int
	hostStreams     int
	operationMu     sync.Mutex
	historyLocks    map[string]*hostHistoryGate
	operations      map[string]*hostSessionOperation
	scheduleClose   sync.Once
	mu              sync.Mutex
	sessions        map[string]*NativeRuntime
	closed          bool
	lastActivity    time.Time
}

func (h *NativeHost) Activate(ctx context.Context, sessionID string, resume bool) (*NativeRuntime, error) {
	return h.activate(ctx, sessionID, resume, nil, nil, nil)
}

func (h *NativeHost) ActivateInWorkspace(ctx context.Context, sessionID string, create schedule.TargetCreate) (*NativeRuntime, error) {
	return h.activate(ctx, sessionID, false, &create, nil, nil)
}

func (h *NativeHost) ActivateWithSetup(ctx context.Context, sessionID string, resume bool, setup NativeSetup) (*NativeRuntime, error) {
	return h.activate(ctx, sessionID, resume, nil, setup, nil)
}

func (h *NativeHost) ActivateInWorkspaceWithSetup(ctx context.Context, sessionID string, create schedule.TargetCreate, setup NativeSetup) (*NativeRuntime, error) {
	return h.activate(ctx, sessionID, false, &create, setup, nil)
}

// ActivateWithSessionOptions uses the same Host admission, Go driver and
// rollback path, while allowing programmatic metadata/seed independent of the
// manual chat workspace convention. Setup still receives the actual Go Agent.
func (h *NativeHost) ActivateWithSessionOptions(ctx context.Context, id string, options assembly.NativeSessionOptions, setup NativeSetup) (*NativeRuntime, error) {
	detached, err := options.Snapshot()
	if err != nil {
		return nil, err
	}
	create := schedule.TargetCreate{}
	if meta := detached.Meta; meta != nil {
		if meta.CWD != nil {
			create.CWD = *meta.CWD
		}
		if meta.AgentPreset != nil {
			create.AgentPreset = *meta.AgentPreset
		}
	}
	return h.activate(ctx, id, false, &create, setup, &detached)
}

func (h *NativeHost) activate(ctx context.Context, sessionID string, resume bool, create *schedule.TargetCreate, setup NativeSetup, options *assembly.NativeSessionOptions) (_ *NativeRuntime, resultErr error) {
	if h.UserID == "" || h.Build == nil || !hostSessionID.MatchString(sessionID) {
		return nil, errors.New("invalid native Host Session activation")
	}
	h.operationMu.Lock()
	if err := h.CheckScheduleTarget(ctx, sessionID); err != nil {
		h.operationMu.Unlock()
		return nil, err
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		h.operationMu.Unlock()
		return nil, ErrHostClosed
	}
	if h.sessions[sessionID] != nil || h.operations[sessionID] != nil {
		h.mu.Unlock()
		h.operationMu.Unlock()
		return nil, ErrHostSessionActive
	}
	ctx, cancel := context.WithCancel(ctx)
	operation := &hostSessionOperation{done: make(chan struct{}), cancel: cancel}
	if h.operations == nil {
		h.operations = make(map[string]*hostSessionOperation)
	}
	h.operations[sessionID] = operation
	h.mu.Unlock()
	h.operationMu.Unlock()
	defer func() {
		cancel()
		h.finishOperation(sessionID, operation, resultErr)
	}()
	releaseHistory := h.historyGate(sessionID, true)
	defer releaseHistory()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var runtime *NativeRuntime
	var err error
	if create != nil && h.BuildInWorkspace != nil {
		runtime, err = h.BuildInWorkspace(sessionID, *create)
	} else {
		runtime, err = h.Build(sessionID, resume)
	}
	if err != nil {
		return nil, err
	}
	if runtime == nil || runtime.Identity.UserID != h.UserID || runtime.Identity.SessionID != sessionID {
		return nil, errors.New("native Host builder returned a foreign Session")
	}
	runtime.approvalAudience = func() bool { return h.hasChatStream(sessionID) }
	runtime.sessionCreation = options
	h.mu.Lock()
	operation.runtime = runtime
	h.mu.Unlock()
	err = runtime.start(ctx, resume, func(s *sessionformat.LiveSession) {
		h.mu.Lock()
		operation.session = s
		h.mu.Unlock()
		releaseHistory()
	}, func(live *assembly.NativeLive) {
		h.mu.Lock()
		operation.live = live
		h.mu.Unlock()
		releaseHistory()
	}, setup)
	if err != nil {
		runtime.mu.Lock()
		retained := runtime.live != nil
		runtime.mu.Unlock()
		if retained {
			h.mu.Lock()
			if h.sessions == nil {
				h.sessions = make(map[string]*NativeRuntime)
			}
			h.sessions[sessionID] = runtime
			h.mu.Unlock()
			return runtime, fmt.Errorf("activate native Session %s: %w", sessionID, err)
		}
		return nil, fmt.Errorf("activate native Session %s: %w", sessionID, err)
	}
	h.mu.Lock()
	publicationErr := ctx.Err()
	if h.closed {
		publicationErr = ErrHostClosed
	}
	if publicationErr != nil {
		h.mu.Unlock()
		runtime.Abort()
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := runtime.Stop(cleanup); err != nil {
			h.mu.Lock()
			if h.sessions == nil {
				h.sessions = make(map[string]*NativeRuntime)
			}
			h.sessions[sessionID] = runtime
			h.mu.Unlock()
			return runtime, errors.Join(publicationErr, err)
		}
		return nil, publicationErr
	}
	if h.sessions == nil {
		h.sessions = make(map[string]*NativeRuntime)
	}
	h.sessions[sessionID] = runtime
	h.lastActivity = time.Now().UTC()
	h.mu.Unlock()
	return runtime, nil
}

// WakeSessions rechecks queued work after managed model settings change.
func (h *NativeHost) WakeSessions() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, runtime := range h.sessions {
		runtime.kick()
	}
}

// AttachChatStream tracks interactive delivery even while the Session is cold.
func (h *NativeHost) AttachChatStream(sessionID string) func() {
	h.approvalMu.Lock()
	if h.approvalStreams == nil {
		h.approvalStreams = make(map[string]int)
	}
	h.approvalStreams[sessionID]++
	h.approvalMu.Unlock()
	return func() {
		h.approvalMu.Lock()
		defer h.approvalMu.Unlock()
		if h.approvalStreams[sessionID] <= 1 {
			delete(h.approvalStreams, sessionID)
			return
		}
		h.approvalStreams[sessionID]--
	}
}

func (h *NativeHost) hasChatStream(sessionID string) bool {
	h.approvalMu.Lock()
	defer h.approvalMu.Unlock()
	return h.hostStreams > 0 || h.approvalStreams[sessionID] > 0
}

// AttachHostStream marks an interactive client for all current and future
// Sessions. Detaching never cancels a question that has already been asked.
func (h *NativeHost) AttachHostStream() func() {
	h.approvalMu.Lock()
	h.hostStreams++
	h.approvalMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			h.approvalMu.Lock()
			h.hostStreams--
			h.approvalMu.Unlock()
		})
	}
}

// StreamSessions returns handles without activating cold Sessions. Callers
// must not hold the Host lock while reading or subscribing to a runtime.
func (h *NativeHost) StreamSessions() (map[string]*NativeRuntime, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	result := make(map[string]*NativeRuntime, len(h.sessions))
	for id, runtime := range h.sessions {
		result[id] = runtime
	}
	return result, h.closed
}

func (h *NativeHost) Session(sessionID string) (*NativeRuntime, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, false
	}
	runtime, ok := h.sessions[sessionID]
	return runtime, ok
}

// WithSessionHistory protects cold initialization for one Session. Readers
// use DurableSessionSuffix/SessionArtifact once a real Go Session is prepared.
func (h *NativeHost) WithSessionHistory(ctx context.Context, id string, read func() error) error {
	release := h.historyGate(id, false)
	defer release()
	if err := ctx.Err(); err != nil {
		return err
	}
	return read()
}

// PrepareSchedule resolves the original Session before Schedule samples the
// final dispatch time. A cold Session is resumed from its own v4 journal;
// a missing or corrupt journal never creates a replacement Session.
func (h *NativeHost) PrepareSchedule(ctx context.Context, sessionID string) error {
	_, err := h.scheduleRuntime(ctx, sessionID)
	return err
}

func (h *NativeHost) scheduleRuntime(ctx context.Context, sessionID string) (*NativeRuntime, error) {
	runtime, found := h.Session(sessionID)
	if !found {
		var err error
		runtime, err = h.Activate(ctx, sessionID, true)
		if errors.Is(err, ErrHostSessionActive) {
			runtime, err = h.AwaitSession(ctx, sessionID)
		}
		if err != nil {
			return nil, err
		}
	}
	return runtime, nil
}

// EnsureScheduleSession creates a reserved destination only when its journal
// is absent. Failed activation of an existing journal remains an error.
func (h *NativeHost) EnsureScheduleSession(ctx context.Context, sessionID string) error {
	return h.EnsureScheduleDestination(ctx, sessionID, schedule.TargetCreate{}, "")
}

func (h *NativeHost) EnsureScheduleDestination(ctx context.Context, sessionID string, create schedule.TargetCreate, title string) error {
	if err := h.PrepareSchedule(ctx, sessionID); errors.Is(err, os.ErrNotExist) {
		if h.ReserveScheduleModel != nil {
			if err := h.ReserveScheduleModel(sessionID); err != nil {
				return err
			}
		}
		_, err := h.ActivateInWorkspace(ctx, sessionID, create)
		if errors.Is(err, ErrHostSessionActive) {
			_, err = h.AwaitSession(ctx, sessionID)
		}
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if h.SaveTitle != nil && title != "" {
		if err := h.SaveTitle(sessionID, title); err != nil {
			log.Printf("Schedule: name Session %s: %v", sessionID, err)
		}
	}
	return nil
}

// DeliverSchedule appends the prepared reminder to the original Session.
func (h *NativeHost) DeliverSchedule(ctx context.Context, sessionID, text string) (string, error) {
	runtime, err := h.scheduleRuntime(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if runtime.Factory.ResolveTurnModel != nil {
		if _, err := runtime.Factory.ResolveTurnModel(ctx); err != nil {
			return "", err
		}
	}
	return runtime.DeliverSchedule(ctx, text)
}

// StopSession drains only this top-level Session and its own children/jobs.
// Other Session runtimes remain live and continue accepting messages.
func (h *NativeHost) StopSession(ctx context.Context, sessionID string) error {
	return h.stopSession(ctx, sessionID, false)
}

func (h *NativeHost) stopSession(ctx context.Context, sessionID string, closing bool) (resultErr error) {
	return h.stopOwnedSession(ctx, sessionID, closing, nil)
}

// StopRuntime releases an exact factory-owned live Agent. Reusing a Session ID
// never transfers an old creator's ownership to its replacement. UI stop-by-ID
// remains the separate StopSession operation.
func (h *NativeHost) StopRuntime(ctx context.Context, runtime *NativeRuntime) error {
	if runtime == nil || runtime.Identity.UserID != h.UserID || !hostSessionID.MatchString(runtime.Identity.SessionID) {
		return errors.New("invalid owned native Agent")
	}
	return h.stopOwnedSession(ctx, runtime.Identity.SessionID, false, runtime)
}

func (h *NativeHost) stopOwnedSession(ctx context.Context, sessionID string, closing bool, expected *NativeRuntime) (resultErr error) {
	h.operationMu.Lock()
	h.mu.Lock()
	if expected != nil && h.sessions[sessionID] != expected && (h.operations[sessionID] == nil || h.operations[sessionID].runtime != expected) {
		h.mu.Unlock()
		h.operationMu.Unlock()
		return nil // This lifetime has already been withdrawn, even after Host close.
	}
	if h.closed && !closing && expected == nil {
		h.mu.Unlock()
		h.operationMu.Unlock()
		return ErrHostClosed
	}
	if operation := h.operations[sessionID]; operation != nil {
		h.mu.Unlock()
		h.operationMu.Unlock()
		if !operation.stopping {
			return ErrHostSessionBusy
		}
		select {
		case <-operation.done:
			return operation.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	runtime := h.sessions[sessionID]
	if runtime == nil {
		h.mu.Unlock()
		h.operationMu.Unlock()
		return ErrHostSessionAbsent
	}
	operation := &hostSessionOperation{done: make(chan struct{}), runtime: runtime, stopping: true}
	if h.operations == nil {
		h.operations = make(map[string]*hostSessionOperation)
	}
	h.operations[sessionID] = operation
	h.mu.Unlock()
	h.operationMu.Unlock()
	defer func() { h.finishOperation(sessionID, operation, resultErr) }()
	var stopErr error
	if expected != nil {
		stopErr = runtime.Dispose(ctx)
	} else {
		stopErr = runtime.Stop(ctx)
	}
	if stopErr != nil {
		// A timed-out stop may still be draining its turn or child Jobs. Keep
		// the runtime visible so Host activity cannot report false idleness.
		return stopErr
	}
	h.mu.Lock()
	delete(h.sessions, sessionID)
	h.lastActivity = time.Now().UTC()
	h.mu.Unlock()
	return nil
}

func (h *NativeHost) Activity() (sessionformat.ActivitySnapshot, error) {
	// A one-shot Schedule can cold-activate its Session and become inactive
	// while Activity is collecting runtimes. Check the timer obligation before
	// taking the Session snapshot as well as after it: otherwise the snapshot
	// can omit the newly activated runtime and report a false idle Host.
	scheduledBefore := h.Schedule != nil && h.Schedule.HasActive()
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return sessionformat.ActivitySnapshot{}, ErrHostClosed
	}
	runtimes := make([]*NativeRuntime, 0, len(h.sessions))
	activating := len(h.operations) != 0
	since := h.lastActivity
	if h.StartedAt.After(since) {
		since = h.StartedAt
	}
	for _, runtime := range h.sessions {
		runtimes = append(runtimes, runtime)
	}
	h.mu.Unlock()
	result := sessionformat.ActivitySnapshot{Idle: !activating, Since: since}
	for _, runtime := range runtimes {
		activity, err := runtime.Activity()
		if err != nil {
			return sessionformat.ActivitySnapshot{}, err
		}
		if !activity.Idle {
			result.Idle = false
		}
		if activity.Since.After(result.Since) {
			result.Since = activity.Since
		}
	}
	scheduledAfter := h.Schedule != nil && h.Schedule.HasActive()
	if scheduledBefore || scheduledAfter {
		result.Idle = false
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return sessionformat.ActivitySnapshot{}, ErrHostClosed
	}
	if h.lastActivity.After(result.Since) {
		result.Since = h.lastActivity
		result.Idle = false
	}
	if len(h.operations) != 0 {
		result.Idle = false
	}
	h.mu.Unlock()
	return result, nil
}

// Close drains every Session. A caller deadline aborts unfinished runtimes,
// but keeps their handles so a later Close can finish their resource cleanup.
// An unfinished inbox is recovered from its own journal on the next resume.
func (h *NativeHost) Close(ctx context.Context) error {
	h.operationMu.Lock()
	h.mu.Lock()
	h.closed = true
	var cancel []context.CancelFunc
	for _, operation := range h.operations {
		if operation.cancel != nil {
			cancel = append(cancel, operation.cancel)
		}
	}
	h.mu.Unlock()
	h.operationMu.Unlock()
	for _, stop := range cancel {
		stop()
	}
	// A due Schedule delivery may be waiting to activate a Session. Release
	// operationMu before waiting for that dispatcher to stop; closed admission
	// makes any late activation fail without constructing another runtime.
	h.scheduleClose.Do(func() {
		if h.Schedule != nil {
			h.Schedule.Close()
		}
	})
	// Setup/disposal may reenter Host services. Wait for admitted operations
	// without a Host mutex; a caller deadline leaves their ownership intact.
	for {
		h.mu.Lock()
		var pending <-chan struct{}
		for _, operation := range h.operations {
			pending = operation.done
			break
		}
		h.mu.Unlock()
		if pending == nil {
			break
		}
		select {
		case <-pending:
		case <-ctx.Done():
			h.mu.Lock()
			unfinished := make(map[*NativeRuntime]bool)
			for _, runtime := range h.sessions {
				unfinished[runtime] = true
			}
			for _, operation := range h.operations {
				if operation.runtime != nil {
					unfinished[operation.runtime] = true
				}
			}
			h.mu.Unlock()
			for runtime := range unfinished {
				runtime.Abort()
			}
			return ctx.Err()
		}
	}
	h.mu.Lock()
	runtimes := make(map[string]*NativeRuntime, len(h.sessions))
	for id, runtime := range h.sessions {
		runtimes[id] = runtime
	}
	h.mu.Unlock()
	var failures []error
	for id := range runtimes {
		if err := h.stopSession(ctx, id, true); err != nil && !errors.Is(err, ErrHostSessionAbsent) {
			failures = append(failures, fmt.Errorf("stop Session %s: %w", id, err))
		}
	}
	return errors.Join(failures...)
}
