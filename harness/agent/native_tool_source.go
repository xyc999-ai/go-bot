package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ToolSource resolves additional capabilities for this Session at the start of
// each model step. Retries and tool dispatch retain that exact set. Configure
// the source before driving the Agent; implementations must support concurrent
// reads by different Sessions and reject calls through disposed registrations.
type ToolSource func(context.Context, string) ([]Tool, error)

type ToolSourceInfo struct {
	SessionID      string                  `json:"sessionId"`
	CWD            string                  `json:"cwd"`
	Tools          []ToolSpec              `json:"tools"`
	ModelSelection ModelRoute              `json:"modelSelection"`
	SandboxPolicy  *ExecutionSandboxPolicy `json:"sandboxPolicy,omitempty"`
}
type toolSourceInfoKey struct{}

func ToolSourceInfoFromContext(ctx context.Context) (ToolSourceInfo, bool) {
	value, ok := ctx.Value(toolSourceInfoKey{}).(ToolSourceInfo)
	return value, ok
}

// WithToolSourceInfo lets an assembly adapter describe its resolved base
// capabilities to existing providers without changing tool execution.
func WithToolSourceInfo(ctx context.Context, info ToolSourceInfo) context.Context {
	return context.WithValue(ctx, toolSourceInfoKey{}, info)
}

func (a *NativeLoop) SetToolSource(source ToolSource) { a.toolSource = source }

func (a *NativeLoop) refreshTools(ctx context.Context) error {
	if a.toolSource == nil {
		if a.promptAssembler != nil {
			return a.installTools(a.baseTools)
		}
		return nil
	}
	info := ToolSourceInfo{SessionID: a.session.Header().ID, CWD: a.session.Header().CWD, ModelSelection: ModelRoute{Provider: a.provider, Model: a.modelID, ReasoningEffort: a.reasoningEffort}, Tools: []ToolSpec{}}
	info.SandboxPolicy = a.executionSandboxPolicy()
	for _, tool := range a.baseTools {
		info.Tools = append(info.Tools, tool.Spec())
	}
	ctx = context.WithValue(ctx, toolSourceInfoKey{}, info)
	additional, err := a.toolSource(ctx, a.session.Header().ID)
	if err != nil {
		return fmt.Errorf("resolve Session tools: %w", err)
	}
	tools := append(append([]Tool(nil), a.baseTools...), additional...)
	return a.installTools(tools)
}

// Publish only after every definition validates. A bad extension cannot leave
// a partially replaced table, shadow a built-in, or cause schema I/O.
func (a *NativeLoop) installTools(tools []Tool) error {
	a.nativeTools.mu.Lock()
	defer a.nativeTools.mu.Unlock()
	byName := make(map[string]Tool, len(tools))
	schemas := make(map[string]*jsonschema.Schema, len(tools))
	safe := make(map[string]bool, len(tools))
	specs := make([]ToolSpec, 0, len(tools))
	previous := make(map[string]ToolSpec, len(a.toolSpecs))
	for _, spec := range a.toolSpecs {
		previous[spec.Name] = spec
	}
	for _, tool := range tools {
		if tool == nil {
			return errors.New("native Agent tool is nil")
		}
		spec := tool.Spec()
		if spec.Name == "" || byName[spec.Name] != nil {
			return fmt.Errorf("invalid or duplicate native Agent tool %q", spec.Name)
		}
		schema := a.toolSchemas[spec.Name]
		if old, ok := previous[spec.Name]; !ok || !bytes.Equal(old.Parameters, spec.Parameters) || schema == nil {
			var err error
			schema, err = compileToolParameters(spec.Parameters)
			if err != nil {
				return fmt.Errorf("compile native Agent tool %q parameters: %w", spec.Name, err)
			}
		}
		spec.Parameters = bytes.Clone(spec.Parameters)
		byName[spec.Name], schemas[spec.Name], safe[spec.Name] = tool, schema, spec.ParallelSafe
		specs = append(specs, spec)
	}
	a.tools, a.toolSchemas, a.parallelSafe, a.toolSpecs = byName, schemas, safe, specs
	return nil
}
