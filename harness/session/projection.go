package sessionformat

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ProjectionCut keeps accepted state distinct from the confirmed journal prefix.
type ProjectionCut uint8

const (
	LiveCut ProjectionCut = iota
	DurableCut
)

// ProjectionDefinition is a statically owned domain fold. Apply runs under the
// Session lock, after acceptance, and must not call back into Session. State and
// event data belong to Session; selectors must return detached values.
type ProjectionDefinition[S any] struct {
	Key     string
	Version int
	Init    func(Header, uint64) S
	Apply   func(S, Event) (S, error)
	View    func(S) (json.RawMessage, error)
}
type projectionValue struct {
	state any
	next  int
	err   error
}
type projectionCell struct {
	definition    any
	version       int
	init          func(Header, uint64) any
	apply         func(any, Event) (any, error)
	view          func(any) (json.RawMessage, error)
	live, durable projectionValue
}

// RegisterProjection acquires a required typed reader. A late registration
// rebuilds once; subsequent events drive live state before AppendBatch returns.
func RegisterProjection[S any](s *LiveSession, def *ProjectionDefinition[S]) (ProjectionHandle[S], error) {
	if s == nil || def == nil || def.Key == "" || def.Version < 1 || def.Init == nil || def.Apply == nil {
		return ProjectionHandle[S]{}, errors.New("invalid session projection")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.projections == nil {
		s.projections = make(map[string]*projectionCell)
	}
	cell := s.projections[def.Key]
	if cell != nil {
		if cell.definition != def || cell.version != def.Version {
			return ProjectionHandle[S]{}, fmt.Errorf("conflicting session projection %q", def.Key)
		}
	} else {
		cell = &projectionCell{definition: def, version: def.Version,
			init:  func(h Header, inherited uint64) any { return def.Init(h, inherited) },
			apply: func(state any, e Event) (any, error) { return def.Apply(state.(S), e) },
		}
		if def.View != nil {
			cell.view = func(state any) (json.RawMessage, error) { return def.View(state.(S)) }
		}
		cell.live.state = cell.init(s.header, s.inheritedCount)
		cell.durable.state = cell.init(s.header, s.inheritedCount)
		s.projections[def.Key] = cell
	}
	s.advanceProjectionLocked(cell, &cell.live, len(s.events))
	if cell.live.err != nil {
		return ProjectionHandle[S]{}, cell.live.err
	}
	return ProjectionHandle[S]{session: s, definition: def}, nil
}

type ProjectionHandle[S any] struct {
	session    *LiveSession
	definition *ProjectionDefinition[S]
}

// ReadProjection selects only required domain fields; it never materializes the
// log or unrelated wire views. The selector must not mutate retained state.
func ReadProjection[S, R any](handle ProjectionHandle[S], cut ProjectionCut, selectValue func(S) R) (R, error) {
	var zero R
	s := handle.session
	if s == nil || handle.definition == nil || selectValue == nil {
		return zero, errors.New("session projection is not registered")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cell := s.projections[handle.definition.Key]
	if cell == nil || cell.definition != handle.definition {
		return zero, errors.New("session projection is not registered")
	}
	value := &cell.live
	through := len(s.events)
	switch cut {
	case LiveCut:
	case DurableCut:
		value = &cell.durable
		through = s.durable
	default:
		return zero, errors.New("invalid session projection cut")
	}
	s.advanceProjectionLocked(cell, value, through)
	if value.err != nil {
		return zero, value.err
	}
	return selectValue(value.state.(S)), nil
}
func (s *LiveSession) advanceProjectionLocked(cell *projectionCell, value *projectionValue, through int) {
	for value.err == nil && value.next < through {
		next, err := cell.apply(value.state, s.events[value.next])
		if err != nil {
			value.err = fmt.Errorf("projection at seq %d: %w", value.next, err)
			return
		}
		value.state = next
		value.next++
	}
}
func (s *LiveSession) driveProjectionsLocked() {
	for _, cell := range s.projections {
		s.advanceProjectionLocked(cell, &cell.live, len(s.events))
	}
}
