package hostruntime

import (
	"context"
	"encoding/json"
	"errors"

	"go-bot/harness/agentpreset"
	"go-bot/harness/session"
)

// SelectAgentPreset coordinates with admission and the driver before the first
// turn. It neither adds a user message nor starts an Agent turn.
func (r *NativeRuntime) SelectAgentPreset(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil || r.stopping || r.fatalErr != nil {
		return errors.New("native Agent is not ready")
	}
	if r.live.Session.IsAgentPresetLocked() {
		return agentpreset.ErrLocked
	}
	if r.activeCancel != nil || r.compactDone != nil {
		return ErrHostSessionBusy
	}
	if r.children != nil {
		r.children.mu.Lock()
		busy := r.children.creating > 0 || len(r.children.children) > 0
		r.children.mu.Unlock()
		if busy {
			return ErrHostSessionBusy
		}
	}
	registry := r.Factory.Presets
	if registry == nil {
		return errors.New("Host Agent Preset registry is unavailable")
	}
	binding, err := registry.Resolve(ctx, id)
	if err != nil {
		return err
	}
	current := r.live.Session.AgentPreset(sessionformat.DurableCut)
	if current == binding.ID() && r.live.Preset.Same(binding) {
		return nil
	}
	artifact := sessionformat.PhysicalArtifact{Header: r.live.Session.Header(), Events: r.live.Session.DurableEvents(), InheritedEventCount: r.live.Session.InheritedEventCount()}
	identity := r.Identity
	identity.AgentPreset = binding.ID()
	if err := r.Factory.CheckPreset(ctx, identity, artifact, binding); err != nil {
		return err
	}
	raw, _ := json.Marshal(map[string]string{"agentPreset": binding.ID()})
	if _, err := r.live.Session.Append(sessionformat.EventInput{Type: "agent-preset/selected", Data: raw}); err != nil {
		return err
	}
	if err := r.live.Session.Flush(ctx); err != nil {
		r.fatalErr = err
		return err
	}
	// The committed selection is now authoritative. Any failure after this
	// point stops admission; recovery must rebuild from that durable fact.
	if err := r.live.Close(); err != nil {
		r.fatalErr = err
		return err
	}
	factory := r.Factory
	factory.Preset = binding
	factory.SessionReady, factory.BeforePublish = nil, nil // Creation observers do not run a second time.
	live, err := factory.Resume(ctx, identity)
	if err != nil {
		r.fatalErr = err
		return err
	}
	r.live = live
	r.Identity = identity
	r.Factory.Preset = binding
	if r.children != nil {
		r.children = newNativeChildManager(r.Factory, r.Identity, live, r.kick)
	}
	live.Questions.Enable()
	live.Questions.SetApprovalAudience(r.approvalAudience)
	return nil
}

func (h *NativeHost) SelectAgentPreset(ctx context.Context, sessionID, id string) error {
	runtime, ok := h.Session(sessionID)
	if !ok {
		var err error
		runtime, err = h.Activate(ctx, sessionID, true)
		if err != nil {
			return err
		}
	}
	return runtime.SelectAgentPreset(ctx, id)
}

// PresetForSession reads durable identity without restoring an Agent or
// resolving a definition which may since have been removed.
func (h *NativeHost) PresetForSession(ctx context.Context, sessionID string) (string, error) {
	if live := h.sessionHistory(sessionID); live != nil {
		return live.AgentPreset(sessionformat.DurableCut), nil
	}
	artifact, err := h.SessionArtifact(ctx, sessionID)
	if err != nil {
		return "", err
	}
	return sessionformat.EffectiveAgentPreset(artifact.Header, artifact.Events)
}
