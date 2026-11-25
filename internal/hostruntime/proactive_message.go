package hostruntime

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

// DeliverProactive is a Go-owned, same-account delivery boundary. It is not a
// child-agent operation or a public route and never steers the user's turn.
func (h *NativeHost) DeliverProactive(ctx context.Context, mainID, senderID, messageID, text string) error {
	if !h.IsMainSession(mainID) || uuid.Validate(senderID) != nil || uuid.Validate(messageID) != nil || senderID == mainID || text == "" {
		return errors.New("invalid proactive delivery target")
	}
	runtime, err := h.scheduleRuntime(ctx, mainID) // ordinary cold resume, no Schedule delivery
	if err != nil {
		return err
	}
	message, err := json.Marshal(map[string]any{
		"id": messageID, "role": "user",
		"source":  map[string]string{"kind": "agent-message", "form": "relay", "senderSessionId": senderID},
		"content": []map[string]string{{"type": "text", "text": text}},
	})
	if err != nil {
		return err
	}
	return runtime.acceptProactiveOnce(ctx, messageID, message)
}

func (r *NativeRuntime) acceptProactiveOnce(ctx context.Context, id string, raw json.RawMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil || r.stopping || r.fatalErr != nil {
		return errors.New("main Session is not available")
	}
	seen := func(raw json.RawMessage) bool {
		var message struct {
			ID     string                `json:"id"`
			Source struct{ Kind string } `json:"source"`
		}
		return json.Unmarshal(raw, &message) == nil && message.ID == id && message.Source.Kind == "agent-message"
	}
	inbox := r.live.Session.Inbox()
	for _, message := range append(inbox.NextTurn, inbox.NextStep...) {
		if seen(message) {
			return nil
		}
	}
	if r.live.Session.HasRelayedMessage(id) {
		return nil
	}
	if _, err := r.live.Agent.AcceptMessage(ctx, raw); err != nil {
		return err
	}
	r.kick()
	return nil
}
