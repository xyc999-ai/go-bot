package sessionsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"go-bot/harness/session"
)

const (
	older = "00000000-0000-4000-8000-000000000001"
	newer = "00000000-0000-4000-8000-000000000002"
)

func historyEvent(seq uint64, kind, role, text string) sessionformat.Event {
	content := map[string]any{"source": map[string]string{"kind": role}, "content": []map[string]string{{"type": "text", "text": text}}}
	var data any = content
	if kind == "assistant/message" {
		data = map[string]any{"message": content}
	}
	raw, _ := json.Marshal(data)
	return sessionformat.Event{Seq: seq, Time: 1_700_000_000_000 + int64(seq), Type: kind, Data: raw}
}

func TestFTS5CommittedHistory(t *testing.T) {
	ctx := context.Background()
	histories := map[string][]sessionformat.Event{
		older: {
			historyEvent(0, "user/message", "user", "挂载卷 on the old server"),
			historyEvent(1, "assistant/message", "assistant", "The backup target is archive storage."),
			historyEvent(2, "user/message", "runtime-context", "hiddenterm"),
			historyEvent(3, "system/message", "system", "hiddenterm"),
			historyEvent(4, "compaction/summary", "", "hiddenterm"),
		},
		newer: {historyEvent(0, "user/message", "user", "挂载卷 and backup")},
	}
	source := Source{
		List: func(context.Context, string, int) (sessionformat.SessionListPage, error) {
			var page sessionformat.SessionListPage
			for _, id := range []string{older, newer} {
				if _, ok := histories[id]; ok {
					page.Sessions = append(page.Sessions, sessionformat.Header{ID: id, CreatedAt: 1_700_000_000_000 + int64(len(page.Sessions))})
				}
			}
			return page, nil
		},
		Snapshot: func(_ context.Context, id string, cursor Cursor) (Snapshot, error) {
			events := histories[id]
			return Snapshot{Cursor: Cursor{Next: len(events), Size: 100 + int64(len(events))}, Events: events[cursor.Next:]}, nil
		},
	}
	path := filepath.Join(t.TempDir(), "index.sqlite")
	index, err := Open(ctx, path, "alice", source)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	if err := index.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	for _, term := range []string{"卷", "挂载", `"挂载卷"`, "挂载 AND serv*", `"old server"`, "挂载 NOT foobar"} {
		found, err := index.Query(ctx, newer, Query{Text: term})
		if err != nil || len(found.Sessions) != 1 || found.Sessions[0].ID != older || found.Sessions[0].Match.ID != 0 {
			t.Fatalf("query %q = %+v, %v", term, found, err)
		}
	}
	for _, query := range []Query{{Text: "hiddenterm"}, {Text: "挂载", Roles: "assistant"}, {Text: "挂载", After: "2025-01-01"}} {
		found, err := index.Query(ctx, newer, query)
		if err != nil || len(found.Sessions) != 0 {
			t.Fatalf("query %+v = %+v, %v", query, found, err)
		}
	}
	zero := uint64(0)
	for _, q := range []Query{{SessionID: older}, {SessionID: older, Anchor: &zero}} {
		found, err := index.Query(ctx, newer, q)
		if err != nil || len(found.Messages) != 2 || found.Messages[0].Content != "挂载卷 on the old server" {
			t.Fatalf("read %+v = %+v, %v", q, found, err)
		}
	}
	browse, err := index.Query(ctx, newer, Query{})
	if err != nil || len(browse.Sessions) != 1 || browse.Sessions[0].ID != older {
		t.Fatalf("browse = %+v, %v", browse, err)
	}

	// Simulate a committed journal append while the index is offline.
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	histories[older] = append(histories[older], historyEvent(5, "assistant/message", "assistant", strings.Repeat("Previous context. ", 100)+"Incremental recovery of 挂载卷"))
	index, err = Open(ctx, path, "alice", source)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	found, err := index.Query(ctx, newer, Query{Text: "recovery"})
	if err != nil || len(found.Sessions) != 1 || found.Sessions[0].Match.ID != 5 ||
		!strings.Contains(found.Sessions[0].Match.Content, "recovery of 挂载卷") || !found.Sessions[0].Match.Truncated {
		t.Fatalf("reopened query = %+v, %v", found, err)
	}
	rebuilt, err := Open(ctx, filepath.Join(t.TempDir(), "rebuilt.sqlite"), "alice", source)
	if err != nil {
		t.Fatal(err)
	}
	defer rebuilt.Close()
	found, err = rebuilt.Query(ctx, newer, Query{Text: "recovery"})
	if err != nil || len(found.Sessions) != 1 {
		t.Fatalf("rebuilt query = %+v, %v", found, err)
	}

	delete(histories, older)
	found, err = index.Query(ctx, newer, Query{Text: "recovery"})
	if err != nil || len(found.Sessions) != 0 {
		t.Fatalf("archived query = %+v, %v", found, err)
	}
	if _, err := index.Query(ctx, newer, Query{SessionID: older}); err == nil {
		t.Fatal("archived session remained readable by ID")
	}

	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	delete(histories, newer)
	other, err := Open(ctx, path, "bob", source)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	found, err = other.Query(ctx, "", Query{Text: "backup"})
	if err != nil || len(found.Sessions) != 0 {
		t.Fatalf("foreign account query = %+v, %v", found, err)
	}
}

func TestPagedSourceDrainsAndRecoversInterruptedSync(t *testing.T) {
	for _, interrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(interrupt), func(t *testing.T) {
			events := make([]sessionformat.Event, 5)
			for i := range events {
				events[i] = historyEvent(uint64(i), "user/message", "user", fmt.Sprintf("needle %d", i))
			}
			calls := 0
			fail := interrupt
			resetAfterFailure := false
			source := Source{List: func(context.Context, string, int) (sessionformat.SessionListPage, error) {
				return sessionformat.SessionListPage{Sessions: []sessionformat.Header{{ID: older, CreatedAt: 1}}}, nil
			}, Snapshot: func(_ context.Context, _ string, cursor Cursor) (Snapshot, error) {
				calls++
				if fail && calls == 2 {
					return Snapshot{}, errors.New("interrupted page read")
				}
				start := cursor.Next
				reset := cursor.Size < 0 && !cursor.Partial
				if reset {
					start = 0
					resetAfterFailure = true
				}
				end := min(len(events), start+2)
				return Snapshot{Cursor: Cursor{Next: end, Size: 99}, Events: events[start:end], More: end < len(events), Reset: reset}, nil
			}}
			path := filepath.Join(t.TempDir(), "paged.sqlite")
			index, err := Open(t.Context(), path, "alice", source)
			if err != nil {
				t.Fatal(err)
			}
			err = index.Sync(t.Context())
			if interrupt {
				if err == nil {
					t.Fatal("partial sync reported success")
				}
				var size int64
				if err := index.db.QueryRow("SELECT size FROM sessions WHERE id=?", older).Scan(&size); err != nil || size != -1 {
					t.Fatal("partial index persisted a complete file version", err)
				}
				fail = false
				if err := index.Sync(t.Context()); err != nil {
					t.Fatal(err)
				}
				if !resetAfterFailure {
					t.Fatal("interrupted cold prefix was not rebuilt")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			var count, next int
			if err := index.db.QueryRow("SELECT count(*) FROM messages WHERE session_id=?", older).Scan(&count); err != nil || count != 5 {
				t.Fatalf("incomplete paged corpus: %d %v", count, err)
			}
			if err := index.db.QueryRow("SELECT next FROM sessions WHERE id=?", older).Scan(&next); err != nil || next != 5 {
				t.Fatal("wrong final source cursor")
			}
			if err := index.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(t.Context(), path, "alice", source)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if err := reopened.Sync(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
