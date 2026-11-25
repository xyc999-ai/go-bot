// Package plugin defines the serializable Host extension management contracts.
package plugin

import "encoding/json"

const Protocol = 1

type Source struct {
	Type     string `json:"type"`
	Spec     string `json:"spec"`
	Registry string `json:"registry,omitempty"`
	AuthRef  string `json:"authRef,omitempty"`
	Username string `json:"username,omitempty"`
}
type RuntimePackage struct {
	Version   string   `json:"version,omitempty"`
	CatalogID string   `json:"catalogId,omitempty"`
	Source    Source   `json:"source"`
	Builds    []string `json:"builds,omitempty"`
}

// CatalogEntry is the public, reviewed description. Installation sources stay
// in the Controller's reviewed remote catalog and are never accepted from clients.
type Localized struct {
	EN string `json:"en"`
	ZH string `json:"zh-CN"`
}
type Tutorial struct {
	Language string `json:"language"`
	Title    string `json:"title"`
	Video    string `json:"video"`
	Poster   string `json:"poster,omitempty"`
}

type CatalogEntry struct {
	ID          string     `json:"pluginId"`
	Package     string     `json:"package"`
	Name        string     `json:"name"`
	Description Localized  `json:"description"`
	Version     string     `json:"version"`
	Icon        string     `json:"icon"`
	Maintainer  string     `json:"maintainer"`
	License     string     `json:"license"`
	Tutorials   []Tutorial `json:"tutorials,omitempty"`
}

// PackageSelection is the only per-plugin data persisted in the user's Redis hash.
type PackageSelection struct {
	CatalogID string `json:"catalogId"`
	Version   string `json:"version"`
	Enabled   bool   `json:"enabled"`
}
type Settings struct {
	Packages map[string]PackageSelection `json:"packages"`
}
type PackageInput struct {
	Enabled bool `json:"enabled"`
}
type PackageInfo struct {
	Name             string          `json:"name"`
	Version          string          `json:"version"`
	Description      string          `json:"description"`
	License          string          `json:"license"`
	Source           Source          `json:"source"`
	API              string          `json:"api"`
	Title            string          `json:"title,omitempty"`
	ConfigSchema     json.RawMessage `json:"configSchema,omitempty"`
	AvailableVersion string          `json:"availableVersion,omitempty"`
	Present          bool            `json:"present"`
}
type InstanceConfig struct {
	ID      string          `json:"id"`
	Package string          `json:"package"`
	Enabled bool            `json:"enabled"`
	Config  json.RawMessage `json:"config,omitempty"`
}
type RuntimeSettings struct {
	Packages  map[string]RuntimePackage `json:"packages"`
	Instances []InstanceConfig          `json:"instances"`
}
type Instance struct {
	ID      string `json:"id"`
	Package string `json:"package"`
	State   string `json:"state"`
	Error   string `json:"error,omitempty"`
}
type Error struct {
	Code         string `json:"code"`
	Name         string `json:"name,omitempty"`
	Message      string `json:"message"`
	Unclassified bool   `json:"-"`
}

func (e *Error) Error() string { return e.Message }
func (e *Error) ToolErrorInfo() (string, string) {
	if e.Unclassified || e.Code == "EXTENSION_ERROR" {
		return "", ""
	}
	if e.Name != "" {
		return e.Name, e.Code
	}
	return "HostError", e.Code
}
