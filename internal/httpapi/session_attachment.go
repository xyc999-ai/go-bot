package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"os"
	"regexp"
	"strconv"

	"github.com/google/uuid"

	"go-bot/harness/attachments"
	"go-bot/harness/content"
	"go-bot/harness/session"
	"go-bot/internal/hostruntime"
)

var attachmentDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type sessionAttachment struct {
	Type       string `json:"type"`
	Attachment struct {
		attachments.FileRef
		MIME   string `json:"mediaType"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	} `json:"attachment"`
}

type attachmentMessage struct {
	Content []sessionAttachment `json:"content"`
}

func attachmentFromEvents(events []sessionformat.Event, id, name string) (sessionAttachment, bool) {
	for _, event := range events {
		var messages []attachmentMessage
		switch event.Type {
		case "user/message":
			var message attachmentMessage
			if json.Unmarshal(event.Data, &message) == nil {
				messages = []attachmentMessage{message}
			}
		case "assistant/message", "tool/result":
			var row struct {
				Message attachmentMessage `json:"message"`
			}
			if json.Unmarshal(event.Data, &row) == nil {
				messages = []attachmentMessage{row.Message}
			}
		case "agent/inbox/spliced":
			var row struct {
				Inserted []attachmentMessage `json:"inserted"`
			}
			if json.Unmarshal(event.Data, &row) == nil {
				messages = row.Inserted
			}
		}
		for _, message := range messages {
			for _, block := range message.Content {
				if block.Attachment.ID == id && (name == "" || name == block.Attachment.Name) &&
					(block.Type == "image" || block.Type == "file") {
					return block, true
				}
			}
		}
	}
	return sessionAttachment{}, false
}

func sessionAttachmentReference(ctx context.Context, host *hostruntime.NativeHost, sessionID, id, name string) (sessionAttachment, bool, error) {
	if _, ok := host.Session(sessionID); ok || host.ReadHistory != nil {
		value, err := host.SessionHistoryWindow(ctx, sessionID, sessionformat.HistoryRequest{Kind: sessionformat.HistoryAttachment, AttachmentID: id, AttachmentName: name})
		if errors.Is(err, os.ErrNotExist) {
			return sessionAttachment{}, false, nil
		}
		if err != nil {
			return sessionAttachment{}, false, err
		}
		if value.Attachment == nil {
			return sessionAttachment{}, false, nil
		}
		var ref sessionAttachment
		raw, err := json.Marshal(value.Attachment)
		if err == nil {
			err = json.Unmarshal(raw, &ref)
		}
		return ref, err == nil, err
	}

	if active, ok := host.Session(sessionID); ok {
		var after *uint64
		for ctx.Err() == nil {
			events, err := active.DurableEventsAfter(after, 500)
			if err != nil {
				return sessionAttachment{}, false, err
			}
			if ref, ok := attachmentFromEvents(events, id, name); ok {
				return ref, true, nil
			}
			if len(events) < 500 {
				return sessionAttachment{}, false, nil
			}
			last := events[len(events)-1].Seq
			after = &last
		}
		return sessionAttachment{}, false, ctx.Err()
	}
	if host.ReadSession == nil {
		return sessionAttachment{}, false, nil
	}
	artifact, err := host.SessionArtifact(ctx, sessionID)
	if errors.Is(err, os.ErrNotExist) {
		return sessionAttachment{}, false, nil
	}
	if err != nil {
		return sessionAttachment{}, false, err
	}
	ref, found := attachmentFromEvents(artifact.Events, id, name)
	return ref, found, nil
}

func downloadSessionAttachment(w http.ResponseWriter, r *http.Request, host *hostruntime.NativeHost, options NativeWorkerHTTPOptions, sessionID, id string) {
	if uuid.Validate(sessionID) != nil || !attachmentDigest.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	ref, found, err := sessionAttachmentReference(r.Context(), host, sessionID, id, r.URL.Query().Get("name"))
	if err != nil {
		http.Error(w, "Session attachments unavailable", http.StatusServiceUnavailable)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	mediaType := "application/octet-stream"
	var data []byte
	switch ref.Type {
	case "image":
		mediaType = ref.Attachment.MIME
		data, err = options.Images.LoadImage(r.Context(), content.ImageAttachment{
			ID: id, MIMEType: mediaType, Bytes: ref.Attachment.Bytes,
			Width: ref.Attachment.Width, Height: ref.Attachment.Height,
		})
	case "file":
		data, err = options.Files.ReadFile(r.Context(), ref.Attachment.FileRef)
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	name := attachments.SafeFileName(ref.Attachment.Name)
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}
