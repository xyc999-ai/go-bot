package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"go-bot/harness/agent"
	"go-bot/harness/attachments"
	"go-bot/harness/content"
	"go-bot/harness/interaction"
	"go-bot/harness/permission"
	"go-bot/harness/session"
	"go-bot/internal/hostruntime"
)

type chatBlock struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Text        string          `json:"text,omitempty"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Attachment  *chatAttachment `json:"attachment,omitempty"`
	File        *chatFile       `json:"file,omitempty"`
	CallID      string          `json:"callId,omitempty"`
}

type chatAttachment struct {
	attachments.FileRef
	MediaType          string                   `json:"mediaType"`
	Width              int                      `json:"width"`
	Height             int                      `json:"height"`
	OriginalDimensions *content.ImageDimensions `json:"originalDimensions,omitempty"`
}

type chatFile struct {
	Source       string `json:"source"`
	AttachmentID string `json:"attachmentId,omitempty"`
	Path         string `json:"path,omitempty"`
}

type chatMessage struct {
	ID     string      `json:"id"`
	Blocks []chatBlock `json:"blocks"`
}

type chatQueueItem struct {
	chatMessage
	Placement string `json:"placement"`
}

func projectChatMessage(raw json.RawMessage, assistant bool) (chatMessage, string) {
	var row struct {
		ID      string                `json:"id"`
		Source  struct{ Kind string } `json:"source"`
		Content []struct {
			Type, Text, ID, Name string
			Attachment           json.RawMessage `json:"attachment"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &row) != nil || row.ID == "" {
		return chatMessage{}, ""
	}
	message := chatMessage{ID: row.ID, Blocks: []chatBlock{}}
	for _, block := range row.Content {
		switch block.Type {
		case "text":
			message.Blocks = append(message.Blocks, chatBlock{Type: "text", Text: block.Text})
		case "image", "file":
			var ref chatAttachment
			if json.Unmarshal(block.Attachment, &ref) != nil || ref.ID == "" {
				continue
			}
			item := chatBlock{Type: block.Type, Name: ref.Name, Attachment: &ref}
			if block.Type == "file" {
				item.File = &chatFile{Source: "attachment", AttachmentID: ref.ID}
				item.Attachment = nil
			}
			message.Blocks = append(message.Blocks, item)
		case "tool-call":
			if assistant && block.Name != "ask_user_question" {
				message.Blocks = append(message.Blocks, chatBlock{Type: "tool", Name: block.Name, CallID: block.ID})
			}
		}
	}
	for i := range message.Blocks {
		message.Blocks[i].ID = row.ID + "/" + strconv.Itoa(i)
	}
	return message, row.Source.Kind
}

func chatQueue(inbox sessionformat.InboxState) []chatQueueItem {
	items := make([]chatQueueItem, 0, len(inbox.NextTurn)+len(inbox.NextStep))
	for _, entry := range []struct {
		messages  []json.RawMessage
		placement string
	}{{inbox.NextTurn, "queued"}, {inbox.NextStep, "steering"}} {
		for _, raw := range entry.messages {
			message, source := projectChatMessage(raw, false)
			if source == "user" {
				items = append(items, chatQueueItem{chatMessage: message, Placement: entry.placement})
			}
		}
	}
	return items
}

// History and SSE use the same projection and v4 sequence.
func publicChatEvent(event sessionformat.Event) (sessionformat.Event, bool) {
	var data any
	switch event.Type {
	case "turn/start":
		var row struct {
			Turn uint64 `json:"turn"`
		}
		if json.Unmarshal(event.Data, &row) != nil {
			return sessionformat.Event{}, false
		}
		data = row
	case "turn/end":
		var row struct {
			Turn   uint64 `json:"turn"`
			Reason struct {
				Kind string `json:"kind"`
			} `json:"reason"`
		}
		if json.Unmarshal(event.Data, &row) != nil {
			return sessionformat.Event{}, false
		}
		data = row
	case "user/message":
		message, source := projectChatMessage(event.Data, false)
		switch source {
		case "user":
		case "schedule":
			message.Blocks = nil
		case "goal":
			message.Blocks = nil
			source = "background"
		default:
			return sessionformat.Event{}, false
		}
		data = struct {
			chatMessage
			Source string `json:"source"`
		}{message, source}
	case "assistant/message":
		var row struct {
			Turn    uint64          `json:"turn"`
			Step    uint64          `json:"step"`
			Message json.RawMessage `json:"message"`
		}
		if json.Unmarshal(event.Data, &row) != nil {
			return sessionformat.Event{}, false
		}
		message, _ := projectChatMessage(row.Message, true)
		data = map[string]any{"turn": row.Turn, "step": row.Step, "message": message}
	case "deliverables/presented":
		var row sessionformat.PresentedFiles
		if json.Unmarshal(event.Data, &row) != nil || row.Turn == 0 || row.CallID == "" {
			return sessionformat.Event{}, false
		}
		message := chatMessage{ID: "delivery/" + strconv.FormatUint(event.Seq, 10)}
		for _, file := range row.Files {
			path := filepath.Clean(file.Path)
			if !filepath.IsAbs(path) || path == string(filepath.Separator) {
				continue
			}
			message.Blocks = append(message.Blocks, chatBlock{
				ID:   fmt.Sprintf("file/%d/%x", row.Turn, sha256.Sum256([]byte(path))),
				Type: "file", Name: filepath.Base(path), Description: file.Description,
				File: &chatFile{Source: "presented", Path: path},
			})
		}
		if len(message.Blocks) == 0 {
			return sessionformat.Event{}, false
		}
		data = map[string]any{"turn": row.Turn, "delivery": true, "message": message}
		event.Type = "assistant/message"
	case "tool/call":
		var row struct {
			CallID string `json:"callId"`
			Name   string `json:"name"`
		}
		if json.Unmarshal(event.Data, &row) != nil || row.CallID == "" || row.Name == "ask_user_question" {
			return sessionformat.Event{}, false
		}
		data = map[string]string{"callId": row.CallID}
	case "tool/result":
		// Compaction rewrites model context. Keep the original result in chat.
		if len(event.SurfaceOp) > 0 && !bytes.Equal(bytes.TrimSpace(event.SurfaceOp), []byte(`"append"`)) {
			return sessionformat.Event{}, false
		}
		var row struct {
			Message struct {
				ID      string `json:"id"`
				CallID  string `json:"toolCallId"`
				IsError bool   `json:"isError"`
				Content []struct {
					Type       string          `json:"type"`
					Attachment json.RawMessage `json:"attachment"`
				} `json:"content"`
			}
			Error struct{ Code string }
		}
		if json.Unmarshal(event.Data, &row) != nil || row.Message.CallID == "" {
			return sessionformat.Event{}, false
		}
		status := "succeeded"
		if row.Message.IsError {
			status = "failed"
		}
		if row.Error.Code == "ABORTED" || row.Error.Code == "TOOL_OUTCOME_UNKNOWN" {
			status = "cancelled"
		}
		result := struct {
			CallID string      `json:"callId"`
			Status string      `json:"status"`
			Code   string      `json:"code"`
			Images []chatBlock `json:"images,omitempty"`
		}{CallID: row.Message.CallID, Status: status, Code: row.Error.Code}
		if status == "succeeded" && row.Message.ID != "" {
			for i, block := range row.Message.Content {
				if block.Type != "image" {
					continue
				}
				var ref chatAttachment
				if json.Unmarshal(block.Attachment, &ref) != nil || !attachmentDigest.MatchString(ref.ID) {
					continue
				}
				result.Images = append(result.Images, chatBlock{
					ID: row.Message.ID + "/" + strconv.Itoa(i), Type: "image", Name: ref.Name, Attachment: &ref,
				})
			}
		}
		data = result
	default:
		return sessionformat.Event{}, false
	}
	encoded, err := json.Marshal(data)
	return sessionformat.Event{Seq: event.Seq, Time: event.Time, Type: event.Type, Data: encoded}, err == nil
}

type chatTranscriptPage struct {
	Events     []sessionformat.Event                `json:"events"`
	Before     *uint64                              `json:"before"`
	ThroughSeq *uint64                              `json:"throughSeq"`
	Queue      []chatQueueItem                      `json:"queue"`
	Questions  []interaction.Pending                `json:"questions"`
	Baseline   *agent.NativeAssistantStreamBaseline `json:"baseline,omitempty"`
	Permission *permission.State                    `json:"permission,omitempty"`
}

func makeChatTranscript(events []sessionformat.Event, before *uint64, limit int) (chatTranscriptPage, error) {
	page := chatTranscriptPage{Events: []sessionformat.Event{}, Queue: []chatQueueItem{}}
	end, start, turns := len(events), 0, 0
	if before != nil && *before < uint64(end) {
		end = int(*before)
	}
	for i := end - 1; i >= 0; i-- {
		if events[i].Type == "turn/start" {
			turns++
			if turns == limit {
				start = i
			}
			if turns > limit {
				first := events[start].Seq
				page.Before = &first
				break
			}
		}
	}
	for _, event := range events[start:end] {
		if public, ok := publicChatEvent(event); ok {
			page.Events = append(page.Events, public)
		}
	}
	if before != nil || len(events) == 0 {
		return page, nil
	}
	last := events[len(events)-1].Seq
	page.ThroughSeq = &last
	inbox, err := sessionformat.FoldInbox(events)
	if err != nil {
		return chatTranscriptPage{}, err
	}
	page.Queue = chatQueue(inbox)
	return page, nil
}

func coldChatQuestions(events []sessionformat.Event) []interaction.Pending {
	return slices.DeleteFunc(interaction.FoldTimedQuestions(events), func(question interaction.Pending) bool {
		return question.State != "continued"
	})
}

func serveChatTranscript(w http.ResponseWriter, r *http.Request, events []sessionformat.Event, runtime *hostruntime.NativeRuntime) {
	limit := 20
	var before *uint64
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var err error
		if limit, err = strconv.Atoi(raw); err != nil || limit < 1 || limit > 100 {
			writeSessionJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_turn_limit"})
			return
		}
	}
	if raw := r.URL.Query().Get("before"); raw != "" {
		value, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || value >= uint64(len(events)) || events[value].Type != "turn/start" {
			writeSessionJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_turn_cursor"})
			return
		}
		before = &value
	}
	page, err := makeChatTranscript(events, before, limit)
	if err != nil {
		writeSessionJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "transcript_unavailable"})
		return
	}
	if before == nil {
		if runtime == nil {
			page.Questions = coldChatQuestions(events)
		} else {
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
		state := permission.Read(events)
		page.Permission = &state
		if len(page.Questions) == 0 {
			page.Questions = nil
		}
	}
	writeSessionJSON(w, http.StatusOK, page)
}

func writeChatSSE(w http.ResponseWriter, name string, value any) bool {
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, encoded)
	return err == nil
}

func chatAssistantChunk(chunk agent.ModelStreamChunk) agent.ModelStreamChunk {
	switch chunk["type"] {
	case nil, "text-delta", "finish":
		return chunk
	case "block-start":
		if chunk["blockType"] == "text" {
			return chunk
		}
	case "block-end":
		switch block := chunk["block"].(type) {
		case agent.ModelStreamChunk:
			if block["type"] == "text" {
				return chunk
			}
		case map[string]any:
			if block["type"] == "text" {
				return chunk
			}
		}
	}
	return agent.ModelStreamChunk{"type": "redacted", "index": chunk["index"]}
}

func waitForChatRuntime(w http.ResponseWriter, r *http.Request, host *hostruntime.NativeHost, id string, events []sessionformat.Event) (*hostruntime.NativeRuntime, *http.Request) {
	if runtime, ok := host.Session(id); ok {
		return runtime, r
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "event stream unavailable", http.StatusServiceUnavailable)
		return nil, r
	}
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("after")
	}
	start := 0
	if raw != "" {
		value, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			writeSessionJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_event_cursor"})
			return nil, r
		}
		if value >= uint64(len(events)) {
			start = len(events)
		} else {
			start = int(value) + 1
		}
	}
	inbox, err := sessionformat.FoldInbox(events)
	if err != nil {
		http.Error(w, "chat state unavailable", http.StatusServiceUnavailable)
		return nil, r
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	for _, event := range events[start:] {
		if projected, ok := publicChatEvent(event); ok {
			if !writeChatSSE(w, "session", projected) {
				return nil, r
			}
		}
	}
	if !writeChatSSE(w, "chat.idle", struct{}{}) ||
		!writeChatSSE(w, "queue.state", map[string]any{"queue": chatQueue(inbox)}) ||
		!writeChatSSE(w, "questions.state", map[string]any{"questions": coldChatQuestions(events)}) {
		return nil, r
	}
	flusher.Flush()
	if len(events) > 0 {
		r = r.Clone(r.Context())
		r.Header.Set("Last-Event-ID", strconv.FormatUint(events[len(events)-1].Seq, 10))
	}
	return awaitChatRuntime(w, r, host, id, flusher)
}
func awaitChatRuntime(w http.ResponseWriter, r *http.Request, host *hostruntime.NativeHost, id string, flusher http.Flusher) (*hostruntime.NativeRuntime, *http.Request) {
	check := time.NewTicker(500 * time.Millisecond)
	heartbeat := time.NewTicker(15 * time.Second)
	defer check.Stop()
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil, r
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
				return nil, r
			}
			flusher.Flush()
		case <-check.C:
			if runtime, ok := host.Session(id); ok {
				return runtime, r
			}
			if err := host.CheckScheduleTarget(r.Context(), id); err != nil {
				return nil, r
			}
		}
	}
}
