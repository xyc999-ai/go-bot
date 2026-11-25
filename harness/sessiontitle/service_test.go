package sessiontitle

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go-bot/harness/agent"
	session "go-bot/harness/session"
)

type journal struct{}

func (journal) Append(context.Context, []session.Event) error { return nil }
func (journal) Close() error                                  { return nil }

func newSession(t *testing.T, generator Generate) (*session.LiveSession, *Service) {
	t.Helper()
	s, err := session.NewLiveSession(session.Header{Type: "session", Version: 4, CreatedAt: 1, ID: "titles", CWD: "/tmp"}, journal{})
	if err != nil {
		t.Fatal(err)
	}
	v, err := New(s, generator)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close(); _ = s.Close() })
	return s, v
}
func human(t *testing.T, s *session.LiveSession, kind, text string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"id": "m", "role": "user", "source": map[string]string{"kind": kind}, "content": []any{map[string]string{"type": "text", "text": text}}})
	if _, err := s.Append(session.EventInput{Type: "user/message", Data: raw, SurfaceOp: json.RawMessage(`"append"`)}); err != nil {
		t.Fatal(err)
	}
}
func titleOf(t *testing.T, s *session.LiveSession) *session.SessionTitle {
	t.Helper()
	state, err := s.TitleState()
	if err != nil {
		t.Fatal(err)
	}
	return state.Current
}
func until(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestFirstHumanRequestGeneratesOnceAndStaysOffSurface(t *testing.T) {
	var calls atomic.Int32
	s, v := newSession(t, func(_ context.Context, route agent.ModelRoute, input string) (string, error) {
		calls.Add(1)
		if route.Provider != "selected" || route.Model != "model" || route.ReasoningEffort != "" || !strings.Contains(input, "请帮我整理一份行业报告") || strings.Contains(input, "second") {
			t.Error("wrong title input or route", route, input)
		}
		return "**AI 行业调研报告**\nignored", nil
	})
	human(t, s, "schedule", "not a human title")
	if err := v.Accepted(t.Context()); err != nil {
		t.Fatal(err)
	}
	if titleOf(t, s) != nil {
		t.Fatal("internal delivery produced a title")
	}
	human(t, s, "user", "请帮我整理一份行业报告")
	human(t, s, "user", "second message claimed in the same batch")
	v.Start(agent.ModelRoute{Provider: "selected", Model: "model", ReasoningEffort: "high"})
	until(t, func() bool { title := titleOf(t, s); return title != nil && title.Source.Kind == "provider" })
	title := titleOf(t, s)
	if title.Title != "AI 行业调研报告" || len(title.MessageSeqs) != 1 || title.MessageSeqs[0] != 1 {
		t.Fatal(title)
	}
	human(t, s, "user", "third")
	v.Start(agent.ModelRoute{Provider: "selected", Model: "model"})
	if calls.Load() != 1 {
		t.Fatal("regenerated title for a later prompt")
	}
	if err := session.ValidateV4Relationships(s.Events()); err != nil {
		t.Fatal("invalid persisted references", err)
	}
	surface, err := s.Surface()
	if err != nil {
		t.Fatal(err)
	}
	if len(surface.Messages) != 4 {
		t.Fatal("title metadata polluted model surface", len(surface.Messages))
	}
	requests := 0
	for _, e := range s.DurableEvents() {
		if e.Type == "session/title-llm-request" {
			requests++
			if strings.Contains(string(e.Data), "apiKey") {
				t.Fatal("credentials logged")
			}
		}
	}
	if requests != 1 {
		t.Fatal("missing auxiliary request audit", requests)
	}
}

func TestManualRenameCancelsAndWinsAgainstLateProvider(t *testing.T) {
	entered, release, canceled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	s, v := newSession(t, func(ctx context.Context, _ agent.ModelRoute, _ string) (string, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return "late model title", nil
	})
	human(t, s, "user", "Please create a concise report about AI")
	v.Start(agent.ModelRoute{Provider: "p", Model: "m"})
	<-entered
	if fallback := titleOf(t, s); fallback.Title != "Please create a concise report" || fallback.Source.Kind != "fallback" {
		t.Fatal(fallback)
	}
	if title, err := v.Rename(t.Context(), "  My\nchosen title  "); err != nil || title != "My chosen title" {
		t.Fatal(title, err)
	}
	if title, err := v.NameIfEmpty(t.Context(), "Schedule destination title"); err != nil || title != "My chosen title" {
		t.Fatal("schedule recovery overwrote an explicit name", title, err)
	}
	<-canceled
	close(release)
	v.Close()
	if title := titleOf(t, s); title.Title != "My chosen title" || title.Source.Kind != "user" || len(title.MessageSeqs) != 0 {
		t.Fatal(title)
	}
}

func TestFailureKeepsFallbackAndResumeDoesNotRetry(t *testing.T) {
	s, v := newSession(t, func(context.Context, agent.ModelRoute, string) (string, error) { return "", errors.New("unavailable") })
	human(t, s, "user", "Investigate the release build failure")
	v.Start(agent.ModelRoute{Provider: "p", Model: "m"})
	v.Close()
	artifact := session.PhysicalArtifact{Header: s.Header(), Events: s.DurableEvents()}
	restored, err := session.RestoreLiveSession(artifact, journal{})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var calls atomic.Int32
	resumed, err := New(restored, func(context.Context, agent.ModelRoute, string) (string, error) { calls.Add(1); return "bad retry", nil })
	if err != nil {
		t.Fatal(err)
	}
	human(t, restored, "user", "follow up")
	resumed.Start(agent.ModelRoute{Provider: "p", Model: "m"})
	resumed.Close()
	if calls.Load() != 0 || titleOf(t, restored).Source.Kind != "fallback" {
		t.Fatal("resume retried automatic generation")
	}
}

func TestCloseCancelsAuxiliaryRequest(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	s, v := newSession(t, func(ctx context.Context, _ agent.ModelRoute, _ string) (string, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		return "", ctx.Err()
	})
	human(t, s, "user", "hello")
	v.Start(agent.ModelRoute{Provider: "p", Model: "m"})
	<-entered
	v.Close()
	select {
	case <-canceled:
	default:
		t.Fatal("close returned before the model request exited")
	}
}

func TestTitleNormalization(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"\x1b[31mHello\x1b[0m\n world\u202e", "Hello world"},
		{"\x1b]0;hidden\aVisible\x1b]unfinished", "Visible"},
		{"\u009dhidden\x1b\\Safe", "Safe"},
	} {
		if got := session.NormalizeTitle(test.input, 80); got != test.want {
			t.Fatalf("%q != %q", got, test.want)
		}
	}
	if got := session.NormalizeTitle("中文标题", 7); got != "中文" {
		t.Fatal("split UTF-8", got)
	}
	if got := session.FallbackTitle(strings.Repeat("测", 40)); len([]rune(got)) != 13 {
		t.Fatal(got)
	}
	if got := GeneratedTitle("\n ***A useful title***\nexplanation"); got != "A useful title" {
		t.Fatal(got)
	}
}
