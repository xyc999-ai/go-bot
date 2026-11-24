package host

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"go-bot/harness/skills"
	"go-bot/internal/durablefs"
)

//go:embed skills/skill-creator
var skillCreatorFiles embed.FS

const skillCreatorMarker = ".skill-creator-initialized"

// seedSkillCreator runs before Sessions start. Publish the complete bundle once;
// existing user files and deliberate deletion survive subsequent Host starts.
func seedSkillCreator(dshHome, agentsHome string) error {
	if err := durablefs.EnsureDirectory(dshHome); err != nil {
		return err
	}
	root, err := os.OpenRoot(dshHome)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := root.Lstat(skillCreatorMarker); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	const target = "skills/skill-creator"
	if err := root.Mkdir("skills", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	// A user may already provide this name through a flat file, another bundle
	// directory, or the shared .agents root. Do not shadow that definition.
	registry := skills.Registry{DSHHome: dshHome, AgentsHome: agentsHome}
	_, existing, err := registry.Find(context.Background(), skills.Lookup{}, "skill-creator")
	if err != nil {
		return err
	}
	if !existing {
		if _, err := root.Lstat(target); errors.Is(err, os.ErrNotExist) {
			stage, err := os.MkdirTemp(dshHome, ".skill-creator-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(stage)
			bundle, err := fs.Sub(skillCreatorFiles, "skills/skill-creator")
			if err != nil {
				return err
			}
			if err := copySkillCreator(bundle, stage); err != nil {
				return err
			}
			// Recheck before publication so an existing user bundle is never merged
			// with the packaged resources, even if its SKILL.md is missing.
			if _, err := root.Lstat(target); errors.Is(err, os.ErrNotExist) {
				if err := root.Rename(filepath.Base(stage), target); err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	// Commit the marker only after the bundle is durable. A crash beforehand
	// retries safely, treating any already-published directory as user data.
	if err := durablefs.SyncDirectory(filepath.Join(dshHome, "skills")); err != nil {
		return err
	}
	marker, err := root.OpenFile(skillCreatorMarker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, writeErr := marker.WriteString("OpenAI skill-creator initialized; existing files are user-managed.\n")
	if err := errors.Join(writeErr, marker.Sync(), marker.Close()); err != nil {
		return err
	}
	return durablefs.SyncDirectory(dshHome)
}

func copySkillCreator(bundle fs.FS, stage string) error {
	var directories []string
	err := fs.WalkDir(bundle, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		target := filepath.Join(stage, filepath.FromSlash(path))
		if entry.IsDir() {
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
			directories = append(directories, target)
			return nil
		}
		data, err := fs.ReadFile(bundle, path)
		if err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(data)
		return errors.Join(writeErr, file.Sync(), file.Close())
	})
	if err != nil {
		return fmt.Errorf("prepare skill-creator: %w", err)
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if err := durablefs.SyncDirectory(directories[i]); err != nil {
			return err
		}
	}
	return nil
}
