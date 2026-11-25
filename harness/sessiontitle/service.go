// Package sessiontitle owns auxiliary naming, outside the Agent turn loop.
package sessiontitle

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"go-bot/harness/agent"
	session "go-bot/harness/session"
	"go-bot/internal/modeldata"
)

const Provider = "session-title-first-prompt-llm"
const System = `Create a concise title for an AI coding-assistant session from the supplied human messages.
Return only the title on one line, **in plain text of natural language**, with no quotes, prefix, explanation, Markdown, XML, or terminal control codes. No code is allowed.
Use the language of the messages.
Aim for about 5 words in non-CJK languages or 10 CJK characters.
If the messages give little to name, still return a short best-effort title, such as Greeting, instead of explaining.`
const MaxOutputTokens = 4096

var ErrEmptyTitle = errors.New("title must not be empty")

type Generate func(context.Context, agent.ModelRoute, string) (string, error)

type Service struct {
	mu       sync.Mutex
	session  *session.LiveSession
	generate Generate
	seen     int
	pending  bool
	closed   bool
	revision uint64
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func New(s *session.LiveSession, generate Generate) (*Service, error) {
	state, err := s.TitleState()
	if err != nil {
		return nil, err
	}
	v := &Service{session: s, generate: generate, seen: state.Count}
	// Loading history can repair the fallback, but cannot schedule an old turn.
	if err := v.fallback(context.Background(), state); err != nil {
		return nil, err
	}
	return v, nil
}

func (s *Service) fallback(ctx context.Context, state session.TitleState) error {
	if state.Current != nil || state.First == nil {
		return nil
	}
	title := session.FallbackTitle(state.First.Text)
	if title == "" {
		return nil
	}
	return s.append(ctx, "session/title", session.SessionTitle{Title: title, MessageSeqs: []uint64{state.First.Seq}, Source: session.TitleSource{Kind: "fallback"}})
}

// Accepted observes newly claimed human messages in the Session log.
func (s *Service) Accepted(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	state, err := s.session.TitleState()
	if err != nil {
		return err
	}
	h := s.session.Header()
	if s.seen == 0 && state.Count > 0 && state.Current == nil && !h.IsSeeded && h.Origin != "subagent" {
		s.pending = s.generate != nil
	}
	s.seen = state.Count
	return s.fallback(ctx, state)
}

func (s *Service) append(ctx context.Context, kind string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err = s.session.Append(session.EventInput{Type: kind, Data: data}); err != nil {
		return err
	}
	return s.session.Flush(ctx)
}

// Start observes the actual model request route, never an options-only guess.
// Auxiliary errors leave the existing fallback and never fail a user turn.
func (s *Service) Start(route agent.ModelRoute) {
	// Inbox admission precedes user/message logging. Observe after the driver
	// claims messages, just before its actual model request.
	if err := s.Accepted(context.Background()); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.pending || route.Provider == "" || route.Model == "" {
		return
	}
	s.pending = false
	state, err := s.session.TitleState()
	if err != nil || state.First == nil || state.Current != nil && state.Current.Source.Kind == "user" {
		return
	}
	message := *state.First
	message.Text = session.TruncateTitleUTF8(message.Text, 4096)
	input, _ := json.Marshal([]session.TitleMessage{message})
	text := "Generate the session title from this JSON array of human messages:\n" + string(input)
	// No route credentials or original conversation system prompt are logged.
	route.ReasoningEffort = ""
	request := map[string]any{"titleProvider": Provider, "messageSeqs": []uint64{message.Seq},
		"route": map[string]string{"provider": route.Provider, "model": route.Model}, "system": System,
		"messages": []any{map[string]any{"role": "user", "source": map[string]string{"kind": "dsh-session-title-llm"}, "content": []any{map[string]string{"type": "text", "text": text}}}}, "maxTokens": MaxOutputTokens}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	if s.append(ctx, "session/title-llm-request", request) != nil {
		cancel()
		return
	}
	s.revision++
	revision := s.revision
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		value, err := s.generate(ctx, route, text)
		if err != nil || ctx.Err() != nil {
			return
		}
		title := GeneratedTitle(value)
		if title == "" {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed || revision != s.revision || ctx.Err() != nil {
			return
		}
		state, err := s.session.TitleState()
		if err != nil || state.Current != nil && state.Current.Source.Kind == "user" {
			return
		}
		_ = s.append(ctx, "session/title", session.SessionTitle{Title: title, MessageSeqs: []uint64{message.Seq},
			Source: session.TitleSource{Kind: "provider", Provider: Provider, Model: &session.ModelSelection{Provider: route.Provider, Model: route.Model}}})
	}()
}

func GeneratedTitle(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, marker := range []string{"***", "**", "*"} {
			if len(line) > len(marker)*2 && strings.HasPrefix(line, marker) && strings.HasSuffix(line, marker) {
				line = strings.TrimSpace(line[len(marker) : len(line)-len(marker)])
				break
			}
		}
		return session.NormalizeTitle(line, 80)
	}
	return ""
}

func (s *Service) Rename(ctx context.Context, text string) (string, error) {
	return s.rename(ctx, text, false)
}

// NameIfEmpty is for a new Schedule destination; recovery must not overwrite
// an existing title, especially one the user has subsequently chosen.
func (s *Service) NameIfEmpty(ctx context.Context, text string) (string, error) {
	return s.rename(ctx, text, true)
}
func (s *Service) rename(ctx context.Context, text string, onlyIfEmpty bool) (string, error) {
	title := session.NormalizeTitle(text, 80)
	if title == "" {
		return "", ErrEmptyTitle
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", errors.New("title service is closed")
	}
	if onlyIfEmpty {
		state, err := s.session.TitleState()
		if err != nil {
			return "", err
		}
		if state.Current != nil {
			return state.Current.Title, nil
		}
	}
	s.pending = false
	s.revision++
	if s.cancel != nil {
		s.cancel()
	}
	err := s.append(ctx, "session/title", session.SessionTitle{Title: title, MessageSeqs: []uint64{}, Source: session.TitleSource{Kind: "user"}})
	return title, err
}

func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.revision++
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

type observedModel struct {
	agent.Model
	before   func(agent.ModelRoute)
	fallback agent.ModelRoute
}

func (m observedModel) CheckRoute(ctx context.Context, route agent.ModelRoute) error {
	if checker, ok := m.Model.(interface {
		CheckRoute(context.Context, agent.ModelRoute) error
	}); ok {
		return checker.CheckRoute(ctx, route)
	}
	return errors.New("model route preflight is unavailable")
}
func (m observedModel) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolSpec) (modeldata.AssistantMessage, error) {
	route, ok := agent.ModelRouteFromContext(ctx)
	if !ok {
		route = m.fallback
	}
	if agent.ModelCallOptionsFromContext(ctx).Purpose == "" {
		m.before(route)
	}
	return m.Model.Complete(ctx, messages, tools)
}

type observedStream struct {
	observedModel
	stream agent.StreamingModel
}

func (m observedStream) StreamChunks(ctx context.Context, messages []agent.Message, tools []agent.ToolSpec, sink agent.ModelStreamSink) (modeldata.AssistantMessage, error) {
	route, ok := agent.ModelRouteFromContext(ctx)
	if !ok {
		route = m.fallback
	}
	m.before(route)
	return m.stream.StreamChunks(ctx, messages, tools, sink)
}
func Observe(model agent.Model, fallback agent.ModelRoute, before func(agent.ModelRoute)) agent.Model {
	m := observedModel{Model: model, before: before, fallback: fallback}
	if stream, ok := model.(agent.StreamingModel); ok {
		return observedStream{observedModel: m, stream: stream}
	}
	return m
}
