package interaction

import (
	"context"
	"encoding/json"
	"fmt"
	"go-bot/harness/session"
	"testing"
)

type questionBenchmarkJournal struct{}

func (questionBenchmarkJournal) Append(context.Context, []sessionformat.Event) error { return nil }
func (questionBenchmarkJournal) Close() error                                        { return nil }
func BenchmarkQuestionProjectionWithLongHistory(b *testing.B) {
	for _, count := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			s, err := sessionformat.NewLiveSession(sessionformat.Header{Type: "session", Version: 4, ID: "questions-benchmark", CreatedAt: 1}, questionBenchmarkJournal{})
			if err != nil {
				b.Fatal(err)
			}
			if _, err := RegisterQuestionProjection(s); err != nil {
				b.Fatal(err)
			}
			for range count {
				if _, err := s.Append(sessionformat.EventInput{Type: "bench/hidden", Ignorable: true, Data: json.RawMessage(`{"payload":"padding"}`)}); err != nil {
					b.Fatal(err)
				}
			}
			if err := s.Flush(b.Context()); err != nil {
				b.Fatal(err)
			}
			if _, err := SessionQuestions(s); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := SessionQuestions(s); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
