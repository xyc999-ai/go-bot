package sessionformat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// EventJournal commits a contiguous suffix of already accepted v4 events.
// A successful Append is the durability checkpoint; an uncertain result must
// be handled by reopening the Session, never by replaying the same suffix.
type EventJournal interface {
	Append(context.Context, []Event) error
	Close() error
}

type EventInput struct {
	Type      string
	Data      json.RawMessage
	SurfaceOp json.RawMessage
	// Nil omits sourceEventSeqs. A non-nil slice contains logical event seqs;
	// the v4 physical writer packs profitable consecutive runs on disk.
	SourceEventSeqs []uint64
	Ignorable       bool
}

// LiveSession is the in-memory owner of one native v4 event log and surface.
// Append does not perform I/O; Flush persists its accepted suffix, matching
// the separation between DeepSeek's live Session and checkpoint policy.
type LiveSession struct {
	appendMu             sync.Mutex
	mu                   sync.Mutex
	flushMu              sync.Mutex
	header               Header
	events               []Event
	surface              *SurfaceState
	inbox                InboxState
	projections          map[string]*projectionCell
	modelSelection       ModelSelectionState
	modelSelectionCursor int
	durable              int
	inheritedCount       uint64
	journal              EventJournal
	poison               bool
	closing              bool
	closed               bool
	closeOnce            sync.Once
	closeErr             error
}

func NewLiveSession(header Header, journal EventJournal) (*LiveSession, error) {
	if err := header.Validate(); err != nil {
		return nil, err
	}
	if journal == nil {
		return nil, errors.New("Session v4 journal is required")
	}
	surface, err := NewSurfaceState(nil)
	if err != nil {
		return nil, err
	}
	s := &LiveSession{header: header, journal: journal, surface: surface}
	if _, err := RegisterProjection(s, &goalDefinition); err != nil {
		return nil, err
	}
	if _, err := RegisterProjection(s, &runtimeDefinition); err != nil {
		return nil, err
	}
	if _, err := RegisterProjection(s, &planDefinition); err != nil {
		return nil, err
	}
	if _, err := RegisterProjection(s, &usageDefinition); err != nil {
		return nil, err
	}
	if _, err := RegisterProjection(s, &childCatalogDefinition); err != nil {
		return nil, err
	}
	if _, err := RegisterProjection(s, &inboxHistoryDefinition); err != nil {
		return nil, err
	}
	return s, nil
}

// RestoreLiveSession adopts the already decoded, detached artifact only after
// validating its event relationships and reconstructing its model surface.
// As in DeepSeek's resume path, an open stored turn is closed before the new
// lifecycle starts with an untagged end-seed marker. A stored end-seed marker
// is retained without adding another.
func RestoreLiveSession(artifact PhysicalArtifact, journal EventJournal) (*LiveSession, error) {
	if journal == nil {
		return nil, errors.New("Session v4 journal is required")
	}
	s, err := replaySession(artifact, journal, len(artifact.Events))
	if err != nil {
		return nil, err
	}
	// Repair belongs to resume, never to fresh constructor seed preparation.
	if _, err := appendInterruptedTurnClosers(s); err != nil {
		return nil, err
	}
	if len(s.events) == 0 || s.events[len(s.events)-1].Type != "session/end-seed" {
		if _, err := s.Append(EventInput{Type: "session/end-seed", Data: json.RawMessage(`{}`)}); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func replaySession(artifact PhysicalArtifact, journal EventJournal, durable int) (*LiveSession, error) {
	if err := artifact.Header.Validate(); err != nil {
		return nil, err
	}
	if err := validateLiveSeedBoundary(artifact); err != nil {
		return nil, err
	}
	if err := ValidateV4Rows(artifact.Events); err != nil {
		return nil, err
	}
	if err := ValidateV4Relationships(artifact.Events); err != nil {
		return nil, err
	}
	surface, err := NewSurfaceState(artifact.Events)
	if err != nil {
		return nil, err
	}
	inbox, err := FoldInbox(artifact.Events)
	if err != nil {
		return nil, err
	}
	if _, err := FoldGoal(artifact.Events); err != nil {
		return nil, err
	}
	if _, err := FoldPlan(artifact.Events); err != nil {
		return nil, err
	}
	if _, err := FoldSubagentCatalog(artifact.Events, artifact.InheritedEventCount); err != nil {
		return nil, err
	}
	if _, err := FoldSubagentDescriptor(artifact.Events); err != nil {
		return nil, err
	}
	s := &LiveSession{header: artifact.Header, journal: journal, surface: surface, inbox: inbox, events: make([]Event, len(artifact.Events))}
	for index, event := range artifact.Events {
		if event.Seq != uint64(index) || !knownV4EventTypes[event.Type] && !event.Ignorable {
			return nil, fmt.Errorf("invalid Session v4 event %d", index)
		}
		s.events[index] = cloneV4Event(event)
	}
	s.durable = durable
	s.inheritedCount = artifact.InheritedEventCount
	if _, err := RegisterProjection(s, &goalDefinition); err != nil {
		return nil, err
	}
	if _, err := RegisterProjection(s, &runtimeDefinition); err != nil {
		return nil, err
	}
	if _, err := RegisterProjection(s, &planDefinition); err != nil {
		return nil, err
	}
	if _, err := RegisterProjection(s, &usageDefinition); err != nil {
		return nil, err
	}
	if _, err := RegisterProjection(s, &childCatalogDefinition); err != nil {
		return nil, err
	}
	if _, err := RegisterProjection(s, &inboxHistoryDefinition); err != nil {
		return nil, err
	}
	return s, nil
}

func validateLiveSeedBoundary(artifact PhysicalArtifact) error {
	if artifact.InheritedEventCount > uint64(len(artifact.Events)) {
		return errors.New("Session v4 inherited event count exceeds the log")
	}
	var lastTagged *uint64
	for _, event := range artifact.Events {
		if event.Type != "session/end-seed" {
			continue
		}
		data, err := objectValue(event.Data)
		if err != nil {
			return errors.New("invalid Session v4 end-seed data")
		}
		if string(data["inherited"]) == "true" {
			seq := event.Seq
			lastTagged = &seq
		}
	}
	if artifact.Header.IsSeeded {
		if lastTagged == nil || *lastTagged != artifact.InheritedEventCount {
			return errors.New("seeded Session v4 has no marker at its inherited cut")
		}
	} else if artifact.InheritedEventCount != 0 {
		return errors.New("unseeded Session v4 has inherited events")
	}
	return nil
}

func (s *LiveSession) Header() Header { return s.header }

func (s *LiveSession) InheritedEventCount() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inheritedCount
}

type Position struct {
	Seq                 uint64 `json:"seq"`
	InheritedEventCount uint64 `json:"inheritedEventCount"`
}

// Position reads the live log, including accepted events not yet flushed. It
// neither clones history nor performs I/O or observer dispatch. A borrowed
// Session remains readable after it has left the loaded-session registry.
func (s *LiveSession) Position() Position {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Position{Seq: uint64(len(s.events)), InheritedEventCount: s.inheritedCount}
}

func (s *LiveSession) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneV4Events(s.events)
}

// EventRange returns a detached immutable-prefix read, including accepted facts
// not yet flushed. An exact upper bound stays valid while later appends occur.
func (s *LiveSession) EventRange(from, to uint64) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if from > to || to > uint64(len(s.events)) {
		return nil, errors.New("Session event range is out of bounds")
	}
	return cloneV4Events(s.events[from:to]), nil
}

func (s *LiveSession) DurableEvents() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneV4Events(s.events[:s.durable])
}

// DurableCursor is the last confirmed event sequence, or -1 for an empty
// journal. It never includes in-memory events awaiting Flush.
func (s *LiveSession) DurableCursor() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(s.durable) - 1
}

// DurableEventsFrom returns only the committed suffix after a consumer's
// cursor. Long-lived projections can advance without copying the entire log
// again after every checkpoint; the returned events remain detached from the
// live Session's mutable storage.
func (s *LiveSession) DurableEventsFrom(cursor int) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cursor < 0 || cursor > s.durable {
		return nil, errors.New("durable Session cursor is out of range")
	}
	return cloneV4Events(s.events[cursor:s.durable]), nil
}

// DurableEventPage reads at most limit committed events after the cursor.
func (s *LiveSession) DurableEventPage(after *uint64, limit int) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	start := 0
	if after != nil {
		if *after >= uint64(s.durable) {
			return []Event{}
		}
		start = int(*after) + 1
	}
	return cloneV4Events(s.events[start:min(start+limit, s.durable)])
}

func cloneV4Events(events []Event) []Event {
	out := make([]Event, len(events))
	for index, event := range events {
		out[index] = cloneV4Event(event)
	}
	return out
}

func (s *LiveSession) Surface() (Surface, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.surface.Snapshot()
}

// ModelSurface reads only current context nodes and their metadata.
func (s *LiveSession) ModelSurface() (Surface, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.surface.ModelSnapshot()
}

func (s *LiveSession) Inbox() InboxState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inbox.Clone()
}

func (s *LiveSession) Append(input EventInput) (Event, error) {
	return s.appendAt(input, time.Now().UnixMilli())
}

// appendAt is reserved for deterministic synthetic repair events. Ordinary
// producers use Append, which stamps the current time at acceptance.
func (s *LiveSession) appendAt(input EventInput, timestamp int64) (Event, error) {
	events, err := s.appendInputs([]EventInput{input}, timestamp)
	if err != nil {
		return Event{}, err
	}
	return events[0], nil
}

func (s *LiveSession) appendLocked(input EventInput, timestamp int64) (Event, error) {
	if s.closed || s.closing || s.poison {
		return Event{}, ErrV4UncertainWrite
	}
	event := Event{
		Type: input.Type, Seq: uint64(len(s.events)), Time: timestamp,
		Data:      append(json.RawMessage(nil), input.Data...),
		SurfaceOp: append(json.RawMessage(nil), input.SurfaceOp...),
		Ignorable: input.Ignorable,
	}
	if input.SourceEventSeqs != nil {
		encoded, err := EncodeSourceSeqs(input.SourceEventSeqs, event.Seq)
		if err != nil {
			return Event{}, err
		}
		event.SourceEventSeqs = encoded
	}
	if !knownV4EventTypes[event.Type] && !event.Ignorable {
		return Event{}, fmt.Errorf("unknown required Session v4 event %q", event.Type)
	}
	if _, err := EncodeEvent(event, event.Seq); err != nil {
		return Event{}, err
	}
	if err := ValidateV4Rows([]Event{event}); err != nil {
		return Event{}, err
	}
	// Goal mutations and admitted goal rounds are state transitions, not
	// independent rows. Reject an invalid transition before it can enter the
	// live log; otherwise a later restore would reject our own artifact.
	if event.Type == "goal/change" || isGoalSourcedMessage(event) {
		cell := s.projections[goalDefinition.Key]
		if cell == nil {
			return Event{}, errors.New("goal projection is not registered")
		}
		next, err := nextGoal(cell.live.state.(goalFoldState), event)
		if err != nil {
			return Event{}, err
		}
		cell.live = projectionValue{state: next, next: len(s.events) + 1}
	}
	nextInbox, err := s.inbox.Next(event)
	if err != nil {
		return Event{}, err
	}
	if err := s.surface.Append(event); err != nil {
		return Event{}, err
	}
	s.inbox = nextInbox
	s.events = append(s.events, event)
	return cloneV4Event(event), nil
}

func (s *LiveSession) Flush(ctx context.Context) error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	return s.flushLocked(ctx)
}

func (s *LiveSession) flushLocked(ctx context.Context) error {
	s.mu.Lock()
	if s.closed || s.poison {
		s.mu.Unlock()
		return ErrV4UncertainWrite
	}
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return err
	}
	through := len(s.events)
	batch := cloneV4Events(s.events[s.durable:through])
	s.mu.Unlock()
	if len(batch) != 0 {
		if err := s.journal.Append(ctx, batch); err != nil {
			if errors.Is(err, ErrV4WriteRolledBack) && !errors.Is(err, ErrV4UncertainWrite) {
				return fmt.Errorf("flush Session v4 events: %w", err)
			}
			s.mu.Lock()
			s.poison = true
			s.mu.Unlock()
			return fmt.Errorf("flush Session v4 events: %w: %w", err, ErrV4UncertainWrite)
		}
	}
	s.mu.Lock()
	s.durable = through
	s.mu.Unlock()
	return nil
}

func (s *LiveSession) Close() error {
	s.closeOnce.Do(func() {
		s.appendMu.Lock()
		defer s.appendMu.Unlock()
		s.mu.Lock()
		s.closing = true
		s.mu.Unlock()
		s.flushMu.Lock()
		defer s.flushMu.Unlock()
		flushErr := s.flushLocked(context.Background())
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.closeErr = errors.Join(flushErr, s.journal.Close())
	})
	return s.closeErr
}
