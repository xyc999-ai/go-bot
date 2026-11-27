package host

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"os"
	"time"

	"go-bot/harness/agentpreset"
	"go-bot/harness/assembly"
	"go-bot/harness/session"
	"go-bot/internal/clientlocale"
	"go-bot/internal/durablefs"
)

func mainWelcome(locale string) string {
	return clientlocale.Welcome(locale)
}

func ensureMainJournal(ctx context.Context, root, id, locale string, claim sessionformat.OwnerClaim, presetIDs ...string) error {
	if _, err := findHostSession(ctx, root, id); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	workspace := defaultSessionWorkspace(root, id)
	if err := durablefs.EnsureDirectory(workspace); err != nil {
		return err
	}
	store := sessionformat.FileStore{Root: sessionLogRoot(root), ProjectCWD: workspace, Claim: claim}
	now := time.Now().UnixMilli()
	message, err := json.Marshal(map[string]any{"stream": []any{}, "message": map[string]any{
		"id": uuid.NewString(), "role": "assistant", "source": map[string]string{"kind": "go-bot/welcome"}, "content": []map[string]string{{"type": "text", "text": mainWelcome(locale)}},
	}})
	if err != nil {
		return err
	}
	preset := agentpreset.Standard
	if len(presetIDs) > 0 {
		preset = presetIDs[0]
	}
	header := sessionformat.Header{Type: "session", Version: sessionformat.Version, ID: id, CreatedAt: now, CWD: workspace, AgentPreset: preset}
	system, err := json.Marshal(map[string]any{"message": map[string]any{
		"id": uuid.NewString(), "role": "system", "source": map[string]string{"kind": "go-bot/initial-system"},
		"content": []map[string]string{{"type": "text", "text": assembly.DefaultCoreSystemPrompt}},
	}})
	if err != nil {
		return err
	}
	prepared, err := sessionformat.PrepareSession(header, []sessionformat.Event{
		{Type: "system/message", Seq: 0, Time: now, Data: system, SurfaceOp: json.RawMessage(`"append"`)},
		{Type: "assistant/message", Seq: 1, Time: now, Data: message, SurfaceOp: json.RawMessage(`"append"`)},
	}, nil)
	if err != nil {
		return err
	}
	journal, err := store.CreatePrepared(ctx, prepared.Header(), prepared.Events())
	if err != nil {
		return err
	}
	return journal.Close()
}
