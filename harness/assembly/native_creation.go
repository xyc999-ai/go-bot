package assembly

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"go-bot/harness/permission"
	"go-bot/harness/session"
)

// NativeSessionMeta mirrors the creation-only metadata accepted by upstream's
// Agent factory. createdAt is deliberately absent: the factory owns that clock.
type NativeSessionMeta struct {
	CWD             *string `json:"cwd,omitempty"`
	ParentSession   *string `json:"parentSession,omitempty"`
	IsSeeded        *bool   `json:"isSeeded,omitempty"`
	Origin          *string `json:"origin,omitempty"`
	DelegationDepth *int    `json:"delegationDepth,omitempty"`
	AgentPreset     *string `json:"agentPreset,omitempty"`
}

type NativeParentSource struct {
	SessionID string `json:"sessionId"`
	EndSeq    uint64 `json:"endSeq"`
}

// Session inputs only. Runtime Agent options, owner/setup callbacks and their
// publication commit are separate capabilities, not serialized Session data.
type NativeSessionOptions struct {
	Meta                *NativeSessionMeta    `json:"meta,omitempty"`
	Seed                []sessionformat.Event `json:"seed"`
	InheritedEventCount *uint64               `json:"inheritedEventCount,omitempty"`
	ParentSource        *NativeParentSource   `json:"parentSource,omitempty"`
}

func (options NativeSessionOptions) Snapshot() (NativeSessionOptions, error) {
	data, err := json.Marshal(options)
	if err != nil {
		return NativeSessionOptions{}, err
	}
	var detached NativeSessionOptions
	err = json.Unmarshal(data, &detached)
	return detached, err
}

func (f NativeFactory) ActivateWithSessionOptions(ctx context.Context, identity NativeRuntimeIdentity, options NativeSessionOptions) (*NativeLive, error) {
	header := sessionformat.Header{Type: "session", Version: sessionformat.Version, ID: identity.SessionID, CreatedAt: time.Now().UnixMilli(), CWD: nativeCWD(identity), AgentPreset: identity.AgentPreset}
	if meta := options.Meta; meta != nil {
		if meta.CWD != nil {
			if !filepath.IsAbs(*meta.CWD) {
				return nil, errors.New("Session cwd must be absolute")
			}
			identity.CWD = *meta.CWD
			header.CWD = *meta.CWD
		}
		if meta.ParentSession != nil {
			header.ParentSession = *meta.ParentSession
		}
		if meta.IsSeeded != nil {
			header.IsSeeded = *meta.IsSeeded
		}
		if meta.Origin != nil {
			if *meta.Origin != "subagent" {
				return nil, errors.New("Session origin must be subagent")
			}
			header.Origin = *meta.Origin
		}
		if meta.DelegationDepth != nil {
			header.DelegationDepth = *meta.DelegationDepth
		}
		if meta.AgentPreset != nil {
			header.AgentPreset = *meta.AgentPreset
			identity.AgentPreset = *meta.AgentPreset
		}
	}
	var err error
	identity.AgentPreset, err = sessionformat.EffectiveAgentPreset(header, options.Seed)
	if err == nil {
		f, identity, err = f.resolvePreset(ctx, identity)
	}
	if err != nil {
		return nil, err
	}
	if header.AgentPreset == "" {
		header.AgentPreset = identity.AgentPreset
	}
	if err := f.validate(identity); err != nil {
		return nil, err
	}
	prepared, err := sessionformat.PrepareSession(header, options.Seed, options.InheritedEventCount)
	if err != nil {
		return nil, err
	}
	events := prepared.Events()
	// Like upstream's copying JSONL backend, storage receives the full
	// constructor history. ParentSource's identity/cut are sharing hints; no
	// parent lookup or live-parent claim is needed to copy this snapshot.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var journal sessionformat.EventJournal
	storedCount := 0
	if storage, ok := f.Storage.(NativePreparedStorage); ok {
		journal, err = storage.CreatePreparedNative(ctx, prepared.Header(), events)
		storedCount = len(events)
	} else {
		journal, err = f.Storage.CreateNative(ctx, prepared.Header())
	}
	if err != nil {
		return nil, err
	}
	s, err := prepared.Attach(journal, storedCount)
	if err != nil {
		return nil, errors.Join(err, journal.Close())
	}
	// Store the constructor's complete history before Session entry/observers.
	if err := s.Flush(ctx); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	return f.publishSession(ctx, identity, s, "activation", nil, func() error {
		for _, event := range s.Events() {
			if event.Type == "permission/preset" || event.Type == "sandbox/mode" || event.Type == "approval/policy" {
				return nil
			}
		}
		return permission.Save(ctx, s, permission.FullAccess, false, true)
	})
}
