package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go-bot/harness/permission"
	"go-bot/harness/session"
	"go-bot/internal/hostruntime"
)

// Host events multiplex the existing public chat projection. There is no
// second journal or global sequence: recovery uses each cached Session cursor.
type hostStreamEvent struct {
	SessionID string `json:"sessionId,omitempty"`
	Type      string `json:"type"`
	Data      any    `json:"data"`
}

func streamHostEvents(w http.ResponseWriter, r *http.Request, host *hostruntime.NativeHost) {
	cursors := map[string]*uint64{}
	if raw := r.URL.Query().Get("cursors"); raw != "" {
		if len(raw) > 8192 || json.Unmarshal([]byte(raw), &cursors) != nil || len(cursors) > 20 {
			http.Error(w, "invalid Session cursors", http.StatusBadRequest)
			return
		}
		for id := range cursors {
			if uuid.Validate(id) != nil {
				http.Error(w, "invalid Session cursor ID", http.StatusBadRequest)
				return
			}
		}
	}
	if _, closed := host.StreamSessions(); closed {
		http.Error(w, "Host closed", http.StatusServiceUnavailable)
		return
	}
	if _, ok := w.(http.Flusher); !ok {
		http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer host.AttachHostStream()()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	send := func(event hostStreamEvent) error {
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err = fmt.Fprintf(w, "event: host\ndata: %s\n\n", data); err != nil {
			return err
		}
		return controller.Flush()
	}
	if send(hostStreamEvent{Type: "sessions.changed", Data: struct{}{}}) != nil {
		return
	}
	events := make(chan hostStreamEvent, 64)
	failures := make(chan error, 1)
	// Producers only write to a bounded local channel. One goroutine owns the
	// HTTP writer; cancellation releases producers even when a client is slow.
	emit := func(event hostStreamEvent) error {
		select {
		case events <- event:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	type subscription struct {
		runtime *hostruntime.NativeRuntime
		stop    context.CancelFunc
	}
	watching := map[string]subscription{}
	defer func() {
		for _, sub := range watching {
			sub.stop()
		}
	}()
	discover := func() (bool, bool) {
		live, closed := host.StreamSessions()
		changed := false
		for id, runtime := range live {
			if previous, ok := watching[id]; ok {
				if previous.runtime == runtime {
					continue
				}
				previous.stop()
			}
			changed = true
			childCtx, stop := context.WithCancel(ctx)
			watching[id] = subscription{runtime, stop}
			after, replay := cursors[id]
			go func(id string, worker *hostruntime.NativeRuntime, after *uint64, replay bool) {
				err := followHostSession(childCtx, worker, after, replay, func(kind string, data any) error {
					return emit(hostStreamEvent{SessionID: id, Type: kind, Data: data})
				})
				if err != nil && childCtx.Err() == nil {
					select {
					case failures <- err:
					default:
					}
				}
			}(id, runtime, after, replay)
		}
		for id, sub := range watching {
			if _, ok := live[id]; !ok {
				sub.stop()
				delete(watching, id)
				changed = true
			}
		}
		return changed, closed
	}
	if _, closed := discover(); closed {
		return
	}
	// A cached cold Session may have acquired a final result while disconnected.
	// Read only the requested cached journals; do not activate or load all history.
	for id, after := range cursors {
		if _, ok := watching[id]; ok {
			continue
		}

		if host.ReadHistory != nil {
			opening, err := host.SessionHistoryWindow(ctx, id, sessionformat.HistoryRequest{Kind: sessionformat.HistoryState})
			if err != nil || host.CheckScheduleTarget(ctx, id) != nil {
				if send(hostStreamEvent{SessionID: id, Type: "session.unavailable", Data: struct{}{}}) != nil {
					return
				}
				continue
			}
			if err := replayHistoryWindow(ctx, host, id, opening, after, func(event sessionformat.Event) error {
				if projected, ok := publicChatEvent(event); ok {
					return send(hostStreamEvent{SessionID: id, Type: "session", Data: projected})
				}
				return nil
			}); err != nil {
				return
			}
			questions, err := historyQuestions(opening, true)
			if err != nil {
				return
			}
			var through *uint64
			if opening.Count > 0 {
				seq := opening.Count - 1
				through = &seq
			}
			for _, event := range []hostStreamEvent{
				{SessionID: id, Type: "questions.state", Data: map[string]any{"questions": questions, "throughSeq": through}},
				{SessionID: id, Type: "session.state", Data: map[string]any{"activeTurn": nil, "throughSeq": through}},
				{SessionID: id, Type: "permissions.state", Data: historyPermission(opening)},
				{SessionID: id, Type: "queue.state", Data: map[string]any{"queue": chatQueue(opening.Inbox), "throughSeq": through}},
			} {
				if send(event) != nil {
					return
				}
			}
			continue
		}
		if host.ReadSession == nil {
			continue
		}
		artifact, err := host.SessionArtifact(ctx, id)
		if err != nil || host.CheckScheduleTarget(ctx, id) != nil {
			if send(hostStreamEvent{SessionID: id, Type: "session.unavailable", Data: struct{}{}}) != nil {
				return
			}
			continue
		}
		for _, event := range artifact.Events {
			if after != nil && event.Seq <= *after {
				continue
			}
			if projected, ok := publicChatEvent(event); ok {
				if send(hostStreamEvent{SessionID: id, Type: "session", Data: projected}) != nil {
					return
				}
			}
		}
		if send(hostStreamEvent{SessionID: id, Type: "questions.state", Data: map[string]any{"questions": coldChatQuestions(artifact.Events)}}) != nil {
			return
		}
		inbox, err := sessionformat.FoldInbox(artifact.Events)
		if err != nil {
			return
		}
		state := permission.Read(artifact.Events)
		for _, event := range []hostStreamEvent{
			{SessionID: id, Type: "session.state", Data: map[string]any{"activeTurn": nil}},
			{SessionID: id, Type: "permissions.state", Data: state},
			{SessionID: id, Type: "queue.state", Data: map[string]any{"queue": chatQueue(inbox)}},
		} {
			if send(event) != nil {
				return
			}
		}
	}
	var scheduleChanged, scheduleDone <-chan struct{}
	var pluginsChanged, pluginsDone <-chan struct{}
	if host.PluginsChanges != nil {
		pluginsChanged, pluginsDone = host.PluginsChanges()
		if send(hostStreamEvent{Type: "extensions.changed", Data: struct{}{}}) != nil {
			return
		}
	}
	var mcpChanged, mcpDone <-chan struct{}
	if host.MCPChanges != nil {
		mcpChanged, mcpDone = host.MCPChanges()
		if host.PluginsChanges == nil && send(hostStreamEvent{Type: "extensions.changed", Data: struct{}{}}) != nil {
			return
		}
	}
	if host.Schedule != nil {
		scheduleChanged, scheduleDone = host.Schedule.Changes()
	}
	if send(hostStreamEvent{Type: "schedule/changed", Data: struct{}{}}) != nil {
		return
	}
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-pluginsDone:
			return
		case <-mcpDone:
			return
		case <-mcpChanged:
			mcpChanged, mcpDone = host.MCPChanges()
			if send(hostStreamEvent{Type: "extensions.changed", Data: struct{}{}}) != nil {
				return
			}
		case <-pluginsChanged:
			pluginsChanged, pluginsDone = host.PluginsChanges()
			if send(hostStreamEvent{Type: "extensions.changed", Data: struct{}{}}) != nil {
				return
			}
		case <-failures:
			return // Reconnect resumes from client-owned cursors.
		case <-scheduleDone:
			return
		case <-scheduleChanged:
			scheduleChanged, scheduleDone = host.Schedule.Changes()
			if send(hostStreamEvent{Type: "schedule/changed", Data: struct{}{}}) != nil {
				return
			}
		case event := <-events:
			if send(event) != nil {
				return
			}
		case <-tick.C:
			changed, closed := discover()
			if closed {
				return
			}
			if changed && send(hostStreamEvent{Type: "sessions.changed", Data: struct{}{}}) != nil {
				return
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil || controller.Flush() != nil {
				return
			}
		}
	}
}

func followHostSession(ctx context.Context, worker NativeWorkerHTTPRuntime, after *uint64, replay bool, send func(string, any) error) error {
	baseline, frames, unsubscribe, err := worker.SubscribeAssistantStreamSnapshot()
	if err != nil {
		return err
	}
	defer unsubscribe()

	var inbox sessionformat.InboxState
	var activeTurn *uint64
	if reader, ok := worker.(historyWindowRuntime); ok {
		request := sessionformat.HistoryRequest{Kind: sessionformat.HistoryState}
		if !replay {
			request.Kind = sessionformat.HistoryTurns
			request.Limit = 20
		}
		opening, readErr := reader.HistoryWindow(ctx, request)
		if readErr != nil {
			return readErr
		}
		if after != nil && (*after >= opening.Count) {
			return errors.New("Session cursor is ahead of journal")
		}
		inbox = opening.Inbox
		if opening.Boundary.Open {
			turn := opening.Boundary.Turn
			activeTurn = &turn
		}
		if err := send("assistant-stream-baseline", map[string]any{"baseline": publicNativeAssistantBaseline(baseline, true)}); err != nil {
			return err
		}
		if replay {
			cursor := after
			for opening.Count > 0 && (cursor == nil || *cursor < opening.Count-1) {
				page, err := worker.DurableEventsAfter(cursor, 200)
				if err != nil {
					return err
				}
				if len(page) == 0 || page[0].Seq >= opening.Count {
					return errors.New("Session history ended before opening cut")
				}
				for _, event := range page {
					if event.Seq >= opening.Count {
						break
					}
					if projected, ok := publicChatEvent(event); ok {
						if err := send("session", projected); err != nil {
							return err
						}
					}
					seq := event.Seq
					cursor = &seq
				}
			}
		} else {
			for _, event := range opening.Events {
				if projected, ok := publicChatEvent(event); ok {
					if err := send("session", projected); err != nil {
						return err
					}
				}
			}
		}
		after = nil
		if opening.Count > 0 {
			seq := opening.Count - 1
			after = &seq
		}
	} else {
		events, err := worker.DurableSessionEvents()
		if err != nil {
			return err
		}
		inbox, err = sessionformat.FoldInbox(events)
		if err != nil {
			return err
		}
		if after != nil && (len(events) == 0 || *after >= uint64(len(events))) {
			return errors.New("Session cursor is ahead of journal")
		}
		// A Session can produce events between its creation/history response and
		// discovery by this Host stream. With no supplied cursor, overlap the same
		// bounded recent history page as the client; sequence deduplication makes
		// this safe. Never silently skip the first message or tool call.
		var recentStart uint64
		if !replay && len(events) > 0 {
			page, err := makeChatTranscript(events, nil, 20)
			if err != nil {
				return err
			}
			if len(page.Events) > 0 {
				recentStart = page.Events[0].Seq
			}
		}
		if err := send("assistant-stream-baseline", map[string]any{"baseline": publicNativeAssistantBaseline(baseline, true)}); err != nil {
			return err
		}
		for _, event := range events {
			if event.Type == "turn/start" {
				var row struct {
					Turn uint64 `json:"turn"`
				}
				_ = json.Unmarshal(event.Data, &row)
				activeTurn = &row.Turn
			} else if event.Type == "turn/end" {
				activeTurn = nil
			}
			if replay && (after == nil || event.Seq > *after) || !replay && event.Seq >= recentStart {
				if projected, ok := publicChatEvent(event); ok {
					if err := send("session", projected); err != nil {
						return err
					}
				}
			}
		}
		if len(events) > 0 {
			seq := events[len(events)-1].Seq
			after = &seq
		}
	}

	if err := send("session.state", map[string]any{"activeTurn": activeTurn, "throughSeq": after}); err != nil {
		return err
	}
	if err := send("queue.state", map[string]any{"queue": chatQueue(inbox), "throughSeq": after}); err != nil {
		return err
	}
	var previousQuestions string
	var previousPermission permission.State
	syncQuestions := func() error {
		if runtime, ok := worker.(permissionRuntime); ok {
			state, err := runtime.GetPermission()
			if err != nil {
				return err
			}
			if state != previousPermission {
				if err := send("permissions.state", map[string]any{"mode": state.Mode, "approval": state.Approval, "throughSeq": after}); err != nil {
					return err
				}
				previousPermission = state
			}
		}
		questions, err := worker.PendingQuestions()
		if err != nil {
			return err
		}
		data := map[string]any{"questions": questions, "throughSeq": after}
		encoded, err := json.Marshal(data)
		if err != nil {
			return err
		}
		if string(encoded) != previousQuestions {
			if err := send("questions.state", data); err != nil {
				return err
			}
			previousQuestions = string(encoded)
		}
		return nil
	}
	if err := syncQuestions(); err != nil {
		return err
	}
	if err := send("session.cursor", map[string]any{"cursor": after}); err != nil {
		return err
	}
	openAttempts := map[string]int{}
	if active := baseline.ActiveAttempt; active != nil {
		openAttempts[active.AttemptID] = active.NextIndex
	}
	poll := func() error {
		moved, queueDirty := false, false
		for {
			page, err := worker.DurableEventsAfter(after, 200)
			if err != nil {
				return err
			}
			for _, event := range page {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				seq := event.Seq
				after = &seq
				moved = true
				if event.Type == "agent/inbox/spliced" {
					inbox, err = inbox.Next(event)
					if err != nil {
						return err
					}
					queueDirty = true
				}
				if projected, ok := publicChatEvent(event); ok {
					if err := send("session", projected); err != nil {
						return err
					}
				}
			}
			if len(page) < 200 {
				break
			}
		}
		if queueDirty {
			if err := send("queue.state", map[string]any{"queue": chatQueue(inbox), "throughSeq": after}); err != nil {
				return err
			}
		}
		if err := syncQuestions(); err != nil {
			return err
		}
		if moved {
			return send("session.cursor", map[string]any{"cursor": after})
		}
		return nil
	}
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-worker.Done():
			return poll()
		case <-tick.C:
			if err := poll(); err != nil {
				return err
			}
		case frame, ok := <-frames:
			if !ok {
				select {
				case <-worker.Done():
					return poll()
				default:
				}
				return errors.New("assistant stream overflow: reconnect from snapshot")
			}
			switch frame.Type {
			case "start":
				openAttempts[frame.AttemptID] = 0
			case "chunk", "end":
				next, seen := openAttempts[frame.AttemptID]
				if !seen || frame.Index == nil || *frame.Index != next {
					delete(openAttempts, frame.AttemptID)
					continue
				}
				if frame.Type == "end" {
					delete(openAttempts, frame.AttemptID)
				} else {
					openAttempts[frame.AttemptID] = next + 1
				}
			default:
				continue
			}
			if projected, ok := publicNativeAssistantFrame(frame); ok {
				projected.Chunk = chatAssistantChunk(projected.Chunk)
				if err := send("assistant-stream", map[string]any{"frame": projected}); err != nil {
					return err
				}
			}
		}
	}
}
