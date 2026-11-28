package authorization

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
)

type PendingPrompt struct {
	ID string `json:"id"`
	Prompt
}
type View struct {
	ID      string          `json:"id"`
	Key     string          `json:"key"`
	Label   string          `json:"label"`
	Method  string          `json:"method"`
	Status  string          `json:"status"`
	Notices []Notice        `json:"notices"`
	Prompts []PendingPrompt `json:"prompts"`
	Error   string          `json:"error,omitempty"`
}

// EntryView adds only this HTTP surface's resumable interaction handle. The
// common Service remains the owner of authorization admission and completion.
type EntryView struct {
	Entry
	Configured    bool   `json:"configured"`
	AccountLabel  string `json:"accountLabel,omitempty"`
	AttemptID     string `json:"attemptId,omitempty"`
	AttemptStatus string `json:"attemptStatus,omitempty"`
}
type answer struct {
	value   string
	decline bool
}
type pendingPrompt struct {
	view   PendingPrompt
	answer chan answer
}
type surfaceAttempt struct {
	view         View
	cancel       context.CancelFunc
	done         chan struct{}
	failure      error
	pending      map[string]*pendingPrompt
	promptOrder  []string
	secrets      []string
	ctx          context.Context
	callbackBase string
	callback     *surfaceCallback
}

// Surface owns transient interaction data for the Host's own HTTP clients.
// Nothing here is persisted or copied into account configuration.
type Surface struct {
	service  *Service
	ctx      context.Context
	mu       sync.Mutex
	attempts map[string]*surfaceAttempt
	latest   map[string]string
	changed  func()
}

func NewSurface(ctx context.Context, service *Service, changed func()) *Surface {
	return &Surface{service: service, ctx: ctx, attempts: map[string]*surfaceAttempt{}, latest: map[string]string{}, changed: changed}
}
func (s *Surface) change() {
	if s.changed != nil {
		s.changed()
	}
}
func (s *Surface) copy(a *surfaceAttempt) View {
	v := a.view
	v.Notices = append([]Notice{}, v.Notices...)
	v.Prompts = make([]PendingPrompt, 0, len(a.pending))
	for _, id := range a.promptOrder {
		p := a.pending[id]
		if p == nil {
			continue
		}
		copy := p.view
		if copy.Loopback != nil {
			value := *copy.Loopback
			copy.Loopback = &value
		}
		copy.Options = append([]Option(nil), p.view.Options...)
		v.Prompts = append(v.Prompts, copy)
	}
	return v
}
func (s *Surface) start(key, method string) (View, error) {
	return s.startWithCallback(key, method, "")
}

func (s *Surface) startWithCallback(key, method, callbackBase string) (View, error) {
	entry := s.service.Describe(key)
	if entry == nil {
		return View{}, failure("NO_FLOW", "No authorization flow is registered for this credential")
	}
	if method == "" {
		method = entry.Methods[0].ID
	}
	s.mu.Lock()
	for id, a := range s.attempts {
		if len(s.attempts) < 32 {
			break
		}
		if a.view.Status != "running" {
			delete(s.attempts, id)
			if s.latest[a.view.Key] == id {
				delete(s.latest, a.view.Key)
			}
		}
	}
	if len(s.attempts) >= 64 {
		s.mu.Unlock()
		return View{}, failure("AUTHORIZATION_BUSY", "Too many authorization attempts")
	}
	ctx, cancel := context.WithCancel(s.ctx)
	a := &surfaceAttempt{view: View{ID: uuid.NewString(), Key: key, Label: entry.Label, Method: method, Status: "running", Notices: []Notice{}}, cancel: cancel, done: make(chan struct{}), pending: map[string]*pendingPrompt{}}
	a.ctx, a.callbackBase = ctx, callbackBase
	s.attempts[a.view.ID] = a
	s.mu.Unlock()
	admitted := make(chan struct{})
	go func() {
		out, err := s.service.begin(ctx, key, method, Interaction{
			Notify: func(_ context.Context, n Notice) error {
				s.mu.Lock()
				a.view.Notices = append(a.view.Notices, n)
				if len(a.view.Notices) > 8 {
					a.view.Notices = a.view.Notices[len(a.view.Notices)-8:]
				}
				s.mu.Unlock()
				s.change()
				return nil
			},
			Prompt:       func(ctx context.Context, p Prompt) (string, error) { return s.prompt(ctx, a, p) },
			Callback:     func(ctx context.Context, state string) (string, error) { return s.prepareCallback(ctx, a, state) },
			WaitCallback: func(ctx context.Context) (map[string][]string, error) { return s.waitCallback(ctx, a) },
		}, func() { close(admitted) }, false)
		s.mu.Lock()
		a.failure = err
		if err != nil {
			a.view.Status = "failed"
			a.view.Error = err.Error()
			for _, secret := range a.secrets {
				a.view.Error = strings.ReplaceAll(a.view.Error, secret, "[redacted]")
			}
		} else {
			a.view.Status = out.Status
		}
		a.pending = map[string]*pendingPrompt{}
		a.secrets = nil
		a.callback = nil
		s.mu.Unlock()
		cancel()
		close(a.done)
		s.change()
	}()
	select {
	case <-admitted:
	case <-a.done:
		if a.failure != nil {
			s.mu.Lock()
			delete(s.attempts, a.view.ID)
			s.mu.Unlock()
			return View{}, a.failure
		}
	case <-s.ctx.Done():
		return View{}, s.ctx.Err()
	}
	s.mu.Lock()
	v := s.copy(a)
	s.latest[key] = a.view.ID
	s.mu.Unlock()
	s.change()
	return v, nil
}
func (s *Surface) prompt(ctx context.Context, a *surfaceAttempt, p Prompt) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if (p.Kind != "text" && p.Kind != "secret" && p.Kind != "select") ||
		(p.WebInput != "" && (p.WebInput != "json-file" || p.Kind == "select")) {
		return "", failure("INVALID_PROMPT", "Unsupported authorization prompt")
	}
	if p.Loopback != nil {
		if p.Kind != "text" || p.WebInput != "" || !validLoopback(*p.Loopback) {
			return "", failure("INVALID_PROMPT", "Unsupported loopback authorization prompt")
		}
		value := *p.Loopback
		p.Loopback = &value
	}
	pending := &pendingPrompt{PendingPrompt{uuid.NewString(), p}, make(chan answer, 1)}
	s.mu.Lock()
	if len(a.pending) >= 16 {
		s.mu.Unlock()
		return "", failure("AUTHORIZATION_BUSY", "Too many pending questions")
	}
	a.pending[pending.view.ID] = pending
	a.promptOrder = append(a.promptOrder, pending.view.ID)
	s.mu.Unlock()
	s.change()
	defer func() { s.mu.Lock(); s.removePrompt(a, pending.view.ID); s.mu.Unlock(); s.change() }()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case answer := <-pending.answer:
		if answer.decline {
			return "", failure("DECLINED", "Authorization declined")
		}
		return answer.value, nil
	}
}
func (s *Surface) Handle(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	var input struct {
		ID       string              `json:"id"`
		Key      string              `json:"key"`
		Method   string              `json:"method"`
		PromptID string              `json:"promptId"`
		Answer   *string             `json:"answer"`
		Decline  bool                `json:"decline"`
		Token    string              `json:"token"`
		Query    map[string][]string `json:"query"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	if method == "list" {
		rows := s.service.List()
		out := make([]EntryView, 0, len(rows))
		for _, row := range rows {
			info, err := s.service.Inspect(ctx, row.Key)
			if err != nil {
				return nil, err
			}
			entry := EntryView{Entry: row, Configured: info.Configured, AccountLabel: info.AccountLabel}
			s.mu.Lock()
			if a := s.attempts[s.latest[row.Key]]; a != nil {
				entry.AttemptID, entry.AttemptStatus = a.view.ID, a.view.Status
			}
			s.mu.Unlock()
			out = append(out, entry)
		}
		return out, nil
	}
	if method == "disconnect" {
		state, err := s.service.Disconnect(ctx, input.Key)
		s.change()
		return state, err
	}
	if method == "start" {
		return s.startWithCallback(input.Key, input.Method, callbackBase(ctx))
	}
	if method == "callback" {
		return s.receiveCallback(input.Token, input.Query)
	}
	if method == "cancelKey" {
		return s.service.CancelAndWait(ctx, input.Key)
	}
	s.mu.Lock()
	a := s.attempts[input.ID]
	if a == nil {
		s.mu.Unlock()
		return nil, failure("AUTHORIZATION_NOT_FOUND", "Authorization is no longer available; start again")
	}
	if method == "cancel" {
		s.mu.Unlock()
		a.cancel()
		select {
		case <-a.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		s.mu.Lock()
		v := s.copy(a)
		s.mu.Unlock()
		return v, nil
	}
	defer s.mu.Unlock()
	if method == "answer" {
		pending := a.pending[input.PromptID]
		if pending == nil {
			return nil, failure("PROMPT_NOT_FOUND", "This question is no longer awaiting an answer")
		}
		if !input.Decline {
			if input.Answer == nil || len(*input.Answer) > 16384 {
				return nil, failure("INVALID_ANSWER", "Provide a valid authorization answer")
			}
			if pending.view.Kind == "select" {
				found := false
				for _, o := range pending.view.Options {
					found = found || o.ID == *input.Answer
				}
				if !found {
					return nil, failure("INVALID_ANSWER", "Choose an offered option")
				}
			}
			// Presentation is independent of privacy: visible callback URLs can
			// contain one-time codes, just as masked fields can contain API keys.
			if pending.view.Kind != "select" && *input.Answer != "" {
				a.secrets = append(a.secrets, *input.Answer)
			}
		}
		value := ""
		if input.Answer != nil {
			value = *input.Answer
		}
		pending.answer <- answer{value, input.Decline}
		s.removePrompt(a, input.PromptID)
	} else if method != "get" {
		return nil, failure("METHOD_NOT_FOUND", "Unknown authorization operation")
	}
	return s.copy(a), nil
}
func (s *Surface) Close() {
	s.mu.Lock()
	for _, a := range s.attempts {
		a.cancel()
	}
	s.mu.Unlock()
}

// Called with Surface.mu held; preserve the original request order in views.
func (s *Surface) removePrompt(a *surfaceAttempt, id string) {
	delete(a.pending, id)
	for i, key := range a.promptOrder {
		if key == id {
			a.promptOrder = append(a.promptOrder[:i], a.promptOrder[i+1:]...)
			break
		}
	}
}

func validLoopback(p Loopback) bool {
	redirect, err := url.Parse(p.RedirectURI)
	if err != nil || redirect.Scheme != "http" || redirect.User != nil || redirect.Port() == "" || redirect.RawQuery != "" || redirect.Fragment != "" {
		return false
	}
	port, err := strconv.Atoi(redirect.Port())
	if err != nil || port < 1 || port > 65535 {
		return false
	}
	switch redirect.Hostname() {
	case "localhost", "127.0.0.1", "::1":
	default:
		return false
	}
	login, err := url.Parse(p.AuthorizationURL)
	return err == nil && login.Scheme == "https" && login.Hostname() != "" && login.User == nil && p.State != "" && len(login.Query()["redirect_uri"]) == 1 && login.Query().Get("redirect_uri") == p.RedirectURI && len(login.Query()["state"]) == 1 && login.Query().Get("state") == p.State
}
