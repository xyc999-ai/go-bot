// Package authorization owns the Host's channel-neutral credential acquisition
// lifecycle. Integrations provide protocol callbacks; credentials owns storage.
package authorization

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"go-bot/internal/credentials"
)

type Error struct{ Code, Message string }

func (e *Error) Error() string           { return e.Message }
func failure(code, message string) error { return &Error{code, message} }

type Method struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type Notice struct {
	Message string `json:"message"`
	URL     string `json:"url,omitempty"`
	Code    string `json:"code,omitempty"`
}
type Option struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// Loopback describes a native-client receiver; CLI exchange remains on the Host.
type Loopback struct {
	AuthorizationURL string `json:"authorizationUrl"`
	RedirectURI      string `json:"redirectUri"`
	State            string `json:"state"`
}
type Prompt struct {
	Kind        string    `json:"kind"`
	Message     string    `json:"message"`
	Placeholder string    `json:"placeholder,omitempty"`
	WebInput    string    `json:"webInput,omitempty"`
	Options     []Option  `json:"options,omitempty"`
	Loopback    *Loopback `json:"loopback,omitempty"`
}
type Interaction struct {
	Notify       func(context.Context, Notice) error
	Prompt       func(context.Context, Prompt) (string, error)
	Callback     func(context.Context, string) (string, error)
	WaitCallback func(context.Context) (map[string][]string, error)
}
type AccountState struct {
	Configured   bool   `json:"configured"`
	AccountLabel string `json:"accountLabel,omitempty"`
}

type Flow struct {
	Key        string                                      `json:"key"`
	Label      string                                      `json:"label"`
	Methods    []Method                                    `json:"methods"`
	Run        func(*Session) error                        `json:"-"`
	Inspect    func(context.Context) (AccountState, error) `json:"-"`
	Disconnect func(context.Context) error                 `json:"-"`
}
type Entry struct {
	Key           string   `json:"key"`
	Label         string   `json:"label"`
	Methods       []Method `json:"methods"`
	InFlight      bool     `json:"inFlight"`
	CanDisconnect bool     `json:"canDisconnect"`
}
type Outcome struct {
	Status string `json:"status"`
}

type Service struct {
	store         *credentials.Store
	mu            sync.Mutex
	flows         map[string]*Flow
	order         []string
	running       map[string]*Session
	disconnecting map[string]bool
	observers     map[uint64]func(string, string)
	nextObserver  uint64
}

func New(store *credentials.Store) *Service {
	return &Service{store: store, disconnecting: map[string]bool{}, flows: map[string]*Flow{}, running: map[string]*Session{}, observers: map[uint64]func(string, string){}}
}
func (s *Service) Register(flow Flow) (func(), error) {
	if !credentials.ValidKey(flow.Key) || flow.Label == "" || len(flow.Methods) == 0 || flow.Run == nil {
		return nil, failure("INVALID_FLOW", "A flow requires a credential key, label, methods and callback")
	}
	seen := map[string]bool{}
	for _, m := range flow.Methods {
		if m.ID == "" || m.Label == "" || seen[m.ID] {
			return nil, failure("INVALID_FLOW", "Invalid authorization methods")
		}
		seen[m.ID] = true
	}
	flow.Methods = append([]Method(nil), flow.Methods...)
	s.mu.Lock()
	if s.flows[flow.Key] != nil {
		s.mu.Unlock()
		return nil, failure("DUPLICATE_FLOW", "This credential already has an authorization flow")
	}
	s.flows[flow.Key] = &flow
	s.order = append(s.order, flow.Key)
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			if s.flows[flow.Key] == &flow {
				delete(s.flows, flow.Key)
				for i, k := range s.order {
					if k == flow.Key {
						s.order = append(s.order[:i], s.order[i+1:]...)
						break
					}
				}
			}
			s.mu.Unlock()
			s.Cancel(flow.Key)
		})
	}, nil
}
func (s *Service) List() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}
func (s *Service) listLocked() []Entry {
	out := make([]Entry, 0, len(s.order))
	for _, key := range s.order {
		out = append(out, s.entry(s.flows[key]))
	}
	return out
}
func (s *Service) entry(f *Flow) Entry {
	return Entry{Key: f.Key, Label: f.Label, Methods: append([]Method(nil), f.Methods...), InFlight: s.running[f.Key] != nil, CanDisconnect: f.Disconnect != nil}
}
func (s *Service) Describe(key string) *Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f := s.flows[key]; f != nil {
		v := s.entry(f)
		return &v
	}
	return nil
}
func (s *Service) Cancel(key string) bool {
	s.mu.Lock()
	a := s.running[key]
	s.mu.Unlock()
	if a != nil {
		a.mu.Lock()
		cancelled := !a.committing
		if cancelled {
			a.cancel()
		}
		a.mu.Unlock()
		return cancelled
	}
	return false
}
func (s *Service) Session(id string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.running {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// Begin returns promptly on withdrawal unless a credential commit was already
// admitted. An ignored cancellation may leave provider work settling, never a
// second core authorization lifecycle or a persisted attempt record.
func (s *Service) Begin(ctx context.Context, key, method string, interaction Interaction) (Outcome, error) {
	return s.begin(ctx, key, method, interaction, nil, false)
}
func (s *Service) begin(ctx context.Context, key, method string, interaction Interaction, admitted func(), drain bool) (out Outcome, err error) {
	s.mu.Lock()
	f := s.flows[key]
	if f == nil {
		s.mu.Unlock()
		return out, failure("NO_FLOW", "No authorization flow is registered for this credential")
	}
	if method == "" {
		method = f.Methods[0].ID
	}
	offered := false
	for _, m := range f.Methods {
		offered = offered || m.ID == method
	}
	if !offered {
		s.mu.Unlock()
		return out, failure("UNKNOWN_METHOD", "Unknown authorization method")
	}
	if s.running[key] != nil || s.disconnecting[key] {
		s.mu.Unlock()
		return out, failure("ALREADY_IN_FLIGHT", "Authorization is already in progress")
	}
	if ctx.Err() != nil {
		s.mu.Unlock()
		return Outcome{"cancelled"}, nil
	}
	call, cancel := context.WithCancel(context.WithoutCancel(ctx))
	a := &Session{ID: uuid.NewString(), Key: key, Method: method, Context: call, caller: ctx, cancel: cancel, service: s, interaction: interaction, done: make(chan struct{})}
	s.running[key] = a
	s.mu.Unlock()
	stop := context.AfterFunc(ctx, func() {
		a.mu.Lock()
		if !a.committing {
			a.cancel()
		}
		a.mu.Unlock()
	})
	defer func() {
		stop()
		cancel()
		s.mu.Lock()
		if s.running[key] == a {
			delete(s.running, key)
		}
		s.mu.Unlock()
		status := "failed"
		if err == nil {
			status = out.Status
		}
		a.settlement = status
		close(a.done)
		s.settled(key, status)
	}()
	off := s.store.Subscribe(func(update credentials.Update) {
		for _, k := range update.Records {
			if k == key {
				a.observed.Store(true)
				break
			}
		}
	})
	defer off()
	if admitted != nil {
		admitted()
	}
	finished := make(chan error, 1)
	go func() {
		var failure error
		defer func() {
			if recover() != nil {
				failure = fmt.Errorf("authorization callback failed")
			}
			finished <- failure
		}()
		failure = f.Run(a)
	}()
	select {
	case err = <-finished:
	case <-call.Done():
		if drain {
			<-finished
		}
		return Outcome{"cancelled"}, nil
	}
	if a.withdrawn() {
		return Outcome{"cancelled"}, nil
	}
	if err != nil {
		if a.declined.Load() {
			return Outcome{"cancelled"}, nil
		}
		return out, err
	}
	if f.Inspect != nil {
		state, inspectErr := f.Inspect(call)
		if a.withdrawn() {
			return Outcome{"cancelled"}, nil
		}
		if inspectErr != nil {
			return out, inspectErr
		}
		if !state.Configured {
			return out, failure("NOT_CONFIGURED", "The native application did not save a usable authorization")
		}
		return Outcome{"authorized"}, nil
	}
	if !a.observed.Load() {
		return out, failure("NOT_COMMITTED", "Authorization finished without committing a credential in this attempt")
	}
	info, e := s.store.DescribeRecord(key)
	if e != nil {
		return out, e
	}
	if !info.Configured {
		return out, failure("NOT_COMMITTED", "Authorization removed its credential instead of committing one")
	}
	return Outcome{"authorized"}, nil
}

type Session struct {
	ID, Key, Method    string
	Context            context.Context
	caller             context.Context
	service            *Service
	cancel             context.CancelFunc
	interaction        Interaction
	mu                 sync.Mutex
	committing         bool
	done               chan struct{}
	settlement         string
	observed, declined atomic.Bool
}

func (a *Session) Commit(record *credentials.Record) error {
	if record == nil {
		return failure("INVALID_CREDENTIAL", "Authorization returned no credential")
	}
	if err := record.Validate(); err != nil {
		return err
	}
	if err := a.AdmitCommit(); err != nil {
		return err
	}
	_, err := a.service.store.ModifyRecord(context.WithoutCancel(a.Context), a.Key, func(*credentials.Record) (*credentials.Record, error) { return record, nil })
	return err
}
func (a *Session) Notify(ctx context.Context, notice Notice) {
	if a.interaction.Notify != nil {
		_ = a.interaction.Notify(ctx, notice)
	}
}
func (a *Session) Prompt(ctx context.Context, prompt Prompt) (string, error) {
	if a.interaction.Prompt == nil {
		a.declined.Store(true)
		return "", failure("DECLINED", "No authorization interaction is available")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	call, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(a.Context, cancel)
	defer stop()
	defer cancel()
	value, err := a.interaction.Prompt(call, prompt)
	var detail interface{ AuthorizationDeclined() bool }
	var auth *Error
	if errors.As(err, &auth) && auth.Code == "DECLINED" || errors.As(err, &detail) && detail.AuthorizationDeclined() {
		a.declined.Store(true)
	}
	return value, err
}

func (s *Service) Subscribe(observer func(string, string)) func() {
	s.mu.Lock()
	s.nextObserver++
	id := s.nextObserver
	s.observers[id] = observer
	s.mu.Unlock()
	return func() { s.mu.Lock(); delete(s.observers, id); s.mu.Unlock() }
}

func (s *Service) settled(key, status string) {
	s.mu.Lock()
	listeners := make([]func(string, string), 0, len(s.observers))
	for _, fn := range s.observers {
		listeners = append(listeners, fn)
	}
	s.mu.Unlock()
	for _, fn := range listeners {
		func() { defer func() { _ = recover() }(); fn(key, status) }()
	}
}

// CancelAndWait also reports a commit that was already admitted as authorized.
func (s *Service) CancelAndWait(ctx context.Context, key string) (Outcome, error) {
	s.mu.Lock()
	a := s.running[key]
	s.mu.Unlock()
	if a == nil {
		return Outcome{"cancelled"}, nil
	}
	a.mu.Lock()
	if !a.committing {
		a.cancel()
	}
	a.mu.Unlock()
	select {
	case <-a.done:
		return Outcome{a.settlement}, nil
	case <-ctx.Done():
		return Outcome{}, ctx.Err()
	}
}

// BeginDrained is for native protocol owners whose cleanup must complete before
// their connection/attempt state can be released. Human/plugin flows use Begin.
func (s *Service) BeginDrained(ctx context.Context, key, method string, interaction Interaction) (Outcome, error) {
	return s.begin(ctx, key, method, interaction, nil, true)
}

// AdmitCommit marks a native protocol's durable-write boundary before it
// announces that boundary. Cancellation cannot retract an already admitted write.
func (a *Session) AdmitCommit() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.Context.Err(); err != nil {
		return err
	}
	// AfterFunc propagates cancellation asynchronously. Inspect the caller at
	// the admission boundary so an already withdrawn request cannot commit.
	if !a.committing && a.caller != nil {
		if err := a.caller.Err(); err != nil {
			return err
		}
	}
	a.committing = true
	return nil
}

func (a *Session) withdrawn() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Context.Err() != nil || !a.committing && a.caller != nil && a.caller.Err() != nil
}
