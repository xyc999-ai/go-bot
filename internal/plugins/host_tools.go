package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go-bot/harness/agent"
	"go-bot/harness/content"
	"go-bot/internal/modeldata"
)

var hostToolName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func decodeHostJSON(raw json.RawMessage, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return failure("INVALID_ARGUMENT", err.Error())
	}
	if !errors.Is(d.Decode(new(any)), io.EOF) {
		return failure("INVALID_ARGUMENT", "Expected one JSON object")
	}
	return nil
}

type hostTool struct {
	runtime    *hostBridge
	row        *hostContribution
	definition struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
		Concurrency string          `json:"concurrency,omitempty"`
		TimeoutMs   int64           `json:"timeoutMs,omitempty"`
	}
	schema                    *jsonschema.Schema
	sessionID, cwd, workspace string
}

func (t *hostTool) ContributionSource() string { return "plugin:" + t.row.Owner }

func (r *hostBridge) newTool(row *hostContribution) (*hostTool, error) {
	t := &hostTool{runtime: r, row: row}
	if err := decodeHostJSON(row.Descriptor, &t.definition); err != nil {
		return nil, err
	}
	d := t.definition
	if !hostToolName.MatchString(d.Name) || len(row.Owner+"__"+d.Name) > 64 || strings.TrimSpace(d.Description) == "" || len(d.Description) > 32000 || d.TimeoutMs < 0 || d.TimeoutMs > int64((24*time.Hour)/time.Millisecond) {
		return nil, failure("INVALID_ARGUMENT", "Invalid tool definition")
	}
	if d.Concurrency != "" && d.Concurrency != "exclusive" && d.Concurrency != "parallel" {
		return nil, failure("INVALID_ARGUMENT", "Invalid tool concurrency")
	}
	var schema map[string]any
	if json.Unmarshal(d.InputSchema, &schema) != nil || schema["type"] != "object" {
		return nil, failure("INVALID_ARGUMENT", "Tool inputSchema needs an object root")
	}
	compiled, err := compileSchema(d.InputSchema)
	if err != nil {
		return nil, err
	}
	t.schema = compiled
	found := false
	for _, callback := range row.Callbacks {
		if callback == "execute" {
			found = true
		}
	}
	if !found {
		return nil, failure("INVALID_ARGUMENT", "Tool execute callback is required")
	}
	return t, nil
}

func (t *hostTool) Spec() agent.ToolSpec {
	return agent.ToolSpec{Name: t.row.Owner + "__" + t.definition.Name, Description: t.definition.Description, Parameters: bytes.Clone(t.definition.InputSchema), ParallelSafe: t.definition.Concurrency == "parallel", Timeout: time.Duration(t.definition.TimeoutMs) * time.Millisecond}
}
func (t *hostTool) Validate(raw json.RawMessage) error { return validateSchema(t.schema, raw) }
func (t *hostTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	result, err := t.ExecuteResult(ctx, raw)
	var text []string
	for _, block := range result.ContentBlocks {
		if block.Type == "text" {
			text = append(text, block.Text)
		}
	}
	return strings.Join(text, "\n"), err
}

type hostResultBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func (t *hostTool) ExecuteResult(ctx context.Context, raw json.RawMessage) (modeldata.ToolResult, error) {
	if err := t.Validate(raw); err != nil {
		return modeldata.ToolResult{}, err
	}
	var output struct {
		Content []hostResultBlock `json:"content"`
		Data    json.RawMessage   `json:"data"`
	}
	call, finish := t.runtime.beginCall(ctx, t)
	defer finish()
	var encoded json.RawMessage
	if err := t.runtime.callback(ctx, t.row, "execute", raw, call, &encoded); err != nil {
		return modeldata.ToolResult{}, err
	}
	if err := decodeHostJSON(encoded, &output); err != nil {
		return modeldata.ToolResult{}, failure("INVALID_RESULT", "Invalid plugin tool result: "+err.Error())
	}
	if len(output.Content) == 0 || len(output.Content) > 256 {
		return modeldata.ToolResult{}, failure("INVALID_RESULT", "Tool content must contain between one and 256 blocks")
	}
	result := modeldata.ToolResult{}
	for _, block := range output.Content {
		switch block.Type {
		case "text":
			if block.Text == "" {
				return result, failure("INVALID_RESULT", "Tool text must not be empty")
			}
			result.ContentBlocks = append(result.ContentBlocks, content.ContentBlock{Type: "text", Text: block.Text})
		default:
			return result, failure("INVALID_RESULT", "Unsupported tool content block")
		}
	}
	if len(output.Data) == 0 {
		output.Data = json.RawMessage(`null`)
	}
	result.Meta, _ = json.Marshal(map[string]any{"canonicalValue": output.Data})
	return result, nil
}

func (r *hostBridge) Tools(ctx context.Context, sessionID string) ([]agent.Tool, error) {
	info, _ := agent.ToolSourceInfoFromContext(ctx)
	cwd := info.CWD
	if cwd == "" {
		cwd = r.config.Workspace
	}
	var result []agent.Tool
	for _, row := range r.rows("tool") {
		tool := *row.tool
		tool.sessionID = sessionID
		tool.cwd = cwd
		tool.workspace = cwd
		result = append(result, &tool)
	}
	return result, nil
}

func validateHostPrompt(row *hostContribution) error {
	var definition hostPromptDefinition
	if err := decodeHostJSON(row.Descriptor, &definition); err != nil {
		return err
	}
	if !identifier.MatchString(definition.ID) || (definition.Location != "system" && definition.Location != "context") {
		return failure("INVALID_ARGUMENT", "Invalid prompt contribution")
	}
	if definition.Text != nil {
		return nil
	}
	for _, method := range row.Callbacks {
		if method == "text" {
			return nil
		}
	}
	return fmt.Errorf("a prompt needs static text or a text callback")
}
