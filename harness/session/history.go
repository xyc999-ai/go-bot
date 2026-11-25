package sessionformat

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
)

var ErrHistoryCursor = errors.New("invalid Session history cursor")

type HistoryKind uint8

const (
	HistoryEvents HistoryKind = iota
	HistoryTurns
	HistoryState
	HistoryAttachment
)

// HistoryRequest is an explicit bounded historical read. Before is an exclusive
// turn/start cursor; After is an exclusive event cursor. State is always at the
// returned Count, never at the caller's older pagination cursor.
type HistoryRequest struct {
	Kind                         HistoryKind
	After                        *uint64
	Before                       *uint64
	Limit                        int
	AttachmentID, AttachmentName string
}
type HistoricalAttachment struct {
	Type       string          `json:"type"`
	Attachment json.RawMessage `json:"attachment"`
}

// HistoryWindow is one immutable observation cut. Views contain small optional
// domain views (for example pending questions), not raw event histories.
type HistoryWindow struct {
	Header              Header
	InheritedEventCount uint64
	Count               uint64
	Events              []Event
	Before              *uint64
	Inbox               InboxState
	Boundary            TurnBoundary
	Permission          PermissionState
	Views               map[string]json.RawMessage
	TitleHint           string
	Attachment          *HistoricalAttachment
}

func historyRange(request HistoryRequest, count uint64, turns []uint64) (uint64, uint64, *uint64, error) {
	switch request.Kind {
	case HistoryState, HistoryAttachment:
		return 0, 0, nil, nil
	case HistoryEvents:
		if request.Limit < 1 || request.Limit > 500 {
			return 0, 0, nil, errors.New("invalid history event limit")
		}
		start := uint64(0)
		if request.After != nil {
			if *request.After >= count {
				return count, count, nil, nil
			}
			start = *request.After + 1
		}
		return start, min(count, start+uint64(request.Limit)), nil, nil
	case HistoryTurns:
		if request.Limit < 1 || request.Limit > 100 {
			return 0, 0, nil, errors.New("invalid history turn limit")
		}
		end := count
		if request.Before != nil {
			end = *request.Before
			index := sort.Search(len(turns), func(i int) bool { return turns[i] >= end })
			if end >= count || index == len(turns) || turns[index] != end {
				return 0, 0, nil, ErrHistoryCursor
			}
		}
		n := sort.Search(len(turns), func(i int) bool { return turns[i] >= end })
		start := uint64(0)
		var before *uint64
		if n >= request.Limit {
			start = turns[n-request.Limit]
			if n > request.Limit {
				v := start
				before = &v
			}
		}
		return start, end, before, nil
	default:
		return 0, 0, nil, errors.New("invalid history request kind")
	}
}

func cloneHistoryState(value HistoryWindow) HistoryWindow {
	value.Inbox = value.Inbox.Clone()
	value.Boundary.GoalRounds = slices.Clone(value.Boundary.GoalRounds)
	value.Views = copyHistoryViews(value.Views)
	if value.Attachment != nil {
		ref := *value.Attachment
		ref.Attachment = slices.Clone(ref.Attachment)
		value.Attachment = &ref
	}
	return value
}
func copyHistoryViews(views map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(views))
	for key, raw := range views {
		out[key] = slices.Clone(raw)
	}
	return out
}
func (s *LiveSession) historyStateLocked() (HistoryWindow, runtimeProjection, error) {
	cell := s.projections[runtimeDefinition.Key]
	if cell == nil {
		return HistoryWindow{}, runtimeProjection{}, errors.New("execution projection is not registered")
	}
	s.advanceProjectionLocked(cell, &cell.durable, s.durable)
	if cell.durable.err != nil {
		return HistoryWindow{}, runtimeProjection{}, cell.durable.err
	}
	state := cell.durable.state.(runtimeProjection)
	inboxCell := s.projections[inboxHistoryDefinition.Key]
	if inboxCell == nil {
		return HistoryWindow{}, state, errors.New("history inbox projection is not registered")
	}
	s.advanceProjectionLocked(inboxCell, &inboxCell.durable, s.durable)
	if inboxCell.durable.err != nil {
		return HistoryWindow{}, state, inboxCell.durable.err
	}
	value := HistoryWindow{Header: s.header, InheritedEventCount: s.inheritedCount, Count: uint64(s.durable), Boundary: state.Boundary, Permission: state.Permission, TitleHint: state.FirstPrompt, Inbox: inboxCell.durable.state.(InboxState), Views: make(map[string]json.RawMessage)}
	value = cloneHistoryState(value)
	for key, c := range s.projections {
		if c.view == nil {
			continue
		}
		s.advanceProjectionLocked(c, &c.durable, s.durable)
		if c.durable.err != nil {
			return HistoryWindow{}, state, c.durable.err
		}
		raw, err := c.view(c.durable.state)
		if err != nil {
			return HistoryWindow{}, state, err
		}
		value.Views[key] = slices.Clone(raw)
	}
	return value, state, nil
}

// HistoryWindow selects events and control state under one Session lock. It
// includes prepared/unpublished owners and never exposes unflushed events.
func (s *LiveSession) HistoryWindow(ctx context.Context, request HistoryRequest) (HistoryWindow, error) {
	if err := ctx.Err(); err != nil {
		return HistoryWindow{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.Kind == HistoryAttachment {
		cell := s.projections[runtimeDefinition.Key]
		if cell == nil {
			return HistoryWindow{}, errors.New("execution projection is not registered")
		}
		s.advanceProjectionLocked(cell, &cell.durable, s.durable)
		if cell.durable.err != nil {
			return HistoryWindow{}, cell.durable.err
		}
		state := cell.durable.state.(runtimeProjection)
		return HistoryWindow{Header: s.header, Count: uint64(s.durable), InheritedEventCount: s.inheritedCount, Attachment: state.attachment(request.AttachmentID, request.AttachmentName)}, nil
	}
	value, state, err := s.historyStateLocked()
	if err != nil {
		return value, err
	}
	from, to, before, err := historyRange(request, value.Count, state.TurnStarts)
	if err != nil {
		return HistoryWindow{}, err
	}
	value.Before = before
	value.Events = cloneV4Events(s.events[from:to])
	return value, nil
}

var inboxHistoryDefinition = ProjectionDefinition[InboxState]{Key: "historyInbox", Version: 1, Init: func(Header, uint64) InboxState { return InboxState{} }, Apply: func(state InboxState, event Event) (InboxState, error) { return state.Next(event) }}
