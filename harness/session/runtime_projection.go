package sessionformat

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
)

// TurnBoundary is the current execution authority, not a historical snapshot.
type TurnBoundary struct {
	LastTurn         uint64
	Turn             uint64
	Step             int
	Open             bool
	OpenTurnStartSeq uint64
	DirectHuman      bool
	GoalRounds       []AdmittedGoalRound
}
type AdmittedGoalRound struct {
	GoalRef
	Round uint64
}
type PermissionState struct {
	Mode     string
	Approval string
}
type TimeContextState struct {
	LastMessage, BeforeStepMessage, LastInjection, LastTurnInjection int64
	HasMessage, HasBeforeStepMessage, HasInjection, HasTurnInjection bool
	BrowserTimeZones                                                 []string
}
type runtimeProjection struct {
	TurnStarts            []uint64
	FirstUserSeen         bool
	FirstPrompt           string
	Attachments           map[string][]HistoricalAttachment
	Boundary              TurnBoundary
	Permission            PermissionState
	CWD                   string
	HeaderSeen            bool
	SkillCatalogPublished bool
	Time                  TimeContextState
	Since                 int64
	Preset                string
	PresetLocked          bool
	Calls                 map[string]string
	Turns                 map[uint64]bool // true after turn/end
	Relayed               map[string]bool
}

var runtimeDefinition = ProjectionDefinition[runtimeProjection]{Key: "execution", Version: 1,
	Init: func(h Header, _ uint64) runtimeProjection {
		return runtimeProjection{CWD: h.CWD, Preset: h.AgentPreset, Since: h.CreatedAt, Permission: PermissionState{"danger-full-access", "never"}, Attachments: make(map[string][]HistoricalAttachment), Calls: make(map[string]string), Turns: make(map[uint64]bool), Relayed: make(map[string]bool)}
	},
	Apply: func(s runtimeProjection, e Event) (runtimeProjection, error) {
		if e.Time > s.Since {
			s.Since = e.Time
		}
		switch e.Type {
		case "turn/start":
			s.TurnStarts = append(s.TurnStarts, e.Seq)
			var d struct{ Turn uint64 }
			_ = json.Unmarshal(e.Data, &d)
			if d.Turn > s.Boundary.LastTurn {
				s.Boundary.LastTurn = d.Turn
			}
			s.Boundary = TurnBoundary{LastTurn: s.Boundary.LastTurn, Turn: d.Turn, Open: true, OpenTurnStartSeq: e.Seq}
			s.Turns[d.Turn] = false
			s.PresetLocked = true
			s.Time.HasTurnInjection = false
			s.Time.LastTurnInjection = 0
			s.Time.BrowserTimeZones = nil
		case "turn/end":
			var d struct{ Turn uint64 }
			_ = json.Unmarshal(e.Data, &d)
			s.Turns[d.Turn] = true
			s.Boundary.Open = false
			s.Boundary.DirectHuman = false
			s.Boundary.GoalRounds = nil
			s.Time.HasTurnInjection = false
			s.Time.LastTurnInjection = 0
		case "step/start":
			var d struct {
				Turn uint64
				Step int
			}
			_ = json.Unmarshal(e.Data, &d)
			if d.Turn == s.Boundary.Turn && d.Step > s.Boundary.Step {
				s.Boundary.Step = d.Step
			}
			s.Time.BeforeStepMessage = s.Time.LastMessage
			s.Time.HasBeforeStepMessage = s.Time.HasMessage
		case "sandbox/mode":
			var d struct{ Mode string }
			if json.Unmarshal(e.Data, &d) == nil && (d.Mode == "read-only" || d.Mode == "workspace-write" || d.Mode == "danger-full-access") {
				s.Permission.Mode = d.Mode
			}
		case "approval/policy":
			var d struct{ Policy string }
			if json.Unmarshal(e.Data, &d) == nil && (d.Policy == "ask" || d.Policy == "never") {
				s.Permission.Approval = d.Policy
			}
		case "working-directory/change":
			var d struct{ CWD string }
			if json.Unmarshal(e.Data, &d) == nil && filepath.IsAbs(d.CWD) {
				s.CWD = filepath.Clean(d.CWD)
			}
		case "request/header":
			s.HeaderSeen = true
		case "agent-preset/selected":
			selected, err := EffectiveAgentPreset(Header{AgentPreset: s.Preset}, []Event{e})
			if err != nil {
				return s, err
			}
			s.Preset = selected
		case "tool/call":
			var d struct{ CallID, Name string }
			if json.Unmarshal(e.Data, &d) == nil {
				s.Calls[d.CallID] = d.Name
			}
		case "user/message":
			s.captureAttachments(e)
			var d struct {
				ID     string
				Source struct {
					Kind                  string
					GoalID                string
					Revision, Round       uint64
					RPCID, ClientTimeZone string
					Entries               json.RawMessage
				}
			}
			if json.Unmarshal(e.Data, &d) == nil {
				if d.Source.Kind == "user" && !s.FirstUserSeen {
					s.FirstUserSeen = true
					var message struct{ Content []struct{ Type, Text string } }
					if json.Unmarshal(e.Data, &message) == nil {
						for _, block := range message.Content {
							if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
								runes := []rune(strings.Join(strings.Fields(block.Text), " "))
								s.FirstPrompt = string(runes[:min(len(runes), 58)])
								break
							}
						}
					}
				}
				if d.Source.Kind == "skill-catalog" {
					var entries []struct{ Name, Description string }
					if json.Unmarshal(d.Source.Entries, &entries) == nil && entries != nil {
						s.SkillCatalogPublished = true
					}
				}
				if d.Source.Kind == "agent-message" {
					s.Relayed[d.ID] = true
				}
				if s.Boundary.Open {
					if d.Source.Kind == "user" {
						s.Boundary.DirectHuman = true
					}
					if d.Source.Kind == "goal" {
						s.Boundary.GoalRounds = append(s.Boundary.GoalRounds, AdmittedGoalRound{GoalRef{d.Source.GoalID, d.Source.Revision}, d.Source.Round})
					}
					if d.Source.Kind == "user" && d.Source.RPCID != "" && d.Source.ClientTimeZone != "" && !slices.Contains(s.Time.BrowserTimeZones, d.Source.ClientTimeZone) {
						s.Time.BrowserTimeZones = append(s.Time.BrowserTimeZones, d.Source.ClientTimeZone)
					}
				}
				if d.Source.Kind == "time-context" {
					s.Time.HasInjection = true
					s.Time.LastInjection = e.Time
					s.Time.HasTurnInjection = true
					s.Time.LastTurnInjection = e.Time
				}
			}
			s.Time.HasMessage = true
			s.Time.LastMessage = e.Time
		case "agent/inbox/spliced":
			s.captureAttachments(e)
		case "assistant/message", "tool/result":
			s.captureAttachments(e)
			s.Time.HasMessage = true
			s.Time.LastMessage = e.Time
		}
		return s, nil
	},
}

func runtimeValue[R any](s *LiveSession, cut ProjectionCut, selectValue func(runtimeProjection) R) R {
	value, err := ReadProjection(ProjectionHandle[runtimeProjection]{s, &runtimeDefinition}, cut, selectValue)
	if err != nil {
		panic(err)
	} // Required built-in state; never silently substitute permissions or authority.
	return value
}
func (s *LiveSession) TurnBoundary(cut ProjectionCut) TurnBoundary {
	return runtimeValue(s, cut, func(r runtimeProjection) TurnBoundary {
		b := r.Boundary
		b.GoalRounds = slices.Clone(b.GoalRounds)
		return b
	})
}
func (s *LiveSession) Permission() PermissionState {
	return runtimeValue(s, LiveCut, func(r runtimeProjection) PermissionState { return r.Permission })
}
func (s *LiveSession) WorkingDirectory() string {
	return runtimeValue(s, LiveCut, func(r runtimeProjection) string { return r.CWD })
}
func (s *LiveSession) HasRequestHeader() bool {
	return runtimeValue(s, LiveCut, func(r runtimeProjection) bool { return r.HeaderSeen })
}
func (s *LiveSession) SkillCatalogPublished() bool {
	return runtimeValue(s, LiveCut, func(r runtimeProjection) bool { return r.SkillCatalogPublished })
}
func (s *LiveSession) TimeContext() TimeContextState {
	return runtimeValue(s, LiveCut, func(r runtimeProjection) TimeContextState {
		t := r.Time
		t.BrowserTimeZones = slices.Clone(t.BrowserTimeZones)
		return t
	})
}
func (s *LiveSession) ToolCallName(id string) string {
	return runtimeValue(s, LiveCut, func(r runtimeProjection) string { return r.Calls[id] })
}
func (s *LiveSession) TurnStatus(turn uint64, cut ProjectionCut) (bool, bool) {
	v := runtimeValue(s, cut, func(r runtimeProjection) [2]bool { ended, found := r.Turns[turn]; return [2]bool{found, ended} })
	return v[0], v[1]
}
func (s *LiveSession) HasRelayedMessage(id string) bool {
	return runtimeValue(s, DurableCut, func(r runtimeProjection) bool { return r.Relayed[id] })
}
func (s *LiveSession) AgentPreset(cut ProjectionCut) string {
	return runtimeValue(s, cut, func(r runtimeProjection) string { return r.Preset })
}
func (s *LiveSession) IsAgentPresetLocked() bool {
	return runtimeValue(s, LiveCut, func(r runtimeProjection) bool { return r.PresetLocked })
}

func (s *runtimeProjection) captureAttachments(event Event) {
	var messages []json.RawMessage
	switch event.Type {
	case "user/message":
		messages = []json.RawMessage{event.Data}
	case "assistant/message", "tool/result":
		var row struct{ Message json.RawMessage }
		if json.Unmarshal(event.Data, &row) == nil {
			messages = []json.RawMessage{row.Message}
		}
	case "agent/inbox/spliced":
		var row struct{ Inserted []json.RawMessage }
		if json.Unmarshal(event.Data, &row) == nil {
			messages = row.Inserted
		}
	}
	for _, raw := range messages {
		var message struct{ Content []HistoricalAttachment }
		if json.Unmarshal(raw, &message) != nil {
			continue
		}
		for _, block := range message.Content {
			if block.Type != "file" && block.Type != "image" {
				continue
			}
			var ref struct {
				AttachmentID string `json:"attachmentId"`
				Name         string `json:"name"`
			}
			if json.Unmarshal(block.Attachment, &ref) != nil || ref.AttachmentID == "" {
				continue
			}
			duplicate := false
			for _, prior := range s.Attachments[ref.AttachmentID] {
				var p struct{ Name string }
				_ = json.Unmarshal(prior.Attachment, &p)
				if p.Name == ref.Name {
					duplicate = true
					break
				}
			}
			if !duplicate {
				s.Attachments[ref.AttachmentID] = append(s.Attachments[ref.AttachmentID], block)
			}
		}
	}
}
func (s runtimeProjection) attachment(id, name string) *HistoricalAttachment {
	for _, block := range s.Attachments[id] {
		var ref struct{ Name string }
		_ = json.Unmarshal(block.Attachment, &ref)
		if name == "" || ref.Name == name {
			copy := block
			copy.Attachment = slices.Clone(copy.Attachment)
			return &copy
		}
	}
	return nil
}
