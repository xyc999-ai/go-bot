package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"slices"
	"strconv"

	"go-bot/harness/interaction"
	"go-bot/harness/permission"
	"go-bot/harness/session"
	"go-bot/internal/hostruntime"
)

type historyWindowRuntime interface {
	HistoryWindow(context.Context, sessionformat.HistoryRequest) (sessionformat.HistoryWindow, error)
}

func historyQuestions(value sessionformat.HistoryWindow, continuedOnly bool) ([]interaction.Pending, error) {
	var questions []interaction.Pending
	if raw := value.Views["userQuestions"]; len(raw) != 0 {
		if err := json.Unmarshal(raw, &questions); err != nil {
			return nil, err
		}
	}
	if continuedOnly {
		questions = slices.DeleteFunc(questions, func(q interaction.Pending) bool { return q.State != "continued" })
	}
	return questions, nil
}
func historyPermission(value sessionformat.HistoryWindow) permission.State {
	return permission.State{Mode: permission.Mode(value.Permission.Mode), Approval: value.Permission.Approval}
}
func windowTranscript(value sessionformat.HistoryWindow, before *uint64) (chatTranscriptPage, error) {
	page := chatTranscriptPage{Events: []sessionformat.Event{}, Queue: []chatQueueItem{}, Before: value.Before}
	for _, event := range value.Events {
		if projected, ok := publicChatEvent(event); ok {
			page.Events = append(page.Events, projected)
		}
	}
	if before == nil {
		if value.Count > 0 {
			last := value.Count - 1
			page.ThroughSeq = &last
		}
		page.Queue = chatQueue(value.Inbox)
		questions, err := historyQuestions(value, true)
		if err != nil {
			return page, err
		}
		page.Questions = questions
		state := historyPermission(value)
		page.Permission = &state
	}
	return page, nil
}
func transcriptRequest(r *http.Request) (sessionformat.HistoryRequest, error) {
	request := sessionformat.HistoryRequest{Kind: sessionformat.HistoryTurns, Limit: 20}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			return request, errors.New("invalid_turn_limit")
		}
		request.Limit = limit
	}
	if raw := r.URL.Query().Get("before"); raw != "" {
		cursor, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return request, sessionformat.ErrHistoryCursor
		}
		request.Before = &cursor
	}
	return request, nil
}
func serveWindowTranscript(w http.ResponseWriter, r *http.Request, host *hostruntime.NativeHost, id string, runtime *hostruntime.NativeRuntime) {
	request, err := transcriptRequest(r)
	if err != nil {
		code := "invalid_turn_limit"
		if errors.Is(err, sessionformat.ErrHistoryCursor) {
			code = "invalid_turn_cursor"
		}
		writeSessionJSON(w, http.StatusBadRequest, map[string]string{"error": code})
		return
	}
	value, err := host.SessionHistoryWindow(r.Context(), id, request)
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, sessionformat.ErrHistoryCursor) {
		writeSessionJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_turn_cursor"})
		return
	}
	if err != nil {
		writeSessionJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "transcript_unavailable"})
		return
	}
	page, err := windowTranscript(value, request.Before)
	if err != nil {
		writeSessionJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "transcript_unavailable"})
		return
	}
	if request.Before == nil && runtime != nil {
		page.Questions, err = runtime.PendingQuestions()
		if err != nil {
			writeSessionJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent_questions_unavailable"})
			return
		}
		baseline, _, unsubscribe, snapshotErr := runtime.SubscribeAssistantStreamSnapshot()
		if snapshotErr != nil {
			writeSessionJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "assistant_stream_unavailable"})
			return
		}
		unsubscribe()
		baseline = publicNativeAssistantBaseline(baseline, true)
		page.Baseline = &baseline
	}
	writeSessionJSON(w, http.StatusOK, page)
}

// replayHistoryWindow streams bounded raw pages through an exact opening cut.
// Later appends are left for the live follower rather than folded into an old
// queue snapshot. An old or ahead cold cursor has the existing empty replay.
func replayHistoryWindow(ctx context.Context, host *hostruntime.NativeHost, id string, opening sessionformat.HistoryWindow, after *uint64, emit func(sessionformat.Event) error) error {
	cursor := after
	for opening.Count > 0 && (cursor == nil || *cursor < opening.Count-1) {
		page, err := host.SessionHistoryWindow(ctx, id, sessionformat.HistoryRequest{Kind: sessionformat.HistoryEvents, After: cursor, Limit: 500})
		if err != nil {
			return err
		}
		if page.Header.ID != opening.Header.ID || page.Header.CreatedAt != opening.Header.CreatedAt || page.Header.CWD != opening.Header.CWD || page.InheritedEventCount != opening.InheritedEventCount || page.Count < opening.Count || len(page.Events) == 0 {
			return sessionformat.ErrHistoryChanged
		}
		expected := uint64(0)
		if cursor != nil {
			expected = *cursor + 1
		}
		for _, event := range page.Events {
			if event.Seq >= opening.Count {
				break
			}
			if event.Seq != expected {
				return sessionformat.ErrHistoryChanged
			}
			if err := emit(event); err != nil {
				return err
			}
			seq := event.Seq
			cursor = &seq
			expected++
		}
	}
	return nil
}
func waitForIndexedChatRuntime(w http.ResponseWriter, r *http.Request, host *hostruntime.NativeHost, id string) (*hostruntime.NativeRuntime, *http.Request) {
	if runtime, ok := host.Session(id); ok {
		return runtime, r
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "event stream unavailable", http.StatusServiceUnavailable)
		return nil, r
	}
	var after *uint64
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("after")
	}
	if raw != "" {
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			writeSessionJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_event_cursor"})
			return nil, r
		}
		after = &v
	}
	opening, err := host.SessionHistoryWindow(r.Context(), id, sessionformat.HistoryRequest{Kind: sessionformat.HistoryState})
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)
		return nil, r
	}
	if err != nil {
		http.Error(w, "chat state unavailable", http.StatusServiceUnavailable)
		return nil, r
	}
	questions, err := historyQuestions(opening, true)
	if err != nil {
		http.Error(w, "chat state unavailable", http.StatusServiceUnavailable)
		return nil, r
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	if err := replayHistoryWindow(r.Context(), host, id, opening, after, func(event sessionformat.Event) error {
		if projected, ok := publicChatEvent(event); ok && !writeChatSSE(w, "session", projected) {
			return errors.New("event stream unavailable")
		}
		return nil
	}); err != nil {
		return nil, r
	}
	if !writeChatSSE(w, "chat.idle", struct{}{}) || !writeChatSSE(w, "queue.state", map[string]any{"queue": chatQueue(opening.Inbox)}) || !writeChatSSE(w, "questions.state", map[string]any{"questions": questions}) {
		return nil, r
	}
	flusher.Flush()
	if opening.Count > 0 {
		r = r.Clone(r.Context())
		r.Header.Set("Last-Event-ID", strconv.FormatUint(opening.Count-1, 10))
	}
	return awaitChatRuntime(w, r, host, id, flusher)
}
