package sessionformat

import (
	"encoding/json"
	"testing"
)

func TestAgentPresetReadsSelectionInsteadOfRewritingCreationHeader(t *testing.T) {
	header := Header{AgentPreset: "standard"}
	events := []Event{{Type: "assistant/message", Data: json.RawMessage(`{"text":"welcome"}`)}, {Type: "agent-preset/selected", Seq: 1, Data: json.RawMessage(`{"agentPreset":"research"}`)}}
	id, err := EffectiveAgentPreset(header, events)
	if err != nil || id != "research" || header.AgentPreset != "standard" || AgentPresetLocked(events) {
		t.Fatalf("%s %v", id, err)
	}
	events = append(events, Event{Type: "turn/start"})
	if !AgentPresetLocked(events) {
		t.Fatal("started session can change preset")
	}
	events = append(events, Event{Type: "agent-preset/selected", Seq: 3, Data: json.RawMessage(`{}`)})
	if _, err := EffectiveAgentPreset(header, events); err == nil {
		t.Fatal("accepted invalid selection")
	}
}
