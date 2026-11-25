// Package catalog validates the reviewed, independently published plugin market.
package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"regexp"
	"strings"

	"go-bot/plugin"
)

const DefaultURL = "https://raw.githubusercontent.com/xyc999-ai/got-bot-plugins/main/catalog.json"
const AssetPrefix = "/assets/plugins/"

type Entry struct {
	DefaultEnabled bool   `json:"defaultEnabled,omitempty"`
	AssetBase      string `json:"assetBase"`
	plugin.CatalogEntry
	Source plugin.Source `json:"source"`
	Builds []string      `json:"builds,omitempty"`
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

func Parse(data []byte) ([]Entry, error) {
	var entries []Entry
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&entries); err != nil {
		return nil, err
	}
	if !errors.Is(d.Decode(new(any)), io.EOF) || entries == nil || len(entries) > 2000 {
		return nil, errors.New("catalog must be one array of at most 2000 releases")
	}
	ids, packages, releases := map[string]string{}, map[string]string{}, map[string]bool{}
	for i := range entries {
		entry := &entries[i]
		if !idPattern.MatchString(entry.ID) || (ids[entry.ID] != "" && ids[entry.ID] != entry.Package) || (packages[entry.Package] != "" && packages[entry.Package] != entry.ID) || releases[entry.ID+"@"+entry.Version] {
			return nil, errors.New("catalog identities and release versions must be unique")
		}
		ids[entry.ID], packages[entry.Package], releases[entry.ID+"@"+entry.Version] = entry.Package, entry.ID, true
		if !versionPattern.MatchString(entry.Version) || entry.Maintainer == "" || entry.License == "" || strings.TrimSpace(entry.Name) == "" {
			return nil, fmt.Errorf("%s: version, maintainer and license are required", entry.ID)
		}
		for _, text := range []plugin.Localized{entry.Description} {
			if strings.TrimSpace(text.EN) == "" || strings.TrimSpace(text.ZH) == "" {
				return nil, fmt.Errorf("%s: English and Simplified Chinese copy are required", entry.ID)
			}
		}
		if !strings.HasPrefix(entry.Icon, AssetPrefix) || !fs.ValidPath(strings.TrimPrefix(entry.Icon, AssetPrefix)) || strings.ContainsAny(entry.Icon, "\\?#") {
			return nil, fmt.Errorf("%s: icon must reference a backend plugin asset", entry.ID)
		}

		if err := distributionURL(entry.AssetBase); err != nil || !strings.HasSuffix(entry.AssetBase, "/") {
			return nil, fmt.Errorf("%s: assetBase must be a release directory URL", entry.ID)
		}
		paths := []string{entry.Icon}
		languages := map[string]bool{}
		for _, tutorial := range entry.Tutorials {
			if (tutorial.Language != "en" && tutorial.Language != "zh-CN") || languages[tutorial.Language] || tutorial.Title == "" || !strings.HasSuffix(tutorial.Video, ".mp4") {
				return nil, fmt.Errorf("%s: invalid tutorial", entry.ID)
			}
			languages[tutorial.Language] = true
			paths = append(paths, tutorial.Video)
			if tutorial.Poster != "" {
				paths = append(paths, tutorial.Poster)
			}
		}
		for _, resource := range paths {
			rel, ok := strings.CutPrefix(resource, AssetPrefix+entry.ID+"/")
			if !ok || !fs.ValidPath(rel) || strings.ContainsAny(rel, "/\\?#") {
				return nil, fmt.Errorf("%s: resource must be a plugin asset filename", entry.ID)
			}
		}

		settings := plugin.RuntimeSettings{Packages: map[string]plugin.RuntimePackage{entry.Package: {Source: entry.Source, Builds: entry.Builds}}}
		if err := settings.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", entry.ID, err)
		}
		switch entry.Source.Type {
		case "registry":
			if entry.Source.Spec != entry.Package+"@"+entry.Version {
				return nil, fmt.Errorf("%s: registry source must match the catalog release", entry.ID)
			}
		case "git": // Settings.Validate requires a full commit, never a branch.
		case "tarball":
			if err := distributionURL(entry.Source.Spec); err != nil {
				return nil, fmt.Errorf("%s: invalid archive URL", entry.ID)
			}

		default:
			return nil, fmt.Errorf("%s: development directories are not market releases", entry.ID)
		}
	}
	return entries, nil
}

// HTTP loopback is useful for a self-hosted development market; published
// releases use HTTPS. The index is supplied by the deployment, never a client.
func distributionURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid distribution URL")
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1" || u.Hostname() == "host.docker.internal"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return errors.New("distribution URL must use HTTPS")
	}
	return nil
}
func Latest(entries []Entry) []Entry {
	result := []Entry{}
	byID := map[string]int{}
	for _, entry := range entries {
		if i, ok := byID[entry.ID]; ok {
			if plugin.NewerVersion(result[i].Version, entry.Version) != "" {
				result[i] = entry
			}
			continue
		}
		byID[entry.ID] = len(result)
		result = append(result, entry)
	}
	return result
}
