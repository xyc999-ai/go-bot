package host

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go-bot/harness/sessionsearch"
)

func TestProactiveDialogueFormatsHistoricalTimesWithoutChangingIndexedMessages(t *testing.T) {
	at := time.Date(2026, 11, 24, 9, 30, 0, 123000000, time.FixedZone("Asia/Shanghai", 8*3600))
	activity := sessionsearch.Activity{
		Since: at.Add(-7 * 24 * time.Hour).UnixMilli(), Truncated: true,
		Sessions: []sessionsearch.ActivitySession{{
			SessionID: "chat-id", Title: "调研", UpdatedAt: at.UnixMilli(),
			Messages: []sessionsearch.Message{
				{ID: 12, Role: "user", Content: "明天再看这份报告", Timestamp: at.UnixMilli(), Truncated: true},
				{ID: 15, Role: "assistant", Content: "好的", Timestamp: at.Add(time.Second).UnixMilli()},
			},
		}},
	}
	var view struct {
		Since     string
		Truncated bool
		Sessions  []struct {
			SessionID string
			Title     string
			UpdatedAt string
			Messages  []struct {
				ID        uint64
				Role      string
				Content   string
				Timestamp string
				Truncated bool
			}
		}
	}
	if err := json.Unmarshal([]byte(proactiveDialogueContext(activity)), &view); err != nil {
		t.Fatal(err)
	}
	if view.Since != "2026-11-17 01:30:00.123 UTC" || !view.Truncated || len(view.Sessions) != 1 {
		t.Fatalf("lost coverage information: %+v", view)
	}
	row := view.Sessions[0]
	if row.SessionID != "chat-id" || row.Title != "调研" || row.UpdatedAt != "2026-11-24 01:30:00.123 UTC" || len(row.Messages) != 2 {
		t.Fatalf("lost conversation information: %+v", row)
	}
	for i, item := range row.Messages {
		source := activity.Sessions[0].Messages[i]
		parsed, err := time.Parse("2006-01-02 15:04:05.000 MST", item.Timestamp)
		if err != nil || parsed.UnixMilli() != source.Timestamp || !strings.HasSuffix(item.Timestamp, " UTC") {
			t.Fatalf("historical instant changed: %+v, %v", item, err)
		}
		if item.ID != source.ID || item.Role != source.Role || item.Content != source.Content || item.Truncated != source.Truncated {
			t.Fatalf("message content/anchor changed: %+v", item)
		}
	}
	if activity.Sessions[0].Messages[0].Timestamp != at.UnixMilli() {
		t.Fatal("model presentation changed the source index timestamp")
	}
}
