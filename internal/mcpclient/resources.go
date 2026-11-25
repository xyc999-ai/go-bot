package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go-bot/harness/agent"
	"go-bot/harness/content"
	"go-bot/internal/modeldata"
	mcpconfig "go-bot/mcp"
)

func (m *Manager) resourceConnection(name string) (*Connection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry := m.entries[name]
	if entry == nil || !entry.config.IsEnabled() {
		return nil, fail("MCP_NOT_FOUND", "MCP server not configured: "+name)
	}
	if entry.status.State == "waiting-authorization" {
		return nil, fail("MCP_AUTHORIZATION_REQUIRED", "Authorize this MCP server in settings before using it")
	}
	if entry.status.State == "waiting-credentials" {
		return nil, fail("MCP_CREDENTIALS_REQUIRED", "Configure this MCP server's credentials before using it")
	}
	if entry.status.State != "connected" || entry.connection == nil {
		return nil, fail("MCP_SERVER_UNAVAILABLE", "MCP server is not connected: "+name)
	}
	return entry.connection, nil
}

type resourceTool struct {
	manager *Manager
	name    string
}

func (t *resourceTool) ContributionSource() string { return "builtin:mcp" }

func (t *resourceTool) Spec() agent.ToolSpec {
	parameters := json.RawMessage(`{"type":"object","properties":{"server":{"type":"string"}},"required":["server"],"additionalProperties":false}`)
	description := "List resources from an explicitly configured MCP server."
	if t.name == "list_mcp_resource_templates" {
		description = "List resource URI templates from an explicitly configured MCP server."
	}
	if t.name == "read_mcp_resource" {
		description = "Read a resource URI from an explicitly configured MCP server."
		parameters = json.RawMessage(`{"type":"object","properties":{"server":{"type":"string"},"uri":{"type":"string"}},"required":["server","uri"],"additionalProperties":false}`)
	}
	return agent.ToolSpec{Name: t.name, Description: description, Parameters: parameters, Timeout: RequestTimeout}
}
func (t *resourceTool) Validate(raw json.RawMessage) error {
	var input struct {
		Server string `json:"server"`
		URI    string `json:"uri"`
	}
	if json.Unmarshal(raw, &input) != nil || !mcpconfig.ValidServerName(input.Server) || (t.name == "read_mcp_resource" && input.URI == "") {
		return fail("MCP_INVALID_ARGUMENTS", "Specify a configured MCP server and required resource URI")
	}
	return nil
}
func (t *resourceTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	result, err := t.ExecuteResult(ctx, raw)
	if err != nil {
		return "", err
	}
	data, _ := json.Marshal(result.ContentBlocks)
	return string(data), nil
}
func (t *resourceTool) ExecutePresented(ctx context.Context, raw json.RawMessage) (string, json.RawMessage, error) {
	result, err := t.ExecuteResult(ctx, raw)
	return result.Content, result.Meta, err
}
func (t *resourceTool) ExecuteRich(ctx context.Context, raw json.RawMessage) (string, []content.ContentBlock, json.RawMessage, error) {
	result, err := t.ExecuteResult(ctx, raw)
	return result.Content, result.ContentBlocks, result.Meta, err
}
func (t *resourceTool) ExecuteResult(ctx context.Context, raw json.RawMessage) (modeldata.ToolResult, error) {
	var empty modeldata.ToolResult
	if err := t.Validate(raw); err != nil {
		return empty, err
	}
	var input struct {
		Server string `json:"server"`
		URI    string `json:"uri"`
	}
	_ = json.Unmarshal(raw, &input)
	connection, err := t.manager.resourceConnection(input.Server)
	if err != nil {
		return empty, err
	}
	result := &sdk.CallToolResult{}
	switch t.name {
	case "list_mcp_resources":
		resources, err := connection.Resources(ctx)
		if err != nil {
			return empty, err
		}
		result.StructuredContent = map[string]any{"resources": resources}
	case "list_mcp_resource_templates":
		templates, err := connection.ResourceTemplates(ctx)
		if err != nil {
			return empty, err
		}
		result.StructuredContent = map[string]any{"resourceTemplates": templates}
	case "read_mcp_resource":
		resource, err := connection.ReadResource(ctx, input.URI)
		if err != nil {
			return empty, err
		}
		for _, row := range resource.Contents {
			if row == nil {
				return empty, fail("MCP_INVALID_RESULT", "MCP resource contains an empty entry")
			}
			if len(row.Blob) > 0 && supportedImageType(row.MIMEType) {
				result.Content = append(result.Content, &sdk.TextContent{Text: fmt.Sprintf("MCP resource %s", row.URI)}, &sdk.ImageContent{Data: row.Blob, MIMEType: row.MIMEType})
			} else {
				result.Content = append(result.Content, &sdk.EmbeddedResource{Resource: row})
			}
		}
	default:
		return empty, fail("MCP_INVALID_ARGUMENTS", "Unknown MCP resource operation")
	}
	adapter := &Tool{connection: connection, definition: ToolDefinition{RawName: t.name}, options: ToolOptions{Images: t.manager.options.Images}}
	return adapter.materialize(ctx, result)
}

func supportedImageType(mime string) bool {
	switch mime {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return true
	}
	return false
}
