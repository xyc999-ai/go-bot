package sessionformat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func indexedHistoryFixture(t testing.TB, padding int) (FileStore, *LiveSession, string) {
	t.Helper()
	store := FileStore{Root: filepath.Join(t.TempDir(), "sessions"), ProjectCWD: "/workspace/history", Claim: v4Claim}
	header := v4TestHeader("history")
	header.CWD = store.ProjectCWD
	journal, err := store.Create(context.Background(), header)
	if err != nil {
		t.Fatal(err)
	}
	live, err := NewLiveSession(header, journal)
	if err != nil {
		t.Fatal(err)
	}
	input := paddingInput()
	input.Data = json.RawMessage(`{"payload":"` + strings.Repeat("x", 512) + `"}`)
	for range padding {
		if _, err := live.Append(input); err != nil {
			t.Fatal(err)
		}
	}
	for turn := 1; turn <= 4; turn++ {
		for _, input := range []EventInput{
			{Type: "turn/start", Data: json.RawMessage(fmt.Sprintf(`{"turn":%d}`, turn))},
			{Type: "step/start", Data: json.RawMessage(fmt.Sprintf(`{"turn":%d,"step":1}`, turn))},
			{Type: "user/message", Data: json.RawMessage(fmt.Sprintf(`{"id":"u%d","role":"user","source":{"kind":"user"},"content":[{"type":"text","text":"Prompt %d"}]}`, turn, turn)), SurfaceOp: json.RawMessage(`"append"`)},
			{Type: "step/end", Data: json.RawMessage(fmt.Sprintf(`{"turn":%d,"step":1}`, turn))},
			{Type: "turn/end", Data: json.RawMessage(fmt.Sprintf(`{"turn":%d,"reason":{"kind":"completed"}}`, turn))},
		} {
			if _, err := live.Append(input); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := live.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	path, err := store.Path(header.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Close() })
	return store, live, path
}
func TestIndexedFileHistoryReusesLargePrefixAndReturnsBoundedPages(t *testing.T) {
	store, live, path := indexedHistoryFixture(t, 17000)
	var builds atomic.Int32
	reader := &FileHistoryReader{Configure: func(*LiveSession) error { builds.Add(1); return nil }}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if before.Size() <= 8<<20 {
		t.Fatal("fixture must exceed the previous whole-artifact cache limit")
	}
	first, err := reader.Read(t.Context(), store, "history", HistoryRequest{Kind: HistoryEvents, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	cursor := uint64(17000)
	next, err := reader.Read(t.Context(), store, "history", HistoryRequest{Kind: HistoryEvents, After: &cursor, Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	if builds.Load() != 1 || len(first.Events) != 3 || len(next.Events) != 4 || next.Events[0].Seq != cursor+1 || next.Count != uint64(len(live.Events())) {
		t.Fatal("history page rebuilt/copied the full prefix")
	}
	entry := reader.entries[path]
	if len(entry.control.Events) != 0 || len(entry.offsets) != int(next.Count)+1 {
		t.Fatal("cached raw event bodies instead of an offset index")
	}
	if next.TitleHint != "Prompt 1" || next.Boundary.Open || next.Boundary.LastTurn != 4 {
		t.Fatalf("bad projected state: %+v", next)
	}
	next.Views["bad"] = json.RawMessage(`true`)
	next.Inbox.NextTurn = append(next.Inbox.NextTurn, json.RawMessage(`{}`))
	third, err := reader.Read(t.Context(), store, "history", HistoryRequest{Kind: HistoryState})
	if err != nil || len(third.Inbox.NextTurn) != 0 || third.Views["bad"] != nil {
		t.Fatal("mutable snapshot alias")
	}
	after, err := os.Stat(path)
	if err != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("read modified the journal")
	}
}
func TestHistoryTurnWindowsMatchOriginalTurnCutsAndDurablePrefix(t *testing.T) {
	store, live, _ := indexedHistoryFixture(t, 3)
	reader := &FileHistoryReader{}
	window, err := reader.Read(t.Context(), store, "history", HistoryRequest{Kind: HistoryTurns, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if window.Before == nil || *window.Before != 13 || len(window.Events) != 10 {
		t.Fatalf("wrong latest cut: %+v", window)
	}
	older, err := reader.Read(t.Context(), store, "history", HistoryRequest{Kind: HistoryTurns, Before: window.Before, Limit: 2})
	if err != nil || older.Before != nil || len(older.Events) != 10 || older.Events[0].Seq != 3 {
		t.Fatalf("wrong older cut: %+v %v", older, err)
	}
	bad := uint64(4)
	if _, err := reader.Read(t.Context(), store, "history", HistoryRequest{Kind: HistoryTurns, Before: &bad, Limit: 2}); !errors.Is(err, ErrHistoryCursor) {
		t.Fatal("accepted cursor inside a turn")
	}
	expected, err := live.HistoryWindow(t.Context(), HistoryRequest{Kind: HistoryTurns, Limit: 2})
	if err != nil || !reflect.DeepEqual(expected, window) {
		t.Fatal("live and file cuts differ")
	}
	if _, err := live.Append(paddingInput()); err != nil {
		t.Fatal(err)
	}
	still, err := live.HistoryWindow(t.Context(), HistoryRequest{Kind: HistoryState})
	if err != nil || still.Count != window.Count {
		t.Fatal("unflushed event escaped history cut")
	}
	if err := live.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	advanced, err := reader.Read(t.Context(), store, "history", HistoryRequest{Kind: HistoryState})
	if err != nil || advanced.Count != window.Count+1 {
		t.Fatalf("append did not invalidate index: %+v %v", advanced, err)
	}
}
func TestIndexedHistoryInvalidatesReplacementAndRejectsCorruption(t *testing.T) {
	store, live, path := indexedHistoryFixture(t, 1)
	reader := &FileHistoryReader{}
	first, err := reader.Read(t.Context(), store, "history", HistoryRequest{Kind: HistoryState})
	if err != nil {
		t.Fatal(err)
	}
	if err := live.Close(); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := []byte(strings.Replace(string(original), "Prompt 1", "Changed!", 1))
	temporary := path + ".replacement"
	if err := os.WriteFile(temporary, replacement, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, path); err != nil {
		t.Fatal(err)
	}
	changed, err := reader.Read(t.Context(), store, "history", HistoryRequest{Kind: HistoryState})
	if err != nil || changed.TitleHint == first.TitleHint {
		t.Fatal("replaced inode reused old state")
	}
	// Damage before a committed turn/end must not be hidden by a warm index.
	damaged := []byte(strings.Replace(string(replacement), `"type":"turn/start"`, `"type":"turn/bogus"`, 1))
	if err := os.WriteFile(path, damaged, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(t.Context(), store, "history", HistoryRequest{Kind: HistoryEvents, Limit: 2}); err == nil {
		t.Fatal("returned cached page after log corruption")
	}
}
func TestIndexedHistoryLeavesTornTailAndRebuildsAfterRepair(t *testing.T) {
	store, live, path := indexedHistoryFixture(t, 0)
	if err := live.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString(`{"type":"user/message"`)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	reader := &FileHistoryReader{}
	window, err := reader.Read(t.Context(), store, "history", HistoryRequest{Kind: HistoryState})
	if err != nil || window.Count != 20 {
		t.Fatalf("torn tail: %+v %v", window, err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("history read repaired the file")
	}
	journal, artifact, err := store.Open(t.Context(), "history")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreLiveSession(artifact, journal)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := restored.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	repaired, err := reader.Read(t.Context(), store, "history", HistoryRequest{Kind: HistoryState})
	if err != nil || repaired.Count <= window.Count {
		t.Fatal("repair did not invalidate observation")
	}
}
func TestIndexedHistoryConcurrentQueries(t *testing.T) {
	store, _, _ := indexedHistoryFixture(t, 10)
	reader := &FileHistoryReader{}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 10 {
				window, err := reader.Read(t.Context(), store, "history", HistoryRequest{Kind: HistoryEvents, Limit: 2})
				if err != nil || len(window.Events) != 2 {
					t.Error("concurrent indexed read failed", err)
				}
			}
		})
	}
	wg.Wait()
}
func BenchmarkIndexedHistoryWindow(b *testing.B) {
	for _, count := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			store, _, _ := indexedHistoryFixture(b, count)
			reader := &FileHistoryReader{}
			request := HistoryRequest{Kind: HistoryTurns, Limit: 2}
			if _, err := reader.Read(b.Context(), store, "history", request); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := reader.Read(b.Context(), store, "history", request); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkColdObservationAndRestore(b *testing.B) {
	for _, count := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			store, _, _ := indexedHistoryFixture(b, count)
			artifact, err := store.ReadArtifact(b.Context(), "history")
			if err != nil {
				b.Fatal(err)
			}
			b.Run("cold-file-state", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					reader := &FileHistoryReader{}
					if _, err := reader.Read(b.Context(), store, "history", HistoryRequest{Kind: HistoryState}); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("restore-decoded-prefix", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := RestoreLiveSession(artifact, &projectionJournal{}); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}
