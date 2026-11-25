// Package agentpreset defines Go-owned Agent capability compositions. It does
// not create Agents, run tools, or manage business plugin lifetimes.
package agentpreset

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const Standard = "standard"

// SourceRef is an existing contribution provider identity, such as
// builtin:memory, plugin:<instance-id>, or mcp:<server-name>.
type SourceRef string

// Selection applies to inherited contributions. Nil allowlists inherit all;
// non-nil empty allowlists inherit none. Local contributions are merged later.
type Selection struct {
	AllowNames   *[]string
	DenyNames    []string
	AllowSources *[]SourceRef
	DenySources  []SourceRef
}

func (s Selection) Allows(name string, source SourceRef) bool {
	return (s.AllowNames == nil || slices.Contains(*s.AllowNames, name)) &&
		!slices.Contains(s.DenyNames, name) &&
		(s.AllowSources == nil || slices.Contains(*s.AllowSources, source)) &&
		!slices.Contains(s.DenySources, source)
}

type PersonaSpec struct {
	Prefix                string
	Suffix                string
	Complete              bool
	IncludeRuntimeContext *bool
}

type Definition struct {
	ID             string
	Name           string
	Description    string
	Order          int
	Persona        PersonaSpec
	Tools          Selection
	LocalTools     []string
	Skills         Selection
	PromptSections Selection
	RuntimeSources Selection
	// RequiredSources are checked against the current Host provider catalog.
	// An absent optional/inherited provider simply contributes nothing.
	RequiredSources []SourceRef
}

var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func Validate(d Definition) error {
	if !validID.MatchString(d.ID) {
		return errors.New("invalid Agent Preset id")
	}
	if d.Persona.Complete && strings.TrimSpace(d.Persona.Prefix) == "" {
		return errors.New("complete persona requires a non-empty prefix")
	}
	for label, values := range map[string][]string{"local tools": d.LocalTools, "required sources": stringsOf(d.RequiredSources)} {
		if err := validateNames(label, values); err != nil {
			return err
		}
	}
	for label, s := range map[string]Selection{"tools": d.Tools, "skills": d.Skills, "prompt sections": d.PromptSections, "runtime sources": d.RuntimeSources} {
		lists := map[string][]string{"deny names": s.DenyNames, "deny sources": stringsOf(s.DenySources)}
		if s.AllowNames != nil {
			lists["allow names"] = *s.AllowNames
		}
		if s.AllowSources != nil {
			lists["allow sources"] = stringsOf(*s.AllowSources)
		}
		for kind, values := range lists {
			if err := validateNames(label+" "+kind, values); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateNames(label string, values []string) error {
	seen := map[string]bool{}
	for _, value := range values {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) || seen[value] {
			return fmt.Errorf("invalid or duplicate %s entry %q", label, value)
		}
		seen[value] = true
	}
	return nil
}

func stringsOf[T ~string](values []T) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = string(value)
	}
	return result
}

func cloneSelection(s Selection) Selection {
	if s.AllowNames != nil {
		v := slices.Clone(*s.AllowNames)
		s.AllowNames = &v
	}
	if s.AllowSources != nil {
		v := slices.Clone(*s.AllowSources)
		s.AllowSources = &v
	}
	s.DenyNames, s.DenySources = slices.Clone(s.DenyNames), slices.Clone(s.DenySources)
	return s
}

func clone(d Definition) Definition {
	d.Tools, d.Skills = cloneSelection(d.Tools), cloneSelection(d.Skills)
	d.PromptSections, d.RuntimeSources = cloneSelection(d.PromptSections), cloneSelection(d.RuntimeSources)
	d.LocalTools, d.RequiredSources = slices.Clone(d.LocalTools), slices.Clone(d.RequiredSources)
	if d.Persona.IncludeRuntimeContext != nil {
		v := *d.Persona.IncludeRuntimeContext
		d.Persona.IncludeRuntimeContext = &v
	}
	return d
}
