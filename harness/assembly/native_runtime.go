package assembly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"go-bot/harness/agent"
	"go-bot/harness/agentpreset"
	"go-bot/harness/attachments"
	"go-bot/harness/goal"
	"go-bot/harness/interaction"
	"go-bot/harness/memory"
	"go-bot/harness/permission"
	"go-bot/harness/plan"
	"go-bot/harness/schedule"
	"go-bot/harness/session"
	"go-bot/harness/sessionsearch"
	"go-bot/harness/skills"
	"go-bot/harness/tools"
)

// NativeRuntimeIdentity stays outside DeepSeek's Session header. User owner
// and workspace are Host deployment facts, not model-surface events.
type NativeRuntimeIdentity struct {
	UserID    string
	SessionID string
	Workspace string
	// CWD is this Agent's initial/current directory inside the shared worker
	// filesystem. Empty uses Workspace; child Sessions persist their own CWD.
	CWD               string
	AgentPreset       string
	PermissionCeiling func() permission.Mode
}

type NativeStorage interface {
	CreateNative(context.Context, sessionformat.Header) (sessionformat.EventJournal, error)
	OpenNative(context.Context) (sessionformat.EventJournal, sessionformat.PhysicalArtifact, error)
}

type NativeChildStorageFactory interface {
	ChildNativeStorage(string) (NativeStorage, error)
}

type NativePreparedStorage interface {
	CreatePreparedNative(context.Context, sessionformat.Header, []sessionformat.Event) (sessionformat.EventJournal, error)
}

type NativeBuilder interface {
	BuildNative(context.Context, NativeRuntimeIdentity, string) (Resources, error)
}

// NativeExtensionScope is owned by one live Agent, including a fork or
// restored Agent. It is not persisted or shared by session ID.
type NativeExtensionScope interface {
	Tools(context.Context, string) ([]agent.Tool, error)
	Context(context.Context, string) ([]agent.RuntimeContextSection, error)
	Close()
}

// NativeFactory assembles one v4 Session and Agent inside the user Host.
type NativeFactory struct {
	Presets              *agentpreset.Registry
	Preset               *agentpreset.Binding
	PresetToolProviders  map[string]NativePresetToolProvider
	LocalToolSource      agent.ToolSource
	Storage              NativeStorage
	Builder              NativeBuilder
	Model                agent.Model
	ResolveTurnModel     func(context.Context) (agent.TurnModel, error)
	ResolveSelectedModel agent.ModelSelectionResolver
	ToolSource           agent.ToolSource
	ToolInvocationSource agent.ToolInvocationSource
	ContextSource        func(context.Context, string) ([]agent.RuntimeContextSection, error)
	SystemContextSource  func(context.Context, string) ([]agent.RuntimeContextSection, error)
	ExtensionScope       func() NativeExtensionScope
	PromptSource         agent.PromptAssemblySource
	// BeforePublish composes the real, unpublished Go Agent. It runs before
	// extension binding and before any driver starts; a failure closes the
	// prepared resources while retaining already committed Session facts.
	SessionReady    func(*sessionformat.LiveSession)
	BeforePublish   func(context.Context, *NativeLive) error
	Provider        string
	ModelID         string
	ReasoningEffort string
	SystemPrompt    string
	Limits          agent.Limits
	Children        tools.ChildCoordinator
	SubagentPresets tools.SubagentPresetSelection
	Schedule        *schedule.Service
	FileAttachments attachments.FileStore
	Memory          *memory.Store
	MemoryChanged   func(context.Context)
	SessionSearch   *sessionsearch.Index
	// WorkflowSchema is an activation-only child contract. It is not persisted
	// and must not leak into ordinary or resumed child Sessions.
	WorkflowSchema map[string]any
	// ToolPreExecutePolicy is the Go-owned execution policy. Business plugins
	// cannot wrap it or register global execution middleware.
	ToolPreExecutePolicy agent.NativePreToolPolicy
}

type NativeLive struct {
	Preset         *agentpreset.Binding
	Agent          *agent.NativeLoop
	Session        *sessionformat.LiveSession
	Goals          *goal.NativeService
	Plan           *plan.NativeService
	Questions      *interaction.NativeQuestions
	Resource       Resources
	Structured     *tools.NativeStructuredOutput
	checkPreset    func(context.Context) error
	closeMu        sync.Once
	closeErr       error
	extensionScope NativeExtensionScope
}

func (l *NativeLive) Close() error {
	l.closeMu.Do(func() {
		if l.extensionScope != nil {
			l.extensionScope.Close()
		}
		if l.Questions != nil {
			l.Questions.Close()
		}
		l.closeErr = errors.Join(l.closeErr, l.Session.Close(), l.Resource.close())
	})
	return l.closeErr
}

func (f NativeFactory) validate(identity NativeRuntimeIdentity) error {

	if f.Storage == nil || f.Builder == nil || f.Model == nil || f.Provider == "" || f.ModelID == "" ||
		f.SystemPrompt == "" || f.Limits.MaxSteps <= 0 || f.Limits.MaxToolCalls <= 0 ||
		identity.UserID == "" || identity.SessionID == "" || !filepath.IsAbs(identity.Workspace) ||
		len(identity.Workspace) > 4096 {
		return errors.New("incomplete native container Assembly")
	}
	if err := f.Limits.ValidateContextWindow(); err != nil {
		return err
	}
	if !filepath.IsAbs(nativeCWD(identity)) || len(nativeCWD(identity)) > 4096 {
		return errors.New("native Agent cwd must be an absolute container path")
	}
	if f.Limits.MaxModelCalls < 0 || f.Limits.MaxTurnDuration < 0 || f.Limits.MaxTurnDuration > 24*time.Hour {
		return errors.New("invalid native model-call or turn-duration limit")
	}
	return nil
}

func nativeCWD(identity NativeRuntimeIdentity) string {
	if identity.CWD != "" {
		return filepath.Clean(identity.CWD)
	}
	return identity.Workspace
}

func (f NativeFactory) Activate(ctx context.Context, identity NativeRuntimeIdentity) (*NativeLive, error) {
	return f.ActivateWithSessionOptions(ctx, identity, NativeSessionOptions{})
}

func (f NativeFactory) Resume(ctx context.Context, identity NativeRuntimeIdentity) (*NativeLive, error) {
	if err := f.validate(identity); err != nil {
		return nil, err
	}
	journal, artifact, err := f.Storage.OpenNative(ctx)
	if err != nil {
		return nil, err
	}
	if artifact.Header.ID != identity.SessionID || artifact.Header.CWD != nativeCWD(identity) {
		_ = journal.Close()
		return nil, errors.New("native Session header differs from its worker identity")
	}
	identity.AgentPreset, err = sessionformat.EffectiveAgentPreset(artifact.Header, artifact.Events)
	if err == nil {
		f, identity, err = f.resolvePreset(ctx, identity)
	}
	if err != nil {
		_ = journal.Close()
		return nil, err
	}
	s, err := sessionformat.RestoreLiveSession(artifact, journal)
	if err != nil {
		_ = journal.Close()
		return nil, err
	}
	if err := s.Flush(ctx); err != nil {
		_ = s.Close()
		return nil, err
	}
	return f.publishSession(ctx, identity, s, "resume", nil, nil)
}

// ActivateChild creates a separate child Session inside the same Host. It has
// its own v4 journal in the shared file tree, but no new container or listener.
func (f NativeFactory) ActivateChild(ctx context.Context, root NativeRuntimeIdentity, parent *NativeLive, childID, label string) (*NativeLive, error) {
	return f.ActivateChildAt(ctx, root, parent, childID, label, "")
}

func (f NativeFactory) ActivateChildAt(ctx context.Context, root NativeRuntimeIdentity, parent *NativeLive, childID, label, cwd string) (*NativeLive, error) {
	return f.activateChild(ctx, root, parent, childID, label, cwd, nil, "")
}

func (f NativeFactory) ActivateChildWithPresetAt(ctx context.Context, root NativeRuntimeIdentity, parent *NativeLive, childID, label, cwd, preset string) (*NativeLive, error) {
	return f.activateChild(ctx, root, parent, childID, label, cwd, nil, preset)
}

// ForkChild captures only the parent's last fully closed turn. The copied
// prefix is stored in the child's own v4 journal, so cold resume never reads
// the parent's newer history to reconstruct this child.
func (f NativeFactory) ForkChild(ctx context.Context, root NativeRuntimeIdentity, parent *NativeLive, childID, label string) (*NativeLive, error) {
	return f.ForkChildAt(ctx, root, parent, childID, label, "")
}

func (f NativeFactory) ForkChildAt(ctx context.Context, root NativeRuntimeIdentity, parent *NativeLive, childID, label, cwd string) (*NativeLive, error) {
	if parent == nil {
		return nil, errors.New("native fork requires a live parent")
	}
	events := parent.Session.DurableEvents()
	cut := -1
	for index, event := range events {
		if event.Type == "turn/end" {
			cut = index
		}
	}
	if cut < 0 {
		return f.activateChild(ctx, root, parent, childID, label, cwd, nil, "")
	}
	return f.activateChild(ctx, root, parent, childID, label, cwd, events[:cut+1], "")
}

func (f NativeFactory) activateChild(ctx context.Context, root NativeRuntimeIdentity, parent *NativeLive, childID, label, cwd string, seed []sessionformat.Event, preset string) (*NativeLive, error) {
	if parent == nil || childID == "" || childID == parent.Session.Header().ID || label == "" {
		return nil, errors.New("invalid native child identity")
	}
	storage, ok := f.Storage.(NativeChildStorageFactory)
	if !ok {
		return nil, errors.New("native child Session storage is unavailable")
	}
	childStorage, err := storage.ChildNativeStorage(childID)
	if err != nil {
		return nil, err
	}
	childFactory := f
	childFactory.Storage = childStorage
	childFactory.Preset = parent.Preset
	if preset != "" {
		if f.Presets == nil {
			return nil, errors.New("child Agent Preset registry is unavailable")
		}
		childFactory.Preset, err = f.Presets.Resolve(ctx, preset)
		if err != nil {
			return nil, err
		}
	}
	identity := root
	identity.SessionID = childID
	identity.AgentPreset = childFactory.Preset.ID()
	identity.PermissionCeiling = func() permission.Mode { return permission.Read(parent.Session.Events()).Mode }
	parentCWD := nativeSessionCWD(parent.Session.Header().CWD, parent.Session.Events())
	if cwd == "" {
		cwd = parentCWD
	} else if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(parentCWD, cwd)
	}
	identity.CWD = filepath.Clean(cwd)
	if err := childFactory.validate(identity); err != nil {
		return nil, err
	}
	parentHeader := parent.Session.Header()
	header := sessionformat.Header{
		Type: "session", Version: sessionformat.Version, ID: childID,
		CreatedAt: time.Now().UTC().UnixMilli(), CWD: parentHeader.CWD,
		ParentSession: parentHeader.ID, Origin: "subagent", IsSeeded: len(seed) > 0,
		DelegationDepth: parentHeader.DelegationDepth + 1,
		AgentPreset:     identity.AgentPreset,
	}
	journal, err := childStorage.CreateNative(ctx, header)
	if err != nil {
		return nil, err
	}
	var s *sessionformat.LiveSession
	if len(seed) == 0 {
		s, err = sessionformat.NewLiveSession(header, journal)
	} else {
		prefix := append([]sessionformat.Event(nil), seed...)
		marker := sessionformat.Event{
			Type: "session/end-seed", Seq: uint64(len(prefix)), Time: prefix[len(prefix)-1].Time,
			Data: json.RawMessage(`{"inherited":true}`),
		}
		prefix = append(prefix, marker)
		artifact := sessionformat.PhysicalArtifact{Header: header, Events: prefix, InheritedEventCount: marker.Seq}
		s, err = sessionformat.RestoreLiveSession(artifact, journal)
		if err == nil {
			err = journal.Append(ctx, prefix)
		}
	}
	if err != nil {
		_ = journal.Close()
		return nil, err
	}
	initialize := func() error {
		if err := permission.Save(ctx, s, permission.Read(parent.Session.Events()).Mode, true, true); err != nil {
			return err
		}
		if identity.CWD != header.CWD {
			data, err := json.Marshal(struct {
				CWD string `json:"cwd"`
			}{CWD: identity.CWD})
			if err != nil {
				return err
			}
			if _, err := s.Append(sessionformat.EventInput{Type: "working-directory/change", Data: data}); err != nil {
				return err
			}
			if err := s.Flush(ctx); err != nil {
				return err
			}
		}
		return nil
	}
	descriptor := &sessionformat.SubagentDescriptor{
		Version: 3, Mode: "continuable", Provider: "native", Label: &label,
		AgentProvider: &f.Provider, AgentModel: &f.ModelID,
	}
	if f.ReasoningEffort != "" {
		descriptor.AgentReasoningEffort = &f.ReasoningEffort
	}
	return childFactory.publishSession(ctx, identity, s, "activation", descriptor, initialize)
}

// ResumeChild reopens only a v4 child already cataloged by its live direct
// parent. The caller verifies adjacency; the durable header and first
// descriptor must then agree with that claim.
func (f NativeFactory) ResumeChild(ctx context.Context, root NativeRuntimeIdentity, parentID, childID string) (*NativeLive, error) {
	storage, ok := f.Storage.(NativeChildStorageFactory)
	if !ok || childID == "" || parentID == "" {
		return nil, errors.New("native child Session storage is unavailable")
	}
	childStorage, err := storage.ChildNativeStorage(childID)
	if err != nil {
		return nil, err
	}
	childFactory := f
	childFactory.Storage = childStorage
	identity := root
	identity.SessionID = childID
	journal, artifact, err := childStorage.OpenNative(ctx)
	if err != nil {
		return nil, err
	}
	identity.CWD = nativeSessionCWD(artifact.Header.CWD, artifact.Events)
	identity.AgentPreset, err = sessionformat.EffectiveAgentPreset(artifact.Header, artifact.Events)
	childFactory.Preset = nil
	if err == nil {
		childFactory, identity, err = childFactory.resolvePreset(ctx, identity)
	}
	if err != nil {
		_ = journal.Close()
		return nil, err
	}
	if err := childFactory.validate(identity); err != nil {
		_ = journal.Close()
		return nil, err
	}
	if artifact.Header.ID != childID || artifact.Header.ParentSession != parentID ||
		artifact.Header.Origin != "subagent" || !filepath.IsAbs(artifact.Header.CWD) || artifact.Header.DelegationDepth < 1 {
		_ = journal.Close()
		return nil, errors.New("native child lineage differs from its parent")
	}
	descriptor, err := sessionformat.FoldSubagentDescriptor(artifact.Events)
	if err != nil || descriptor == nil || descriptor.Mode != "continuable" || descriptor.Provider != "native" {
		_ = journal.Close()
		return nil, errors.New("native child lacks a resumable current descriptor")
	}
	if descriptor.AgentProvider != nil {
		childFactory.Provider = *descriptor.AgentProvider
	}
	if descriptor.AgentModel != nil {
		childFactory.ModelID = *descriptor.AgentModel
	}
	if descriptor.AgentReasoningEffort != nil {
		childFactory.ReasoningEffort = *descriptor.AgentReasoningEffort
	}
	if childFactory.Provider != f.Provider || childFactory.ModelID != f.ModelID || childFactory.ReasoningEffort != f.ReasoningEffort {
		childFactory.Model = agent.RoutedModel{Base: f.Model, Route: agent.ModelRoute{
			Provider: childFactory.Provider, Model: childFactory.ModelID, ReasoningEffort: childFactory.ReasoningEffort,
		}}
	}
	s, err := sessionformat.RestoreLiveSession(artifact, journal)
	if err != nil {
		_ = journal.Close()
		return nil, err
	}
	if err := s.Flush(ctx); err != nil {
		_ = s.Close()
		return nil, err
	}
	return childFactory.publishSession(ctx, identity, s, "resume", nil, nil)
}

func (f NativeFactory) publishSession(ctx context.Context, identity NativeRuntimeIdentity, s *sessionformat.LiveSession, reason string, descriptor *sessionformat.SubagentDescriptor, initialize func() error) (_ *NativeLive, resultErr error) {
	if f.SessionReady != nil {
		f.SessionReady(s)
	}
	if initialize != nil {
		if err := initialize(); err != nil {
			return nil, errors.Join(err, s.Close())
		}
	}
	live, err := f.publish(ctx, identity, s, reason, descriptor)
	if err != nil {
		return nil, err
	}
	return live, nil
}

// DeepSeek keeps the original project cwd in the immutable Session header;
// later working-directory/change events determine the effective cwd.
func nativeSessionCWD(initial string, events []sessionformat.Event) string {
	current := initial
	for _, event := range events {
		if event.Type != "working-directory/change" {
			continue
		}
		var change struct {
			CWD string `json:"cwd"`
		}
		if json.Unmarshal(event.Data, &change) == nil && filepath.IsAbs(change.CWD) {
			current = filepath.Clean(change.CWD)
		}
	}
	return current
}

func (f NativeFactory) publish(ctx context.Context, identity NativeRuntimeIdentity, s *sessionformat.LiveSession, reason string, descriptor *sessionformat.SubagentDescriptor) (*NativeLive, error) {
	resource, err := f.Builder.BuildNative(ctx, identity, reason)
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	if resource.Files == nil || resource.Shell == nil || resource.Skills == nil {
		_ = s.Close()
		_ = resource.close()
		return nil, errors.New("native container Builder omitted base capabilities")
	}
	resource.Skills = f.presetSkills(resource.Skills)
	toolset, err := baseTools(resource)
	if err != nil {
		_ = s.Close()
		_ = resource.close()
		return nil, err
	}
	skillLookup := func() skills.Lookup {
		return skills.Lookup{CWD: nativeSessionCWD(s.Header().CWD, s.Events()), Workspace: identity.Workspace, SessionID: s.Header().ID}
	}
	skillTool := &tools.Skill{Registry: resource.Skills, Lookup: skillLookup}
	toolset = append(toolset, skillTool, tools.Present{
		Workspace: identity.Workspace,
		CWD:       func() string { return skillLookup().CWD },
	})
	goals, err := goal.NewNative(s)
	if err != nil {
		_ = s.Close()
		_ = resource.close()
		return nil, err
	}
	plans, err := plan.NewNative(s)
	if err != nil {
		_ = s.Close()
		_ = resource.close()
		return nil, err
	}
	questions := interaction.NewNativeQuestions()
	approval := permission.ApprovalService{Session: s, Questions: questions}
	permissionState := func() permission.State {
		state := permission.Read(s.Events())
		if identity.PermissionCeiling != nil {
			ceiling := identity.PermissionCeiling()
			if ceiling.Rank() < state.Mode.Rank() {
				state.Mode = ceiling
			}
			state.Approval = "never"
		}
		return state
	}
	permissionRoot := s.Header().CWD
	if resource.ConfineMutation != nil {
		for i, tool := range toolset {
			switch tool.Spec().Name {
			case "shellexecute", "writefile", "editfile":
				toolset[i] = tools.PermissionTool{Tool: tool, State: permissionState, Root: permissionRoot,
					Approvals: approval, ConfineMutation: resource.ConfineMutation}
			}
		}
	}
	toolset = append(toolset, tools.NativeTodoWrite{Session: s, AllowParallelInProgress: true})
	toolset = append(toolset,
		tools.NativeGetGoal{Goals: goals, Session: s},
		tools.NativeCreateGoal{Goals: goals, Session: s},
		tools.NativeUpdateGoal{Goals: goals, Session: s},
		tools.NativeAskUserQuestion{Questions: questions},
		tools.NativeExitPlanMode{Plan: plans, Questions: questions},
	)
	if f.Schedule != nil {
		for _, name := range []string{"schedule_create", "schedule_list", "schedule_view", "schedule_edit"} {
			toolset = append(toolset, tools.NativeSchedule{Service: f.Schedule, SessionID: identity.SessionID,
				Name: name, Delegated: s.Header().Origin == "subagent",
				CWD: s.Header().CWD, AgentPreset: f.Preset.ID()})
		}
	}
	if f.Children != nil {
		if err := f.SubagentPresets.ValidateConfig(false); err != nil {
			_ = s.Close()
			_ = resource.close()
			return nil, err
		}
		toolset = append(toolset,
			tools.Subagent{Coordinator: f.Children, ParentID: identity.SessionID, PresetSelection: f.SubagentPresets},
			tools.Subagent{Coordinator: f.Children, ParentID: identity.SessionID, Fork: true},
			tools.SendMessage{Coordinator: f.Children, SenderID: identity.SessionID},
			tools.InterruptAgent{Coordinator: f.Children, ParentID: identity.SessionID},
			tools.ListAgents{Coordinator: f.Children, ParentID: identity.SessionID},
		)
		if coordinator, ok := f.Children.(tools.WorkflowCoordinator); ok {
			toolset = append(toolset, tools.NativeWorkflow{
				Coordinator: coordinator, ParentID: identity.SessionID, Session: s, Jobs: resource.Jobs,
			})
		}
	}
	toolset = append(toolset, resource.ExtraTools...)
	if f.Memory != nil {
		toolset = append(toolset, tools.NativeMemory{Store: f.Memory, Changed: f.MemoryChanged})
	}
	if f.SessionSearch != nil {
		toolset = append(toolset, tools.NativeSessionSearch{Index: f.SessionSearch, SessionID: identity.SessionID})
	}
	var presetLocal, activationLocal []agent.Tool
	if f.Preset != nil {
		for _, name := range f.Preset.Definition().LocalTools {
			provider := f.PresetToolProviders[name]
			if provider == nil {
				_ = s.Close()
				_ = resource.close()
				return nil, fmt.Errorf("unknown preset tool provider %q", name)
			}
			rows, e := provider(ctx, identity, s, resource)
			if e != nil {
				_ = s.Close()
				_ = resource.close()
				return nil, e
			}
			presetLocal = append(presetLocal, rows...)
		}
	}
	var structured *tools.NativeStructuredOutput
	if f.WorkflowSchema != nil {
		if s.Header().Origin != "subagent" {
			_ = s.Close()
			_ = resource.close()
			return nil, errors.New("structured output is limited to child activation")
		}
		structured, err = tools.NewNativeStructuredOutput(f.WorkflowSchema)
		if err != nil {
			_ = s.Close()
			_ = resource.close()
			return nil, err
		}
		activationLocal = append(activationLocal, structured)
	}
	inheritedTools := append([]agent.Tool(nil), toolset...)
	toolset, err = f.presetTools(toolset, presetLocal, activationLocal)
	if err != nil {
		_ = s.Close()
		_ = resource.close()
		return nil, err
	}
	activeToolset := toolset
	cwd := resource.DefaultWorkdir
	if cwd == "" {
		cwd = nativeCWD(identity)
	}
	basePrompt, err := f.renderPresetPrompt(identity, activeToolset, cwd, false, structured != nil)
	if err != nil {
		_ = s.Close()
		_ = resource.close()
		return nil, err
	}

	loop, err := agent.NewNativeLoop(s, f.Model, f.Provider, f.ModelID, basePrompt, nil,
		f.Limits.MaxSteps, f.Limits.MaxToolCalls)
	if err != nil {
		_ = s.Close()
		_ = resource.close()
		return nil, fmt.Errorf("assemble native Agent loop: %w", err)
	}
	loop.SetReasoningEffort(f.ReasoningEffort)
	loop.SetSandboxModeSource(func() string { return string(permissionState().Mode) })
	loop.SetTurnModelResolver(f.ResolveTurnModel)
	loop.SetModelSelectionResolver(f.ResolveSelectedModel)
	loop.SetToolInvocationSource(f.ToolInvocationSource)
	loop.SetToolQuestions(questions)
	loop.SetRuntimeContextSource(func(ctx context.Context, id string) ([]agent.RuntimeContextSection, error) {
		if f.Preset != nil {
			persona := f.Preset.Definition().Persona
			if persona.IncludeRuntimeContext != nil && !*persona.IncludeRuntimeContext {
				return nil, nil
			}
		}
		if f.ContextSource == nil {
			return nil, nil
		}
		rows, e := f.ContextSource(agentpreset.WithBinding(ctx, f.Preset), id)
		return filterPresetContexts(f.Preset, rows, true), e
	})
	loop.SetPromptAssembler(func(ctx context.Context, input agent.PromptAssemblyInput) (agent.PromptAssembly, error) {
		out := agent.PromptAssembly{SystemPrompt: input.SystemPrompt, Tools: input.Tools}
		var e error
		if f.PromptSource != nil {
			out, e = f.PromptSource(agentpreset.WithBinding(ctx, f.Preset), input)
		}
		if f.Preset.Definition().Persona.Complete {
			out.SystemPrompt = input.SystemPrompt
		} else {
			suffix, err := f.presetSuffix(identity, cwd)
			if err != nil {
				return out, err
			}
			if suffix != "" {
				out.SystemPrompt += "\n\n" + suffix
			}
		}
		out.Contexts = filterPresetContexts(f.Preset, out.Contexts, true)
		return out, e
	})
	// The clock is standard-preset context for every Agent, including a child;
	// only the four Schedule management tools are root-only.
	if f.Schedule != nil && presetSectionAllowed(f.Preset, "time-context", "builtin:time", true) {
		if err := loop.EnableTimeContext("UTC", 10*time.Minute); err != nil {
			_ = s.Close()
			_ = resource.close()
			return nil, err
		}
	}
	if err := loop.SetModelCallLimit(f.Limits.MaxModelCalls); err != nil {
		_ = s.Close()
		_ = resource.close()
		return nil, err
	}
	if err := loop.SetParallelToolLimit(f.Limits.MaxParallelToolCalls); err != nil {
		_ = s.Close()
		_ = resource.close()
		return nil, err
	}
	if err := loop.SetContextPressure(f.Limits.ContextWindowTokens, f.Limits.ReservedOutputTokens, f.Limits.ContextHeadroomTokens); err != nil {
		_ = s.Close()
		_ = resource.close()
		return nil, err
	}
	loop.SetPreStepHook(func(stepCtx context.Context) (agent.NativePreStepDecision, error) {
		if descriptor != nil {
			data, err := json.Marshal(descriptor)
			if err != nil {
				return agent.NativePreStepEnter, err
			}
			if _, err := s.Append(sessionformat.EventInput{Type: "subagent/descriptor", Data: data}); err != nil {
				return agent.NativePreStepEnter, err
			}
			descriptor = nil
			if err := s.Flush(stepCtx); err != nil {
				return agent.NativePreStepEnter, err
			}
		}
		return agent.NativePreStepEnter, plans.ApplyPending(stepCtx)
	})
	if resource.ConfineMutation != nil {
		loop.SetRuntimeContext(func() []agent.RuntimeContextSection {
			file, approval := permission.Guidance(permissionState(), permissionRoot)
			return filterPresetContexts(f.Preset, []agent.RuntimeContextSection{
				{Name: "working-directory:current", Text: fmt.Sprintf("Current working directory: %q.", skillLookup().CWD)},
				{Name: "sandbox:policy", Text: file},
				{Name: "approval:policy", Text: approval},
			}, true)
		})
	}

	loop.SetToolPolicyApprover(func(ctx context.Context, execution agent.ToolExecution, ask agent.ToolPolicyAsk) error {
		reason := ask.Reason
		if reason == "" {
			reason = ask.DisplayReason["en"]
		}
		if reason == "" {
			reason = "This tool requires approval before executing."
		}
		return approval.Request(ctx, permission.Request{Tool: execution.Name, Reason: reason, Detail: string(execution.Arguments)})
	})
	loop.SetToolPreExecutePolicy(f.ToolPreExecutePolicy)

	loop.SetSkillCatalogProvider(skillTool, func(stepCtx context.Context) (agent.NativeSkillCatalogSnapshot, error) {
		discovery, err := resource.Skills.Snapshot(stepCtx, skillLookup())
		if err != nil {
			return agent.NativeSkillCatalogSnapshot{}, err
		}
		snapshot := agent.NativeSkillCatalogSnapshot{Complete: discovery.Complete}
		for _, entry := range discovery.Entries {
			if entry.ModelInvocable {
				snapshot.Entries = append(snapshot.Entries, agent.NativeSkillCatalogEntry{
					Name: entry.Name, Description: entry.Description,
				})
			}
		}
		return snapshot, nil
	})
	loop.SetSkillInvocationLoader(func(stepCtx context.Context, name string) (string, bool, error) {
		candidate, found, err := resource.Skills.Find(stepCtx, skillLookup(), name)
		if err != nil || !found {
			return "", false, err
		}
		loaded, found, err := candidate.Load(stepCtx)
		if err != nil || !found || !loaded.UserInvocable {
			return "", false, err
		}
		return tools.RenderSkillContent(loaded.Value), true, nil
	})
	loop.SetSystemPromptProvider(func() (string, error) {
		mode, err := plans.View()
		if err != nil {
			return "", err
		}
		return f.renderPresetPrompt(identity, activeToolset, cwd, mode.Active, structured != nil)
	})
	instructionReader := &nativeInstructionReader{}
	loop.SetInstructionProvider(func(stepCtx context.Context, touched []string) (agent.NativeInstructionSnapshot, error) {
		return instructionReader.loadCurrent(stepCtx, cwd, touched...)
	})
	loop.SetGoalClaimGuard(func(message json.RawMessage, inbox sessionformat.InboxState) (bool, error) {
		if len(inbox.NextTurn) != 1 || len(inbox.NextStep) != 0 {
			return false, nil // a competing accepted input takes precedence
		}
		var data struct {
			Source struct {
				Kind     string `json:"kind"`
				GoalID   string `json:"goalId"`
				Revision uint64 `json:"revision"`
				Round    uint64 `json:"round"`
			} `json:"source"`
		}
		if err := json.Unmarshal(message, &data); err != nil {
			return false, err
		}
		view, found, err := goals.Get()
		if err != nil || !found {
			return false, err
		}
		return data.Source.Kind == "goal" && view.Goal.ID == data.Source.GoalID &&
			view.Goal.Revision == data.Source.Revision && view.RoundsStarted+1 == data.Source.Round &&
			view.Goal.Phase == "active" && view.Activation == "armed" &&
			data.Source.Round <= view.Goal.MaxGoalRounds, nil
	})
	loop.SetFilePathResolver(func(ref agent.NativeFileAttachmentRef) string {
		return f.FileAttachments.Path(attachments.FileRef{ID: ref.ID, Name: ref.Name, Bytes: ref.Bytes})
	})
	var scope NativeExtensionScope
	if f.ExtensionScope != nil {
		scope = f.ExtensionScope()
	}
	loop.SetSystemContextSource(func(ctx context.Context, sid string) ([]agent.RuntimeContextSection, error) {
		if f.Preset != nil && f.Preset.Definition().Persona.Complete {
			return nil, nil
		}
		ctx = agentpreset.WithBinding(ctx, f.Preset)
		var sections []agent.RuntimeContextSection
		if f.SystemContextSource != nil {
			rows, err := f.SystemContextSource(ctx, sid)
			if err != nil {
				return nil, err
			}
			sections = append(sections, rows...)
		}
		if scope != nil {
			rows, err := scope.Context(ctx, sid)
			if err != nil {
				return nil, err
			}
			sections = append(sections, rows...)
		}
		return filterPresetContexts(f.Preset, sections, false), nil
	})
	var refreshPresetTools agent.ToolSource
	if f.Preset != nil {
		refreshPresetTools = func(ctx context.Context, sid string) ([]agent.Tool, error) {
			info, _ := agent.ToolSourceInfoFromContext(ctx)
			info.Tools = nil
			for _, tool := range activeToolset {
				info.Tools = append(info.Tools, tool.Spec())
			}
			ctx = agent.WithToolSourceInfo(agentpreset.WithBinding(ctx, f.Preset), info)
			inherited := append([]agent.Tool(nil), inheritedTools...)
			if f.ToolSource != nil {
				rows, e := f.ToolSource(ctx, sid)
				if e != nil {
					return nil, e
				}
				inherited = append(inherited, rows...)
			}
			if scope != nil {
				rows, e := scope.Tools(ctx, sid)
				if e != nil {
					return nil, e
				}
				inherited = append(inherited, rows...)
			}
			local := append([]agent.Tool(nil), activationLocal...)
			if f.LocalToolSource != nil {
				rows, e := f.LocalToolSource(ctx, sid)
				if e != nil {
					return nil, e
				}
				local = append(local, rows...)
			}
			resolved, e := f.presetTools(inherited, presetLocal, local)
			if e != nil {
				return nil, e
			}
			activeToolset = resolved
			return resolved, nil
		}
		loop.SetToolSource(refreshPresetTools)
	}
	live := &NativeLive{Preset: f.Preset, Agent: loop, Session: s, Goals: goals, Plan: plans, Questions: questions, Resource: resource, Structured: structured, extensionScope: scope}
	live.checkPreset = func(checkCtx context.Context) error {
		// Validate Go-owned resources without starting optional business plugins.
		// Dynamic plugin/MCP contributions still resolve through the original step boundary.
		prompt, e := f.renderPresetPrompt(identity, toolset, cwd, false, structured != nil)
		if e != nil {
			return e
		}
		_, e = agent.NewNativeLoop(s, f.Model, f.Provider, f.ModelID, prompt, toolset, f.Limits.MaxSteps, f.Limits.MaxToolCalls)
		return e
	}
	if err := live.checkPreset(ctx); err != nil {
		return nil, errors.Join(err, live.Close())
	}

	if f.BeforePublish != nil {
		if err := f.BeforePublish(ctx, live); err != nil {
			return nil, errors.Join(err, live.Close())
		}
	}
	return live, nil
}
