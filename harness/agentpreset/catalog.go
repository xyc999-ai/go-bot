package agentpreset

import (
	"fmt"
	"slices"
)

// Catalog is read-only Host metadata. Building one must not start plugins,
// create an Agent, or invoke business callbacks.
type Catalog struct {
	Sources    []SourceRef
	LocalTools []string
	// Nil defers tool-name checks to the embedding factory. A populated map
	// includes known builtins and current registered business tools.
	Tools map[string]SourceRef
}

func BuiltinToolSource(name string) SourceRef {
	switch name {
	case "readfile", "writefile", "editfile", "listfiles", "glob", "grep", "present", "readimage":
		return "builtin:files"
	case "memory":
		return "builtin:memory"
	case "skill":
		return "builtin:skills"
	case "session_search":
		return "builtin:session-search"
	}
	return SourceRef("builtin:" + name)
}

func (c Catalog) Check(d Definition) error {
	for _, source := range d.RequiredSources {
		if !slices.Contains(c.Sources, source) {
			return fmt.Errorf("required provider %q is unavailable", source)
		}
	}
	for _, name := range d.LocalTools {
		if !slices.Contains(c.LocalTools, name) {
			return fmt.Errorf("unknown local tool provider %q", name)
		}
	}
	if c.Tools != nil {
		if d.Tools.AllowNames != nil {
			for _, name := range *d.Tools.AllowNames {
				if _, exists := c.Tools[name]; !exists {
					return fmt.Errorf("unknown selected tool %q", name)
				}
			}
		}
		for _, name := range d.Tools.DenyNames {
			if _, exists := c.Tools[name]; !exists {
				return fmt.Errorf("unknown excluded tool %q", name)
			}
		}
	}
	return nil
}
