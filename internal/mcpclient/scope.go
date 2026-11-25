package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"

	"go-bot/harness/agent"
	"go-bot/harness/skills"
)

type serverView struct {
	name, loading, state string
	connection           *Connection
	generation           context.Context
	discovery            Discovery
	tools                []*Tool
}

func (m *Manager) views() []serverView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []serverView
	for _, entry := range m.entries {
		if !entry.config.IsEnabled() {
			continue
		}
		result = append(result, serverView{name: entry.name, loading: entry.config.Loading(), state: entry.status.State, connection: entry.connection, generation: entry.generation, discovery: entry.discovery, tools: append([]*Tool(nil), entry.tools...)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result
}

// Scope belongs to one live Agent instance. A fork or cold recovery creates a
// new Scope, not a journal-restored set of registrations. Loaded names survive
// connection generations only within this Scope while they remain offered.
type Scope struct {
	manager *Manager
	mu      sync.Mutex
	loaded  map[string]map[string]bool
	closed  bool
}

func (m *Manager) NewScope() *Scope { return &Scope{manager: m, loaded: map[string]map[string]bool{}} }
func (s *Scope) Close()             { s.mu.Lock(); s.closed = true; s.loaded = nil; s.mu.Unlock() }

func (s *Scope) Tools(_ context.Context, _ string) ([]agent.Tool, error) {
	views := s.manager.views()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fail("MCP_SCOPE_CLOSED", "MCP Agent scope is closed")
	}
	var result []agent.Tool
	for _, view := range views {
		if view.loading == "eager" {
			for _, tool := range view.tools {
				result = append(result, tool)
			}
			continue
		}
		if view.generation == nil {
			continue
		}
		result = append(result, &loaderTool{scope: s, view: view})
		offered := map[string]bool{}
		for _, tool := range view.tools {
			raw := tool.definition.RawName
			if raw == "load_tools" {
				continue
			}
			offered[raw] = true
			if s.loaded[view.name][raw] {
				result = append(result, tool)
			}
		}
		for raw := range s.loaded[view.name] {
			if !offered[raw] {
				delete(s.loaded[view.name], raw)
			}
		}
	}
	if len(views) > 0 {
		for _, name := range []string{"list_mcp_resources", "list_mcp_resource_templates", "read_mcp_resource"} {
			result = append(result, &resourceTool{manager: s.manager, name: name})
		}
	}
	return result, nil
}

func (s *Scope) Context(_ context.Context, _ string) ([]agent.RuntimeContextSection, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, fail("MCP_SCOPE_CLOSED", "MCP Agent scope is closed")
	}
	views := s.manager.views()
	if len(views) == 0 {
		return nil, nil
	}
	var sections []agent.RuntimeContextSection
	names := make([]string, 0, len(views))
	for _, view := range views {
		names = append(names, view.name)
	}
	sections = append(sections, agent.RuntimeContextSection{Name: "mcp:servers", Source: "builtin:mcp", Text: "Configured MCP servers: " + strings.Join(names, ", ") + ". MCP resource tools require an explicit server name."})
	for _, view := range views {
		if view.discovery.Instructions != "" {
			sections = append(sections, agent.RuntimeContextSection{Name: "mcp:" + view.name + ":instructions", Source: "mcp:" + view.name, Text: "MCP server " + view.name + " instructions (literal text):\n" + view.discovery.Instructions})
		}
		if view.loading != "on-demand" || len(view.tools) == 0 {
			continue
		}
		var definitions []ToolDefinition
		for _, tool := range view.tools {
			if tool.definition.RawName != "load_tools" {
				definitions = append(definitions, tool.definition)
			}
		}
		sort.Slice(definitions, func(i, j int) bool { return jsLess(definitions[i].RawName, definitions[j].RawName) })
		if len(definitions) == 0 {
			continue
		}
		lines := []string{"### On-demand tools from MCP server " + view.name, "These tools are not in your tool list. Before calling one, load it with " + PublicToolName(view.name, "load_tools") + "."}
		var catalog []string
		for _, definition := range definitions {
			summary := descriptionSummary(definition.Description)
			line := "- " + definition.RawName
			if summary != "" {
				line += ": " + summary
			}
			catalog = append(catalog, line)
		}
		lines = append(lines, strings.Join(catalog, "\n"))
		sections = append(sections, agent.RuntimeContextSection{Name: "mcp:" + view.name + ":catalog", Source: "mcp:" + view.name, Text: strings.Join(lines, "\n\n")})
	}
	return sections, nil
}

func jsLess(a, b string) bool {
	x, y := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return len(x) < len(y)
}
func descriptionSummary(text string) string {
	text = strings.TrimFunc(text, skills.IsSpace)
	text = strings.SplitN(text, "\n", 2)[0]
	text = strings.Join(strings.FieldsFunc(text, skills.IsSpace), " ")
	if len(utf16.Encode([]rune(text))) <= 160 {
		return text
	}
	var runes []rune
	units := 0
	for _, r := range text {
		width := 1
		if r > 0xffff {
			width = 2
		}
		if units+width > 157 {
			break
		}
		units += width
		runes = append(runes, r)
	}
	return string(runes) + "..."
}

type loaderTool struct {
	scope *Scope
	view  serverView
}

func (t *loaderTool) ContributionSource() string { return "mcp:" + t.view.name }

func (t *loaderTool) Spec() agent.ToolSpec {
	return agent.ToolSpec{Name: PublicToolName(t.view.name, "load_tools"), Description: fmt.Sprintf("Add tools from MCP server %s to your tool list. The system prompt lists them under \"On-demand tools from MCP server %s\".", t.view.name, t.view.name), Parameters: json.RawMessage(`{"type":"object","properties":{"tools":{"type":"array","items":{"type":"string"},"minItems":1}},"required":["tools"],"additionalProperties":false}`)}
}
func (t *loaderTool) Validate(raw json.RawMessage) error {
	var input struct {
		Tools []string `json:"tools"`
	}
	if json.Unmarshal(raw, &input) != nil || len(input.Tools) == 0 {
		return fail("MCP_INVALID_ARGUMENTS", "MCP loader requires at least one tool name")
	}
	return nil
}
func (t *loaderTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := t.Validate(raw); err != nil {
		return "", err
	}
	if t.view.generation == nil || t.view.generation.Err() != nil {
		return "", fail("MCP_SERVER_UNAVAILABLE", "MCP tool catalog is unavailable")
	}
	var input struct {
		Tools []string `json:"tools"`
	}
	_ = json.Unmarshal(raw, &input)
	offered := map[string]string{}
	for _, tool := range t.view.tools {
		if tool.definition.RawName != "load_tools" {
			offered[tool.definition.RawName] = tool.definition.PublicName
		}
	}
	for _, name := range input.Tools {
		if offered[name] == "" {
			return "", fail("MCP_UNKNOWN_TOOL", fmt.Sprintf("MCP server %s does not offer %q; use names from its on-demand tool list", t.view.name, name))
		}
	}
	t.scope.mu.Lock()
	defer t.scope.mu.Unlock()
	if t.scope.closed {
		return "", fail("MCP_SCOPE_CLOSED", "MCP Agent scope is closed")
	}
	if t.scope.loaded[t.view.name] == nil {
		t.scope.loaded[t.view.name] = map[string]bool{}
	}
	var loaded, present []string
	seen := map[string]bool{}
	for _, name := range input.Tools {
		if seen[name] {
			continue
		}
		seen[name] = true
		if t.scope.loaded[t.view.name][name] {
			present = append(present, offered[name])
		} else {
			loaded = append(loaded, offered[name])
			t.scope.loaded[t.view.name][name] = true
		}
	}
	var message []string
	if len(loaded) > 0 {
		message = append(message, "Loaded: "+strings.Join(loaded, ", ")+".")
	}
	if len(present) > 0 {
		message = append(message, "Already loaded: "+strings.Join(present, ", ")+".")
	}
	return strings.Join(message, "\n"), nil
}
