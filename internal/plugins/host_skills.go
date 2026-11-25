package plugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"go-bot/harness/skills"
)

type hostSkillSummary struct {
	Name           string               `json:"name"`
	Description    string               `json:"description"`
	WhenToUse      string               `json:"whenToUse,omitempty"`
	ModelInvocable *bool                `json:"modelInvocable,omitempty"`
	UserInvocable  *bool                `json:"userInvocable,omitempty"`
	ResourceBase   *skills.ResourceBase `json:"resourceBase,omitempty"`
}
type hostSkillDefinition struct {
	hostSkillSummary
	Content string `json:"content"`
}
type hostSkillItem struct {
	hostSkillSummary
	Key json.RawMessage `json:"key"`
}

func (s hostSkillSummary) validate() error {
	if !skills.IsName(s.Name) || strings.TrimSpace(s.Description) == "" || len(s.Description) > 16000 {
		return failure("INVALID_ARGUMENT", "A Skill needs a valid name and description")
	}
	if base := s.ResourceBase; base != nil {
		switch base.Kind {
		case "directory":
			if !filepath.IsAbs(base.Path) {
				return failure("INVALID_ARGUMENT", "A Skill resource directory must be absolute")
			}
		case "url":
			if !strings.HasPrefix(base.URL, "https://") && !strings.HasPrefix(base.URL, "http://") {
				return failure("INVALID_ARGUMENT", "A Skill resource URL must use HTTP(S)")
			}
		default:
			return failure("INVALID_ARGUMENT", "Invalid Skill resource base")
		}
	}
	return nil
}
func (s hostSkillSummary) summary() skills.Summary {
	model, user := true, true
	if s.ModelInvocable != nil {
		model = *s.ModelInvocable
	}
	if s.UserInvocable != nil {
		user = *s.UserInvocable
	}
	return skills.Summary{Name: s.Name, Description: s.Description, WhenToUse: s.WhenToUse, ModelInvocable: model, UserInvocable: user}
}
func (s hostSkillDefinition) loaded(provider string) (skills.Loaded, error) {
	if err := s.hostSkillSummary.validate(); err != nil {
		return skills.Loaded{}, err
	}
	if strings.TrimSpace(s.Content) == "" || len(s.Content) > 256<<10 {
		return skills.Loaded{}, failure("INVALID_RESULT", "A Skill needs nonempty content within 256 KiB")
	}
	return skills.Loaded{Summary: s.summary(), Value: skills.Value{Name: s.Name, Provider: provider, Content: s.Content, ResourceBase: s.ResourceBase}}, nil
}
func validateHostSkill(row *hostContribution) error {
	switch row.Kind {
	case "skill":
		var d hostSkillDefinition
		if err := decodeHostJSON(row.Descriptor, &d); err != nil {
			return err
		}
		_, err := d.loaded(row.Owner)
		return err
	case "skill-directory":
		var d struct {
			Path string `json:"path"`
		}
		if err := decodeHostJSON(row.Descriptor, &d); err != nil {
			return err
		}
		info, err := os.Stat(d.Path)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(d.Path) || !info.IsDir() {
			return failure("INVALID_ARGUMENT", "A Skill root must be an existing absolute directory")
		}
		return nil
	case "skill-provider":
		var d struct {
			ID string `json:"id"`
		}
		if err := decodeHostJSON(row.Descriptor, &d); err != nil {
			return err
		}
		if !identifier.MatchString(d.ID) {
			return failure("INVALID_ARGUMENT", "A Skill provider needs an id")
		}
		found := map[string]bool{}
		for _, name := range row.Callbacks {
			found[name] = true
		}
		if !found["list"] || !found["get"] {
			return failure("INVALID_ARGUMENT", "A Skill provider needs list and get callbacks")
		}
		return nil
	}
	return failure("NOT_FOUND", "Unknown Skill contribution")
}

// Skills supplies sources to the existing Go discovery merger. Filesystem
// user/project sources keep their original rank and win over bundled sources.
func (r *hostBridge) Skills(_ context.Context, _ skills.Lookup) ([]skills.Provider, error) {
	var result []skills.Provider
	for _, kind := range []string{"skill", "skill-directory", "skill-provider"} {
		for _, row := range r.rows(kind) {
			provider := skills.Provider{Name: row.Owner + ":" + row.ID, Source: "plugin:" + row.Owner}
			provider.List = func(ctx context.Context, lookup skills.Lookup) (skills.ProviderSnapshot, error) {
				if row.ctx.Err() != nil {
					return skills.ProviderSnapshot{}, failure("UNAVAILABLE", "Skill contribution was disposed")
				}
				snapshot := skills.ProviderSnapshot{Complete: true, Entries: []skills.ProviderEntry{}}
				add := func(d hostSkillSummary, key json.RawMessage) error {
					if err := d.validate(); err != nil {
						return err
					}
					if !json.Valid(key) {
						return failure("INVALID_RESULT", "Skill provider keys must be JSON values")
					}
					snapshot.Entries = append(snapshot.Entries, skills.ProviderEntry{Summary: d.summary(), Rank: 600 + float64(row.order), Data: key})
					return nil
				}
				switch row.Kind {
				case "skill":
					var d hostSkillDefinition
					_ = json.Unmarshal(row.Descriptor, &d)
					if err := add(d.hostSkillSummary, json.RawMessage(`null`)); err != nil {
						return snapshot, err
					}
				case "skill-directory":
					var d struct {
						Path string `json:"path"`
					}
					_ = json.Unmarshal(row.Descriptor, &d)
					disk := &skills.Registry{CustomDirs: []string{d.Path}}
					files, err := disk.Snapshot(ctx, skills.Lookup{})
					if err != nil {
						return snapshot, err
					}
					snapshot.Complete = files.Complete
					for _, candidate := range files.Entries {
						key, _ := json.Marshal(candidate.Name)
						snapshot.Entries = append(snapshot.Entries, skills.ProviderEntry{Summary: candidate.Summary, Rank: 600 + float64(row.order), Data: key})
					}
				case "skill-provider":
					var listed struct {
						Items    []hostSkillItem `json:"items"`
						Complete bool            `json:"complete"`
					}
					if err := r.callback(ctx, row, "list", map[string]string{"cwd": lookup.CWD}, map[string]bool{"discovery": true}, &listed); err != nil {
						return snapshot, err
					}
					if len(listed.Items) > 4096 {
						return snapshot, failure("INVALID_RESULT", "Skill provider returned too many entries")
					}
					snapshot.Complete = listed.Complete
					seen := map[string]bool{}
					for _, item := range listed.Items {
						if seen[item.Name] {
							return snapshot, failure("INVALID_RESULT", "Skill provider returned duplicate names")
						}
						seen[item.Name] = true
						if err := add(item.hostSkillSummary, item.Key); err != nil {
							return snapshot, err
						}
					}
				}
				return snapshot, nil
			}
			provider.Load = func(ctx context.Context, lookup skills.Lookup, entry skills.ProviderEntry) (skills.Loaded, error) {
				if row.ctx.Err() != nil {
					return skills.Loaded{}, failure("UNAVAILABLE", "Skill contribution was disposed")
				}
				switch row.Kind {
				case "skill":
					var d hostSkillDefinition
					_ = json.Unmarshal(row.Descriptor, &d)
					return d.loaded(provider.Name)
				case "skill-directory":
					var d struct {
						Path string `json:"path"`
					}
					_ = json.Unmarshal(row.Descriptor, &d)
					disk := &skills.Registry{CustomDirs: []string{d.Path}}
					candidate, found, err := disk.Find(ctx, skills.Lookup{}, entry.Name)
					if err != nil {
						return skills.Loaded{}, err
					}
					if !found {
						return skills.Loaded{}, os.ErrNotExist
					}
					value, found, err := candidate.Load(ctx)
					if err == nil && !found {
						err = os.ErrNotExist
					}
					return value, err
				default:
					var d *hostSkillDefinition
					if err := r.callback(ctx, row, "get", map[string]any{"key": entry.Data, "context": map[string]string{"cwd": lookup.CWD}}, map[string]bool{"discovery": true}, &d); err != nil {
						return skills.Loaded{}, err
					}
					if d == nil {
						return skills.Loaded{}, os.ErrNotExist
					}
					if d.Name != entry.Name {
						return skills.Loaded{}, failure("INVALID_RESULT", "Skill provider changed the selected identity")
					}
					return d.loaded(provider.Name)
				}
			}
			result = append(result, provider)
		}
	}
	return result, nil
}

func (r *hostBridge) skillRequest(ctx context.Context, method string, raw json.RawMessage, discovery bool) (any, error) {
	if discovery {
		return nil, failure("CONFLICT", "Skill discovery cannot recursively query the merged Skill registry")
	}
	if r.config.Skills == nil {
		return nil, failure("UNAVAILABLE", "Go Skill registry is not configured")
	}
	var input struct {
		CWD  string `json:"cwd"`
		Name string `json:"name,omitempty"`
	}
	if err := decodeHostJSON(raw, &input); err != nil {
		return nil, err
	}
	lookup := skills.Lookup{CWD: input.CWD, Workspace: r.config.Workspace}
	if lookup.CWD == "" {
		lookup.CWD = lookup.Workspace
	}
	if !filepath.IsAbs(lookup.CWD) {
		return nil, failure("INVALID_ARGUMENT", "Skill lookup requires an absolute cwd")
	}
	switch method {
	case "invalidate":
		return nil, nil // No extra Bridge cache; Go revalidates every selected source.
	case "list":
		snapshot, err := r.config.Skills.Snapshot(ctx, lookup)
		if err != nil {
			return nil, err
		}
		items := []map[string]any{}
		for _, entry := range snapshot.Entries {
			items = append(items, hostSkillCatalog(entry.Catalog()))
		}
		return map[string]any{"items": items, "complete": snapshot.Complete}, nil
	case "get":
		candidate, found, err := r.config.Skills.Find(ctx, lookup, input.Name)
		if err != nil || !found {
			return nil, err
		}
		value, found, err := candidate.Load(ctx)
		if err != nil || !found {
			return nil, err
		}
		definition := candidate.Definition(value)
		result := hostSkillCatalog(definition.CatalogSkill)
		result["content"] = definition.Content
		return result, nil
	default:
		return nil, failure("NOT_FOUND", "Unknown Skill method")
	}
}

func hostSkillCatalog(value skills.CatalogSkill) map[string]any {
	result := map[string]any{"name": value.Name, "description": value.Description, "source": value.Source, "provider": value.Provider, "modelInvocable": value.Invocation.ModelInvocable, "userInvocable": value.Invocation.UserInvocable}
	if value.WhenToUse != "" {
		result["whenToUse"] = value.WhenToUse
	}
	if value.Path != "" {
		result["path"] = value.Path
	}
	if value.ResourceBase != nil {
		result["resourceBase"] = value.ResourceBase
	}
	return result
}
