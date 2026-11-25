package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-bot/harness/agent"
	"go-bot/harness/assembly"
	"go-bot/harness/attachments"
	"go-bot/harness/content"
	"go-bot/harness/goal"
	"go-bot/harness/interaction"
	"go-bot/harness/permission"
	"go-bot/harness/plan"
	"go-bot/harness/schedule"
	"go-bot/harness/session"
	"go-bot/internal/hostruntime"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// NativeWorkerHTTPRuntime is the worker-owned v4 Session surface. It exposes
// native events without translating them back to the retired Go event type.
type NativeWorkerHTTPRuntime interface {
	SubmitMessage(context.Context, []byte) (string, error)
	UpdateQueuedMessage(context.Context, string, bool) error
	DurableSessionEvents() ([]sessionformat.Event, error)
	CompactNow(context.Context) (bool, error)
	GetGoal() (goal.NativeView, bool, error)
	ResumeGoal(context.Context, sessionformat.GoalRef) (goal.NativeView, error)
	Usage() (sessionformat.UsageSnapshot, error)
	GetPlan() (plan.View, error)
	SetPlan(context.Context, bool) (plan.View, error)
	PendingQuestions() ([]interaction.Pending, error)
	AnswerQuestion(context.Context, string, interaction.Answer) error
	CancelQuestion(context.Context, string) error
	CancelTurn(context.Context, string) (string, error)
	DurableEventsAfter(*uint64, int) ([]sessionformat.Event, error)
	SubscribeAssistantStreamSnapshot() (agent.NativeAssistantStreamBaseline, <-chan agent.NativeAssistantStreamFrame, func(), error)
	Done() <-chan struct{}
	LastError() error
}

type permissionRuntime interface {
	GetPermission() (permission.State, error)
	SetPermission(context.Context, permission.Mode) (permission.State, error)
	AttachApprovalClient() func()
}

type NativeWorkerHTTPOptions struct {
	HostFilesRoot     string
	ReadSchedules     func(context.Context) (schedule.Document, error)
	Images            attachments.Store
	Files             attachments.FileStore
	ImageInputEnabled bool
	OnMessageAccepted func(string)
}

// NewNativeWorkerHTTP serves one already-started Session Agent through its
// owning user Host's shared HTTP listener. Gateway authenticates the client
// and routes by user ID; the Host dispatches by Session ID.
func NewNativeWorkerHTTP(worker NativeWorkerHTTPRuntime, identity assembly.NativeRuntimeIdentity, options NativeWorkerHTTPOptions) http.Handler {
	router := gin.New()
	router.Use(gin.Recovery())
	var admissionMu sync.Mutex
	group := router.Group("/v1/sessions/:id")
	group.Use(func(c *gin.Context) {
		if c.Param("id") != identity.SessionID {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	})
	group.POST("/messages", func(c *gin.Context) {
		admissionMu.Lock()
		defer admissionMu.Unlock()
		nativeWorkerMessage(c, worker, options)
	})
	for _, operation := range []struct {
		method, path string
		steer        bool
	}{{http.MethodPost, "/queue/:messageId/steer", true}, {http.MethodDelete, "/queue/:messageId", false}} {
		group.Handle(operation.method, operation.path, func(c *gin.Context) {
			err := worker.UpdateQueuedMessage(c.Request.Context(), c.Param("messageId"), operation.steer)
			switch {
			case errors.Is(err, agent.ErrQueuedMessageNotFound):
				c.JSON(http.StatusNotFound, gin.H{"error": "queued_message_not_found"})
			case errors.Is(err, agent.ErrSteerUnavailable):
				c.JSON(http.StatusConflict, gin.H{"error": "steer_unavailable"})
			case err != nil:
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "queue_update_failed"})
			default:
				c.Status(http.StatusNoContent)
			}
		})
	}
	group.POST("/commands/compact", func(c *gin.Context) {
		compacted, err := worker.CompactNow(c.Request.Context())
		switch {
		case errors.Is(err, hostruntime.ErrNativeCompactionBusy):
			c.JSON(http.StatusConflict, gin.H{"error": "agent_busy"})
		case err != nil:
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "compaction_failed"})
		default:
			c.JSON(http.StatusOK, gin.H{"compacted": compacted})
		}
	})
	group.POST("/turns/:turnId/cancel", func(c *gin.Context) {
		state, err := worker.CancelTurn(c.Request.Context(), c.Param("turnId"))
		switch {
		case errors.Is(err, hostruntime.ErrNativeTurnNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "turn_not_found"})
		case errors.Is(err, hostruntime.ErrNativeTurnFinished):
			c.JSON(http.StatusConflict, gin.H{"error": "turn_finished"})
		case err != nil:
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent_cancel_failed"})
		default:
			c.JSON(http.StatusAccepted, gin.H{"turnId": c.Param("turnId"), "state": state})
		}
	})
	group.GET("/goal", func(c *gin.Context) {
		view, found, err := worker.GetGoal()
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent_goal_unavailable"})
			return
		}
		if !found {
			c.JSON(http.StatusOK, gin.H{"goal": nil})
			return
		}
		c.JSON(http.StatusOK, gin.H{"goal": view})
	})
	group.POST("/goal/resume", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
		var request struct {
			GoalID   string `json:"goalId" binding:"required"`
			Revision uint64 `json:"revision" binding:"required"`
		}
		if err := c.ShouldBindJSON(&request); err != nil || request.GoalID == "" || request.Revision == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_goal_resume_request"})
			return
		}
		view, err := worker.ResumeGoal(c.Request.Context(), sessionformat.GoalRef{ID: request.GoalID, Revision: request.Revision})
		if err != nil {
			nativeGoalError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"goal": view})
	})
	if runtime, ok := worker.(permissionRuntime); ok {
		group.GET("/permissions", func(c *gin.Context) {
			state, err := runtime.GetPermission()
			if err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, state)
		})
		group.POST("/permissions", func(c *gin.Context) {
			var input struct {
				Mode permission.Mode `json:"mode"`
			}
			if c.ShouldBindJSON(&input) != nil || input.Mode.Rank() == 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_permission_mode"})
				return
			}
			admissionMu.Lock()
			defer admissionMu.Unlock()
			state, err := runtime.SetPermission(c.Request.Context(), input.Mode)
			switch {
			case errors.Is(err, hostruntime.ErrPermissionBusy):
				c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			case err != nil:
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			default:
				c.JSON(http.StatusOK, state)
			}
		})
	}

	group.GET("/plan", func(c *gin.Context) {
		view, err := worker.GetPlan()
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent_plan_unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"plan": view})
	})
	group.POST("/plan", func(c *gin.Context) {
		var request struct {
			Active *bool `json:"active"`
		}
		if err := c.ShouldBindJSON(&request); err != nil || request.Active == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_plan_mode_selection"})
			return
		}
		view, err := worker.SetPlan(c.Request.Context(), *request.Active)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent_plan_unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"plan": view})
	})
	group.GET("/questions", func(c *gin.Context) {
		pending, err := worker.PendingQuestions()
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent_questions_unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"questions": pending})
	})
	group.POST("/questions/:callId/answer", func(c *gin.Context) {
		var answer interaction.Answer
		if err := c.ShouldBindJSON(&answer); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_question_answer"})
			return
		}
		if err := worker.AnswerQuestion(c.Request.Context(), c.Param("callId"), answer); err != nil {
			nativeQuestionError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
	group.POST("/questions/:callId/cancel", func(c *gin.Context) {
		if err := worker.CancelQuestion(c.Request.Context(), c.Param("callId")); err != nil {
			nativeQuestionError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
	group.GET("/events", func(c *gin.Context) { nativeWorkerEvents(c, worker) })
	group.GET("/stream", func(c *gin.Context) { nativeWorkerStream(c, worker) })
	group.GET("/files", func(c *gin.Context) {
		root, err := os.OpenRoot(identity.Workspace)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "workspace_unavailable"})
			return
		}
		defer root.Close()
		listWorkspaceFiles(c.Writer, c.Request, root, "", func(err error) {
			if errors.Is(err, os.ErrNotExist) {
				c.JSON(http.StatusNotFound, gin.H{"error": "session_not_found"})
				return
			}
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "workspace_unavailable"})
		})
	})
	group.GET("/files/content", func(c *gin.Context) { downloadWorkspaceFile(c, identity.Workspace) })
	group.GET("/usage", func(c *gin.Context) {
		usage, err := worker.Usage()
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent_usage_unavailable"})
			return
		}
		c.JSON(http.StatusOK, usage)
	})
	group.GET("", func(c *gin.Context) {
		state := "ready"
		select {
		case <-worker.Done():
			state = "stopped"
		default:
		}
		result := gin.H{"sessionId": identity.SessionID, "mode": "container", "state": state}
		if err := worker.LastError(); err != nil {
			result["lastError"] = err.Error()
		}
		c.JSON(http.StatusOK, result)
	})
	return router
}

func nativeGoalError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, goal.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "goal_not_found"})
	case errors.Is(err, goal.ErrStaleRevision):
		c.JSON(http.StatusConflict, gin.H{"error": "goal_revision_stale"})
	case errors.Is(err, goal.ErrInvalidTransition):
		c.JSON(http.StatusConflict, gin.H{"error": "goal_invalid_transition"})
	default:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent_goal_unavailable"})
	}
}

func nativeQuestionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, interaction.ErrNotPending):
		c.JSON(http.StatusNotFound, gin.H{"error": "question_not_pending"})
	case errors.Is(err, interaction.ErrAlreadyAnswered):
		c.JSON(http.StatusConflict, gin.H{"error": "question_already_answered"})
	case errors.Is(err, interaction.ErrInvalidAnswer):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_question_answer"})
	default:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent_questions_unavailable"})
	}
}

func nativeWorkerMessage(c *gin.Context, worker NativeWorkerHTTPRuntime, options NativeWorkerHTTPOptions) {
	clientTimeZone, err := canonicalClientTimeZone(c.GetHeader("X-Client-Time-Zone"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_client_time_zone"})
		return
	}
	mediaType, _, _ := mime.ParseMediaType(c.GetHeader("Content-Type"))
	var content string
	var files []attachments.FileRef
	var images []map[string]any
	var blocks []any
	if mediaType == "multipart/form-data" {
		if c.Request.ContentLength > maxCreatorInputRequestBytes {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "input_too_large"})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCreatorInputRequestBytes)
		reader, err := c.Request.MultipartReader()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_message"})
			return
		}
		var staged []stagedNativePart
		content, staged, err = stageNativeMessageParts(c.Request.Context(), reader)
		defer func() {
			for _, item := range staged {
				_ = item.file.Close()
			}
		}()
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.Is(err, errInputTooLarge) || errors.As(err, &tooLarge) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "input_too_large"})
			} else {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_message"})
			}
			return
		}
		if !options.ImageInputEnabled {
			for _, item := range staged {
				if item.kind == "image" {
					c.JSON(http.StatusBadRequest, gin.H{"error": "image_input_unavailable"})
					return
				}
			}
		}
		for _, item := range staged {
			if _, err := item.file.Seek(0, io.SeekStart); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "input_upload_failed"})
				return
			}
			if item.kind == "image" {
				data, err := io.ReadAll(io.LimitReader(item.file, attachments.MaxImageSourceBytes+1))
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "input_upload_failed"})
					return
				}
				ref, err := options.Images.SaveImage(c.Request.Context(), data, item.mediaType, attachments.SafeFileName(item.name), "")
				if errors.Is(err, attachments.ErrInvalidImage) {
					c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_image"})
					return
				}
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "input_upload_failed"})
					return
				}
				image := nativeImageAttachment(ref)
				images = append(images, image)
				blocks = append(blocks, map[string]any{"type": "image", "attachment": image})
				continue
			}
			ref, err := options.Files.SaveFile(c.Request.Context(), item.file, item.name)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "input_upload_failed"})
				return
			}
			files = append(files, ref)
			blocks = append(blocks, map[string]any{"type": "file", "attachment": ref})
		}
		// Keep mixed attachments in upload order, then append the user instruction.
		if content != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": content})
		}
	} else {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxMessageContentBytes)
		var request struct {
			Content string `json:"content" binding:"required"`
		}
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_message"})
			return
		}
		content = request.Content
		blocks = []any{map[string]any{"type": "text", "text": content}}
	}
	if strings.TrimSpace(content) == "" && len(files) == 0 && len(images) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_message"})
		return
	}
	messageID := uuid.NewString()
	source := map[string]any{"kind": "user", "rpcId": messageID}
	if clientTimeZone != "" {
		source["clientTimeZone"] = clientTimeZone
	}
	message, err := json.Marshal(map[string]any{
		"id": messageID, "role": "user", "source": source, "content": blocks,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid_message"})
		return
	}
	acceptedID, err := worker.SubmitMessage(c.Request.Context(), message)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent_message_failed"})
		return
	}
	// DeepSeek turn numbers are assigned when the driver claims an inbox item,
	// not when HTTP admits it. Do not fabricate a turnId at this boundary.
	if options.OnMessageAccepted != nil && strings.TrimSpace(content) != "" {
		options.OnMessageAccepted(content)
	}
	c.JSON(http.StatusAccepted, gin.H{"messageId": acceptedID, "files": files, "images": images})
}

func nativeImageAttachment(ref content.ImageAttachment) map[string]any {
	attachment := map[string]any{
		"attachmentId": ref.ID, "mediaType": ref.MIMEType,
		"bytes": ref.Bytes, "width": ref.Width, "height": ref.Height,
	}
	if ref.Name != "" {
		attachment["name"] = ref.Name
	}
	if ref.OriginalDimensions != nil {
		attachment["originalDimensions"] = ref.OriginalDimensions
	}
	return attachment
}

func nativeWorkerEvents(c *gin.Context, worker NativeWorkerHTTPRuntime) {
	after, limit, code := eventPageParams(c.Request)
	if code != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": code})
		return
	}
	events, err := worker.DurableEventsAfter(after, limit)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent_events_unavailable"})
		return
	}
	c.JSON(http.StatusOK, publicEventPageFrom(events))
}

func nativeWorkerStream(c *gin.Context, worker NativeWorkerHTTPRuntime) {
	rawCursor := c.GetHeader("Last-Event-ID")
	if rawCursor == "" {
		rawCursor = c.Query("after")
	}
	var after *uint64
	if rawCursor != "" {
		value, err := strconv.ParseUint(rawCursor, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_event_cursor"})
			return
		}
		after = &value
	}
	chat := c.Query("view") == "chat"
	if runtime, ok := worker.(permissionRuntime); chat && ok {
		defer runtime.AttachApprovalClient()()
	}

	var inbox sessionformat.InboxState
	var queueDirty bool
	var queueCut uint64
	if reader, ok := worker.(historyWindowRuntime); chat && ok {
		opening, err := reader.HistoryWindow(c.Request.Context(), sessionformat.HistoryRequest{Kind: sessionformat.HistoryState})
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chat_state_unavailable"})
			return
		}
		inbox, queueCut, queueDirty = opening.Inbox, opening.Count, true
	} else {
		if chat {
			if after != nil {
				events, err := worker.DurableSessionEvents()
				if err == nil {
					if *after < uint64(len(events)) {
						events = events[:*after+1]
					}
					inbox, err = sessionformat.FoldInbox(events)
				}
				if err != nil {
					c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chat_state_unavailable"})
					return
				}
			}
			queueDirty = true
		}
	}

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "event_stream_unavailable"})
		return
	}
	baseline, frames, unsubscribe, err := worker.SubscribeAssistantStreamSnapshot()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "assistant_stream_unavailable"})
		return
	}
	defer unsubscribe()
	page, err := worker.DurableEventsAfter(after, 200)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent_events_unavailable"})
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache, no-transform")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	baseline = publicNativeAssistantBaseline(baseline, chat)
	encodedBaseline, err := json.Marshal(map[string]any{"baseline": baseline})
	if err != nil {
		return
	}
	if _, err := fmt.Fprintf(c.Writer, "event: assistant-stream-baseline\ndata: %s\n\n", encodedBaseline); err != nil {
		return
	}
	flusher.Flush()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	lastHeartbeat := time.Now()
	// A subscriber can join in the middle of a process-local attempt. Only
	// publish dense attempts whose start this connection actually observed;
	// the eventual durable assistant message is still replayed by cursor.
	openAttempts := make(map[string]int)
	if active := baseline.ActiveAttempt; active != nil {
		openAttempts[active.AttemptID] = active.NextIndex
	}
	project := publicNativeEvent
	if chat {
		project = publicChatEvent
	}
	var lastQuestions string
	var lastPermission permission.State
	questionsDirty := chat
	for {
		for _, event := range page {
			seq := event.Seq
			after = &seq
			if chat && event.Type == "agent/inbox/spliced" && event.Seq >= queueCut {
				inbox, err = inbox.Next(event)
				if err != nil {
					return
				}
				queueDirty = true
			}
			projected, ok := project(event)
			if !ok {
				continue
			}
			encoded, err := json.Marshal(projected)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(c.Writer, "id: %d\nevent: session\ndata: %s\n\n", event.Seq, encoded); err != nil {
				return
			}
		}
		flusher.Flush()
		if len(page) == 200 {
			page, err = worker.DurableEventsAfter(after, 200)
			if err != nil {
				return
			}
			continue
		}
		if queueDirty {
			if !writeChatSSE(c.Writer, "queue.state", map[string]any{"queue": chatQueue(inbox)}) {
				return
			}
			queueDirty = false
			flusher.Flush()
		}
		if questionsDirty {
			if runtime, ok := worker.(permissionRuntime); ok {
				state, err := runtime.GetPermission()
				if err != nil {
					return
				}
				if state != lastPermission {
					if !writeChatSSE(c.Writer, "permissions.state", state) {
						return
					}
					lastPermission = state
					flusher.Flush()
				}
			}
			questions, err := worker.PendingQuestions()
			if err != nil {
				return
			}
			encoded, err := json.Marshal(map[string]any{"questions": questions})
			if err != nil {
				return
			}
			if string(encoded) != lastQuestions {
				if _, err := fmt.Fprintf(c.Writer, "event: questions.state\ndata: %s\n\n", encoded); err != nil {
					return
				}
				lastQuestions = string(encoded)
				flusher.Flush()
			}
			questionsDirty = false
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-worker.Done():
			return
		case frame, open := <-frames:
			if !open {
				return
			}
			switch frame.Type {
			case "start":
				openAttempts[frame.AttemptID] = 0
			case "chunk":
				next, seen := openAttempts[frame.AttemptID]
				if !seen || frame.Index == nil || *frame.Index != next {
					delete(openAttempts, frame.AttemptID)
					continue
				}
				openAttempts[frame.AttemptID] = next + 1
			case "end":
				next, seen := openAttempts[frame.AttemptID]
				delete(openAttempts, frame.AttemptID)
				if !seen || frame.Index == nil || *frame.Index != next {
					continue
				}
			default:
				continue
			}
			if projected, ok := publicNativeAssistantFrame(frame); ok {
				if chat {
					projected.Chunk = chatAssistantChunk(projected.Chunk)
				}
				encoded, err := json.Marshal(map[string]any{"frame": projected})
				if err != nil {
					return
				}
				if _, err := fmt.Fprintf(c.Writer, "event: assistant-stream\ndata: %s\n\n", encoded); err != nil {
					return
				}
				flusher.Flush()
			}
		case <-ticker.C:
			questionsDirty = chat
		}
		page, err = worker.DurableEventsAfter(after, 200)
		if err != nil {
			return
		}
		if len(page) == 0 && time.Since(lastHeartbeat) >= 15*time.Second {
			if _, err := c.Writer.WriteString(": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
			lastHeartbeat = time.Now()
		}
	}
}

func publicNativeAssistantFrame(frame agent.NativeAssistantStreamFrame) (agent.NativeAssistantStreamFrame, bool) {
	if frame.Type != "chunk" {
		return frame, true
	}
	redact := frame.Chunk["type"] == "reasoning-delta" || frame.Chunk["type"] == "usage" ||
		(frame.Chunk["type"] == "block-start" && frame.Chunk["blockType"] == "reasoning")
	if frame.Chunk["type"] == "block-end" {
		switch block := frame.Chunk["block"].(type) {
		case agent.ModelStreamChunk:
			if block["type"] == "reasoning" {
				redact = true
			}
		case map[string]any:
			if block["type"] == "reasoning" {
				redact = true
			}
		}
	}
	if redact {
		frame.Chunk = agent.ModelStreamChunk{"type": "redacted", "index": frame.Chunk["index"]}
	} else if frame.Chunk["type"] == "finish" {
		// The durable turn result carries the user-facing outcome. Raw provider
		// failure details and token telemetry stay in the private v4 journal.
		kind := "error"
		switch reason := frame.Chunk["reason"].(type) {
		case agent.ModelStreamChunk:
			if value, ok := reason["kind"].(string); ok {
				kind = value
			}
		case map[string]any:
			if value, ok := reason["kind"].(string); ok {
				kind = value
			}
		}
		frame.Chunk = agent.ModelStreamChunk{"type": "finish", "reason": map[string]any{"kind": kind}}
	}
	return frame, true
}

func publicNativeAssistantBaseline(baseline agent.NativeAssistantStreamBaseline, chat bool) agent.NativeAssistantStreamBaseline {
	active := baseline.ActiveAttempt
	if active == nil {
		return baseline
	}
	for _, entry := range active.Stream {
		record, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		switch record["type"] {
		case "reasoning-chunks":
			if texts, ok := record["texts"].([]any); ok {
				for index := range texts {
					texts[index] = ""
				}
			}
		case "chunk":
			if raw, ok := record["chunk"].(map[string]any); ok {
				projected, _ := publicNativeAssistantFrame(agent.NativeAssistantStreamFrame{Type: "chunk", Chunk: agent.ModelStreamChunk(raw)})
				if chat {
					projected.Chunk = chatAssistantChunk(projected.Chunk)
				}
				record["chunk"] = map[string]any(projected.Chunk)
			}
		}
	}
	return baseline
}

// The full v4 artifact stays private to the worker. Creator subscribers get
// only transcript/control events, never system prompts, request snapshots,
// model attempts or compaction internals. Unknown future event types fail
// closed until their public projection is designed.
func publicNativeEvent(event sessionformat.Event) (sessionformat.Event, bool) {
	switch event.Type {
	case "session/title", "turn/start", "turn/end", "tool/call", "tool/result", "todo/write", "goal/change", "plan/mode", "subagent/descriptor":
		return event, true
	case "user/message":
		var data struct {
			Source struct {
				Kind string `json:"kind"`
			} `json:"source"`
		}
		if json.Unmarshal(event.Data, &data) != nil || data.Source.Kind != "user" {
			return sessionformat.Event{}, false
		}
		return event, true
	case "assistant/message":
		var data struct {
			Turn        uint64 `json:"turn"`
			Step        uint64 `json:"step"`
			Interrupted bool   `json:"interrupted"`
			Message     struct {
				ID      string            `json:"id"`
				Role    string            `json:"role"`
				Content []json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(event.Data, &data) != nil || data.Message.Role != "assistant" {
			return sessionformat.Event{}, false
		}
		content := make([]json.RawMessage, 0, len(data.Message.Content))
		for _, block := range data.Message.Content {
			var kind struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(block, &kind) == nil && kind.Type != "reasoning" {
				content = append(content, block)
			}
		}
		public := map[string]any{"turn": data.Turn, "step": data.Step,
			"message": map[string]any{"id": data.Message.ID, "role": "assistant", "content": content}}
		if data.Interrupted {
			public["interrupted"] = true
		}
		event.Data, _ = json.Marshal(public)
		return event, true
	default:
		return sessionformat.Event{}, false
	}
}
