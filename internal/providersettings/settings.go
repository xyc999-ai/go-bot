// Package providersettings defines account settings and their public projection.
package providersettings

import (
	"strings"
	"unicode"
)

type Descriptor struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DefaultModel string `json:"defaultModel,omitempty"`
	KeyOptional  bool   `json:"keyOptional,omitempty"`
}

type Credential struct {
	APIKey string `json:"apiKey"`
	Model  string `json:"model,omitempty"`
}

type Settings struct {
	Provider string                `json:"provider,omitempty"`
	Entries  map[string]Credential `json:"entries,omitempty"`
	APIKey   string                `json:"apiKey,omitempty"` // Legacy web search records.
}

type Selection struct {
	Provider string `json:"provider,omitempty"`
	Credential
}

func (s Settings) Current(defaultID string) Selection {
	id := s.Provider
	if id == "" {
		id = defaultID
	}
	return Selection{Provider: id, Credential: s.Entry(id, defaultID)}
}

func (s Settings) Entry(id, defaultID string) Credential {
	if entry, ok := s.Entries[id]; ok {
		return entry
	}
	if id == defaultID {
		return Credential{APIKey: s.APIKey}
	}
	return Credential{}
}

func (s Settings) WithEntry(id, defaultID string, entry Credential, selectProvider bool) Settings {
	entries := make(map[string]Credential, len(s.Entries)+1)
	for key, value := range s.Entries {
		entries[key] = value
	}
	if s.APIKey != "" {
		entries[defaultID] = s.Entry(defaultID, defaultID)
	}
	entries[id] = entry
	s.APIKey, s.Entries = "", entries
	if selectProvider {
		s.Provider = id
	}
	return s
}

func ValidAPIKey(key string) bool {
	return len(key) <= 4096 && strings.TrimSpace(key) == key && strings.IndexFunc(key, unicode.IsControl) == -1
}

func ValidModel(model string) bool {
	return model != "" && len(model) <= 256 && strings.IndexFunc(model, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) == -1
}
