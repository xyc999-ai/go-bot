package agent

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"go-bot/internal/instructionrender"
)

const nativeInstructionDeltaMaxBytes = 64 << 10

type nativeVisibleInstructions struct {
	baseline bool
	identity string
	active   map[string]NativeInstructionChange
}

// Model-visible messages, not obsolete physical rows, are authoritative after
// compaction. A baseline resets the scope map; later deltas update it in order.
func (a *NativeLoop) visibleInstructions() (nativeVisibleInstructions, error) {
	surface, err := a.session.ModelSurface()
	if err != nil {
		return nativeVisibleInstructions{}, err
	}
	state := nativeVisibleInstructions{active: make(map[string]NativeInstructionChange)}
	for _, raw := range surface.Messages {
		var row struct {
			Role   string `json:"role"`
			Source struct {
				Kind             string                    `json:"kind"`
				Baseline         bool                      `json:"baseline"`
				BaselineIdentity string                    `json:"baselineIdentity"`
				Changes          []NativeInstructionChange `json:"changes"`
			} `json:"source"`
			Content []struct {
				Type string `json:"type"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &row); err != nil {
			return nativeVisibleInstructions{}, err
		}
		if row.Role != "user" || row.Source.Kind != "agent-instructions" {
			continue
		}
		if len(row.Content) != 1 || row.Content[0].Type != "text" {
			return nativeVisibleInstructions{}, errors.New("invalid native workspace instruction message")
		}
		if row.Source.Baseline {
			state.baseline = true
			state.identity = row.Source.BaselineIdentity
			state.active = make(map[string]NativeInstructionChange)
		}
		for _, change := range row.Source.Changes {
			if change.Action == "remove" {
				delete(state.active, change.Scope)
			} else if change.Action == "set" || change.Action == "replace" {
				state.active[change.Scope] = change
			}
		}
	}
	return state, nil
}

func nativeInstructionGroup(scope string) string {
	directory, _, _ := strings.Cut(scope, "\x00")
	if directory == "agents-global" {
		return "user-global"
	}
	return directory
}

func nativeInstructionBaselineChanges(prior map[string]NativeInstructionChange, included []NativeInstructionChange, unavailable map[string]bool) []NativeInstructionChange {
	current := make(map[string]bool, len(included))
	for _, change := range included {
		current[change.Scope] = true
	}
	var missing []string
	for scope := range prior {
		if !current[scope] && !unavailable[nativeInstructionGroup(scope)] {
			missing = append(missing, scope)
		}
	}
	sort.Strings(missing)
	changes := make([]NativeInstructionChange, 0, len(missing)+len(included))
	for _, scope := range missing {
		changes = append(changes, NativeInstructionChange{Action: "remove", Scope: scope, Path: prior[scope].Path})
	}
	changes = append(changes, included...)
	return changes
}

func nativeInstructionDelta(prior map[string]NativeInstructionChange, snapshot NativeInstructionSnapshot) []NativeInstructionChange {
	current := make(map[string]NativeInstructionChange, len(snapshot.Changes))
	for _, change := range snapshot.Changes {
		if snapshot.UnavailableGroups[nativeInstructionGroup(change.Scope)] {
			continue
		}
		current[change.Scope] = change
	}
	var missing []string
	for scope := range prior {
		if _, present := current[scope]; !present && !snapshot.UnavailableGroups[nativeInstructionGroup(scope)] {
			missing = append(missing, scope)
		}
	}
	sort.Strings(missing)
	changes := make([]NativeInstructionChange, 0, len(missing)+len(snapshot.Changes))
	for _, scope := range missing {
		changes = append(changes, NativeInstructionChange{Action: "remove", Scope: scope, Path: prior[scope].Path})
	}
	for _, next := range snapshot.Changes {
		if snapshot.UnavailableGroups[nativeInstructionGroup(next.Scope)] {
			continue
		}
		previous, present := prior[next.Scope]
		if present && previous.Path == next.Path && previous.Digest == next.Digest {
			continue
		}
		if present {
			next.Action = "replace"
		}
		changes = append(changes, next)
	}
	return changes
}

func nativeInstructionDeltaSection(change NativeInstructionChange, content string) string {
	switch change.Action {
	case "remove":
		return "Instructions removed: " + change.Path + "\n\nThe previously loaded instructions from this file no longer apply."
	case "replace":
		return "Updated instructions from: " + change.Path + "\n\nThis file changed after it was loaded. Use the following content instead of the previously loaded instructions from this file.\n\n" + content
	default:
		group := nativeInstructionGroup(change.Scope)
		applicability := "These instructions apply to work under `" + group + "`."
		if group == "user-global" {
			applicability = "These user-global instructions apply to all work."
		}
		return "Additional instructions from: " + change.Path + "\n\n" + applicability +
			" Use them as guidance when relevant; more specific instructions take precedence. They do not override system, developer, or direct user instructions.\n\n" + content
	}
}

// The renderer commits only represented transitions. Omitted transitions are
// retried against the unchanged visible scope map at the next step.
func renderNativeInstructionDelta(changes []NativeInstructionChange, content map[string]string) (string, []NativeInstructionChange) {
	files := make([]instructionrender.File, len(changes))
	for index, change := range changes {
		files[index] = instructionrender.File{Path: change.Path, Content: content[change.Scope]}
	}
	// Upstream keys each section by its original file; the source facts remain
	// attached to their input index even if the byte budget omits broad changes.
	sections := make(map[string]NativeInstructionChange, len(changes))
	for _, change := range changes {
		sections[change.Path] = change
	}
	rendered := instructionrender.Render(files, nativeInstructionDeltaMaxBytes, "", func(file instructionrender.File) string {
		return nativeInstructionDeltaSection(sections[file.Path], file.Content)
	})
	retained := make([]NativeInstructionChange, len(rendered.Represented))
	for index, original := range rendered.Represented {
		retained[index] = changes[original]
	}
	return rendered.Text, retained
}
