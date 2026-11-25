package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"go-bot/harness/session"
	"go-bot/harness/sessionsearch"
	"go-bot/internal/hostruntime"
)

func TestColdSearchSourceUsesBoundedContinuations(t *testing.T) {
	id := uuid.NewString()
	store := sessionformat.FileStore{Root: filepath.Join(t.TempDir(), "sessions"), ProjectCWD: "/workspace/search", Claim: func(context.Context, string) (func(), error) { return func() {}, nil }}
	header := sessionformat.Header{Type: "session", Version: 4, ID: id, CreatedAt: 1, CWD: store.ProjectCWD}
	journal, err := store.Create(t.Context(), header)
	if err != nil {
		t.Fatal(err)
	}
	live, err := sessionformat.NewLiveSession(header, journal)
	if err != nil {
		t.Fatal(err)
	}
	appendEvent := func(kind, data string, op json.RawMessage) {
		t.Helper()
		if _, err := live.Append(sessionformat.EventInput{Type: kind, Data: json.RawMessage(data), SurfaceOp: op}); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent("turn/start", `{"turn":1}`, nil)
	appendEvent("step/start", `{"turn":1,"step":1}`, nil)
	for i := range 1201 {
		appendEvent("user/message", fmt.Sprintf(`{"id":"u%d","role":"user","source":{"kind":"user"},"content":[{"type":"text","text":"history %d"}]}`, i, i), json.RawMessage(`"append"`))
	}
	appendEvent("step/end", `{"turn":1,"step":1}`, nil)
	appendEvent("turn/end", `{"turn":1,"reason":{"kind":"completed"}}`, nil)
	if err := live.Close(); err != nil {
		t.Fatal(err)
	}
	path, _ := store.Path(id)
	reader := &sessionformat.FileHistoryReader{}
	reads := 0
	owner := &hostruntime.NativeHost{Archived: func(string) (bool, error) { return false, nil }, SessionLogInfo: func(string) (os.FileInfo, error) { return os.Stat(path) }, ReadSession: func(context.Context, string) (sessionformat.PhysicalArtifact, error) {
		t.Fatal("search loaded a full Host artifact")
		return sessionformat.PhysicalArtifact{}, nil
	}, ReadHistory: func(ctx context.Context, id string, q sessionformat.HistoryRequest) (sessionformat.HistoryWindow, error) {
		reads++
		if q.Limit > 500 {
			t.Fatal("unbounded search window")
		}
		return reader.Read(ctx, store, id, q)
	}}
	var cursor sessionsearch.Cursor
	total := 0
	for range 4 {
		snapshot, err := readHistorySnapshot(t.Context(), owner, id, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Events) > 500 {
			t.Fatal("unbounded source snapshot")
		}
		total += len(snapshot.Events)
		cursor = snapshot.Cursor
		if !snapshot.More {
			break
		}
	}
	if total != 1205 || reads != 3 || cursor.Partial {
		t.Fatalf("source skipped or repeated history: total=%d reads=%d cursor=%+v", total, reads, cursor)
	}
	unchanged, err := readHistorySnapshot(t.Context(), owner, id, cursor)
	if err != nil || len(unchanged.Events) != 0 || reads != 3 {
		t.Fatal("unchanged cold source repeated a read")
	}
}
