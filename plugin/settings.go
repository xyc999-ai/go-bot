package plugin

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
)

var packageName = regexp.MustCompile(`^(?:@[a-z0-9._-]+/)?[a-z0-9][a-z0-9._-]*$`)
var exactVersion = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
var instanceName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)
var gitCommit = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)

func ValidPackageName(name string) bool { return len(name) <= 214 && packageName.MatchString(name) }
func (s RuntimeSettings) Validate() error {
	if len(s.Packages) > 200 || len(s.Instances) > 200 {
		return errors.New("plugin settings exceed the supported limit")
	}
	for name, pkg := range s.Packages {
		if !ValidPackageName(name) {
			return errors.New("invalid package name")
		}
		if len(pkg.Builds) > 100 {
			return errors.New("too many plugin build permissions")
		}
		for _, build := range pkg.Builds {
			if build == name {
				continue
			}
			at := strings.LastIndex(build, "@")
			if at <= 0 || !ValidPackageName(build[:at]) || !exactVersion.MatchString(build[at+1:]) {
				return errors.New("plugin build permissions must name the package or exact dependency versions")
			}
		}

		source := pkg.Source
		if source.Spec == "" || len(source.Spec) > 4096 || strings.ContainsAny(source.Spec, "\x00\r\n") {
			return errors.New("invalid package source")
		}
		switch source.Type {
		case "registry":
			if !strings.HasPrefix(source.Spec, name+"@") || !exactVersion.MatchString(strings.TrimPrefix(source.Spec, name+"@")) {
				return errors.New("stored registry sources require exact versions")
			}

		case "directory":
			if !filepath.IsAbs(source.Spec) {
				return errors.New("development directories must be absolute Host paths")
			}
		case "tarball":
		case "git":
			_, ref, ok := strings.Cut(source.Spec, "#")
			commit, _, _ := strings.Cut(ref, "&path:/")
			if !ok || !gitCommit.MatchString(commit) {
				return errors.New("stored Git sources require a full commit")
			}
		default:
			return errors.New("unknown package source type")
		}
	}
	seen := map[string]bool{}
	for _, row := range s.Instances {
		if !instanceName.MatchString(row.ID) || seen[row.ID] {
			return errors.New("instance IDs use 1–24 lowercase letters, digits or hyphens, starting with a letter, and must be unique")
		}
		seen[row.ID] = true
		if _, found := s.Packages[row.Package]; !found {
			return errors.New("instance package is not configured")
		}
		if len(row.Config) > 0 {
			var value map[string]json.RawMessage
			if json.Unmarshal(row.Config, &value) != nil || value == nil {
				return errors.New("plugin instance config must be a JSON object")
			}
		}
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if len(data) > 768<<10 {
		return errors.New("plugin settings exceed 768 KiB")
	}
	return nil
}
func (s RuntimeSettings) Clone() RuntimeSettings {
	data, _ := json.Marshal(s)
	var out RuntimeSettings
	_ = json.Unmarshal(data, &out)
	if out.Packages == nil {
		out.Packages = map[string]RuntimePackage{}
	}
	if out.Instances == nil {
		out.Instances = []InstanceConfig{}
	}
	return out
}

// Clone returns an independent, non-nil selection map.
func (s Settings) Clone() Settings {
	out := Settings{Packages: map[string]PackageSelection{}}
	for name, selection := range s.Packages {
		out.Packages[name] = selection
	}
	return out
}
func (s Settings) Validate() error {
	if len(s.Packages) > 200 {
		return errors.New("too many selected plugins")
	}
	for name, selection := range s.Packages {
		if !ValidPackageName(name) || !packageName.MatchString(selection.CatalogID) || !exactVersion.MatchString(selection.Version) {
			return errors.New("invalid plugin selection")
		}
	}
	return nil
}
