package hostruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"go-bot/harness/agent"
	"go-bot/harness/assembly"
	"go-bot/harness/jobs"
	"go-bot/harness/permission"
	"go-bot/harness/session"
	"go-bot/harness/subagent"
	"go-bot/harness/tools"
	"go-bot/harness/workflow"
)

const maxNativeChildren = 8

type nativeChildProcess struct {
	live      *assembly.NativeLive
	parentID  string
	wake      chan struct{}
	cancel    context.CancelFunc
	done      chan struct{}
	jobDone   chan struct{}
	jobError  error
	running   bool
	closing   bool
	admitted  int
	admitDone chan struct{}
}

// Deliveries to one child share a gate so a cold Session is materialized only
// once. Other children and Host shutdown never wait on the global manager lock
// while a target's journal or inbox is doing file I/O.
type nativeChildDelivery struct {
	turn chan struct{}
	refs int
}

type nativeChildManager struct {
	mu           sync.Mutex
	factory      assembly.NativeFactory
	identity     assembly.NativeRuntimeIdentity
	root         *assembly.NativeLive
	rootWake     func()
	children     map[string]*nativeChildProcess
	deliveries   map[string]*nativeChildDelivery
	creating     int
	pending      sync.WaitGroup
	closed       bool
	pendingDone  chan struct{}
	childrenDone chan struct{}
}

func newNativeChildManager(factory assembly.NativeFactory, identity assembly.NativeRuntimeIdentity, root *assembly.NativeLive, rootWake func()) *nativeChildManager {
	return &nativeChildManager{factory: factory, identity: identity, root: root, rootWake: rootWake, children: make(map[string]*nativeChildProcess), deliveries: make(map[string]*nativeChildDelivery)}
}

// The root worker's idle lease covers every child it hosts, not only the
// root's own v4 inbox. Child Jobs are process-local but keep that lease busy.
func (m *nativeChildManager) Activity() (active bool, since time.Time) {
	m.mu.Lock()
	active = m.creating != 0
	processes := make([]*nativeChildProcess, 0, len(m.children))
	for _, process := range m.children {
		processes = append(processes, process)
		if process.running || process.closing {
			active = true
		}
	}
	m.mu.Unlock()
	for _, process := range processes {
		snapshot := process.live.Session.DurableActivity()
		if !snapshot.Idle {
			active = true
		}
		if snapshot.Since.After(since) {
			since = snapshot.Since
		}
		if process.live.Resource.Jobs != nil {
			jobActive, jobSince := process.live.Resource.Jobs.Activity()
			if jobActive {
				active = true
			}
			if jobSince.After(since) {
				since = jobSince
			}
		}
	}
	return active, since
}

func (m *nativeChildManager) liveLocked(id string) (*assembly.NativeLive, *nativeChildProcess) {
	if m.root.Session.Header().ID == id {
		return m.root, nil
	}
	process := m.children[id]
	if process == nil {
		return nil, nil
	}
	return process.live, process
}

func (m *nativeChildManager) SpawnChild(ctx context.Context, parentID, label, prompt string, fork bool) (string, error) {
	return m.SpawnChildAt(ctx, parentID, label, prompt, "", fork)
}

func (m *nativeChildManager) SpawnChildAt(ctx context.Context, parentID, label, prompt, cwd string, fork bool) (string, error) {
	return m.spawnChildAt(ctx, parentID, label, prompt, cwd, "", fork)
}

func (m *nativeChildManager) SpawnChildWithPreset(ctx context.Context, parentID, label, prompt, cwd, preset string, fork bool) (string, error) {
	if fork && preset != "" {
		return "", errors.New("fork cannot choose an independent Agent Preset")
	}
	return m.spawnChildAt(ctx, parentID, label, prompt, cwd, preset, fork)
}

func (m *nativeChildManager) spawnChildAt(ctx context.Context, parentID, label, prompt, cwd, preset string, fork bool) (string, error) {
	childFactory := m.factory
	m.mu.Lock()
	if m.closed || len(m.children)+m.creating >= maxNativeChildren {
		m.mu.Unlock()
		return "", errors.New("native child capacity is unavailable")
	}
	parent, _ := m.liveLocked(parentID)
	if parent == nil {
		m.mu.Unlock()
		return "", errors.New("parent Agent is not live in this worker")
	}
	if parent.Session.Header().DelegationDepth >= 1 {
		m.mu.Unlock()
		return "", errors.New("native child depth exceeds the default limit of one")
	}
	m.pending.Add(1)
	m.creating++
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.creating--
		m.mu.Unlock()
		m.pending.Done()
	}()

	id := uuid.NewString()
	var child *assembly.NativeLive
	var err error
	if fork {
		child, err = childFactory.ForkChildAt(ctx, m.identity, parent, id, label, cwd)
	} else {
		child, err = childFactory.ActivateChildWithPresetAt(ctx, m.identity, parent, id, label, cwd, preset)
	}
	if err != nil {
		return "", err
	}
	guidance := fmt.Sprintf("Your parent agent id is %q. Before you finish, send your result to that agent with send_message({agent_id: %q, message: \"<self-contained result>\"}). The parent shares your workspace but does not automatically receive your transcript, tool output, or reasoning. Sending a message does not end your turn.", parentID, parentID)
	message, err := nativeAgentMessage(map[string]any{"kind": "user"}, prompt+"\n\n"+guidance)
	if err != nil {
		_ = child.Close()
		return "", err
	}
	if _, err := child.Agent.AcceptMessage(ctx, message); err != nil {
		_ = child.Close()
		return "", err
	}
	if _, err := subagent.RecordNativeChild(ctx, parent.Session, child.Session.Header(), label); err != nil {
		_ = child.Close()
		return "", err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return "", errors.Join(errors.New("native child manager is stopping"), child.Close())
	}
	process := m.publishLocked(ctx, child, parentID)
	m.mu.Unlock()
	m.wakeChild(process)
	return id, nil
}

// RunWorkflowChild delivers one caller-owned child result to the workflow
// script. It records a normal continuable child Session for later discovery,
// but sends no separate completion notice to the parent model: the workflow
// tool itself returns the aggregated result. The active child is visible to
// Host activity/reaping and is cancelled when its run or parent stops.
func (m *nativeChildManager) RunWorkflowChild(ctx context.Context, parentID, prompt string, options workflow.AgentOptions) (result workflow.AgentResult, err error) {
	return m.RunWorkflowChildObserved(ctx, parentID, prompt, options, nil)
}

func (m *nativeChildManager) RunWorkflowChildObserved(ctx context.Context, parentID, prompt string, options workflow.AgentOptions, started func(string)) (result workflow.AgentResult, err error) {
	if options.Provider != "" || options.Model != "" {
		return workflow.AgentResult{}, errors.New("workflow children inherit model.main; provider/model overrides are not accepted")
	}

	label := options.Label
	if label == "" {
		label, _, _ = strings.Cut(prompt, "\n")
		characters := []rune(label)
		if len(characters) > 48 {
			label = string(characters[:47]) + "…"
		}
	}
	if label == "" {
		label = "workflow child"
	}
	m.mu.Lock()
	if m.closed || len(m.children)+m.creating >= maxNativeChildren {
		m.mu.Unlock()
		return workflow.AgentResult{}, errors.New("native child capacity is unavailable")
	}
	parent, _ := m.liveLocked(parentID)
	if parent == nil || parent.Session.Header().DelegationDepth >= 1 {
		m.mu.Unlock()
		return workflow.AgentResult{}, errors.New("workflow parent Agent is not eligible")
	}
	m.pending.Add(1)
	m.creating++
	m.mu.Unlock()
	creating := true
	defer func() {
		if creating {
			m.mu.Lock()
			m.creating--
			m.mu.Unlock()
			m.pending.Done()
		}
	}()

	id := uuid.NewString()
	childFactory := m.factory
	childFactory.WorkflowSchema = options.Schema
	child, err := childFactory.ActivateChild(ctx, m.identity, parent, id, label)
	if err != nil {
		return workflow.AgentResult{}, err
	}
	message, err := nativeAgentMessage(map[string]any{"kind": "user"}, prompt)
	if err == nil {
		_, err = child.Agent.AcceptMessage(ctx, message)
	}
	if err == nil {
		_, err = subagent.RecordNativeChild(ctx, parent.Session, child.Session.Header(), label)
	}
	if err != nil {
		return workflow.AgentResult{}, errors.Join(err, child.Close())
	}
	runCtx, cancel := context.WithCancel(ctx)
	process := &nativeChildProcess{live: child, parentID: parentID, cancel: cancel, done: make(chan struct{}), running: true}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		return workflow.AgentResult{}, errors.Join(errors.New("native child manager is stopping"), child.Close())
	}
	m.children[id] = process
	m.creating--
	m.pending.Done()
	creating = false
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		process.closing = true
		admitDone := m.admissionDoneLocked(process)
		m.mu.Unlock()
		cancel()
		if admitDone != nil {
			<-admitDone
		}
		if closeErr := child.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		m.mu.Lock()
		if m.children[id] == process {
			delete(m.children, id)
		}
		m.mu.Unlock()
		close(process.done)
	}()
	if started != nil {
		started(id)
	}
	ran, answer, runErr := child.Agent.RunNext(runCtx)
	m.mu.Lock()
	process.running = false
	m.mu.Unlock()
	if ctx.Err() != nil {
		return workflow.AgentResult{}, ctx.Err()
	}
	if runCtx.Err() != nil {
		return workflow.AgentResult{}, runCtx.Err()
	}
	if !ran {
		return workflow.AgentResult{}, errors.New("workflow child did not start a turn")
	}
	if errors.Is(runErr, sessionformat.ErrV4UncertainWrite) {
		return workflow.AgentResult{}, runErr
	}
	if options.Schema != nil {
		if runErr != nil || child.Structured == nil {
			return workflow.AgentResult{Completed: false}, nil
		}
		value, captured := child.Structured.Captured()
		return workflow.AgentResult{Value: value, Completed: captured}, nil
	}
	return workflow.AgentResult{Value: answer, Completed: runErr == nil}, nil
}

func nativeAgentMessage(source map[string]any, text string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"id": uuid.NewString(), "role": "user", "source": source,
		"content": []map[string]any{{"type": "text", "text": text}},
	})
}

func (m *nativeChildManager) publishLocked(firstTurn context.Context, live *assembly.NativeLive, parentID string) *nativeChildProcess {
	ctx, cancel := context.WithCancel(context.Background())
	process := &nativeChildProcess{live: live, parentID: parentID, wake: make(chan struct{}, 1), cancel: cancel, done: make(chan struct{})}
	m.children[live.Session.Header().ID] = process
	if live.Resource.Jobs != nil {
		process.jobDone = make(chan struct{})
		go m.watchChildJobs(ctx, process, live.Resource.Jobs)
	}
	go m.driveChild(ctx, firstTurn, process)
	return process
}

func (m *nativeChildManager) watchChildJobs(ctx context.Context, process *nativeChildProcess, registry *jobs.Registry) {
	defer close(process.jobDone)
	for {
		select {
		case <-ctx.Done():
			return
		case <-registry.SettledWake():
		}
		if err := registry.DeliverSettled(func(job jobs.Snapshot) error {
			message, err := nativeJobNoticeMessage(job)
			if err == nil {
				m.mu.Lock()
				if !m.closed && ctx.Err() == nil && m.children[process.live.Session.Header().ID] == process {
					if process.running {
						_, err = process.live.Agent.InjectMessage(ctx, message)
					} else {
						_, err = process.live.Agent.AcceptMessage(ctx, message)
					}
				}
				m.mu.Unlock()
			}
			if err != nil {
				return fmt.Errorf("persist child Job %s completion: %w", job.ID, err)
			}
			// A turn can end between busy inspection and the durable append.
			m.wakeChild(process)
			return nil
		}); err != nil {
			m.mu.Lock()
			process.jobError = err
			m.mu.Unlock()
			process.cancel()
			return
		}
	}
}

func (m *nativeChildManager) wakeChild(process *nativeChildProcess) {
	select {
	case process.wake <- struct{}{}:
	default:
	}
}

func (m *nativeChildManager) driveChild(ctx, firstTurn context.Context, process *nativeChildProcess) {
	defer close(process.done)
	defer func() {
		m.mu.Lock()
		process.closing = true
		admitDone := m.admissionDoneLocked(process)
		m.mu.Unlock()
		process.cancel()
		if process.jobDone != nil {
			<-process.jobDone
		}
		if admitDone != nil {
			<-admitDone
		}
		m.mu.Lock()
		jobError := process.jobError
		m.mu.Unlock()
		if jobError != nil {
			_ = m.deliverSettlement(process, "", jobError)
		}
		_ = process.live.Close()
		// Keep this process in the manager until its Session and resources
		// have actually closed. Otherwise Close can miss a child between
		// deregistration and the final file/Job cleanup.
		m.mu.Lock()
		if m.children[process.live.Session.Header().ID] == process {
			delete(m.children, process.live.Session.Header().ID)
		}
		m.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-process.wake:
		}
		for ctx.Err() == nil {
			m.mu.Lock()
			process.running = true
			m.mu.Unlock()
			turnCtx := ctx
			if firstTurn != nil {
				if model, ok := agent.TurnModelFromContext(firstTurn); ok {
					turnCtx = agent.WithTurnModel(ctx, model)
				}
				firstTurn = nil
			}
			ran, answer, runErr := process.live.Agent.RunNext(turnCtx)
			m.mu.Lock()
			process.running = false
			m.mu.Unlock()
			if !ran {
				if runErr != nil {
					return
				}
				break
			}
			if ctx.Err() != nil {
				return
			}
			if runErr != nil {
				process.live.Goals.Disarm()
			}
			if err := m.deliverSettlement(process, answer, runErr); err != nil {
				return
			}
		}
	}
}

func (m *nativeChildManager) deliverSettlement(process *nativeChildProcess, answer string, runErr error) error {
	id := process.live.Session.Header().ID
	summary := fmt.Sprintf("Background subagent %s finished and will do no further work unless you send it more.", id)
	if runErr != nil {
		summary = fmt.Sprintf("Background subagent %s failed before it finished.", id)
	}
	content := summary + "\nIts closing message:\n" + answer
	if answer == "" {
		content = summary + "\nIt left no closing message."
	}
	if runErr != nil {
		content += "\n" + runErr.Error()
	}
	message, err := nativeAgentMessage(map[string]any{
		"kind": "subagent-settled", "form": "notice", "summary": summary, "senderSessionId": id,
	}, content)
	if err != nil {
		return err
	}
	m.mu.Lock()
	parent, parentProcess := m.liveLocked(process.parentID)
	m.mu.Unlock()
	if parent == nil {
		return errors.New("native child parent is no longer live")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := parent.Agent.SteerMessage(ctx, message); err != nil {
		return err
	}
	if parentProcess == nil {
		m.rootWake()
	} else {
		m.wakeChild(parentProcess)
	}
	return nil
}

func (m *nativeChildManager) SendChildMessage(ctx context.Context, senderID, targetID, content string) (string, error) {
	release, err := m.beginDelivery(ctx, targetID)
	if err != nil {
		return "", err
	}
	defer release()

	var target *assembly.NativeLive
	var targetProcess *nativeChildProcess
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return "", errors.New("native child manager is stopping")
		}
		sender, senderProcess := m.liveLocked(senderID)
		if sender == nil || (senderProcess != nil && senderProcess.closing) {
			m.mu.Unlock()
			return "", errors.New("sender Agent is not live")
		}
		target, targetProcess = m.liveLocked(targetID)
		if senderProcess == nil || senderProcess.parentID != targetID {
			children, err := sender.Session.Children(sessionformat.DurableCut)
			if err != nil {
				m.mu.Unlock()
				return "", err
			}
			found := false
			for _, child := range children {
				if child.ChildID == targetID && child.Mode == "continuable" {
					found = true
					break
				}
			}
			if !found {
				m.mu.Unlock()
				return "", errors.New("target is not an adjacent continuable Agent")
			}
			if target == nil {
				if len(m.children)+m.creating >= maxNativeChildren {
					m.mu.Unlock()
					return "", errors.New("native child capacity is unavailable")
				}
				m.creating++
				m.mu.Unlock()
				identity := m.identity
				identity.PermissionCeiling = func() permission.Mode { return permission.ReadSession(m.root.Session).Mode }
				resumed, err := m.factory.ResumeChild(ctx, identity, senderID, targetID)
				m.mu.Lock()
				m.creating--
				if err != nil {
					m.mu.Unlock()
					return "", err
				}
				if m.closed || m.children[targetID] != nil {
					m.mu.Unlock()
					return "", errors.Join(errors.New("native child manager is stopping or target changed"), resumed.Close())
				}
				target = resumed
				targetProcess = m.publishLocked(ctx, resumed, senderID)
			}
		}
		if targetProcess != nil && targetProcess.closing {
			done := targetProcess.done
			m.mu.Unlock()
			select {
			case <-done:
				continue // The durable child can now be cold-resumed.
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		if targetProcess != nil {
			targetProcess.admitted++
		}
		m.mu.Unlock()
		break
	}
	if targetProcess != nil {
		defer m.finishAdmission(targetProcess)
	}
	if target == nil {
		return "", errors.New("target parent Agent is not live")
	}
	message, err := nativeAgentMessage(map[string]any{
		"kind": "agent-message", "form": "relay", "senderSessionId": senderID,
	}, fmt.Sprintf("Agent %s sent a message: %s", senderID, content))
	if err != nil {
		return "", err
	}
	id, err := target.Agent.SteerMessage(ctx, message)
	if err != nil {
		return "", err
	}
	if targetProcess == nil {
		m.rootWake()
	} else {
		m.wakeChild(targetProcess)
	}
	return id, nil
}

// The manager lock chooses the admission-before-close cutoff. File-backed
// inbox append can then proceed without that lock, while child disposal waits
// for every already admitted append before closing its Session journal.
func (m *nativeChildManager) admissionDoneLocked(process *nativeChildProcess) chan struct{} {
	if process.admitted == 0 {
		return nil
	}
	if process.admitDone == nil {
		process.admitDone = make(chan struct{})
	}
	return process.admitDone
}

func (m *nativeChildManager) finishAdmission(process *nativeChildProcess) {
	m.mu.Lock()
	process.admitted--
	if process.admitted == 0 && process.admitDone != nil {
		close(process.admitDone)
		process.admitDone = nil
	}
	m.mu.Unlock()
}

func (m *nativeChildManager) beginDelivery(ctx context.Context, targetID string) (func(), error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("native child manager is stopping")
	}
	if m.deliveries == nil {
		m.deliveries = make(map[string]*nativeChildDelivery)
	}
	delivery := m.deliveries[targetID]
	if delivery == nil {
		delivery = &nativeChildDelivery{turn: make(chan struct{}, 1)}
		delivery.turn <- struct{}{}
		m.deliveries[targetID] = delivery
	}
	delivery.refs++
	m.pending.Add(1)
	m.mu.Unlock()
	finish := func() {
		m.mu.Lock()
		delivery.refs--
		if delivery.refs == 0 && m.deliveries[targetID] == delivery {
			delete(m.deliveries, targetID)
		}
		m.mu.Unlock()
		m.pending.Done()
	}
	select {
	case <-delivery.turn:
		if err := ctx.Err(); err != nil {
			delivery.turn <- struct{}{}
			finish()
			return nil, err
		}
		return func() { delivery.turn <- struct{}{}; finish() }, nil
	case <-ctx.Done():
		finish()
		return nil, ctx.Err()
	}
}

func (m *nativeChildManager) InterruptChild(_ context.Context, parentID, childID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	parent, _ := m.liveLocked(parentID)
	if parent == nil {
		return "", errors.New("parent Agent is not live")
	}
	children, err := parent.Session.Children(sessionformat.DurableCut)
	if err != nil {
		return "", err
	}
	for _, entry := range children {
		if entry.ChildID == childID {
			if process := m.children[childID]; process != nil {
				process.closing = true
				process.cancel()
				return "stopping", nil
			}
			return "inactive", nil
		}
	}
	return "", errors.New("target is not a direct child Agent")
}

func (m *nativeChildManager) ListChildren(ctx context.Context, parentID string) ([]tools.ChildStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parent, _ := m.liveLocked(parentID)
	if parent == nil {
		return nil, errors.New("parent Agent is not live")
	}
	children, err := parent.Session.Children(sessionformat.DurableCut)
	if err != nil {
		return nil, err
	}
	result := make([]tools.ChildStatus, 0, len(children))
	for _, entry := range children {
		// Match the model-facing DeepSeek list_agents view: one-shot children
		// remain in durable history but cannot be continued with send_message.
		if entry.Mode != "continuable" {
			continue
		}
		activity := "inactive"
		if process := m.children[entry.ChildID]; process != nil {
			if process.running {
				activity = "running"
			} else {
				activity = "idle"
			}
		}
		label := ""
		if entry.Label != nil {
			label = *entry.Label
		}
		result = append(result, tools.ChildStatus{SessionID: entry.ChildID, Label: label, Activity: activity})
	}
	return result, nil
}

// Close prevents new children, cancels active work, then waits for its
// completion. A deadline leaves the manager closed but retryable: the owner
// must not close its root Session while children may still be writing.
func (m *nativeChildManager) Close(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	for _, process := range m.children {
		process.closing = true
		process.cancel()
	}
	if m.pendingDone == nil {
		done := make(chan struct{})
		m.pendingDone = done
		go func() {
			m.pending.Wait()
			close(done)
		}()
	}
	pendingDone := m.pendingDone
	m.mu.Unlock()
	select {
	case <-pendingDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	m.mu.Lock()
	if m.childrenDone == nil {
		processes := make([]*nativeChildProcess, 0, len(m.children))
		for _, process := range m.children {
			processes = append(processes, process)
			process.closing = true
			process.cancel()
		}
		m.childrenDone = make(chan struct{})
		done := m.childrenDone
		go func() {
			for _, process := range processes {
				<-process.done
			}
			close(done)
		}()
	}
	done := m.childrenDone
	m.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *nativeChildManager) CancelAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, process := range m.children {
		process.closing = true
		process.cancel()
	}
}
