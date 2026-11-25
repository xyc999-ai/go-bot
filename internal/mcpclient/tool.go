package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"go-bot/harness/agent"
	"go-bot/harness/content"
	"go-bot/internal/modeldata"
)

type ImageWriter interface {
	SaveImage(context.Context, []byte, string, string, string) (content.ImageAttachment, error)
}

type ToolOptions struct {
	Images   ImageWriter
	Lifetime context.Context
}

type Tool struct {
	connection *Connection
	definition ToolDefinition
	options    ToolOptions
}

func (t *Tool) ContributionSource() string { return "mcp:" + t.connection.Name }

func NewTool(connection *Connection, definition ToolDefinition, options ToolOptions) *Tool {
	definition.InputSchema = append(json.RawMessage(nil), definition.InputSchema...)
	definition.OutputSchema = append(json.RawMessage(nil), definition.OutputSchema...)
	if options.Lifetime == nil {
		options.Lifetime = connection.ctx
	}
	return &Tool{connection: connection, definition: definition, options: options}
}

func (t *Tool) Spec() agent.ToolSpec {
	return agent.ToolSpec{Name: t.definition.PublicName, Description: t.definition.Description, Parameters: append(json.RawMessage(nil), t.definition.InputSchema...), Timeout: RequestTimeout}
}
func (t *Tool) Validate(raw json.RawMessage) error {
	if t.definition.input == nil {
		return fail("MCP_INVALID_SCHEMA", "MCP tool has no validated input schema")
	}
	value, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		return err
	}
	return t.definition.input.Validate(value)
}

func (t *Tool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	result, err := t.ExecuteResult(ctx, args)
	if err != nil {
		return "", err
	}
	var text []string
	for _, block := range result.ContentBlocks {
		if block.Type == "text" {
			text = append(text, block.Text)
		}
	}
	message := strings.Join(text, "\n")
	if result.IsError {
		return message, fail(result.Code, message)
	}
	return message, nil
}
func (t *Tool) ExecutePresented(ctx context.Context, args json.RawMessage) (string, json.RawMessage, error) {
	result, err := t.ExecuteResult(ctx, args)
	if err != nil {
		return "", nil, err
	}
	var text []string
	for _, block := range result.ContentBlocks {
		if block.Type == "text" {
			text = append(text, block.Text)
		}
	}
	if result.IsError {
		err = fail(result.Code, strings.Join(text, "\n"))
	}
	return strings.Join(text, "\n"), result.Meta, err
}
func (t *Tool) ExecuteRich(ctx context.Context, args json.RawMessage) (string, []content.ContentBlock, json.RawMessage, error) {
	result, err := t.ExecuteResult(ctx, args)
	if err != nil {
		return "", nil, nil, err
	}
	if result.IsError {
		err = fail(result.Code, "MCP server reported a tool error")
	}
	return "", result.ContentBlocks, result.Meta, err
}

// ExecuteResult retains MCP's isError outcome and the order of its text/image
// blocks. Images enter the existing content-addressed attachment store once;
// canonical metadata contains references, never a second copy of pixels.
func (t *Tool) ExecuteResult(ctx context.Context, args json.RawMessage) (modeldata.ToolResult, error) {
	var output modeldata.ToolResult
	if err := t.Validate(args); err != nil {
		return output, err
	}
	if t.options.Lifetime.Err() != nil {
		return output, fail("MCP_TOOL_UNAVAILABLE", "This MCP tool generation is no longer available")
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(t.options.Lifetime, cancel)
	defer stop()
	defer cancel()
	result, err := t.connection.Call(ctx, t.definition.RawName, args)
	if err != nil {
		return output, err
	}
	if result == nil {
		return output, fail("MCP_INVALID_RESULT", "MCP tool returned no result")
	}
	if !result.IsError && t.definition.output != nil {
		if result.StructuredContent == nil {
			return output, fail("MCP_INVALID_RESULT", "MCP tool declared output schema but returned no structured content")
		}
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			return output, fail("MCP_INVALID_RESULT", "MCP structured content is not JSON")
		}
		value, err := jsonschema.UnmarshalJSON(strings.NewReader(string(data)))
		if err != nil || t.definition.output.Validate(value) != nil {
			return output, fail("MCP_INVALID_RESULT", "MCP structured content does not match its output schema")
		}
	}
	return t.materialize(ctx, result)
}

func (t *Tool) materialize(ctx context.Context, result *sdk.CallToolResult) (modeldata.ToolResult, error) {
	var output modeldata.ToolResult
	var err error
	canonical := map[string]any{"content": []any{}}
	protocol := []any{}
	totalText, totalImages := 0, 0
	addText := func(text string) error {
		if result.IsError {
			text = t.connection.redact(text)
		}
		totalText += len(text)
		if totalText > 256<<10 {
			return fail("MCP_RESULT_LIMIT", "MCP tool text exceeds 256 KiB")
		}
		if text != "" {
			output.ContentBlocks = append(output.ContentBlocks, content.ContentBlock{Type: "text", Text: text})
			protocol = append(protocol, map[string]any{"type": "text", "text": text})
		}
		return nil
	}
	for _, block := range result.Content {
		switch value := block.(type) {
		case *sdk.TextContent:
			if err = addText(value.Text); err != nil {
				return modeldata.ToolResult{}, err
			}
		case *sdk.ImageContent:
			if t.options.Images == nil {
				return output, fail("MCP_IMAGE_UNAVAILABLE", "MCP image attachment storage is unavailable")
			}
			totalImages += len(value.Data)
			if totalImages > 8<<20 {
				return output, fail("MCP_RESULT_LIMIT", "MCP tool images exceed 8 MiB")
			}
			switch value.MIMEType {
			case "image/png", "image/jpeg", "image/webp", "image/gif":
			default:
				return output, fail("MCP_INVALID_RESULT", "Unsupported MCP image media type")
			}
			ref, err := t.options.Images.SaveImage(ctx, value.Data, value.MIMEType, "", "")
			if err != nil {
				return output, fail("MCP_INVALID_RESULT", t.connection.redact(err.Error()))
			}
			output.ContentBlocks = append(output.ContentBlocks, content.ImageBlock(ref))
			protocol = append(protocol, map[string]any{"type": "image", "attachment": map[string]any{"attachmentId": ref.ID, "mediaType": ref.MIMEType, "bytes": ref.Bytes, "width": ref.Width, "height": ref.Height, "name": ref.Name}})
		case *sdk.ResourceLink:
			if err = addText(fmt.Sprintf("MCP resource %q: %s\n%s", value.Name, value.URI, value.Description)); err != nil {
				return output, err
			}
		case *sdk.EmbeddedResource:
			if value.Resource == nil {
				return output, fail("MCP_INVALID_RESULT", "MCP embedded resource is missing")
			}
			text := value.Resource.Text
			if len(value.Resource.Blob) > 0 {
				text = fmt.Sprintf("MCP resource %q contains %d bytes of %s; binary preview is not supported.", value.Resource.URI, len(value.Resource.Blob), value.Resource.MIMEType)
			}
			if err = addText(text); err != nil {
				return output, err
			}
		case *sdk.AudioContent:
			if err = addText(fmt.Sprintf("MCP audio result: %s, %d bytes; audio preview is not supported.", value.MIMEType, len(value.Data))); err != nil {
				return output, err
			}
		default:
			if err = addText("MCP returned a content type that this Host does not render."); err != nil {
				return output, err
			}
		}
	}
	if result.StructuredContent != nil && !result.IsError {
		canonical["structuredContent"] = result.StructuredContent
		if len(output.ContentBlocks) == 0 {
			data, err := json.Marshal(result.StructuredContent)
			if err != nil {
				return output, err
			}
			if err = addText(string(data)); err != nil {
				return output, err
			}
		}
	}
	canonical["content"] = protocol
	output.IsError = result.IsError
	if result.IsError {
		output.Code, output.ErrorName = "MCP_TOOL_ERROR", "McpToolError"
	}
	value, err := json.Marshal(canonical)
	if err != nil {
		return output, err
	}
	if len(value) > 512<<10 {
		return output, fail("MCP_RESULT_LIMIT", "MCP canonical result exceeds 512 KiB")
	}
	output.Meta, err = json.Marshal(map[string]any{"mcp": map[string]any{"server": t.connection.Name, "tool": t.definition.RawName}, "canonicalValue": json.RawMessage(value)})
	return output, err
}

var _ agent.ResultTool = (*Tool)(nil)
var _ agent.RichTool = (*Tool)(nil)
