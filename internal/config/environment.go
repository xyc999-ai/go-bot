package config

import (
	"bufio"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
)

type bindings map[string]func(string) error

func (b bindings) bindStrings(fields map[string]*string) {
	for key, field := range fields {
		b[key] = func(value string) error {
			*field = value
			return nil
		}
	}
}

func (b bindings) apply(values map[string]string) error {
	for _, key := range slices.Sorted(maps.Keys(b)) {
		if value, ok := values[key]; ok {
			if err := b[key](value); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
		}
	}
	return nil
}

func (b bindings) processValues() map[string]string {
	values := make(map[string]string)
	for key := range b {
		if value, ok := os.LookupEnv(key); ok {
			values[key] = value
		}
	}
	return values
}

func profilePath(path string) string {
	if path == "" {
		return ".env"
	}
	return path
}

func readEnvironment(path string, known, active bindings) error {
	path = profilePath(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read deployment profile (initialize it with scripts/start.sh --init-env): %w", err)
	}
	values, err := parseEnvironmentFile(path, string(data), known)
	if err != nil {
		return err
	}
	return active.apply(values)
}

// Values are literal, like Docker's --env-file assignments. Files are data,
// never shell scripts. Errors name the field or line without echoing values.
func parseEnvironmentFile(path, data string, b bindings) (map[string]string, error) {
	values := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(data))
	for line := 1; scanner.Scan(); line++ {
		raw := strings.TrimLeft(scanner.Text(), " \t")
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		key, value, ok := strings.Cut(raw, "=")
		if !ok || strings.ContainsAny(value, "\r\x00") {
			return nil, fmt.Errorf("%s:%d: expected KEY=value", path, line)
		}
		if _, known := b[key]; !known && key != "CONTROLLER_API_TOKEN" && key != "CONTROLLER_ALLOWED_ORIGINS" && key != "CONTROLLER_HTTPS_ORIGIN" {
			return nil, fmt.Errorf("%s:%d: unknown environment field", path, line)
		}
		if _, duplicate := values[key]; duplicate {
			return nil, fmt.Errorf("%s:%d: duplicate %s", path, line, key)
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read environment file %s: %w", path, err)
	}
	return values, nil
}

func controllerBindings(c *Config) bindings {
	b := bindings{}
	b.bindStrings(map[string]*string{
		"HARNESS_STORAGE":     &c.Storage.Type,
		"MODEL_API_KEY":       &c.Model.Main.APIKey,
		"MODEL_NAME":          &c.Model.Main.Name,
		"MODEL_URL":           &c.Model.Main.BaseURL,
		"WEB_SEARCH_PROVIDER": &c.Web.Provider,
		"WEB_SEARCH_API_KEY":  &c.Web.APIKey,
	})
	b.bindStrings(map[string]*string{
		"HARNESS_RUNTIME":  &c.Runtime.Provider,
		"HOST_IMAGE":       &c.Runtime.Image,
		"NAS_MOUNT_TARGET": &c.Storage.NASMountTarget,

		"CONTROLLER_LISTEN": &c.Controller.Listen,

		"ECI_REGION":                      &c.Runtime.ECI.Region,
		"ECI_VSWITCH_ID":                  &c.Runtime.ECI.VSwitchID,
		"ECI_SECURITY_GROUP_ID":           &c.Runtime.ECI.SecurityGroupID,
		"ECI_IMAGE_CACHE_ID":              &c.Runtime.ECI.ImageCacheID,
		"ALIBABA_CLOUD_ACCESS_KEY_ID":     &c.Cloud.Aliyun.Credentials.AccessKeyID,
		"ALIBABA_CLOUD_ACCESS_KEY_SECRET": &c.Cloud.Aliyun.Credentials.AccessKeySecret,
	})
	return b
}
