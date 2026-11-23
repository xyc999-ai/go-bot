package config

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go-bot/internal/localstate"
)

func privateProfilePath(path string) (string, error) {
	path = profilePath(path)
	if _, err := os.Lstat(path); err == nil {
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if filepath.Base(path) == ".env.example" {
		return "", errors.New("use a private environment profile instead of .env.example")
	}
	return path, nil
}

// SaveECIImageCacheID updates an existing private deployment profile.
func SaveECIImageCacheID(path, id string) error {
	if !ValidImageCacheID(id) {
		return errors.New("invalid ECI image cache ID")
	}
	path, err := privateProfilePath(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	c := Default()
	if _, err := parseEnvironmentFile(path, string(data), controllerBindings(&c)); err != nil {
		return err
	}
	return localstate.WritePrivate(path, []byte(updateEnvironmentFile(string(data), map[string]string{"ECI_IMAGE_CACHE_ID": id})))
}

func updateEnvironmentFile(data string, changes map[string]string) string {
	if len(changes) == 0 {
		return data
	}
	lines := strings.Split(strings.TrimSuffix(data, "\n"), "\n")
	for i, line := range lines {
		key, _, _ := strings.Cut(strings.TrimLeft(line, " \t"), "=")
		if value, ok := changes[key]; ok {
			lines[i] = key + "=" + value
			delete(changes, key)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(changes)) {
		lines = append(lines, key+"="+changes[key])
	}
	return strings.Join(lines, "\n") + "\n"
}
