package agent

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/google/uuid"
)

type RuntimeContextSection struct {
	Name   string `json:"name"`
	Text   string `json:"text"`
	Source string `json:"source,omitempty"`
}

// SetSystemContextSource supplies dynamic system sections at each model step.
// ensureSystem commits their assembled text through the existing v4
// system/message replacement mechanism, including withdrawal and compaction.
func (a *NativeLoop) SetSystemContextSource(source func(context.Context, string) ([]RuntimeContextSection, error)) {
	a.systemContextSource = source
}

func (a *NativeLoop) SetRuntimeContext(provider func() []RuntimeContextSection) {
	a.runtimeContext = provider
}

func (a *NativeLoop) SetRuntimeContextSource(source func(context.Context, string) ([]RuntimeContextSection, error)) {
	a.runtimeContextSource = source
}

func (a *NativeLoop) ensureRuntimeContext(ctx context.Context) error {
	if a.runtimeContext == nil && a.runtimeContextSource == nil && a.promptAssembler == nil {
		return nil
	}
	var sections []RuntimeContextSection
	sections = append(sections, a.assembledContexts...)
	if a.runtimeContext != nil {
		sections = append(sections, a.runtimeContext()...)
	}
	if a.runtimeContextSource != nil {
		extra, err := a.runtimeContextSource(ctx, a.session.Header().ID)
		if err != nil {
			return err
		}
		sections = append(sections, extra...)
	}
	surface, err := a.session.ModelSurface()
	if err != nil {
		return err
	}
	previous := false
	for _, messageData := range slices.Backward(surface.Messages) {
		// Use retained model messages so a compacted snapshot is reannounced.
		var message struct {
			Source struct {
				Kind     string
				Form     string
				Sections []RuntimeContextSection
			}
		}
		if json.Unmarshal(messageData, &message) != nil || message.Source.Kind != "runtime-context" || message.Source.Form != "snapshot" {
			continue
		}
		if slices.Equal(message.Source.Sections, sections) {
			return nil
		}
		previous = true
		break
	}
	if len(sections) == 0 && !previous {
		return nil
	}
	paragraphs := []string{"Current runtime context. This snapshot supersedes earlier runtime-context snapshots."}
	for _, section := range sections {
		paragraphs = append(paragraphs, section.Text)
	}
	message := map[string]any{
		"id": uuid.NewString(), "role": "user",
		"source":  map[string]any{"kind": "runtime-context", "form": "snapshot", "sections": sections},
		"content": []any{map[string]any{"type": "text", "text": strings.Join(paragraphs, "\n\n")}},
	}
	_, err = a.append("user/message", message, json.RawMessage(`"append"`), nil)
	return err
}
