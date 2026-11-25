package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"unicode/utf16"

	"go-bot/harness/skills"
	"strings"

	"github.com/google/uuid"
)

type NativeSkillCatalogEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type NativeSkillCatalogSnapshot struct {
	Entries  []NativeSkillCatalogEntry
	Complete bool
}

type skillCatalogSource struct {
	Kind       string                    `json:"kind"`
	Form       string                    `json:"form"`
	Entries    []NativeSkillCatalogEntry `json:"entries"`
	Update     bool                      `json:"update,omitempty"`
	Incomplete bool                      `json:"incomplete,omitempty"`
}

func readSkillCatalog(raw json.RawMessage) (skillCatalogSource, bool) {
	var message struct {
		Source skillCatalogSource `json:"source"`
	}
	err := json.Unmarshal(raw, &message)
	source := message.Source
	return source, err == nil && source.Kind == "skill-catalog" && source.Entries != nil
}

func (a *NativeLoop) ensureSkillCatalog(ctx context.Context) error {
	if a.skillCatalogForStep == nil {
		return nil
	}
	snapshot := NativeSkillCatalogSnapshot{Complete: true}
	if a.skillCatalogTool != nil && a.tools[a.skillCatalogTool.Spec().Name] == a.skillCatalogTool {
		var err error
		snapshot, err = a.skillCatalogForStep(ctx)
		if err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	source := skillCatalogSource{
		Kind: "skill-catalog", Form: "catalog", Incomplete: !snapshot.Complete,
		Entries: make([]NativeSkillCatalogEntry, 0, len(snapshot.Entries)),
	}
	for _, entry := range snapshot.Entries {
		entry.Description = skillCatalogDescription(entry.Description)
		source.Entries = append(source.Entries, entry)
	}
	surface, err := a.session.ModelSurface()
	if err != nil {
		return err
	}
	for i := len(surface.Messages) - 1; i >= 0; i-- {
		visible, ok := readSkillCatalog(surface.Messages[i])
		if !ok {
			continue
		}
		if visible.Incomplete == source.Incomplete && slices.Equal(visible.Entries, source.Entries) {
			return nil
		}
		source.Update = true
		break
	}
	if !source.Update {
		source.Update = a.session.SkillCatalogPublished()
	}
	if !source.Update && !source.Incomplete && len(source.Entries) == 0 {
		return nil
	}
	fields := map[string]any{"kind": source.Kind, "form": source.Form, "entries": source.Entries}
	if source.Update {
		fields["update"] = true
	}
	if source.Incomplete {
		fields["incomplete"] = true
	}
	message := nativeTextMessage(uuid.NewString(), "user", fields, renderSkillCatalog(source))
	_, err = a.append("user/message", message, json.RawMessage(`"append"`), nil)
	return err
}

func skillCatalogDescription(value string) string {
	normalized := strings.Join(strings.FieldsFunc(value, skills.IsSpace), " ")
	length := 0
	cutoff := 0
	for index, character := range normalized {
		units := utf16.RuneLen(character)
		length += units
		if length > 500 {
			return normalized[:cutoff] + "..."
		}
		if length <= 497 {
			cutoff = index + len(string(character))
		}
	}
	return normalized
}

func renderSkillCatalog(source skillCatalogSource) string {
	const direct = "A user may also invoke a skill directly; its <skill_content> block then appears in this conversation. Follow it, and do not call the `skill` tool again for that skill."
	const emptyIncomplete = "No skills could be confirmed in this discovery attempt."
	head := "A skill is a reusable set of task-specific instructions. The following skills are available in this session:"
	help := "If the user names a skill, or the task clearly matches a skill's description, call the `skill` tool with the exact skill name before taking task actions. Load all applicable skills, then follow their full instructions. This catalog contains summaries only; do not infer or follow a skill's instructions until it has been loaded."
	tail := direct
	if source.Update {
		head = "The available skill catalog changed. This complete catalog replaces every earlier available-skills list in this session:"
		if source.Incomplete {
			head = "Skill discovery is incomplete. This catalog of currently available skills replaces every earlier available-skills list in this session:"
		}
		help = "Use only names in this replacement catalog. If the user names a listed skill, or the task clearly matches its description, call the `skill` tool with the exact name before acting."
		if len(source.Entries) == 0 {
			help = "No skills are currently available through the `skill` tool. Do not use names from earlier skill catalogs."
			if source.Incomplete {
				help = emptyIncomplete + " Do not use names from earlier skill catalogs."
			}
			tail = "A user may still invoke a skill directly; its <skill_content> block then appears in this conversation. Follow it, and do not call the `skill` tool for it."
		}
	} else if len(source.Entries) == 0 {
		head = emptyIncomplete
		help = "Wait for an updated catalog before calling the `skill` tool. Do not infer skill instructions from an empty catalog."
	}
	lines := []string{"<system-reminder>", head, "", "<available_skills>"}
	for _, entry := range source.Entries {
		lines = append(lines, fmt.Sprintf("- `%s`: %s", entry.Name, skills.EscapeText(entry.Description)))
	}
	lines = append(lines, "</available_skills>", "")
	if source.Incomplete {
		lines = append(lines, "Some skills could not be checked. Only confirmed skills are listed; a lower-priority definition of the same name may be selected when a preferred source is unavailable. Discovery will be retried on the next request.", "")
	}
	return strings.Join(append(lines, help, tail, "</system-reminder>"), "\n")
}

// Explicit /name gestures are recognized only in claimed human messages.
// Their bodies are appended after other step context, as in the native
// Harness, so the model sees the same skill rendering as a skill result.
func (a *NativeLoop) ensureInvokedSkills(ctx context.Context, claimed []json.RawMessage) error {
	if a.skillInvocationLoader == nil {
		return nil
	}
	seen := make(map[string]bool)
	for _, raw := range claimed {
		var message struct {
			Source struct {
				Kind string `json:"kind"`
			} `json:"source"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &message); err != nil {
			return err
		}
		if message.Source.Kind != "user" {
			continue
		}
		for _, block := range message.Content {
			if block.Type != "text" {
				continue
			}
			for _, token := range strings.FieldsFunc(block.Text, skills.IsSpace) {
				if !strings.HasPrefix(token, "/") || len(token) < 2 {
					continue
				}
				name := token[1:]
				if !skills.IsName(name) || seen[name] {
					continue
				}
				seen[name] = true
				content, available, err := a.skillInvocationLoader(ctx, name)
				if err != nil {
					return err
				}
				if !available {
					continue
				}
				injection := nativeTextMessage(uuid.NewString(), "user", map[string]any{
					"kind": "skill-invocation", "name": name, "form": "instructions",
				}, content)
				if _, err := a.append("user/message", injection, json.RawMessage(`"append"`), nil); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
