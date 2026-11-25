package sessionformat

import (
	"encoding/json"
	"fmt"
	"strings"
)

// EffectiveAgentPreset reads committed selection facts without activating an
// Agent or loading its composition. The Header remains the creation fact.
func EffectiveAgentPreset(header Header, events []Event) (string, error) {
	id := header.AgentPreset
	for _, event := range events {
		if event.Type != "agent-preset/selected" {
			continue
		}
		var data struct {
			AgentPreset string `json:"agentPreset"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil || strings.TrimSpace(data.AgentPreset) == "" {
			return "", fmt.Errorf("invalid agent-preset/selected event at seq %d", event.Seq)
		}
		id = data.AgentPreset
	}
	return id, nil
}

func AgentPresetLocked(events []Event) bool {
	for _, event := range events {
		if event.Type == "turn/start" {
			return true
		}
	}
	return false
}
