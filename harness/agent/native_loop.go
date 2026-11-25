package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"

	agentcontent "go-bot/harness/content"
	"go-bot/harness/session"
	"go-bot/internal/modeldata"
)

// NativeLoop is the production v4 execution path for a Host Session. It uses
// the shared model and tool implementations while writing DeepSeek's native
// Session events directly, including inbox, attachments and compaction.
type NativeLoop struct {
	*nativeTools
	// mu orders inbox admission against the first-step claim checkpoint. A
	// later message may be admitted while a model or tool call is running.
	mu       sync.Mutex
	turnOpen bool
	// streamMu guards process-local Assistant attempt frames. Slow observers
	// are dropped instead of stalling provider reads or durable Session writes.
	streamMu             sync.Mutex
	streamSubscribers    map[chan NativeAssistantStreamFrame]struct{}
	streamAttemptCounter uint64
	streamRevision       uint64
	streamActive         *nativeActiveAssistantAttempt
	// runMu permits only one driver to execute turns for this Agent.
	runMu                     sync.Mutex
	session                   *sessionformat.LiveSession
	model                     Model
	resolveTurnModel          func(context.Context) (TurnModel, error)
	resolveSelectedModel      ModelSelectionResolver
	provider                  string
	modelID                   string
	reasoningEffort           string
	configuredReasoningEffort string
	systemPrompt              string
	baseTools                 []Tool
	toolSource                ToolSource
	toolInvocationSource      ToolInvocationSource
	toolSpecs                 []ToolSpec
	maxSteps                  int
	maxToolCalls              int
	maxModelCalls             int
	modelCalls                int
	repeatGuard               repeatToolReminder
	repeatReminders           []map[string]any
	contextWindowTokens       int
	reservedOutputTokens      int
	contextHeadroomTokens     int
	pressureMeter             *nativePressureMeter
	requestLogged             bool
	modelProjection           map[uint64]nativeModelCacheEntry
	retryPolicy               nativeRetryPolicy
	filePath                  NativeFilePathResolver
	goalClaim                 func(json.RawMessage, sessionformat.InboxState) (bool, error)
	runtimeContext            func() []RuntimeContextSection
	runtimeContextSource      func(context.Context, string) ([]RuntimeContextSection, error)
	systemContextSource       func(context.Context, string) ([]RuntimeContextSection, error)
	promptAssembler           PromptAssemblySource
	assembledContexts         []RuntimeContextSection
	finishToolStep            func(context.Context, string) error
	preStep                   func(context.Context) (NativePreStepDecision, error)
	promptForStep             func() (string, error)
	instructionsForStep       func(context.Context, []string) (NativeInstructionSnapshot, error)
	skillCatalogTool          Tool
	skillCatalogForStep       func(context.Context) (NativeSkillCatalogSnapshot, error)
	skillInvocationLoader     func(context.Context, string) (string, bool, error)
	timeContext               *nativeTimeContext
	instructionScanned        int
	instructionCalls          map[string]string
	instructionTouched        []string
}

// NativeInstructionSnapshot is the current, bounded workspace-instruction
// baseline. It enters v4 history as a user-role agent-instructions message,
// never as an unrecorded addition to the system prompt.
type NativeInstructionSnapshot struct {
	Identity string
	Text     string
	Changes  []NativeInstructionChange
	// The replacement baseline has a longer intro and can represent a different
	// suffix under the same byte budget. Keep its text and exact source facts.
	ReplacementText    string
	ReplacementChanges []NativeInstructionChange
	// Content is retained only by the per-Session provider so a changed scope
	// can be rendered as a delta. It is never added to the source metadata.
	Content map[string]string
	// A failed file probe preserves the last visible instruction for its group.
	UnavailableGroups map[string]bool
}

type NativeInstructionChange struct {
	Action string `json:"action"`
	Scope  string `json:"scope"`
	Path   string `json:"path"`
	Digest string `json:"digest,omitempty"`
}

func (a *NativeLoop) SetTurnModelResolver(resolve func(context.Context) (TurnModel, error)) {
	a.resolveTurnModel = resolve
}

// SessionHeader and DurableSessionEventPage expose detached, read-only Host
// capabilities. The existing Go Session remains the journal and lifecycle owner.
func (a *NativeLoop) SessionHeader() sessionformat.Header { return a.session.Header() }

// SessionRecord lends the existing Go owner to Host capability adapters; it
// does not construct another session or transfer writer ownership.
func (a *NativeLoop) SessionRecord() *sessionformat.LiveSession { return a.session }
func (a *NativeLoop) DurableSessionEventPage(after *uint64, limit int) []sessionformat.Event {
	return a.session.DurableEventPage(after, limit)
}

// pinTurnModel is called with runMu held, before claiming durable work.
func (a *NativeLoop) pinTurnModel(ctx context.Context) (context.Context, error) {
	if a.resolveTurnModel == nil {
		return ctx, nil
	}
	model, ok := TurnModelFromContext(ctx)
	if !ok {
		var err error
		model, err = a.resolveTurnModel(ctx)
		if err != nil {
			return ctx, err
		}
	}
	if a.provider != model.Route.Provider || a.modelID != model.Route.Model {
		a.requestLogged = false
	}
	a.model, a.provider, a.modelID = model.Model, model.Route.Provider, model.Route.Model
	if a.resolveSelectedModel != nil {
		// The web/mobile model setting remains an explicit Host-owned input
		// for these manually managed turns. Record that input before step
		// preparation, so synchronous SDK reads agree with the real request.
		effort := model.Route.ReasoningEffort
		if effort == "" {
			effort = a.configuredReasoningEffort
		}
		a.reasoningEffort = effort
		if err := a.session.SelectModel(sessionformat.ModelSelection{Provider: model.Route.Provider, Model: model.Route.Model, ReasoningEffort: effort}); err != nil {
			return ctx, err
		}
	}
	ctx = context.WithValue(ctx, turnModelKey{}, model)
	return WithModelRoute(ctx, model.Route), nil
}

func (a *NativeLoop) SetInstructionProvider(provider func(context.Context, []string) (NativeInstructionSnapshot, error)) {
	a.instructionsForStep = provider
}

// SetSkillCatalogProvider binds discovery to the registered tool pointer.
func (a *NativeLoop) SetSkillCatalogProvider(tool Tool, provider func(context.Context) (NativeSkillCatalogSnapshot, error)) {
	a.skillCatalogTool = tool
	a.skillCatalogForStep = provider
}

func (a *NativeLoop) SetSkillInvocationLoader(loader func(context.Context, string) (string, bool, error)) {
	a.skillInvocationLoader = loader
}

// ErrNativeUserCancellation is the context cause for an explicit user stop.
// It is not a model or tool failure and must become the original v4 aborted
// turn reason, not an error turn or an owner-disposal reason.
var ErrNativeUserCancellation = errors.New("native Agent turn cancelled by user")

// SetModelCallLimit bounds all primary, retry and summary requests in one
// turn. Configure it before the worker starts driving this loop.
func (a *NativeLoop) SetModelCallLimit(limit int) error {
	if limit < 0 {
		return errors.New("negative native model-call limit")
	}
	a.maxModelCalls = limit
	return nil
}

// SetParallelToolLimit bounds overlapping dispatch of tools explicitly
// declared parallel-safe. Exclusive calls remain barriers in model order.
func (a *NativeLoop) SetParallelToolLimit(limit int) error {
	if limit < 0 || limit > 16 {
		return errors.New("parallel tool-call limit must be between 1 and 16")
	}
	if limit == 0 {
		limit = 4
	}
	a.maxParallelToolCalls = limit
	return nil
}

// A child route can pin adapter-owned effort independently of its provider
// and model. The effective value is part of the durable request header.
func (a *NativeLoop) SetReasoningEffort(effort string) {
	a.reasoningEffort = effort
	a.configuredReasoningEffort = effort
}

// SetContextPressure enables DeepSeek-style pre-request pressure checks for a
// route with known capacity. Zero window leaves pressure detection to the
// provider-confirmed overflow path.
func (a *NativeLoop) SetContextPressure(window, reservedOutput, headroom int) error {
	limits := Limits{ContextWindowTokens: window, ReservedOutputTokens: reservedOutput, ContextHeadroomTokens: headroom}
	if err := limits.ValidateContextWindow(); err != nil {
		return err
	}
	if window > 0 {
		threshold := min(window*8/10, window-reservedOutput-headroom)
		retain := (window - reservedOutput) * 16 / 100
		if retain >= threshold {
			return errors.New("native compaction retention must be below its pressure threshold")
		}
	}
	a.contextWindowTokens, a.reservedOutputTokens, a.contextHeadroomTokens = window, reservedOutput, headroom
	return nil
}

// SetGoalClaimGuard installs the same-session continuation fence. A goal
// message is never admitted to the surface merely because it is queued; the
// exact current goal and competing input are checked at the claim boundary.
func (a *NativeLoop) SetGoalClaimGuard(guard func(json.RawMessage, sessionformat.InboxState) (bool, error)) {
	a.goalClaim = guard
}

// NativePreStepDecision mirrors the core Loop's enter/reject boundary. A
// rejection consumes the claimed inbox input and closes the turn as blocked
// without opening a step or calling the model.
type NativePreStepDecision uint8

const (
	NativePreStepEnter NativePreStepDecision = iota
	NativePreStepReject
)

func (a *NativeLoop) SetPreStepHook(hook func(context.Context) (NativePreStepDecision, error)) {
	a.preStep = hook
}

func (a *NativeLoop) SetSystemPromptProvider(provider func() (string, error)) {
	a.promptForStep = provider
}

func (a *NativeLoop) completeModel(ctx context.Context, messages []Message) (modeldata.AssistantMessage, error) {
	if a.maxModelCalls > 0 && a.modelCalls >= a.maxModelCalls {
		return modeldata.AssistantMessage{}, ErrModelCallBudget
	}
	a.modelCalls++
	return a.model.Complete(ctx, messages, a.toolSpecs)
}

// completeAttemptModel records the exact chunks delivered by a streaming
// model. A failed request returns its partial stream to the retry driver; no
// chunk becomes a surface message until that driver settles the attempt.
func (a *NativeLoop) completeAttemptModel(ctx context.Context, turn uint64, step int, messages []Message) (response modeldata.AssistantMessage, err error) {
	streaming, ok := a.model.(StreamingModel)
	if !ok {
		return a.completeModel(ctx, messages)
	}
	if a.maxModelCalls > 0 && a.modelCalls >= a.maxModelCalls {
		return modeldata.AssistantMessage{}, ErrModelCallBudget
	}
	a.modelCalls++
	accumulator := &nativeStreamAccumulator{records: make([]any, 0, 32)}
	attemptID := a.startStreamAttempt(turn, step, accumulator)
	recordChunk := func(chunk ModelStreamChunk) error {
		encoded, marshalErr := json.Marshal(chunk)
		if marshalErr != nil {
			return marshalErr
		}
		var snapshot map[string]any
		if unmarshalErr := json.Unmarshal(encoded, &snapshot); unmarshalErr != nil {
			return unmarshalErr
		}
		stamp := time.Now().UnixMilli()
		a.streamMu.Lock()
		index := accumulator.chunks
		accumulator.push(ModelStreamChunk(snapshot), stamp)
		a.publishStreamFrameLocked(NativeAssistantStreamFrame{Type: "chunk", AttemptID: attemptID, Index: &index, Time: stamp, Chunk: ModelStreamChunk(snapshot)})
		a.streamMu.Unlock()
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("model stream panicked: %v", recovered)
		}
		if err == nil && !nativeStreamHasFinish(accumulator.records) {
			err = errors.New("model stream ended without finish chunk")
		}
		if err != nil && !nativeStreamHasFinish(accumulator.records) {
			failure, _ := nativeModelFailure(err)
			reason := map[string]any{"kind": "error", "failure": map[string]any{"message": failure.Message, "code": failure.Code}}
			if ctx.Err() != nil {
				reason = map[string]any{"kind": "aborted", "failure": map[string]any{"message": err.Error(), "code": "ABORTED"}}
			}
			if appendErr := recordChunk(ModelStreamChunk{"type": "finish", "reason": reason}); appendErr != nil {
				err = errors.Join(err, appendErr)
			}
		}
		response.AttemptID = attemptID
		response.StreamRecords = accumulator.records
		response.StreamCount = accumulator.chunks
	}()
	response, err = streaming.StreamChunks(ctx, messages, a.toolSpecs, recordChunk)
	return
}

func nativeStreamHasFinish(records []any) bool {
	if len(records) == 0 {
		return false
	}
	entry, ok := records[len(records)-1].(map[string]any)
	if !ok {
		return false
	}
	chunk, ok := entry["chunk"].(map[string]any)
	return ok && chunk["type"] == "finish"
}

func NewNativeLoop(s *sessionformat.LiveSession, model Model, provider, modelID, systemPrompt string, tools []Tool, maxSteps, maxToolCalls int) (*NativeLoop, error) {
	if s == nil || model == nil || provider == "" || modelID == "" || maxSteps <= 0 || maxToolCalls <= 0 {
		return nil, errors.New("incomplete native Agent loop")
	}
	repeatGuard, err := newRepeatToolReminder(nil)
	if err != nil {
		return nil, err
	}
	loop := &NativeLoop{nativeTools: &nativeTools{header: s.Header, tools: make(map[string]Tool), toolSchemas: make(map[string]*jsonschema.Schema), parallelSafe: make(map[string]bool), maxParallelToolCalls: 4}, session: s, model: model, provider: provider, modelID: modelID, systemPrompt: systemPrompt,
		maxSteps: maxSteps, maxToolCalls: maxToolCalls, retryPolicy: defaultNativeRetryPolicy(), repeatGuard: repeatGuard}
	loop.nativeTools.postResult = loop.observeRepeatedToolResult
	if err := loop.installTools(tools); err != nil {
		return nil, err
	}
	loop.baseTools = append([]Tool(nil), tools...)
	return loop, nil
}

// AcceptMessage persists a complete native user message before execution.
// HTTP multipart admission can pass its text and attachment blocks together;
// there is no idempotency key or separate upload operation here.
func (a *NativeLoop) AcceptMessage(ctx context.Context, message json.RawMessage) (string, error) {
	return a.appendInboxMessage(ctx, "next-turn", message)
}

// SteerMessage wakes the driver at the next step boundary in the same turn.
// The caller is responsible for signaling its worker driver after admission.
func (a *NativeLoop) SteerMessage(ctx context.Context, message json.RawMessage) (string, error) {
	return a.appendInboxMessage(ctx, "next-step", message)
}

// InjectMessage uses the same next-step queue without asking an idle driver to
// wake. Job and tool producers can use it while a turn is already active.
func (a *NativeLoop) InjectMessage(ctx context.Context, message json.RawMessage) (string, error) {
	return a.appendInboxMessage(ctx, "next-step", message)
}

var ErrQueuedMessageNotFound = errors.New("queued message not found")
var ErrSteerUnavailable = errors.New("no active turn for steering")

// UpdateQueuedMessage edits the next-turn inbox under the same lock as claims.
// A move is recorded as one splice so recovery sees it in exactly one queue.
func (a *NativeLoop) UpdateQueuedMessage(ctx context.Context, id string, steer bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if steer && !a.turnOpen {
		return ErrSteerUnavailable
	}
	for index, raw := range a.session.Inbox().NextTurn {
		var message struct {
			ID     string                `json:"id"`
			Source struct{ Kind string } `json:"source"`
		}
		if json.Unmarshal(raw, &message) != nil || message.ID != id || message.Source.Kind != "user" {
			continue
		}
		splice := map[string]any{"target": "next-turn", "start": index, "removedCount": 1, "inserted": []any{}}
		if steer {
			splice["moveToNextStep"] = true
		} else {
			splice["outcome"] = "canceled"
		}
		if _, err := a.append("agent/inbox/spliced", splice, nil, nil); err != nil {
			return err
		}
		return a.session.Flush(ctx)
	}
	return ErrQueuedMessageNotFound
}

func (a *NativeLoop) appendInboxMessage(ctx context.Context, target string, message json.RawMessage) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var identity struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(message, &identity) != nil || identity.ID == "" {
		return "", errors.New("native user message requires an ID")
	}
	inbox := a.session.Inbox()
	start := len(inbox.NextTurn)
	if target == "next-step" {
		start = len(inbox.NextStep)
	}
	if _, err := a.append("agent/inbox/spliced", map[string]any{"target": target, "start": start, "inserted": []json.RawMessage{message}}, nil, nil); err != nil {
		return "", err
	}
	if err := a.session.Flush(ctx); err != nil {
		return "", err
	}
	return identity.ID, nil
}

func (a *NativeLoop) AcceptText(ctx context.Context, text string) (string, error) {
	if text == "" {
		return "", errors.New("empty user turn")
	}
	message, err := json.Marshal(nativeTextMessage(uuid.NewString(), "user", map[string]any{"kind": "user"}, text))
	if err != nil {
		return "", err
	}
	return a.AcceptMessage(ctx, message)
}

// CancelPending mirrors DeepSeek's Inbox.clear: next-step is discarded before
// next-turn, and each nonempty removal is a durable canceled splice. It does
// not itself interrupt a running turn; the owner cancels that turn's context.
func (a *NativeLoop) CancelPending(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	inbox := a.session.Inbox()
	for _, pending := range []struct {
		target string
		count  int
	}{{"next-step", len(inbox.NextStep)}, {"next-turn", len(inbox.NextTurn)}} {
		if pending.count == 0 {
			continue
		}
		if _, err := a.append("agent/inbox/spliced", map[string]any{
			"target": pending.target, "start": 0, "removedCount": pending.count,
			"inserted": []any{}, "outcome": "canceled",
		}, nil, nil); err != nil {
			return err
		}
	}
	return a.session.Flush(ctx)
}

// RunNext claims a durable next-step batch and, when present, one next-turn
// message. Admission and model execution are separate, matching the worker
// HTTP request and its background driver.
func (a *NativeLoop) RunNext(ctx context.Context) (ran bool, answer string, err error) {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	a.mu.Lock()
	if _, err := sessionformat.RepairInterruptedTurn(ctx, a.session); err != nil {
		a.mu.Unlock()
		return false, "", err
	}
	inbox := a.session.Inbox()
	for len(inbox.NextTurn) > 0 && nativeMessageSourceKind(inbox.NextTurn[0]) == "goal" {
		valid := false
		if a.goalClaim != nil {
			valid, err = a.goalClaim(inbox.NextTurn[0], inbox)
			if err != nil {
				a.mu.Unlock()
				return false, "", err
			}
		}
		if valid {
			break
		}
		if _, err := a.append("agent/inbox/spliced", map[string]any{
			"target": "next-turn", "start": 0, "removedCount": 1,
			"inserted": []any{}, "outcome": "canceled",
		}, nil, nil); err != nil {
			a.mu.Unlock()
			return false, "", err
		}
		if err := a.session.Flush(ctx); err != nil {
			a.mu.Unlock()
			return false, "", err
		}
		inbox = a.session.Inbox()
	}
	if len(inbox.NextTurn) == 0 && len(inbox.NextStep) == 0 {
		a.mu.Unlock()
		return false, "", nil
	}
	ctx, err = a.pinTurnModel(ctx)
	if err != nil {
		a.mu.Unlock()
		return false, "", err
	}
	// runClaimed owns mu until the claimed input and its first request are
	// durable. Otherwise a concurrent admission Flush could persist only the
	// inbox removal and lose the claimed user message after a crash.
	answer, err = a.runClaimed(ctx, a.mu.Unlock)
	return true, answer, err
}

func nativeMessageSourceKind(message json.RawMessage) string {
	var data struct {
		Source struct {
			Kind string `json:"kind"`
		} `json:"source"`
	}
	if json.Unmarshal(message, &data) != nil {
		return ""
	}
	return data.Source.Kind
}

// RunTextTurn is a direct test and local-control convenience. Production HTTP
// admission uses AcceptMessage, then the background driver calls RunNext.
func (a *NativeLoop) RunTextTurn(ctx context.Context, text string) (string, error) {
	if _, err := a.AcceptText(ctx, text); err != nil {
		return "", err
	}
	_, answer, err := a.RunNext(ctx)
	return answer, err
}

func (a *NativeLoop) runClaimed(ctx context.Context, releaseAdmission func()) (answer string, err error) {
	claimLocked := true
	releaseClaim := func() {
		if claimLocked {
			claimLocked = false
			releaseAdmission()
		}
	}
	defer releaseClaim()
	a.modelCalls = 0
	turn := a.session.TurnBoundary(sessionformat.LiveCut).LastTurn + 1
	if _, err = a.append("turn/start", map[string]any{"turn": turn}, nil, nil); err != nil {
		return "", err
	}
	a.turnOpen = true
	stepOpen := false
	maxTokensSeen := false
	blocked := false
	defer func() {
		err = errors.Join(err, a.releaseToolStep(ctx))
		if !claimLocked {
			a.mu.Lock()
			claimLocked = true
		}
		a.turnOpen = false
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("native Agent panic: %v", recovered)
		}
		settle := context.Background()
		if stepOpen {
			_, closeErr := a.append("step/end", map[string]any{"turn": turn, "step": a.session.TurnBoundary(sessionformat.LiveCut).Step}, nil, nil)
			err = errors.Join(err, closeErr)
		}
		reason := map[string]any{"kind": "completed"}
		if ctx.Err() != nil {
			cause := map[string]any{"kind": "disposed"}
			switch {
			case errors.Is(context.Cause(ctx), ErrNativeUserCancellation):
				cause = map[string]any{"kind": "user"}
			case errors.Is(context.Cause(ctx), context.DeadlineExceeded):
				cause = map[string]any{"kind": "hook", "reason": "turn deadline exceeded"}
			}
			reason = map[string]any{"kind": "aborted", "reason": cause}
		} else if err != nil {
			reason = map[string]any{"kind": "error", "error": nativeTurnFailure(err)}
		} else if blocked {
			reason = map[string]any{"kind": "blocked"}
		} else if maxTokensSeen {
			reason = map[string]any{"kind": "max-tokens"}
		}
		_, closeErr := a.append("turn/end", map[string]any{"turn": turn, "reason": reason}, nil, nil)
		err = errors.Join(err, closeErr, a.session.Flush(settle))
	}()
	toolCount := 0
	turnContext := ctx
	for step := 1; step <= a.maxSteps; step++ {
		ctx = turnContext
		if err = ctx.Err(); err != nil {
			return "", err
		}
		inbox := a.session.Inbox()
		claimed := make([]json.RawMessage, 0, len(inbox.NextStep)+1)
		if len(inbox.NextStep) > 0 {
			claimed = append(claimed, inbox.NextStep...)
			if _, err = a.append("agent/inbox/spliced", map[string]any{"target": "next-step", "start": 0,
				"removedCount": len(inbox.NextStep), "inserted": []any{}}, nil, nil); err != nil {
				return "", err
			}
		}
		if step == 1 && len(inbox.NextTurn) > 0 {
			claimed = append(claimed, inbox.NextTurn[0])
			if _, err = a.append("agent/inbox/spliced", map[string]any{"target": "next-turn", "start": 0,
				"removedCount": 1, "inserted": []any{}}, nil, nil); err != nil {
				return "", err
			}
		}
		ctx, err = a.captureStepModel(ctx)
		if err != nil {
			return "", err
		}
		if a.preStep != nil {
			var decision NativePreStepDecision
			if decision, err = a.preStep(ctx); err != nil {
				return "", err
			}
			if decision == NativePreStepReject {
				blocked = true
				return "", nil
			}
			if decision != NativePreStepEnter {
				return "", errors.New("invalid native pre-step decision")
			}
		}
		if err = a.refreshTools(ctx); err != nil {
			return "", err
		}
		if err = a.refreshToolInvocationSource(ctx); err != nil {
			return "", err
		}
		if _, err = a.append("step/start", map[string]any{"turn": turn, "step": step}, nil, nil); err != nil {
			return "", err
		}
		stepOpen = true
		if err = a.ensureSystem(ctx, turn, step); err != nil {
			return "", err
		}
		if err = a.ensureSkillCatalog(ctx); err != nil {
			return "", err
		}
		claimedDirectUser := false
		for _, message := range claimed {
			var incoming struct {
				Source struct {
					Kind string `json:"kind"`
				} `json:"source"`
			}
			if err = json.Unmarshal(message, &incoming); err != nil {
				return "", err
			}
			claimedDirectUser = claimedDirectUser || incoming.Source.Kind == "user"
			if _, err = a.append("user/message", message, json.RawMessage(`"append"`), nil); err != nil {
				return "", err
			}
		}
		if err = a.ensureInstructions(ctx); err != nil {
			return "", err
		}
		if err = a.ensureRuntimeContext(ctx); err != nil {
			return "", err
		}
		if err = a.ensureTimeContext(ctx, turn, step); err != nil {
			return "", err
		}
		if err = a.ensureInvokedSkills(ctx, claimed); err != nil {
			return "", err
		}
		if err = a.session.Flush(ctx); err != nil {
			return "", err
		}
		if claimedDirectUser {
			a.repeatGuard.reset()
		}
		releaseClaim()
		if err = a.maybeCompactNativePressure(ctx, turn); err != nil {
			return "", err
		}
		a.mu.Lock()
		if err = a.logRequest(turn, step); err == nil {
			err = a.session.Flush(ctx)
		}
		a.mu.Unlock()
		if err != nil {
			return "", err
		}
		messages, projectionErr := a.modelMessages()
		if projectionErr != nil {
			return "", projectionErr
		}
		response, modelErr := a.completeNativeStep(ctx, turn, step, messages)
		if modelErr != nil {
			return "", modelErr
		}
		if response.MaxTokens {
			// A truncated step cannot authorize tool side effects, even if a
			// provider supplied apparently complete tool-call fragments.
			response.ToolCalls = nil
			blocks := response.OutputBlocks[:0]
			for _, block := range response.OutputBlocks {
				if block.Type != "tool-call" {
					blocks = append(blocks, block)
				}
			}
			response.OutputBlocks = blocks
		}
		var assistantEvent sessionformat.Event
		if assistantEvent, err = a.logAssistant(turn, step, response); err != nil {
			a.endStreamAttempt(response.AttemptID, response.StreamCount, "", 0, false)
			return "", err
		}
		maxTokensSeen = maxTokensSeen || response.MaxTokens
		answer = response.Content
		if err = a.session.Flush(ctx); err != nil {
			a.endStreamAttempt(response.AttemptID, response.StreamCount, "", 0, false)
			return "", err
		}
		a.endStreamAttempt(response.AttemptID, response.StreamCount, "assistant/message", assistantEvent.Seq, true)
		a.repeatReminders = nil
		toolCtx := context.WithValue(ctx, toolQuestionKey{}, sync.OnceValue(func() string {
			return latestToolQuestion(a.session)
		}))
		budgetExceeded, concluded, toolErr := a.executeNativeStepTools(toolCtx, turn, step, response.ToolCalls, &toolCount)
		toolErr = errors.Join(toolErr, a.releaseToolStep(ctx))
		if toolErr != nil {
			a.repeatReminders = nil
			return "", toolErr
		}
		for _, reminder := range a.repeatReminders {
			if _, err = a.append("user/message", reminder, json.RawMessage(`"append"`), nil); err != nil {
				return "", err
			}
		}
		if len(a.repeatReminders) > 0 {
			if err = a.session.Flush(ctx); err != nil {
				return "", err
			}
		}
		a.repeatReminders = nil
		if err = ctx.Err(); err != nil {
			return "", err
		}
		if _, err = a.append("step/end", map[string]any{"turn": turn, "step": step}, nil, nil); err != nil {
			return "", err
		}
		stepOpen = false
		if budgetExceeded {
			return "", errors.New("native Agent tool-call budget exhausted")
		}
		// Serialize the stop decision with next-step admission. If a steer
		// was accepted before this boundary, it stays in the current turn;
		// one accepted afterward starts another turn when the worker wakes.
		a.mu.Lock()
		claimLocked = true
		if (concluded || len(response.ToolCalls) == 0) && len(a.session.Inbox().NextStep) == 0 {
			return answer, nil
		}
	}
	return "", errors.New("native Agent step budget exhausted")
}

func nextNativeTurn(events []sessionformat.Event) uint64 {
	var turn uint64
	for _, event := range events {
		if event.Type != "turn/start" {
			continue
		}
		var data struct {
			Turn uint64 `json:"turn"`
		}
		if json.Unmarshal(event.Data, &data) == nil && data.Turn > turn {
			turn = data.Turn
		}
	}
	return turn + 1
}

func currentNativeStep(events []sessionformat.Event, turn uint64) int {
	step := 0
	for _, event := range events {
		if event.Type != "step/start" {
			continue
		}
		var data struct {
			Turn uint64 `json:"turn"`
			Step int    `json:"step"`
		}
		if json.Unmarshal(event.Data, &data) == nil && data.Turn == turn && data.Step > step {
			step = data.Step
		}
	}
	return step
}

func nativeTextMessage(id, role string, source map[string]any, text string) map[string]any {
	content := []any{}
	if text != "" {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	return map[string]any{"id": id, "role": role, "source": source, "content": content}
}

func (a *NativeLoop) append(eventType string, data any, surfaceOp json.RawMessage, sources []uint64) (sessionformat.Event, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return sessionformat.Event{}, err
	}
	return a.session.Append(sessionformat.EventInput{Type: eventType, Data: encoded, SurfaceOp: surfaceOp, SourceEventSeqs: sources})
}

func (a *NativeLoop) ensureSystem(ctx context.Context, turn uint64, step int) error {
	prompt := a.systemPrompt
	if a.promptForStep != nil {
		var err error
		prompt, err = a.promptForStep()
		if err != nil {
			return err
		}
	}
	if a.systemContextSource != nil {
		sections, err := a.systemContextSource(ctx, a.session.Header().ID)
		if err != nil {
			return err
		}
		for _, section := range sections {
			if section.Text != "" {
				prompt += "\n\n" + section.Text
			}
		}
	}
	var assemblyErr error
	prompt, assemblyErr = a.assemblePrompt(ctx, prompt)
	if assemblyErr != nil {
		return assemblyErr
	}
	surface, err := a.session.ModelSurface()
	if err != nil {
		return err
	}
	message := nativeTextMessage(uuid.NewString(), "system", map[string]any{"kind": "system-prompt"}, prompt)
	data := map[string]any{"turn": turn, "step": step, "message": message}
	operation := json.RawMessage(`"append"`)
	var sources []uint64
	if len(surface.Nodes) > 0 {
		head := surface.Nodes[0]
		if surface.NodeTypes[head] == "system/message" {
			var existing struct {
				Message struct {
					Content []struct{ Type, Text string } `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(surface.NodeMessages[head], &existing.Message) != nil {
				return errors.New("invalid prior system message")
			}
			prior := ""
			if len(existing.Message.Content) == 1 && existing.Message.Content[0].Type == "text" {
				prior = existing.Message.Content[0].Text
			}
			if prior == prompt {
				return nil
			}
			operation, err = json.Marshal(map[string]any{"op": "replace", "startSeq": head, "endSeq": head})
			if err != nil {
				return err
			}
			sources = []uint64{head}
		}
	}
	_, err = a.append("system/message", data, operation, sources)
	return err
}

func (a *NativeLoop) logRequest(turn uint64, step int) error {
	if a.requestLogged {
		return nil
	}
	header, err := a.nativeRequestHeader()
	if err != nil {
		return err
	}
	reason := "initial"
	if a.session.HasRequestHeader() {
		reason = "resume"
	}
	if _, err := a.append("request/header", map[string]any{"header": header, "reason": reason}, nil, nil); err != nil {
		return err
	}
	if _, err := a.append("request/context", map[string]any{"provider": a.provider, "model": a.modelID}, nil, nil); err != nil {
		return err
	}
	a.requestLogged = true
	return nil
}

func (a *NativeLoop) nativeRequestHeader() (map[string]any, error) {
	config := map[string]any{"provider": a.provider, "model": a.modelID}
	if a.reasoningEffort != "" {
		config["reasoningEffort"] = a.reasoningEffort
	}
	header := map[string]any{"config": config}
	if len(a.toolSpecs) > 0 {
		tools, err := nativeCanonicalTools(a.toolSpecs)
		if err != nil {
			return nil, err
		}
		header["tools"] = tools
	}
	return header, nil
}

func nativeCanonicalTools(specs []ToolSpec) ([]any, error) {
	tools := make([]any, 0, len(specs))
	for _, spec := range specs {
		var parameters map[string]any
		if err := json.Unmarshal(spec.Parameters, &parameters); err != nil || parameters == nil {
			return nil, fmt.Errorf("tool %q has non-object parameters", spec.Name)
		}
		tools = append(tools, map[string]any{"name": spec.Name, "description": spec.Description, "parameters": parameters})
	}
	return tools, nil
}

func (a *NativeLoop) logAssistant(turn uint64, step int, response modeldata.AssistantMessage) (sessionformat.Event, error) {
	content := make([]map[string]any, 0, len(response.ToolCalls)+1)
	if len(response.OutputBlocks) > 0 {
		var text strings.Builder
		calls := make([]modeldata.ToolRequest, 0, len(response.ToolCalls))
		for _, block := range response.OutputBlocks {
			switch block.Type {
			case "text":
				text.WriteString(block.Text)
				content = append(content, map[string]any{"type": "text", "text": block.Text})
			case "reasoning":
				content = append(content, map[string]any{"type": "reasoning", "text": block.Text})
			case "tool-call":
				if block.ID == "" || block.Name == "" {
					return sessionformat.Event{}, errors.New("model returned invalid tool-call identity")
				}
				calls = append(calls, modeldata.ToolRequest{ID: block.ID, Name: block.Name, Arguments: block.Arguments})
				content = append(content, map[string]any{"type": "tool-call", "id": block.ID, "name": block.Name, "arguments": block.Arguments})
			default:
				return sessionformat.Event{}, fmt.Errorf("unsupported ordered model output block %q", block.Type)
			}
		}
		if text.String() != response.Content || len(calls) != len(response.ToolCalls) {
			return sessionformat.Event{}, errors.New("ordered model output does not match execution projection")
		}
		for index, call := range calls {
			if call != response.ToolCalls[index] {
				return sessionformat.Event{}, errors.New("ordered model tool calls do not match execution projection")
			}
		}
	} else {
		if response.Content != "" {
			content = append(content, map[string]any{"type": "text", "text": response.Content})
		}
		for _, call := range response.ToolCalls {
			if call.ID == "" || call.Name == "" {
				return sessionformat.Event{}, errors.New("model returned invalid tool-call identity")
			}
			content = append(content, map[string]any{"type": "tool-call", "id": call.ID, "name": call.Name, "arguments": call.Arguments})
		}
	}
	message := map[string]any{"id": uuid.NewString(), "role": "assistant", "source": map[string]any{"kind": "model", "provider": a.provider, "model": a.modelID}, "content": content}
	if response.ProviderReplay != nil && !response.Interrupted && !response.MaxTokens {
		message["providerReplay"] = response.ProviderReplay
	}
	stream := response.StreamRecords
	streamed := stream != nil
	stamp := time.Now().UnixMilli()
	if !streamed {
		stream = make([]any, 0, len(content)*2+2)
		for index, block := range content {
			kind := block["type"]
			stream = append(stream,
				map[string]any{"type": "chunk", "time": stamp, "chunk": map[string]any{"type": "block-start", "index": index, "blockType": kind}},
				map[string]any{"type": "chunk", "time": stamp, "chunk": map[string]any{"type": "block-end", "index": index, "block": block}},
			)
		}
	}
	data := map[string]any{"turn": turn, "step": step, "message": message, "stream": stream}
	if response.Interrupted {
		data["interrupted"] = true
	}
	if response.Usage != nil {
		usage, err := nativeUsage(response.Usage)
		if err != nil {
			return sessionformat.Event{}, err
		}
		data["usage"] = usage
		if !streamed {
			stream = append(stream, map[string]any{"type": "chunk", "time": stamp, "chunk": map[string]any{"type": "usage", "usage": usage}})
		}
	}
	if !streamed {
		finish := "stop"
		if response.MaxTokens {
			finish = "max-tokens"
		} else if len(response.ToolCalls) > 0 {
			finish = "tool-calls"
		}
		stream = append(stream, map[string]any{"type": "chunk", "time": stamp, "chunk": map[string]any{"type": "finish", "reason": map[string]any{"kind": finish}}})
	}
	data["stream"] = stream
	return a.append("assistant/message", data, json.RawMessage(`"append"`), nil)
}

func nativeUsage(usage *modeldata.ModelUsage) (map[string]any, error) {
	if usage == nil || usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.TotalTokens < 0 || usage.CachedInputTokens < 0 || usage.CacheCreationInputTokens < 0 ||
		usage.ReasoningTokens < 0 || usage.ReasoningTokens > usage.OutputTokens ||
		usage.CachedInputTokens > math.MaxInt64-usage.CacheCreationInputTokens || usage.CachedInputTokens+usage.CacheCreationInputTokens > usage.InputTokens {
		return nil, ErrInvalidProviderUsage
	}
	result := map[string]any{"inputTokens": usage.InputTokens - usage.CachedInputTokens - usage.CacheCreationInputTokens, "outputTokens": usage.OutputTokens}
	if usage.TotalTokens > 0 {
		result["totalTokens"] = usage.TotalTokens
	}
	if usage.CachedInputTokens > 0 {
		result["cacheReadTokens"] = usage.CachedInputTokens
	}
	if usage.CacheCreationInputTokens > 0 {
		result["cacheWriteTokens"] = usage.CacheCreationInputTokens
	}
	if usage.ReasoningTokens > 0 {
		result["reasoningTokens"] = usage.ReasoningTokens
	}
	return result, nil
}

func (a *nativeTools) executeNativeToolDirect(ctx context.Context, turn, step uint64, call modeldata.ToolRequest, tool Tool, schema *jsonschema.Schema) (result modeldata.ToolResult) {
	result = modeldata.ToolResult{ID: call.ID, Name: call.Name}
	defer func() {
		if recovered := recover(); recovered != nil {
			result.IsError, result.Code, result.Content = true, modeldata.ToolOutcomeUnknown, fmt.Sprintf("Tool panicked; its outcome is unknown: %v", recovered)
		}
	}()
	if tool == nil {
		result.IsError, result.Code, result.ErrorName, result.Content = true, "UNKNOWN_TOOL", "ToolNotFoundError", fmt.Sprintf("Error: unknown tool %q", call.Name)
		return result
	}
	presented, hasPresentation := tool.(PresentedTool)
	arguments := json.RawMessage(call.Arguments)
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	if !json.Valid(arguments) {
		result.IsError, result.Code, result.Content = true, "INVALID_ARGUMENTS", "Invalid tool arguments JSON."
		if hasPresentation {
			result.ErrorName, result.Code = "ToolArgsError", "INVALID_ARGS"
			result.Content = `Error: invalid arguments: "arguments" must be an object`
		}
		return result
	}
	if schema == nil {
		result.IsError, result.Code, result.Content = true, "TOOL_ERROR", "Tool parameter schema was not compiled."
		return result
	} else if err := validateToolParameters(schema, arguments); err != nil {
		result.IsError, result.Code, result.Content = true, "INVALID_ARGUMENTS", err.Error()
		if hasPresentation {
			result.ErrorName, result.Code = "ToolArgsError", "INVALID_ARGS"
			result.Content = presentedToolArgumentError(err)
		}
		return result
	}
	if err := tool.Validate(arguments); err != nil {
		result.IsError, result.Code, result.Content = true, "INVALID_ARGUMENTS", err.Error()
		if hasPresentation {
			result.Code, result.Content = "", "Error: "+err.Error()
		}
		return result
	}
	if ctx.Err() != nil {
		result.IsError, result.Code, result.ErrorName, result.Content = true, "ABORTED_BEFORE_DISPATCH", "AbortError", "tool call aborted before dispatch"
		return result
	}
	markToolBodyStarted(ctx)
	control := &toolControl{}
	executionCtx := withToolPosition(ctx, turn, step)
	executionCtx = withToolCallID(executionCtx, call.ID)
	executionCtx = context.WithValue(executionCtx, toolControlKey{}, control)
	cancel := func() {}
	var toolTimeoutCause error
	timeout := tool.Spec().Timeout
	var content string
	var blocks []agentcontent.ContentBlock
	canonicalResult := false
	var err error

	// A tool request budget is not an ordinary user-approval timeout.
	// Start it only after the pre-dispatch approval has settled.
	if err == nil && timeout > 0 {
		toolTimeoutCause = errors.New("Harness tool call deadline")
		executionCtx, cancel = context.WithTimeoutCause(executionCtx, timeout, toolTimeoutCause)
	}
	defer cancel()
	if err != nil {
		// Process policy denial as a normal tool failure.
	} else if normalized, ok := tool.(ResultTool); ok {
		var body modeldata.ToolResult
		body, err = normalized.ExecuteResult(executionCtx, arguments)
		content, blocks, result.Meta = body.Content, body.ContentBlocks, body.Meta
		result.IsError, result.Code, result.ErrorName, result.ErrorReason = body.IsError, body.Code, body.ErrorName, body.ErrorReason
		result.AdditionalContexts = body.AdditionalContexts
		result.ConcludeTurn = body.ConcludeTurn
		canonicalResult = true
	} else if rich, ok := tool.(RichTool); ok {
		content, blocks, result.Meta, err = rich.ExecuteRich(executionCtx, arguments)
	} else if hasPresentation {
		content, result.Meta, err = presented.ExecutePresented(executionCtx, arguments)
	} else if structured, ok := tool.(ContentTool); ok {
		content, blocks, err = structured.ExecuteContent(executionCtx, arguments)
	} else {
		content, err = tool.Execute(executionCtx, arguments)
	}
	if toolTimeoutCause != nil && errors.Is(context.Cause(executionCtx), toolTimeoutCause) {
		result.IsError, result.Code = true, "TOOL_TIMEOUT"
		result.Content = fmt.Sprintf("Error: tool call timed out after %dms", timeout.Milliseconds())
		result.Meta = nil
		return result
	}
	if hasPresentation && err == nil && ctx.Err() != nil {
		result.IsError, result.ErrorName, result.Code = true, "AbortError", "ABORTED"
		result.Content, result.Meta = "Error: tool call aborted", nil
		return result
	}
	if err != nil {
		result.IsError, result.Code, result.Content = true, "TOOL_ERROR", err.Error()
		result.Meta = nil
		var info interface{ ToolErrorInfo() (string, string) }
		if hasPresentation {
			result.Code, result.Content = "", "Error: "+err.Error()
			if errors.As(err, &info) {
				result.ErrorName, result.Code = info.ToolErrorInfo()
			} else if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				result.ErrorName, result.Code = "AbortError", "ABORTED"
				result.Content = "Error: tool call aborted"
			}
		} else if ctx.Err() != nil || errors.Is(err, ErrToolOutcomeUnknown) {
			result.Code = modeldata.ToolOutcomeUnknown
		} else if errors.As(err, &info) {
			result.ErrorName, result.Code = info.ToolErrorInfo()
		}
		return result
	}
	if err := agentcontent.ValidateDurableContentBlocks(blocks); err != nil {
		result.IsError, result.Code, result.Content = true, "TOOL_ERROR", err.Error()
		return result
	}
	result.Content = content
	result.ContentBlocks = agentcontent.CloneContentBlocks(blocks)
	result.ConcludeTurn = !result.IsError && (result.ConcludeTurn || control.concludeTurn)
	if hasPresentation {
		if result.IsError && !canonicalResult {
			result.Meta = nil
		}
	}
	return result
}

func (a *NativeLoop) logToolResult(turn uint64, step int, callSeq uint64, result modeldata.ToolResult) error {
	message := nativeTextMessage(uuid.NewString(), "tool", map[string]any{"kind": "tool", "callId": result.ID}, result.Content)
	content := message["content"].([]any)
	for _, block := range result.ContentBlocks {
		switch block.Type {
		case "text":
			content = append(content, map[string]any{"type": "text", "text": block.Text})
		case "image":
			if block.ImageRefID == "" {
				return errors.New("tool image result lacks attachment")
			}
			attachment := map[string]any{"attachmentId": block.ImageRefID, "mediaType": block.MIMEType,
				"bytes": block.Bytes, "width": block.Width, "height": block.Height}
			if block.Name != "" {
				attachment["name"] = block.Name
			}
			if block.OriginalDimensions != nil {
				attachment["originalDimensions"] = block.OriginalDimensions
			}
			content = append(content, map[string]any{"type": "image", "attachment": attachment})
		default:
			return fmt.Errorf("unsupported native tool content block %q", block.Type)
		}
	}
	message["content"] = content
	message["toolCallId"] = result.ID
	message["isError"] = result.IsError
	data := map[string]any{"turn": turn, "step": step, "message": message}
	if len(result.Meta) != 0 {
		data["meta"] = result.Meta
	}
	if result.IsError && result.Code != "" {
		name := result.ErrorName
		if name == "" {
			name = "Error"
			if result.Code == "TOOL_TIMEOUT" {
				name = "ToolTimeoutError"
			}
		}
		data["error"] = map[string]any{"name": name, "code": result.Code}
		if result.ErrorReason != nil {
			data["error"].(map[string]any)["reason"] = *result.ErrorReason
		}
	}
	_, err := a.append("tool/result", data, json.RawMessage(`"append"`), []uint64{callSeq})
	return err
}

func latestToolQuestion(s *sessionformat.LiveSession) string {
	surface, err := s.ModelSurface()
	if err != nil {
		return ""
	}
	for i := len(surface.Messages) - 1; i >= 0; i-- {
		var message struct {
			Role    string `json:"role"`
			Source  MessageSource
			Content []agentcontent.ContentBlock
		}
		if json.Unmarshal(surface.Messages[i], &message) != nil || message.Role != "user" || message.Source.Kind != "user" {
			continue
		}
		var text []string
		for _, block := range message.Content {
			if block.Type == "text" {
				text = append(text, block.Text)
			}
		}
		return strings.Join(text, "\n")
	}
	return ""
}

func nativeModelMessages(s *sessionformat.LiveSession, resolveFile NativeFilePathResolver) ([]Message, error) {
	return nativeModelMessagesCached(s, resolveFile, nil)
}

type nativeModelCacheEntry struct {
	raw     json.RawMessage
	message Message
}

func (a *NativeLoop) modelMessages() ([]Message, error) {
	if a.modelProjection == nil {
		a.modelProjection = make(map[uint64]nativeModelCacheEntry)
	}
	return nativeModelMessagesCached(a.session, a.filePath, a.modelProjection)
}
func nativeModelMessagesCached(s *sessionformat.LiveSession, resolveFile NativeFilePathResolver, cache map[uint64]nativeModelCacheEntry) ([]Message, error) {
	surface, err := s.ModelSurface()
	if err != nil {
		return nil, err
	}

	messages := make([]Message, 0, len(surface.Messages))
	retained := make(map[uint64]bool, len(surface.MessageSeqs))
	for index, raw := range surface.Messages {
		seq := surface.MessageSeqs[index]
		retained[seq] = true
		if cached, ok := cache[seq]; ok && bytes.Equal(cached.raw, raw) && (cached.message.Role != "tool" || cached.message.ToolName == s.ToolCallName(cached.message.ToolCallID)) {
			messages = append(messages, cloneNativeModelMessage(cached.message))
			continue
		}
		var value struct {
			ProviderReplay *modeldata.ProviderReplay `json:"providerReplay"`
			IsError        bool                      `json:"isError"`
			Role           string                    `json:"role"`
			ToolCallID     string                    `json:"toolCallId"`
			Content        []struct {
				Type       string `json:"type"`
				Text       string `json:"text"`
				ID         string `json:"id"`
				Name       string `json:"name"`
				Arguments  string `json:"arguments"`
				Offloaded  bool   `json:"offloaded"`
				Attachment struct {
					ID                 string                        `json:"attachmentId"`
					MIMEType           string                        `json:"mediaType"`
					Bytes              int64                         `json:"bytes"`
					Width              int                           `json:"width"`
					Height             int                           `json:"height"`
					Name               string                        `json:"name"`
					OriginalDimensions *agentcontent.ImageDimensions `json:"originalDimensions"`
				} `json:"attachment"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		message := Message{Role: value.Role, ToolCallID: value.ToolCallID, IsError: value.IsError, ProviderReplay: value.ProviderReplay}
		var textParts []string
		var orderedBlocks []agentcontent.ContentBlock
		var assistantOrder []modeldata.AssistantOutputBlock
		imageIndex := 0
		for _, block := range value.Content {
			switch block.Type {
			case "text":
				textParts = append(textParts, block.Text)
				orderedBlocks = append(orderedBlocks, agentcontent.ContentBlock{Type: "text", Text: block.Text})
				if value.Role == "assistant" {
					assistantOrder = append(assistantOrder, modeldata.AssistantOutputBlock{Type: "text", Text: block.Text})
				}
			case "reasoning":
				if value.Role != "assistant" {
					return nil, errors.New("native reasoning block requires assistant role")
				}
				assistantOrder = append(assistantOrder, modeldata.AssistantOutputBlock{Type: "reasoning", Text: block.Text})
			case "tool-call":
				message.ToolCalls = append(message.ToolCalls, modeldata.ToolRequest{ID: block.ID, Name: block.Name, Arguments: block.Arguments})
				if value.Role == "assistant" {
					assistantOrder = append(assistantOrder, modeldata.AssistantOutputBlock{Type: "tool-call", ID: block.ID, Name: block.Name, Arguments: block.Arguments})
				}
			case "image":
				if block.Attachment.ID == "" || block.Attachment.MIMEType == "" {
					return nil, errors.New("native image block lacks an attachment")
				}
				orderedBlocks = append(orderedBlocks, agentcontent.ContentBlock{
					Type: "image", ImageRefID: block.Attachment.ID, MIMEType: block.Attachment.MIMEType,
					Bytes: block.Attachment.Bytes, Width: block.Attachment.Width, Height: block.Attachment.Height,
					Name: block.Attachment.Name, OriginalDimensions: block.Attachment.OriginalDimensions,
				})
				if block.Offloaded {
					if message.OffloadedImageIndexes == nil {
						message.OffloadedImageIndexes = make(map[int]struct{})
					}
					message.OffloadedImageIndexes[imageIndex] = struct{}{}
				}
				imageIndex++
			case "file":
				if value.Role != "user" {
					return nil, errors.New("native file block requires user role")
				}
				ref := NativeFileAttachmentRef{ID: block.Attachment.ID, Name: block.Attachment.Name, Bytes: block.Attachment.Bytes}
				if ref.ID == "" || ref.Name == "" || ref.Bytes < 0 {
					return nil, errors.New("native file block lacks a valid attachment")
				}
				path := ""
				if resolveFile != nil {
					path = resolveFile(ref)
				}
				handle := nativeFileHandleText(ref, path)
				textParts = append(textParts, handle)
				orderedBlocks = append(orderedBlocks, agentcontent.ContentBlock{Type: "text", Text: handle})
			default:
				return nil, fmt.Errorf("native model projection does not yet support %q content", block.Type)
			}
		}
		if imageIndex == 0 {
			message.Content = strings.Join(textParts, "\n")
		}
		message.ContentBlocks = orderedBlocks
		message.ContentBlocksComplete = true
		message.OrderedAssistantBlocks = assistantOrder
		if value.Role == "tool" {
			message.ToolName = s.ToolCallName(value.ToolCallID)
			if message.ToolName == "" {
				return nil, fmt.Errorf("tool result %q has no prior call", value.ToolCallID)
			}
		}
		if cache != nil {
			cache[seq] = nativeModelCacheEntry{append(json.RawMessage(nil), raw...), cloneNativeModelMessage(message)}
		}
		messages = append(messages, message)
	}
	for seq := range cache {
		if !retained[seq] {
			delete(cache, seq)
		}
	}
	return messages, nil
}
func cloneNativeModelMessage(message Message) Message {
	message.ContentBlocks = agentcontent.CloneContentBlocks(message.ContentBlocks)
	message.OrderedAssistantBlocks = slices.Clone(message.OrderedAssistantBlocks)
	message.ToolCalls = slices.Clone(message.ToolCalls)
	if message.Source != nil {
		source := *message.Source
		message.Source = &source
	}
	if message.ProviderReplay != nil {
		replay := *message.ProviderReplay
		replay.Content = make([]json.RawMessage, len(message.ProviderReplay.Content))
		for i, raw := range message.ProviderReplay.Content {
			replay.Content[i] = slices.Clone(raw)
		}
		message.ProviderReplay = &replay
	}
	message.OffloadedImageIndexes = maps.Clone(message.OffloadedImageIndexes)
	return message
}
