package sessionformat

import (
	"encoding/json"
	"time"
)

// AppendBatch validates the complete suffix before making it visible. The
// journal remains Go-owned and is persisted separately by Flush/checkpoints.
func (s *LiveSession) AppendBatch(inputs []EventInput) ([]Event, error) {
	return s.appendInputs(inputs, time.Now().UnixMilli())
}
func (s *LiveSession) appendInputs(inputs []EventInput, timestamp int64) ([]Event, error) {
	s.appendMu.Lock()
	defer s.appendMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendInputsLocked(inputs, timestamp)
}
func (s *LiveSession) appendInputsLocked(inputs []EventInput, timestamp int64) ([]Event, error) {
	if s.closed || s.closing || s.poison {
		return nil, ErrV4UncertainWrite
	}
	surface := *s.surface
	for _, input := range inputs {
		if input.Type == "image/offload" {
			surface.projected = make(map[uint64]json.RawMessage, len(s.surface.projected))
			for key, value := range s.surface.projected {
				surface.projected[key] = value
			}
			break
		}
	}
	// Only the strict Goal transition is speculative. Other domain cells are
	// driven after the entire batch is accepted, so a rejected suffix cannot
	// mutate their maps or publish partial state.
	projections := make(map[string]*projectionCell, len(s.projections))
	for key, cell := range s.projections {
		projections[key] = cell
	}
	if cell := projections[goalDefinition.Key]; cell != nil {
		copied := *cell
		projections[goalDefinition.Key] = &copied
	}
	shadow := &LiveSession{projections: projections, header: s.header, events: s.events, surface: &surface, inbox: s.inbox.Clone()}
	events := make([]Event, 0, len(inputs))
	for _, input := range inputs {
		event, err := shadow.appendLocked(input, timestamp)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	s.events, s.surface, s.inbox, s.projections = shadow.events, shadow.surface, shadow.inbox, shadow.projections
	s.driveProjectionsLocked()
	return cloneV4Events(events), nil
}
