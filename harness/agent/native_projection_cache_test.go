package agent

import (
	"encoding/json"
	"fmt"
	"testing"

	"go-bot/harness/session"
)

func TestNativeModelCacheDetachesAndInvalidatesProjectedContent(t *testing.T) {
	s, err := sessionformat.NewLiveSession(sessionformat.Header{Type: "session", Version: 4, ID: "cache", CreatedAt: 1}, &nativeTestJournal{})
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"id":"image","role":"user","source":{"kind":"user"},"content":[{"type":"text","text":"hello"},{"type":"image","attachment":{"attachmentId":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","mediaType":"image/png","bytes":1,"width":1,"height":1}}]}`)
	e, err := s.Append(sessionformat.EventInput{Type: "user/message", Data: raw, SurfaceOp: json.RawMessage(`"append"`)})
	if err != nil {
		t.Fatal(err)
	}
	loop := &NativeLoop{session: s}
	first, err := loop.modelMessages()
	if err != nil {
		t.Fatal(err)
	}
	first[0].ContentBlocks[0].Text = "corrupted"
	second, err := loop.modelMessages()
	if err != nil || second[0].ContentBlocks[0].Text != "hello" {
		t.Fatalf("cached message mutated: %+v %v", second, err)
	}
	_, err = s.Append(sessionformat.EventInput{Type: "image/offload", Data: json.RawMessage(`{"targets":[{"seq":` + jsonNumber(e.Seq) + `,"imageIndexes":[0]}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	third, err := loop.modelMessages()
	if err != nil || !third[0].IsImageOffloaded(0) {
		t.Fatalf("offload did not invalidate cached message: %+v %v", third, err)
	}
	third[0].OffloadedImageIndexes[99] = struct{}{}
	fourth, err := loop.modelMessages()
	if err != nil || fourth[0].IsImageOffloaded(99) {
		t.Fatal("offload map borrowed from cache")
	}
}
func jsonNumber(v uint64) string { raw, _ := json.Marshal(v); return string(raw) }

func TestNativeModelCacheEvictsReplacedSurfaceNodes(t *testing.T) {
	s, err := sessionformat.NewLiveSession(sessionformat.Header{Type: "session", Version: 4, ID: "replace-cache", CreatedAt: 1}, &nativeTestJournal{})
	if err != nil {
		t.Fatal(err)
	}
	appendText := func(id, text string, op json.RawMessage, sources []uint64) {
		t.Helper()
		raw, _ := json.Marshal(nativeTextMessage(id, "user", map[string]any{"kind": "user"}, text))
		if _, err := s.Append(sessionformat.EventInput{Type: "user/message", Data: raw, SurfaceOp: op, SourceEventSeqs: sources}); err != nil {
			t.Fatal(err)
		}
	}
	appendText("a", "first", json.RawMessage(`"append"`), nil)
	appendText("b", "second", json.RawMessage(`"append"`), nil)
	loop := &NativeLoop{session: s}
	if _, err := loop.modelMessages(); err != nil {
		t.Fatal(err)
	}
	appendText("summary", "summary", json.RawMessage(`{"op":"replace","startSeq":0,"endSeq":1}`), []uint64{0, 1})
	projected, err := loop.modelMessages()
	if err != nil || len(projected) != 1 || projected[0].Content != "summary" || len(loop.modelProjection) != 1 {
		t.Fatalf("replacement retained old cache: %+v %v", projected, err)
	}
	if _, exists := loop.modelProjection[0]; exists {
		t.Fatal("shadowed content retained")
	}
}
func BenchmarkNativeModelProjectionWithLongHiddenHistory(b *testing.B) {
	for _, count := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			s, err := sessionformat.NewLiveSession(sessionformat.Header{Type: "session", Version: 4, ID: "model-bench", CreatedAt: 1}, &nativeTestJournal{})
			if err != nil {
				b.Fatal(err)
			}
			for range count {
				if _, err := s.Append(sessionformat.EventInput{Type: "bench/hidden", Ignorable: true, Data: json.RawMessage(`{"payload":"padding"}`)}); err != nil {
					b.Fatal(err)
				}
			}
			raw, _ := json.Marshal(nativeTextMessage("current", "user", map[string]any{"kind": "user"}, "current visible context"))
			if _, err := s.Append(sessionformat.EventInput{Type: "user/message", Data: raw, SurfaceOp: json.RawMessage(`"append"`)}); err != nil {
				b.Fatal(err)
			}
			loop := &NativeLoop{session: s}
			if _, err := loop.modelMessages(); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := loop.modelMessages(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
