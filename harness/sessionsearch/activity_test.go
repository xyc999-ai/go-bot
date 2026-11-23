package sessionsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sessionformat "go-bot/harness/session"
)

func TestRecentActivityUsesVisibleMessageTimesAndBoundedUnicodeText(t *testing.T) {
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	histories := map[string][]sessionformat.Event{}
	for n := 1; n <= 12; n++ {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", n)
		for seq, text := range []string{"first request", "first answer", "second request", "second answer", "third request", "third answer", "fourth request", "fourth answer", "fifth request", strings.Repeat("界", 1200)} {
			role, kind := "user", "user/message"
			if seq%2 != 0 {
				role, kind = "assistant", "assistant/message"
			}
			event := historyEvent(uint64(seq), kind, role, text)
			event.Time = now.Add(-time.Duration(n)*time.Hour).UnixMilli() + int64(seq)
			histories[id] = append(histories[id], event)
		}
	}
	// Old journal creation must not hide its recent real conversation; a
	// recently-created but inactive journal must not bring ancient text back.
	main := "00000000-0000-4000-8000-000000000012"
	stale := "00000000-0000-4000-8000-000000000099"
	old := historyEvent(0, "user/message", "user", "stale conversation")
	old.Time = now.Add(-8 * 24 * time.Hour).UnixMilli()
	histories[stale] = []sessionformat.Event{old}
	internal := historyEvent(10, "user/message", "agent-message", "never copy internal relay")
	internal.Time = now.UnixMilli()
	tool := historyEvent(11, "tool/result", "tool", "never copy tool output")
	tool.Time = now.UnixMilli()
	welcome := historyEvent(12, "assistant/message", "go-bot/welcome", "never infer interests from the welcome")
	welcome.Time = now.UnixMilli()
	mixed := historyEvent(13, "assistant/message", "assistant", "visible conclusion")
	mixed.Time = now.Add(-time.Hour).UnixMilli()
	var data map[string]json.RawMessage
	_ = json.Unmarshal(mixed.Data, &data)
	data["message"] = json.RawMessage(`{"content":[{"type":"text","text":"visible conclusion"},{"type":"tool-call","name":"shell","arguments":"secret trace"}]}`)
	mixed.Data, _ = json.Marshal(data)
	histories[main] = append(histories[main], internal, tool, welcome, mixed)
	source := Source{
		List: func(context.Context, string, int) (sessionformat.SessionListPage, error) {
			var page sessionformat.SessionListPage
			for id := range histories {
				page.Sessions = append(page.Sessions, sessionformat.Header{ID: id, CreatedAt: 1})
			}
			return page, nil
		},
		Snapshot: func(_ context.Context, id string, cursor Cursor) (Snapshot, error) {
			return Snapshot{Cursor: Cursor{Next: len(histories[id]), Size: int64(len(histories[id]))}, Events: histories[id][cursor.Next:]}, nil
		},
		Title: func(id string) string { return "Chat " + id },
	}
	index, err := Open(t.Context(), filepath.Join(t.TempDir(), "history.sqlite"), "test", source)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	activity, err := index.RecentActivity(t.Context(), main, now)
	if err != nil {
		t.Fatal(err)
	}
	if !activity.Truncated || len(activity.Sessions) != 9 || activity.Sessions[0].SessionID != main {
		t.Fatalf("main plus eight recent other conversations: %+v", activity)
	}
	chars := 0
	for _, entry := range activity.Sessions {
		if entry.SessionID == stale {
			t.Fatal("ancient messages included")
		}
		users := 0
		for _, message := range entry.Messages {
			if message.Timestamp != histories[entry.SessionID][message.ID].Time {
				t.Fatal("activity lost the original message timestamp")
			}
			chars += len([]rune(message.Content))
			if len([]rune(message.Content)) > 1000 {
				t.Fatal("Unicode preview exceeded its limit")
			}
			if message.Role == "user" {
				users++
			}
			for _, forbidden := range []string{"first request", "first answer", "internal relay", "tool output", "welcome", "secret trace"} {
				if strings.Contains(message.Content, forbidden) {
					t.Fatalf("copied excluded text %q: %+v", forbidden, message)
				}
			}
			if strings.HasPrefix(message.Content, "界") && (!message.Truncated || len([]rune(message.Content)) != 1000) {
				t.Fatal("long preview did not retain exactly 1000 characters with a truncation flag")
			}
		}
		if users != 4 {
			t.Fatalf("expected the last four user exchanges, got %d: %+v", users, entry)
		}
	}
	if chars > 30000 {
		t.Fatal("total activity budget exceeded")
	}
	// Archived sessions disappear through the same real source list used by
	// session_search. The activity collector cannot keep stale indexed text.
	delete(histories, main)
	activity, err = index.RecentActivity(t.Context(), main, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range activity.Sessions {
		if entry.SessionID == main {
			t.Fatal("archived main still exposed")
		}
	}
}
