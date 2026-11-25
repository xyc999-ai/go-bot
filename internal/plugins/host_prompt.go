package plugins

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"go-bot/harness/agent"
	"go-bot/harness/agentpreset"
)

type hostPromptDefinition struct {
	ID       string  `json:"id"`
	Location string  `json:"location"`
	Text     *string `json:"text,omitempty"`
	Order    int     `json:"order,omitempty"`
}

// AssemblePrompt appends only declared contributions. It never delegates core
// prompt assembly, tool selection, memory or compaction to the Node process.
func (r *hostBridge) AssemblePrompt(ctx context.Context, input agent.PromptAssemblyInput) (agent.PromptAssembly, error) {
	output := agent.PromptAssembly{SystemPrompt: input.SystemPrompt, Tools: input.Tools}
	rows := r.rows("prompt")
	definition := func(row *hostContribution) hostPromptDefinition {
		var d hostPromptDefinition
		_ = json.Unmarshal(row.Descriptor, &d)
		return d
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := definition(rows[i]), definition(rows[j])
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		if rows[i].Owner != rows[j].Owner {
			return rows[i].Owner < rows[j].Owner
		}
		return a.ID < b.ID
	})
	size := 0
	for _, row := range rows {
		d := definition(row)
		if binding := agentpreset.FromContext(ctx); binding != nil {
			preset := binding.Definition()
			name, source := row.Owner+"/"+d.ID, agentpreset.SourceRef("plugin:"+row.Owner)
			if d.Location == "system" {
				if preset.Persona.Complete || !preset.PromptSections.Allows(name, source) {
					continue
				}
			} else if (preset.Persona.IncludeRuntimeContext != nil && !*preset.Persona.IncludeRuntimeContext) || !preset.RuntimeSources.Allows(name, source) {
				continue
			}
		}
		text := ""
		if d.Text != nil {
			text = *d.Text
		} else {
			view := map[string]any{"sessionId": input.SessionID, "cwd": input.CWD, "workspace": input.CWD, "model": map[string]string{"provider": input.ModelSelection.Provider, "name": input.ModelSelection.Model}}
			if err := r.callback(ctx, row, "text", view, nil, &text); err != nil {
				return output, err
			}
		}
		size += len(text)
		if size > 256<<10 {
			return output, failure("INVALID_RESULT", "Plugin prompt contributions exceed 256 KiB")
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		if d.Location == "system" {
			output.SystemPrompt += "\n\n" + text
		} else {
			output.Contexts = append(output.Contexts, agent.RuntimeContextSection{Name: row.Owner + "/" + d.ID, Text: text, Source: "plugin:" + row.Owner})
		}
	}
	return output, ctx.Err()
}
