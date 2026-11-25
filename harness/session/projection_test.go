package sessionformat

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type projectionJournal struct{ fail error }

func (j *projectionJournal) Append(context.Context, []Event) error { return j.fail }
func (*projectionJournal) Close() error                            { return nil }
func projectionSession(t testing.TB) *LiveSession {
	t.Helper()
	s, err := NewLiveSession(Header{Type: "session", Version: 4, ID: "projection", CreatedAt: 1}, &projectionJournal{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func paddingInput() EventInput {
	return EventInput{Type: "test/padding", Data: json.RawMessage(`{"payload":"padding"}`), Ignorable: true}
}
func goalInput(operation string, revision uint64) EventInput {
	goal := GoalSnapshot{ID: "goal-test", Revision: revision, Objective: "finish", Phase: "active", MaxGoalRounds: 256}
	raw, _ := json.Marshal(GoalChange{Kind: "goal/change", Version: 1, Operation: operation, Goal: &goal, CreatedAt: 1, UpdatedAt: 1})
	return EventInput{Type: "goal/change", Data: raw}
}

func TestProjectionConsumesEachPrefixOnceAndSeparatesDurability(t *testing.T) {
	s := projectionSession(t)
	calls := 0
	def := &ProjectionDefinition[int]{Key: "count", Version: 1, Init: func(Header, uint64) int { return 0 }, Apply: func(n int, _ Event) (int, error) { calls++; return n + 1, nil }}
	for range 3 {
		if _, err := s.Append(paddingInput()); err != nil {
			t.Fatal(err)
		}
	}
	h, err := RegisterProjection(s, def)
	if err != nil {
		t.Fatal(err)
	}
	get := func(cut ProjectionCut) int {
		v, err := ReadProjection(h, cut, func(n int) int { return n })
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if calls != 3 || get(LiveCut) != 3 || get(DurableCut) != 0 {
		t.Fatalf("initial calls=%d", calls)
	}
	for range 10 {
		if get(LiveCut) != 3 {
			t.Fatal("changed")
		}
	}
	if calls != 3 {
		t.Fatalf("replayed cached prefix: %d", calls)
	}
	if _, err := s.Append(paddingInput()); err != nil {
		t.Fatal(err)
	}
	if calls != 4 || get(DurableCut) != 0 {
		t.Fatal("live event leaked into durable state")
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if get(DurableCut) != 4 || calls != 8 {
		t.Fatalf("durable catchup calls=%d", calls)
	}
	if get(DurableCut) != 4 || calls != 8 {
		t.Fatal("durable prefix was replayed twice")
	}
	conflict := *def
	if _, err := RegisterProjection(s, &conflict); err == nil {
		t.Fatal("conflicting owner accepted")
	}
}

func TestGoalProjectionBatchRollbackAndDetachedRead(t *testing.T) {
	s := projectionSession(t)
	if _, err := s.AppendBatch([]EventInput{goalInput("create", 1), goalInput("pause", 3)}); err == nil {
		t.Fatal("invalid batch accepted")
	}
	if state, err := s.Goal(); err != nil || state.Goal != nil || len(s.Events()) != 0 {
		t.Fatalf("rejected batch leaked: %+v %v", state, err)
	}
	if _, err := s.Append(goalInput("create", 1)); err != nil {
		t.Fatalf("rejected batch leaked seen IDs: %v", err)
	}
	state, _ := s.Goal()
	state.Goal.Objective = "corrupted"
	state.LastRef.Revision = 100
	actual, _ := s.Goal()
	if actual.Goal.Objective != "finish" || actual.LastRef.Revision != 1 {
		t.Fatal("borrowed mutable state")
	}
	def := &ProjectionDefinition[map[string]int]{Key: "mutable-count", Version: 1, Init: func(Header, uint64) map[string]int { return map[string]int{} }, Apply: func(s map[string]int, e Event) (map[string]int, error) { s[e.Type]++; return s, nil }}
	h, err := RegisterProjection(s, def)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendBatch([]EventInput{paddingInput(), goalInput("pause", 7)}); err == nil {
		t.Fatal("invalid suffix accepted")
	}
	count, err := ReadProjection(h, LiveCut, func(v map[string]int) int { return v["test/padding"] })
	if err != nil || count != 0 {
		t.Fatal("rejected suffix drove mutable domain state")
	}
}

func TestGoalProjectionMatchesEveryFoldPrefix(t *testing.T) {
	s := projectionSession(t)
	for _, input := range []EventInput{goalInput("create", 1), paddingInput(), goalInput("edit", 2), paddingInput()} {
		if _, err := s.Append(input); err != nil {
			t.Fatal(err)
		}
		expected, err := FoldGoal(s.Events())
		if err != nil {
			t.Fatal(err)
		}
		actual, err := s.Goal()
		if err != nil || !reflect.DeepEqual(actual, expected) {
			t.Fatalf("got %+v want %+v: %v", actual, expected, err)
		}
	}
}

func TestProjectionConcurrentReadsAndFlush(t *testing.T) {
	s := projectionSession(t)
	if _, err := s.Append(goalInput("create", 1)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 100 {
				_, _ = s.Goal()
				_ = s.Permission()
				_ = s.TurnBoundary(LiveCut)
				_ = s.DurableActivity()
			}
		})
	}
	wg.Go(func() {
		for range 100 {
			if _, err := s.Append(paddingInput()); err != nil {
				t.Error(err)
			}
			if err := s.Flush(context.Background()); err != nil {
				t.Error(err)
			}
		}
	})
	wg.Wait()
}

func BenchmarkGoalProjection(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			s := projectionSession(b)
			if _, err := s.Append(goalInput("create", 1)); err != nil {
				b.Fatal(err)
			}
			input := paddingInput()
			input.Data = json.RawMessage(`{"payload":"` + strings.Repeat("x", 512) + `"}`)
			for range n {
				if _, err := s.Append(input); err != nil {
					b.Fatal(err)
				}
			}
			b.Run("cached", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := s.Goal(); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("full-replay", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := FoldGoal(s.Events()); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("append-edit", func(b *testing.B) {
				revision := uint64(2)
				b.ReportAllocs()
				for b.Loop() {
					if _, err := s.Append(goalInput("edit", revision)); err != nil {
						b.Fatal(err)
					}
					revision++
				}
			})
		})
	}
}

func TestProjectionConfirmedRollbackDoesNotAdvanceDurableCut(t *testing.T) {
	s := projectionSession(t)
	journal := s.journal.(*projectionJournal)
	def := &ProjectionDefinition[int]{Key: "durable-count", Version: 1, Init: func(Header, uint64) int { return 0 }, Apply: func(n int, _ Event) (int, error) { return n + 1, nil }}
	h, err := RegisterProjection(s, def)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(paddingInput()); err != nil {
		t.Fatal(err)
	}
	journal.fail = ErrV4WriteRolledBack
	if err := s.Flush(context.Background()); err == nil {
		t.Fatal("expected rollback")
	}
	value, err := ReadProjection(h, DurableCut, func(v int) int { return v })
	if err != nil || value != 0 {
		t.Fatal("failed flush exposed a phantom durable event")
	}
	journal.fail = nil
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	value, err = ReadProjection(h, DurableCut, func(v int) int { return v })
	if err != nil || value != 1 {
		t.Fatal("confirmed retry failed to advance exactly once")
	}
}

func TestModelSurfaceKeepsOnlyCurrentContextMetadata(t *testing.T) {
	s := projectionSession(t)
	message := func(id, text string) json.RawMessage {
		raw, _ := json.Marshal(map[string]any{"id": id, "role": "user", "source": map[string]string{"kind": "user"}, "content": []map[string]string{{"type": "text", "text": text}}})
		return raw
	}
	if _, err := s.Append(EventInput{Type: "user/message", Data: message("first", "old"), SurfaceOp: json.RawMessage(`"append"`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(EventInput{Type: "user/message", Data: message("second", "new"), SurfaceOp: json.RawMessage(`{"op":"replace","startSeq":0,"endSeq":0}`), SourceEventSeqs: []uint64{0}}); err != nil {
		t.Fatal(err)
	}
	diagnostic, err := s.Surface()
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.ModelSurface()
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostic.Replacements) != 1 || len(current.Replacements) != 0 || len(current.NodeTypes) != 1 || len(current.NodeMessages) != 1 || len(current.Messages) != 1 || !reflect.DeepEqual(current.Messages, diagnostic.Messages) {
		t.Fatal("model view lost context or retained history diagnostics")
	}
	current.NodeMessages[1][0] = '!'
	again, err := s.ModelSurface()
	if err != nil || again.NodeMessages[1][0] == '!' {
		t.Fatal("model node data aliases the live log")
	}
}

func BenchmarkSessionProjectionReadsAndAppend(b *testing.B) {
	for _, count := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			s := projectionSession(b)
			for range count {
				if _, err := s.Append(paddingInput()); err != nil {
					b.Fatal(err)
				}
			}
			if err := s.Flush(b.Context()); err != nil {
				b.Fatal(err)
			}
			if _, err := s.Usage(); err != nil {
				b.Fatal(err)
			}
			b.Run("permission", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					_ = s.Permission()
				}
			})
			b.Run("plan", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := s.Plan(); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("usage", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := s.Usage(); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("unrelated-append", func(b *testing.B) {
				input := paddingInput()
				b.ReportAllocs()
				for b.Loop() {
					if _, err := s.Append(input); err != nil {
						b.Fatal(err)
					}
				}
			})
			for _, size := range []int{1, 32} {
				b.Run(fmt.Sprintf("batch-%d", size), func(b *testing.B) {
					inputs := make([]EventInput, size)
					for i := range inputs {
						inputs[i] = paddingInput()
					}
					b.ReportAllocs()
					for b.Loop() {
						if _, err := s.AppendBatch(inputs); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}

func BenchmarkGoalWithLargeHistoricalToolResult(b *testing.B) {
	for _, megabytes := range []int{1, 16} {
		b.Run(fmt.Sprintf("%dMiB", megabytes), func(b *testing.B) {
			s := projectionSession(b)
			events := coreToolExchange()
			var result map[string]json.RawMessage
			if err := json.Unmarshal(events[4].Data, &result); err != nil {
				b.Fatal(err)
			}
			var message map[string]any
			if err := json.Unmarshal(result["message"], &message); err != nil {
				b.Fatal(err)
			}
			message["content"] = []map[string]string{{"type": "text", "text": strings.Repeat("x", megabytes<<20)}}
			result["message"], _ = json.Marshal(message)
			events[4].Data, _ = json.Marshal(result)
			for _, event := range events {
				input := EventInput{Type: event.Type, Data: event.Data, SurfaceOp: event.SurfaceOp}
				if event.Type == "tool/result" {
					input.SourceEventSeqs = []uint64{3}
				}
				if _, err := s.Append(input); err != nil {
					b.Fatal(err)
				}
			}
			if _, err := s.Append(goalInput("create", 1)); err != nil {
				b.Fatal(err)
			}
			b.Run("cached", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := s.Goal(); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("full-replay", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := FoldGoal(s.Events()); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

func BenchmarkGoalRoundAdmission(b *testing.B) {
	for _, count := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			s := projectionSession(b)
			for range count {
				if _, err := s.Append(paddingInput()); err != nil {
					b.Fatal(err)
				}
			}
			goal := GoalSnapshot{ID: "g", Revision: 1, Objective: "continue", Phase: "active", MaxGoalRounds: 1<<53 - 1}
			raw, _ := json.Marshal(GoalChange{Kind: "goal/change", Version: 1, Operation: "create", Goal: &goal, CreatedAt: 1, UpdatedAt: 1})
			if _, err := s.Append(EventInput{Type: "goal/change", Data: raw}); err != nil {
				b.Fatal(err)
			}
			if _, err := s.Append(EventInput{Type: "turn/start", Data: json.RawMessage(`{"turn":1}`)}); err != nil {
				b.Fatal(err)
			}
			if _, err := s.Append(EventInput{Type: "step/start", Data: json.RawMessage(`{"turn":1,"step":1}`)}); err != nil {
				b.Fatal(err)
			}
			round := uint64(1)
			b.ReportAllocs()
			for b.Loop() {
				raw := json.RawMessage(fmt.Sprintf(`{"id":"round-%d","role":"user","source":{"kind":"goal","goalId":"g","revision":1,"round":%d},"content":[{"type":"text","text":"continue"}]}`, round, round))
				if _, err := s.Append(EventInput{Type: "user/message", Data: raw, SurfaceOp: json.RawMessage(`"append"`)}); err != nil {
					b.Fatal(err)
				}
				round++
			}
		})
	}
}
