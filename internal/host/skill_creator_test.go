package host

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-bot/harness/skills"
	"go-bot/harness/tools"
)

func TestSkillCreatorSeedsDiscoverableBundleAndPreservesUserChanges(t *testing.T) {
	dsh := filepath.Join(t.TempDir(), ".dsh")
	if err := seedSkillCreator(dsh, ""); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dsh, "skills", "skill-creator")
	for _, name := range []string{"SKILL.md", "LICENSE.txt", "UPSTREAM.md", "agents/openai.yaml",
		"references/openai_yaml.md", "scripts/init_skill.py", "scripts/quick_validate.py", "scripts/generate_openai_yaml.py"} {
		info, err := os.Stat(filepath.Join(base, name))
		if err != nil || info.Size() == 0 {
			t.Fatalf("missing bundled resource %s: %v", name, err)
		}
	}
	registry := &skills.Registry{DSHHome: dsh}
	tool := tools.Skill{Registry: registry, Lookup: func() skills.Lookup { return skills.Lookup{} }}
	loaded, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"skill-creator"}`))
	if err != nil || !strings.Contains(loaded, "# Skill Creator") || !strings.Contains(loaded, base) {
		t.Fatalf("load seeded skill with resource base: %s, %v", loaded, err)
	}
	path := filepath.Join(base, "SKILL.md")
	custom := "---\nname: skill-creator\ndescription: User customization\n---\nMy instructions\n"
	if err := os.WriteFile(path, []byte(custom), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(base, "scripts", "init_skill.py")); err != nil {
		t.Fatal(err)
	}
	if err := seedSkillCreator(dsh, ""); err != nil {
		t.Fatal(err)
	}
	loaded, err = tool.Execute(context.Background(), json.RawMessage(`{"name":"skill-creator"}`))
	if err != nil || !strings.Contains(loaded, "My instructions") {
		t.Fatalf("overwrote user instructions: %s, %v", loaded, err)
	}
	if _, err := os.Stat(filepath.Join(base, "scripts", "init_skill.py")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restored a user-deleted resource: %v", err)
	}
	if err := os.RemoveAll(base); err != nil {
		t.Fatal(err)
	}
	if err := seedSkillCreator(dsh, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(base); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restored user-deleted skill: %v", err)
	}
}

func TestSkillCreatorPreservesPreexistingTargets(t *testing.T) {
	for _, kind := range []string{"directory", "file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dsh := t.TempDir()
			parent := filepath.Join(dsh, "skills")
			if err := os.Mkdir(parent, 0700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(parent, "skill-creator")
			var err error
			switch kind {
			case "directory":
				err = os.Mkdir(target, 0700) // Even an empty directory belongs to the user.
			case "file":
				err = os.WriteFile(target, []byte("user data"), 0600)
			case "symlink":
				err = os.Symlink("missing-user-directory", target)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := seedSkillCreator(dsh, ""); err != nil {
				t.Fatal(err)
			}
			if kind == "directory" {
				entries, err := os.ReadDir(target)
				if err != nil || len(entries) != 0 {
					t.Fatalf("merged resources into user directory: %v, %v", entries, err)
				}
			} else if kind == "file" {
				data, err := os.ReadFile(target)
				if err != nil || string(data) != "user data" {
					t.Fatalf("replaced user file: %q, %v", data, err)
				}
			} else if link, err := os.Readlink(target); err != nil || link != "missing-user-directory" {
				t.Fatalf("replaced user symlink: %q, %v", link, err)
			}
		})
	}
}

func TestSkillCreatorFailedInitializationCanRetry(t *testing.T) {
	dsh := t.TempDir()
	blocked := filepath.Join(dsh, "skills")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := seedSkillCreator(dsh, ""); err == nil {
		t.Fatal("expected initialization error")
	}
	if _, err := os.Stat(filepath.Join(dsh, skillCreatorMarker)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marked failed initialization complete: %v", err)
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if err := seedSkillCreator(dsh, ""); err != nil {
		t.Fatalf("retry initialization: %v", err)
	}
	entries, err := os.ReadDir(dsh)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".skill-creator-") && entry.Name() != skillCreatorMarker {
			t.Fatalf("left staging files behind: %s", entry.Name())
		}
	}
}

func TestSkillCreatorDoesNotShadowExistingUserDefinitions(t *testing.T) {
	for _, source := range []string{"flat", "bundle", "agents"} {
		t.Run(source, func(t *testing.T) {
			account := t.TempDir()
			dsh, agents := filepath.Join(account, ".dsh"), filepath.Join(account, ".agents")
			path := filepath.Join(dsh, "skills", "my-creator.md")
			if source == "bundle" {
				path = filepath.Join(dsh, "skills", "custom-creator", "SKILL.md")
			} else if source == "agents" {
				path = filepath.Join(agents, "skills", "skill-creator", "SKILL.md")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("---\nname: skill-creator\ndescription: My creator\n---\nUser instructions\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := seedSkillCreator(dsh, agents); err != nil {
				t.Fatal(err)
			}
			registry := skills.Registry{DSHHome: dsh, AgentsHome: agents}
			entry, found, err := registry.Find(t.Context(), skills.Lookup{}, "skill-creator")
			if err != nil || !found || entry.Description != "My creator" {
				t.Fatalf("shadowed existing user definition: %+v, %t, %v", entry, found, err)
			}
			if err := os.RemoveAll(filepath.Dir(path)); err != nil {
				t.Fatal(err)
			}
			if err := seedSkillCreator(dsh, agents); err != nil {
				t.Fatal(err)
			}
			if _, found, err := registry.Find(t.Context(), skills.Lookup{}, "skill-creator"); err != nil || found {
				t.Fatalf("restored deleted user definition: %t, %v", found, err)
			}
		})
	}
}
