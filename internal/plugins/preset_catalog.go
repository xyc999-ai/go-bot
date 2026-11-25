package plugins

import (
	"go-bot/harness/agentpreset"
)

// PresetCatalog inspects active Go registrations without contacting Node.
func (m *Manager) PresetCatalog() agentpreset.Catalog {
	c := agentpreset.Catalog{Tools: map[string]agentpreset.SourceRef{}}
	r, err := m.executionRuntime()
	if err != nil || r == nil {
		return c
	}
	seen := map[agentpreset.SourceRef]bool{}
	for _, kind := range []string{"tool", "skill", "prompt"} {
		for _, row := range r.rows(kind) {
			source := agentpreset.SourceRef("plugin:" + row.Owner)
			if !seen[source] {
				c.Sources = append(c.Sources, source)
				seen[source] = true
			}
			if row.tool != nil {
				c.Tools[row.tool.Spec().Name] = source
			}
		}
	}
	return c
}
