package assembly

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"go-bot/harness/agent"
	"go-bot/harness/agentpreset"
	"go-bot/harness/memory"
	"go-bot/harness/plan"
	"go-bot/harness/session"
	"go-bot/harness/skills"
	"go-bot/harness/tools"
)

type NativePresetToolProvider func(context.Context, NativeRuntimeIdentity, *sessionformat.LiveSession, Resources) ([]agent.Tool, error)

func (f NativeFactory) resolvePreset(ctx context.Context, identity NativeRuntimeIdentity) (NativeFactory, NativeRuntimeIdentity, error) {
	if f.Preset != nil {
		if identity.AgentPreset != "" && identity.AgentPreset != f.Preset.ID() {
			return f, identity, errors.New("Agent binding differs from saved preset")
		}
		identity.AgentPreset = f.Preset.ID()
		return f, identity, nil
	}
	if f.Presets == nil {
		f.Presets = agentpreset.New(agentpreset.Standard, nil)
		if _, err := f.Presets.Register(agentpreset.Definition{ID: agentpreset.Standard}); err != nil {
			return f, identity, err
		}
	}
	binding, err := f.Presets.Resolve(ctx, identity.AgentPreset)
	if err != nil {
		return f, identity, err
	}
	f.Preset, identity.AgentPreset = binding, binding.ID()
	return f, identity, nil
}

func toolPresetSource(tool agent.Tool) agentpreset.SourceRef {
	if source, ok := tool.(interface{ ContributionSource() string }); ok {
		return agentpreset.SourceRef(source.ContributionSource())
	}
	return agentpreset.BuiltinToolSource(tool.Spec().Name)
}

func (f NativeFactory) presetTools(inherited []agent.Tool, locals ...[]agent.Tool) ([]agent.Tool, error) {
	if f.Preset == nil {
		return nil, errors.New("Agent Preset binding is required")
	}
	d := f.Preset.Definition()
	filtered := make([]agent.Tool, 0, len(inherited))
	for _, tool := range inherited {
		if tool == nil {
			return nil, errors.New("nil inherited tool")
		}
		if d.Tools.Allows(tool.Spec().Name, toolPresetSource(tool)) {
			filtered = append(filtered, tool)
		}
	}
	layers := append([][]agent.Tool{filtered}, locals...)
	var result []agent.Tool
	positions := map[string]int{}
	for _, layer := range layers {
		seen := map[string]bool{}
		for _, tool := range layer {
			if tool == nil {
				return nil, errors.New("nil local tool")
			}
			name := tool.Spec().Name
			if name == "" || seen[name] {
				return nil, fmt.Errorf("duplicate or empty tool in one preset layer: %q", name)
			}
			seen[name] = true
			if i, exists := positions[name]; exists {
				result[i] = tool
			} else {
				positions[name] = len(result)
				result = append(result, tool)
			}
		}
	}
	return result, nil
}

func (f NativeFactory) presetSkills(registry *skills.Registry) *skills.Registry {
	if f.Preset == nil {
		return registry
	}
	view := *registry
	prior := registry.VisibleCandidate
	d := f.Preset.Definition()
	view.VisibleCandidate = func(lookup skills.Lookup, entry skills.Candidate) bool {
		return (prior == nil || prior(lookup, entry)) && d.Skills.Allows(entry.Name, agentpreset.SourceRef(entry.ContributionSource()))
	}
	return &view
}

func presetSectionAllowed(binding *agentpreset.Binding, name string, source agentpreset.SourceRef, runtime bool) bool {
	if binding == nil {
		return true
	}
	d := binding.Definition()
	if runtime {
		return d.RuntimeSources.Allows(name, source)
	}
	return d.PromptSections.Allows(name, source)
}

func presetContextSource(name string) agentpreset.SourceRef {
	if strings.HasPrefix(name, "mcp:") {
		parts := strings.SplitN(strings.TrimPrefix(name, "mcp:"), ":", 2)
		return agentpreset.SourceRef("mcp:" + parts[0])
	}
	if i := strings.IndexByte(name, '/'); i > 0 {
		return agentpreset.SourceRef("plugin:" + name[:i])
	}
	return agentpreset.SourceRef("builtin:" + strings.SplitN(name, ":", 2)[0])
}

func filterPresetContexts(binding *agentpreset.Binding, rows []agent.RuntimeContextSection, runtime bool) []agent.RuntimeContextSection {
	if binding == nil {
		return rows
	}
	d := binding.Definition()
	if runtime && d.Persona.IncludeRuntimeContext != nil && !*d.Persona.IncludeRuntimeContext {
		return nil
	}
	if !runtime && d.Persona.Complete {
		return nil
	}
	var filtered []agent.RuntimeContextSection
	for _, row := range rows {
		source := agentpreset.SourceRef(row.Source)
		if source == "" {
			source = presetContextSource(row.Name)
		}
		if presetSectionAllowed(binding, row.Name, source, runtime) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func (f NativeFactory) renderPresetPrompt(identity NativeRuntimeIdentity, toolset []agent.Tool, cwd string, planActive, structured bool) (string, error) {
	d := f.Preset.Definition()
	variables := map[string]string{"account.id": identity.UserID, "session.id": identity.SessionID, "cwd": cwd, "workspace": identity.Workspace}
	core := d.Persona.Prefix
	if core == "" {
		core = f.SystemPrompt
	}
	core, err := agentpreset.Render(core, variables)
	if err != nil {
		return "", err
	}
	if d.Persona.Complete {
		return core, nil
	}
	var result strings.Builder
	add := func(name string, source agentpreset.SourceRef, text string) {
		if strings.TrimSpace(text) != "" && d.PromptSections.Allows(name, source) {
			if result.Len() > 0 {
				result.WriteString("\n\n")
			}
			result.WriteString(strings.TrimSpace(text))
		}
	}
	add("persona:prefix", "builtin:persona", core)
	add("tools:guidance", "builtin:tools", composePrompt("", toolset, cwd))
	if f.Memory != nil && hasPresetTool(toolset, "memory") {
		add("memory:instruction", "builtin:memory", memory.Instruction)
	}
	if f.SessionSearch != nil && hasPresetTool(toolset, "session_search") {
		add("session-search:instruction", "builtin:session-search", "If the user refers to an earlier conversation, or relevant context may be in a previous session, look it up with session_search before asking the user to repeat it.")
	}
	if structured {
		add("structured-output:instruction", "builtin:structured-output", tools.NativeStructuredOutputInstruction)
	}
	if planActive {
		add("plan:policy", "builtin:plan", plan.Policy)
	}
	if f.Memory != nil {
		sections, err := f.Memory.PromptSections()
		if err != nil {
			slog.Warn("read account memory", "error", err)
		}
		for _, section := range sections {
			add(section.Name, "builtin:memory", section.Text)
		}
	}
	if _, err := f.presetSuffix(identity, cwd); err != nil {
		return "", err
	}
	return result.String(), nil
}

func hasPresetTool(tools []agent.Tool, name string) bool {
	for _, tool := range tools {
		if tool.Spec().Name == name {
			return true
		}
	}
	return false
}

func toolSpecs(rows []agent.Tool) []agent.ToolSpec {
	result := make([]agent.ToolSpec, 0, len(rows))
	for _, tool := range rows {
		result = append(result, tool.Spec())
	}
	return result
}

type presetProbeJournal struct{}

func (presetProbeJournal) Append(ctx context.Context, _ []sessionformat.Event) error {
	return ctx.Err()
}
func (presetProbeJournal) Close() error { return nil }

// CheckPreset prepares and validates an unpublished composition using a detached
// in-memory Session. No model runs and no facts are written to the real journal.
func (f NativeFactory) CheckPreset(ctx context.Context, identity NativeRuntimeIdentity, artifact sessionformat.PhysicalArtifact, binding *agentpreset.Binding) error {
	f.Preset = binding
	f.SessionReady, f.BeforePublish = nil, nil
	identity.AgentPreset = binding.ID()
	shadow, err := sessionformat.RestoreLiveSession(artifact, presetProbeJournal{})
	if err != nil {
		return err
	}
	// This is a detached resume using the existing Builder contract. A new
	// lifecycle reason would reject valid container builders.
	live, err := f.publish(ctx, identity, shadow, "resume", nil)
	if err != nil {
		return err
	}
	checkErr := live.checkPreset(ctx)
	return errors.Join(checkErr, live.Close())
}

func (f NativeFactory) presetSuffix(identity NativeRuntimeIdentity, cwd string) (string, error) {
	d := f.Preset.Definition()
	if d.Persona.Complete || !d.PromptSections.Allows("persona:suffix", "builtin:persona") {
		return "", nil
	}
	return agentpreset.Render(d.Persona.Suffix, map[string]string{"account.id": identity.UserID, "session.id": identity.SessionID, "cwd": cwd, "workspace": identity.Workspace})
}
