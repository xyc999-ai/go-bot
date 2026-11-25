package sessionformat

import "time"

// ActivitySnapshot is worker telemetry for session activity. It is derived
// from the durable v4 Session, not written as another Session event.
type ActivitySnapshot struct {
	Since time.Time `json:"since"`
	Idle  bool      `json:"idle"`
}

// DurableActivity leaves a Session busy while its inbox has work or a turn
// has no durable end. The worker also checks its in-flight driver separately.
func (s *LiveSession) DurableActivity() ActivitySnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	cell := s.projections[runtimeDefinition.Key]
	s.advanceProjectionLocked(cell, &cell.durable, s.durable)
	if cell.durable.err != nil {
		panic(cell.durable.err)
	}
	state := cell.durable.state.(runtimeProjection)
	snapshot := ActivitySnapshot{Since: time.UnixMilli(state.Since).UTC(), Idle: !state.Boundary.Open && len(s.inbox.NextTurn) == 0 && len(s.inbox.NextStep) == 0}
	return snapshot
}
