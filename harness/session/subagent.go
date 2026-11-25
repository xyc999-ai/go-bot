package sessionformat

import (
	"encoding/json"
	"errors"
	"fmt"
)

// SubagentCatalogEntry is a parent-owned discovery fact. Local children use
// payload v0; upstream v1 also describes an unknown local mode, while v2
// describes an external execution without a local child Session. These
// payload versions are independent of the Session v4 format version.
type SubagentCatalogEntry struct {
	Version        uint64  `json:"version"`
	ChildID        string  `json:"childId"`
	ChildCreatedAt uint64  `json:"childCreatedAt"`
	Mode           string  `json:"mode"`
	Label          *string `json:"label,omitempty"`
}

func DecodeSubagentCatalog(raw json.RawMessage) (SubagentCatalogEntry, error) {
	fields, err := objectValue(raw)
	if err != nil {
		return SubagentCatalogEntry{}, errors.New("subagent/catalog requires object data")
	}
	version, err := safeCount(fields["version"])
	if err != nil || version > 2 {
		return SubagentCatalogEntry{}, errors.New("subagent/catalog has unsupported version")
	}
	var mode string
	if json.Unmarshal(fields["mode"], &mode) != nil {
		return SubagentCatalogEntry{}, errors.New("subagent/catalog mode must be text")
	}
	validMode := (version <= 1 && (mode == "one-shot" || mode == "continuable")) ||
		(version == 1 && mode == "unknown") || (version == 2 && mode == "external")
	if !validMode {
		return SubagentCatalogEntry{}, errors.New("subagent/catalog mode is invalid")
	}
	expected := 4
	_, hasLabel := fields["label"]
	if hasLabel {
		expected++
	}
	if len(fields) != expected {
		return SubagentCatalogEntry{}, errors.New("subagent/catalog has unknown fields")
	}
	var childID string
	if json.Unmarshal(fields["childId"], &childID) != nil {
		return SubagentCatalogEntry{}, errors.New("subagent/catalog childId must be text")
	}
	createdAt, err := safeCount(fields["childCreatedAt"])
	if err != nil {
		return SubagentCatalogEntry{}, errors.New("subagent/catalog childCreatedAt must be nonnegative")
	}
	entry := SubagentCatalogEntry{Version: version, ChildID: childID, ChildCreatedAt: createdAt, Mode: mode}
	if mode == "continuable" && !hasLabel {
		return SubagentCatalogEntry{}, errors.New("continuable subagent/catalog requires label")
	}
	if hasLabel {
		var label string
		if json.Unmarshal(fields["label"], &label) != nil {
			return SubagentCatalogEntry{}, errors.New("subagent/catalog label must be text")
		}
		entry.Label = &label
	}
	return entry, nil
}

// FoldSubagentCatalog ignores inherited fork facts and preserves direct-child
// event order, matching the upstream parent projection.
func FoldSubagentCatalog(events []Event, inheritedEventCount uint64) ([]SubagentCatalogEntry, error) {
	children := make([]SubagentCatalogEntry, 0)
	for _, event := range events {
		if event.Type != "subagent/catalog" || event.Seq < inheritedEventCount {
			continue
		}
		entry, err := DecodeSubagentCatalog(event.Data)
		if err != nil {
			return nil, fmt.Errorf("subagent/catalog at seq %d: %w", event.Seq, err)
		}
		children = append(children, entry)
	}
	return children, nil
}

type SubagentToolFilter struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

type SubagentDescriptor struct {
	Version              uint64              `json:"version"`
	Mode                 string              `json:"mode"`
	Provider             string              `json:"provider"`
	Label                *string             `json:"label,omitempty"`
	AgentProvider        *string             `json:"agentProvider,omitempty"`
	AgentModel           *string             `json:"agentModel,omitempty"`
	AgentReasoningEffort *string             `json:"agentReasoningEffort,omitempty"`
	Persona              *string             `json:"persona,omitempty"`
	ToolFilter           *SubagentToolFilter `json:"toolFilter,omitempty"`
}

func decodeDescriptorOptionalString(fields map[string]json.RawMessage, key string) (*string, error) {
	raw, ok := fields[key]
	if !ok {
		return nil, nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return nil, fmt.Errorf("subagent/descriptor %s must be text", key)
	}
	return &value, nil
}

// DecodeSubagentDescriptor follows the current upstream v3 parser. A foreign
// descriptor version is not claimed as resumable by this runtime.
func DecodeSubagentDescriptor(raw json.RawMessage) (*SubagentDescriptor, error) {
	fields, err := objectValue(raw)
	if err != nil {
		return nil, errors.New("subagent/descriptor requires object data")
	}
	version, err := safeCount(fields["version"])
	if err != nil {
		return nil, errors.New("subagent/descriptor version must be numeric")
	}
	if version != 3 {
		return nil, nil
	}
	var mode, provider string
	if json.Unmarshal(fields["mode"], &mode) != nil || mode != "one-shot" && mode != "continuable" {
		return nil, errors.New("subagent/descriptor mode is invalid")
	}
	if json.Unmarshal(fields["provider"], &provider) != nil {
		return nil, errors.New("subagent/descriptor provider must be text")
	}
	allowed := map[string]bool{"version": true, "mode": true, "provider": true, "label": true}
	if mode == "continuable" {
		for _, name := range []string{"agentProvider", "agentModel", "agentReasoningEffort", "persona", "toolFilter"} {
			allowed[name] = true
		}
	}
	for key := range fields {
		if !allowed[key] {
			return nil, fmt.Errorf("subagent/descriptor has unknown field %q", key)
		}
	}
	if mode == "continuable" {
		if _, ok := fields["label"]; !ok {
			return nil, errors.New("continuable subagent/descriptor requires label")
		}
	}
	descriptor := &SubagentDescriptor{Version: version, Mode: mode, Provider: provider}
	for _, field := range []struct {
		name string
		out  **string
	}{
		{"label", &descriptor.Label}, {"agentProvider", &descriptor.AgentProvider},
		{"agentModel", &descriptor.AgentModel}, {"agentReasoningEffort", &descriptor.AgentReasoningEffort},
		{"persona", &descriptor.Persona},
	} {
		value, err := decodeDescriptorOptionalString(fields, field.name)
		if err != nil {
			return nil, err
		}
		*field.out = value
	}
	if rawFilter, ok := fields["toolFilter"]; ok {
		filter, err := objectValue(rawFilter)
		if err != nil || len(filter) == 0 || len(filter) > 2 {
			return nil, errors.New("subagent/descriptor toolFilter is invalid")
		}
		parsed := &SubagentToolFilter{}
		for key, rawItems := range filter {
			if key != "allow" && key != "deny" {
				return nil, errors.New("subagent/descriptor toolFilter has unknown field")
			}
			var items []string
			if json.Unmarshal(rawItems, &items) != nil || items == nil {
				return nil, fmt.Errorf("subagent/descriptor toolFilter.%s must be a string array", key)
			}
			if key == "allow" {
				parsed.Allow = items
			} else {
				parsed.Deny = items
			}
		}
		descriptor.ToolFilter = parsed
	}
	return descriptor, nil
}

// FoldSubagentDescriptor uses the first descriptor as authoritative, even if
// another same-named event appears later.
func FoldSubagentDescriptor(events []Event) (*SubagentDescriptor, error) {
	for _, event := range events {
		if event.Type == "subagent/descriptor" {
			return DecodeSubagentDescriptor(event.Data)
		}
	}
	return nil, nil
}

type childCatalogState struct {
	inherited uint64
	entries   []SubagentCatalogEntry
}

var childCatalogDefinition = ProjectionDefinition[childCatalogState]{Key: "subagentCatalog", Version: 1,
	Init: func(_ Header, inherited uint64) childCatalogState {
		return childCatalogState{inherited: inherited, entries: []SubagentCatalogEntry{}}
	},
	Apply: func(s childCatalogState, e Event) (childCatalogState, error) {
		if e.Type != "subagent/catalog" || e.Seq < s.inherited {
			return s, nil
		}
		entry, err := DecodeSubagentCatalog(e.Data)
		if err != nil {
			return s, err
		}
		s.entries = append(s.entries, entry)
		return s, nil
	},
}

func (s *LiveSession) Children(cut ProjectionCut) ([]SubagentCatalogEntry, error) {
	return ReadProjection(ProjectionHandle[childCatalogState]{s, &childCatalogDefinition}, cut, func(state childCatalogState) []SubagentCatalogEntry {
		out := make([]SubagentCatalogEntry, len(state.entries))
		for i, entry := range state.entries {
			out[i] = entry
			if entry.Label != nil {
				label := *entry.Label
				out[i].Label = &label
			}
		}
		return out
	})
}
