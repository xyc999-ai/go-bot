package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"go-bot/harness/content"
	"go-bot/harness/session"
	"go-bot/internal/modeldata"
)

// The v4 checkpoint is a surface replacement, not a private message overlay.
// Keep the current conversation prefix and append the instruction as the last
// user message, as DeepSeek's compaction-basic summarizer does.
const nativeCompactionInstruction = `You are now acting as a compaction engine for this AI coding assistant. Condense the conversation ABOVE into a structured checkpoint that lets another model resume the work with no loss of essential context.

Output EXACTLY the Markdown structure below: keep every section, in order. Use terse bullets, not prose paragraphs. Write "(none)" for an empty section — never drop a section.

## Primary Request and Intent
- [the user's original and evolving goals; quote verbatim where the exact wording matters]

## Key Technical Concepts
- [technologies, frameworks, patterns, and conventions in play]

## Files and Code
- [exact path: why it matters, key changes or snippets]

## Errors and Fixes
- [error: how it was resolved, plus any related user feedback]

## Pending Jobs
- [explicitly requested work not yet completed]

## Current Work
- [precisely what was in progress at this checkpoint]

## Next Step
- [the single next action, directly in line with the most recent request, or "(none)"]

## Critical Context
- [decisions and their rationale, constraints, user preferences, open questions, data needed to continue]

Rules:
- Write concise English engineering prose. Preserve exact file paths, commands, error strings, identifiers, numeric values, function signatures, and syntax fragments.
- Capture user feedback and explicit instructions faithfully, especially corrections.
- Do NOT mention this summarization request or that the context was compacted.
- Output only the checkpoint text: do not call any tool or take any other action.
- If the conversation already contains a <compacted-summary> block, it is a PRIOR checkpoint. Do not copy it forward verbatim: preserve still-true facts, drop stale ones, and merge newer information into a single consolidated summary under the same structure.`

const nativeCheckpointPreamble = "This is an automatically generated checkpoint condensing an earlier span of the conversation to free up context. Treat the captured context as established background and build on it without restating it. Continue the task directly from the messages that follow, without acknowledging this checkpoint."

type nativeCompactRange struct {
	start, end  uint64
	seqs        []uint64
	tokens      int // fixed-heuristic shadow price written to v4
	routeTokens int // model-request price used for the shrink decision
}

// A summary failure leaves the original surface intact after its durable
// compaction/end marker has been committed. The proactive pressure path may
// continue with that surface; a failed journal write is never downgraded.
type nativeSummaryFailure struct{ cause error }

func (e *nativeSummaryFailure) Error() string { return e.cause.Error() }
func (e *nativeSummaryFailure) Unwrap() error { return e.cause }

// selectNativeCompactRange follows DeepSeek's overflow retention of zero:
// preserve at least the newest surface node, protect the system head, and
// back the cut up until no assistant tool call is separated from its result.
func selectNativeCompactRange(surface sessionformat.Surface, events []sessionformat.Event, retainTokens int, resolveFile NativeFilePathResolver) (*nativeCompactRange, error) {
	if retainTokens < 0 || len(surface.Messages) != len(surface.MessageSeqs) {
		return nil, errors.New("invalid native compaction surface or retention")
	}
	nodes := surface.Nodes
	if len(nodes) == 0 {
		return nil, nil
	}
	prices, err := nativeSurfacePrices(surface, resolveFile)
	if err != nil {
		return nil, err
	}
	balanced := make([]bool, len(nodes)+1)
	balanced[0] = true
	openCalls := 0
	for index, seq := range nodes {
		if surface.NodeTypes == nil && seq >= uint64(len(events)) {
			return nil, errors.New("native compaction surface names a missing event")
		}
		kind := surface.NodeTypes[seq]
		if surface.NodeTypes == nil {
			kind = events[seq].Type
		}
		switch kind {
		case "assistant/message":
			var data struct {
				Message struct {
					Content []struct {
						Type string `json:"type"`
					} `json:"content"`
				} `json:"message"`
			}
			raw := surface.NodeMessages[seq]
			if surface.NodeMessages == nil {
				var wrapped struct{ Message json.RawMessage }
				if err := json.Unmarshal(events[seq].Data, &wrapped); err != nil {
					return nil, err
				}
				raw = wrapped.Message
			}
			if err := json.Unmarshal(raw, &data.Message); err != nil {
				return nil, err
			}
			for _, block := range data.Message.Content {
				if block.Type == "tool-call" {
					openCalls++
				}
			}
		case "tool/result":
			openCalls--
			if openCalls < 0 {
				return nil, errors.New("native compaction surface has an unpaired tool result")
			}
		}
		balanced[index+1] = openCalls == 0
	}
	first := 0
	headType := surface.NodeTypes[nodes[0]]
	if surface.NodeTypes == nil {
		headType = events[nodes[0]].Type
	}
	if headType == "system/message" {
		first = 1
	}
	accumulated, keepFrom := 0, len(nodes)
	for index := len(nodes) - 1; index >= 0; index-- {
		accumulated += prices[index].request
		keepFrom = index
		if accumulated >= retainTokens {
			break
		}
	}
	if keepFrom <= first {
		return nil, nil
	}
	for keepFrom > first && !balanced[keepFrom] {
		keepFrom--
	}
	if keepFrom <= first || !balanced[first] {
		return nil, nil
	}
	selected := &nativeCompactRange{start: nodes[first], end: nodes[keepFrom-1], seqs: append([]uint64(nil), nodes[first:keepFrom]...)}
	for index := first; index < keepFrom; index++ {
		selected.tokens += prices[index].heuristic
		selected.routeTokens += prices[index].request
	}
	return selected, nil
}

// compactNativeOverflow performs one provider-confirmed overflow transaction.
func (a *NativeLoop) compactNativeOverflow(ctx context.Context, turn uint64) (bool, error) {
	compacted, err := a.compactNative(ctx, turn, 0)
	if err != nil || !compacted {
		return compacted, err
	}
	// The failed request already contained current project instructions, but
	// its compaction checkpoint can shadow them. Re-add current guidance before
	// the retry builds its new model request from the replacement surface.
	if err := a.ensureInstructions(ctx); err != nil {
		return true, err
	}
	return true, a.session.Flush(ctx)
}

// CompactNow is a human-initiated, between-turn maintenance command. It is
// serialized with the turn driver but not admitted as a user message. The
// worker must keep queued prompts from being driven until this call returns.
func (a *NativeLoop) CompactNow(ctx context.Context) (bool, error) {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	a.mu.Lock()
	inbox := a.session.Inbox()
	if len(inbox.NextTurn) != 0 || len(inbox.NextStep) != 0 {
		a.mu.Unlock()
		return false, errors.New("native Agent has pending input")
	}
	var err error
	ctx, err = a.pinTurnModel(ctx)
	if err != nil {
		a.mu.Unlock()
		return false, err
	}
	commandID := uuid.NewString()
	_, err = a.append("command/run", map[string]any{"commandId": commandID, "name": "compact", "args": ""}, nil, nil)
	if err == nil {
		err = a.session.Flush(ctx)
	}
	a.mu.Unlock()
	if err != nil {
		return false, err
	}
	compacted, compactErr := a.compactNativeWithCommand(ctx, 0, 0, commandID)
	a.mu.Lock()
	defer a.mu.Unlock()
	kind := "success"
	if compactErr != nil {
		kind = "error"
	}
	done := map[string]any{"commandId": commandID, "kind": kind}
	if compactErr != nil {
		done["text"] = compactErr.Error()
	}
	_, doneErr := a.append("command/done", done, nil, nil)
	if doneErr == nil {
		doneErr = a.session.Flush(context.Background())
	}
	return compacted, errors.Join(compactErr, doneErr)
}

// compactNative performs one v4 compaction transaction. It leaves
// admission open while the auxiliary model runs, but checks that the selected
// surface span is still exactly the one being replaced before committing.
func (a *NativeLoop) compactNative(ctx context.Context, turn uint64, retainTokens int) (bool, error) {
	return a.compactNativeWithCommand(ctx, turn, retainTokens, "")
}

func (a *NativeLoop) compactNativeWithCommand(ctx context.Context, turn uint64, retainTokens int, commandID string) (bool, error) {
	var owner any = turn
	if turn == 0 {
		owner = nil
	}
	marker := func(id string) map[string]any {
		data := map[string]any{"compactionId": id, "turn": owner}
		if commandID != "" {
			data["sourceCommandId"] = commandID
		}
		return data
	}
	a.mu.Lock()
	surface, err := a.session.ModelSurface()
	if err != nil {
		a.mu.Unlock()
		return false, err
	}
	selection, err := selectNativeCompactRange(surface, nil, retainTokens, a.filePath)
	if err != nil || selection == nil {
		a.mu.Unlock()
		return false, err
	}
	messages, err := a.nativeSummaryMessages(surface, selection)
	if err != nil {
		a.mu.Unlock()
		return false, err
	}
	compactionID := uuid.NewString()
	start, err := a.append("compaction/start", marker(compactionID), nil, nil)
	if err == nil {
		err = a.session.Flush(ctx)
		if err != nil {
			end := marker(compactionID)
			end["error"] = map[string]any{"name": "Error", "message": err.Error()}
			_, closeErr := a.append("compaction/end", end, nil, nil)
			err = errors.Join(err, closeErr, a.session.Flush(context.Background()))
		}
	}
	a.mu.Unlock()
	if err != nil {
		return false, err
	}
	request := append(append([]Message(nil), messages...), Message{Role: "user", Content: nativeCompactionInstruction})
	summaryCap := a.contextHeadroomTokens
	summaryCtx := WithModelCallOptions(ctx, ModelCallOptions{Purpose: "compaction", MaxOutputTokens: summaryCap})
	var response modeldata.AssistantMessage
	var summarizeErr error
	var fatalErr error
	for {
		response, summarizeErr = a.completeNativeSummary(summaryCtx, request)
		var offload *ImageOffloadRequiredError
		if !errors.As(summarizeErr, &offload) || offload == nil || offload.Count <= 0 || ctx.Err() != nil || response.Content != "" || len(response.ToolCalls) != 0 || response.Usage != nil {
			break
		}
		a.mu.Lock()
		changed, offloadErr := a.offloadNativeImagesFromSources(offload.Count, selection.seqs)
		if offloadErr == nil && changed {
			offloadErr = a.session.Flush(ctx)
		}
		a.mu.Unlock()
		if offloadErr != nil {
			fatalErr = offloadErr
			summarizeErr = errors.Join(summarizeErr, offloadErr)
			break
		}
		if !changed {
			break
		}
		current, surfaceErr := a.session.ModelSurface()
		if surfaceErr != nil || !nativeCompactSpanStable(current.Nodes, selection) {
			fatalErr = errors.Join(surfaceErr, errors.New("native summary region changed after image offload"))
			summarizeErr = errors.Join(summarizeErr, surfaceErr, errors.New("native summary region changed after image offload"))
			break
		}
		messages, surfaceErr = a.nativeSummaryMessages(current, selection)
		if surfaceErr != nil {
			fatalErr = surfaceErr
			summarizeErr = errors.Join(summarizeErr, surfaceErr)
			break
		}
		request = append(append([]Message(nil), messages...), Message{Role: "user", Content: nativeCompactionInstruction})
	}
	summary := strings.TrimSpace(response.Content)
	if summarizeErr == nil && response.MaxTokens {
		summarizeErr = errors.New("native compaction summary truncated at the token cap")
	} else if summarizeErr == nil && (summary == "" || len(response.ToolCalls) > 0 || len(summary) > maxSummaryBytes) {
		summarizeErr = errors.New("native compaction produced no safe bounded text summary")
	}
	checkpoint := map[string]any{
		"id": uuid.NewString(), "role": "user",
		"source": map[string]any{"kind": "compact-checkpoint", "compactionId": compactionID},
		"content": []any{
			map[string]any{"type": "text", "text": nativeCheckpointPreamble + "\n\n<compacted-summary>"},
			map[string]any{"type": "text", "text": summary},
			map[string]any{"type": "text", "text": "</compacted-summary>"},
		},
	}
	if commandID != "" {
		checkpoint["source"].(map[string]any)["sourceCommandId"] = commandID
	}
	checkpointRaw, marshalErr := json.Marshal(checkpoint)
	if summarizeErr == nil && marshalErr != nil {
		summarizeErr = marshalErr
	}
	if summarizeErr == nil {
		price, priceErr := estimateNativeMessage(checkpointRaw)
		if priceErr != nil {
			summarizeErr = priceErr
		} else if price >= selection.routeTokens {
			summarizeErr = fmt.Errorf("native compaction checkpoint %d tokens does not shrink %d-token span", price, selection.routeTokens)
		}
	}
	var summaryUsage map[string]any
	if summarizeErr == nil && response.Usage != nil {
		summaryUsage, summarizeErr = nativeUsage(response.Usage)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	closeFailed := func(cause error) (bool, error) {
		end := marker(compactionID)
		end["error"] = map[string]any{"name": "Error", "message": cause.Error()}
		_, endErr := a.append("compaction/end", end, nil, nil)
		flushErr := a.session.Flush(context.Background())
		return false, errors.Join(cause, endErr, flushErr)
	}
	if summarizeErr == nil {
		current, surfaceErr := a.session.ModelSurface()
		if surfaceErr != nil {
			fatalErr = surfaceErr
			summarizeErr = surfaceErr
		} else if !nativeCompactSpanStable(current.Nodes, selection) {
			fatalErr = errors.New("native compaction surface changed during summarization")
			summarizeErr = errors.New("native compaction surface changed during summarization")
		}
	}
	if summarizeErr != nil {
		end := marker(compactionID)
		end["error"] = map[string]any{"name": "Error", "message": summarizeErr.Error()}
		_, endErr := a.append("compaction/end", end, nil, nil)
		flushErr := a.session.Flush(context.Background())
		if fatalErr != nil || endErr != nil || flushErr != nil {
			return false, errors.Join(summarizeErr, endErr, flushErr)
		}
		return false, &nativeSummaryFailure{cause: summarizeErr}
	}
	summaryData := map[string]any{
		"compactionId":  compactionID,
		"summary":       []any{map[string]any{"type": "text", "text": summary}},
		"shadowedRange": map[string]uint64{"start": selection.start, "end": selection.end},
		"shadowedSeqs":  selection.seqs, "shadowedTokenCount": selection.tokens,
		"provider": a.provider, "model": a.modelID,
	}
	if len(response.RawOutput) > 0 {
		summaryData["rawOutput"] = response.RawOutput
	} else {
		// Non-transport model implementations can only provide a flattened
		// text response. It is an unmarked summarizer result in v4 terms.
		summaryData["rawOutput"] = []content.ContentBlock{{Type: "text", Text: response.Content}}
	}
	if commandID != "" {
		summaryData["sourceCommandId"] = commandID
	}
	if summaryCap > 0 {
		summaryData["maxTokens"] = summaryCap
	}
	if summaryUsage != nil {
		summaryData["usage"] = summaryUsage
	}
	summaryEvent, err := a.append("compaction/summary", summaryData, nil, nil)
	if err != nil {
		return closeFailed(err)
	}
	op, err := json.Marshal(map[string]any{"op": "replace", "startSeq": selection.start, "endSeq": selection.end})
	if err != nil {
		return closeFailed(err)
	}
	sources := make([]uint64, 0, len(selection.seqs)+2)
	sources = append(sources, start.Seq, summaryEvent.Seq)
	sources = append(sources, selection.seqs...)
	if _, err := a.append("user/message", checkpoint, op, sources); err != nil {
		return closeFailed(err)
	}
	if _, err := a.append("compaction/end", marker(compactionID), nil, nil); err != nil {
		return closeFailed(err)
	}
	return true, a.session.Flush(ctx)
}

func (a *NativeLoop) completeNativeSummary(ctx context.Context, messages []Message) (response modeldata.AssistantMessage, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			response = modeldata.AssistantMessage{}
			err = fmt.Errorf("native compaction model panicked: %v", recovered)
		}
	}()
	return a.completeModel(ctx, messages)
}

func nativeCompactSpanStable(nodes []uint64, selected *nativeCompactRange) bool {
	for start := 0; start+len(selected.seqs) <= len(nodes); start++ {
		if nodes[start] != selected.start {
			continue
		}
		for index, seq := range selected.seqs {
			if nodes[start+index] != seq {
				return false
			}
		}
		return true
	}
	return false
}

func (a *NativeLoop) nativeSummaryMessages(surface sessionformat.Surface, selected *nativeCompactRange) ([]Message, error) {
	all, err := a.modelMessages()
	if err != nil {
		return nil, err
	}
	if len(all) != len(surface.MessageSeqs) {
		return nil, errors.New("native summary model projection lost source positions")
	}
	selectedSeqs := make(map[uint64]bool, len(selected.seqs)+1)
	for _, seq := range selected.seqs {
		selectedSeqs[seq] = true
	}
	if len(surface.Nodes) > 0 {
		if surface.NodeTypes[surface.Nodes[0]] == "system/message" {
			selectedSeqs[surface.Nodes[0]] = true
		}
	}
	input := make([]Message, 0, len(selectedSeqs))
	for index, seq := range surface.MessageSeqs {
		if selectedSeqs[seq] {
			input = append(input, all[index])
		}
	}
	return input, nil
}
