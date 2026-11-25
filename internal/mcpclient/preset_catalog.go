package mcpclient

import "go-bot/harness/agentpreset"

// PresetCatalog reads already discovered metadata, never making MCP requests.
func (m *Manager) PresetCatalog() agentpreset.Catalog {
	c := agentpreset.Catalog{Tools: map[string]agentpreset.SourceRef{}}
	for _, view := range m.views() {
		source := agentpreset.SourceRef("mcp:" + view.name)
		c.Sources = append(c.Sources, source)
		for _, tool := range view.tools {
			c.Tools[tool.Spec().Name] = source
		}
		if view.loading == "on-demand" {
			c.Tools[PublicToolName(view.name, "load_tools")] = source
		}
	}
	if len(c.Sources) > 0 {
		c.Sources = append(c.Sources, "builtin:mcp")
		for _, name := range []string{"list_mcp_resources", "list_mcp_resource_templates", "read_mcp_resource"} {
			c.Tools[name] = "builtin:mcp"
		}
	}
	return c
}
