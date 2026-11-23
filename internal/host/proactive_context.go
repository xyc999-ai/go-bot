package host

import (
	"encoding/json"
	"time"

	"go-bot/harness/sessionsearch"
)

// Only the model-facing presentation changes. The committed-message index and
// its search API retain their original Unix-millisecond timestamps.
func proactiveDialogueContext(activity sessionsearch.Activity) string {
	type message struct {
		ID        uint64 `json:"id"`
		Role      string `json:"role"`
		Content   string `json:"content"`
		Timestamp string `json:"timestamp"`
		Truncated bool   `json:"truncated,omitempty"`
	}
	type conversation struct {
		SessionID string    `json:"sessionId"`
		Title     string    `json:"title,omitempty"`
		UpdatedAt string    `json:"updatedAt"`
		Messages  []message `json:"messages"`
	}
	view := struct {
		Since     string         `json:"since"`
		Sessions  []conversation `json:"sessions"`
		Truncated bool           `json:"truncated"`
	}{Sessions: []conversation{}, Truncated: activity.Truncated}
	format := func(ms int64) string {
		return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04:05.000 MST")
	}
	view.Since = format(activity.Since)
	for _, entry := range activity.Sessions {
		row := conversation{SessionID: entry.SessionID, Title: entry.Title, UpdatedAt: format(entry.UpdatedAt), Messages: []message{}}
		for _, item := range entry.Messages {
			row.Messages = append(row.Messages, message{ID: item.ID, Role: item.Role, Content: item.Content, Timestamp: format(item.Timestamp), Truncated: item.Truncated})
		}
		view.Sessions = append(view.Sessions, row)
	}
	data, _ := json.Marshal(view)
	return string(data)
}
