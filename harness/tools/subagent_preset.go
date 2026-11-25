package tools

import (
	"context"
	"errors"
	"slices"

	"go-bot/harness/agentpreset"
)

type SubagentPresetSelection struct {
	AgentPreset         string
	AllowedAgentPresets []string
}

type ChildPresetCoordinator interface {
	SpawnChildWithPreset(context.Context, string, string, string, string, string, bool) (string, error)
}

func (s SubagentPresetSelection) ValidateConfig(fork bool) error {
	if fork && (s.AgentPreset != "" || s.AllowedAgentPresets != nil) {
		return errors.New("fork inherits the parent preset; independent selection is unavailable")
	}
	if s.AllowedAgentPresets != nil && len(s.AllowedAgentPresets) == 0 {
		return errors.New("allowedAgentPresets must not be empty")
	}
	seen := map[string]bool{}
	for _, id := range s.AllowedAgentPresets {
		if err := agentpreset.Validate(agentpreset.Definition{ID: id}); err != nil || seen[id] {
			return errors.New("invalid or duplicate allowed Agent Preset")
		}
		seen[id] = true
	}
	if s.AgentPreset != "" {
		if err := agentpreset.Validate(agentpreset.Definition{ID: s.AgentPreset}); err != nil {
			return err
		}
		if s.AllowedAgentPresets != nil && !slices.Contains(s.AllowedAgentPresets, s.AgentPreset) {
			return errors.New("default child Agent Preset must be in allowedAgentPresets")
		}
	}
	return nil
}

func (s SubagentPresetSelection) Select(requested string, provided, fork bool) (string, error) {
	if err := s.ValidateConfig(fork); err != nil {
		return "", err
	}
	if !provided {
		return s.AgentPreset, nil
	}
	if fork || s.AllowedAgentPresets == nil {
		return "", errors.New("agent_preset selection is disabled for this tool")
	}
	if !slices.Contains(s.AllowedAgentPresets, requested) {
		return "", errors.New("agent_preset is not in this tool's allowedAgentPresets")
	}
	return requested, nil
}
