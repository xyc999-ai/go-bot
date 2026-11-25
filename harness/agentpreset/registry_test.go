package agentpreset

import (
	"context"
	"errors"
	"testing"
)

func TestBindingRetainsDefinitionWhileNewBindingsAndAvailabilityChange(t *testing.T) {
	available := true
	r := New(Standard, func(_ context.Context, d Definition) error {
		if len(d.RequiredSources) > 0 && !available {
			return errors.New("plugin is not enabled")
		}
		return nil
	})
	allow := []string{"readfile"}
	d := Definition{ID: Standard, Tools: Selection{AllowNames: &allow}}
	unregister, err := r.Register(d)
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.Resolve(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	allow[0] = "shellexecute"
	if !first.Definition().Tools.Allows("readfile", "builtin:files") {
		t.Fatal("caller mutated binding")
	}
	copy := first.Definition()
	(*copy.Tools.AllowNames)[0] = "writefile"
	if !first.Definition().Tools.Allows("readfile", "builtin:files") {
		t.Fatal("returned definition mutated binding")
	}
	d = Definition{ID: Standard, RequiredSources: []SourceRef{"plugin:calendar"}}
	if err := r.Replace(d); err != nil {
		t.Fatal(err)
	}
	unregister()
	second, err := r.Resolve(t.Context(), "")
	if err != nil || first.Same(second) {
		t.Fatalf("replacement: %v", err)
	}
	if len(first.Definition().RequiredSources) != 0 {
		t.Fatal("old Agent changed with declaration")
	}
	available = false
	if _, err := r.Resolve(t.Context(), Standard); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	rows := r.List(t.Context())
	if len(rows) != 1 || rows[0].Broken == "" || !rows[0].IsDefault {
		t.Fatalf("diagnostics: %+v", rows)
	}
	if _, err := r.Resolve(t.Context(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if !first.Definition().Tools.Allows("readfile", "builtin:files") {
		t.Fatal("unavailable new definition changed old binding")
	}
}

func TestSelectionEmptyAndIndependentAxes(t *testing.T) {
	empty := []string{}
	if (Selection{AllowNames: &empty}).Allows("readfile", "builtin:files") {
		t.Fatal("empty allowlist inherited tools")
	}
	s := Selection{DenyNames: []string{"present"}, DenySources: []SourceRef{"plugin:mail"}}
	if !s.Allows("readfile", "builtin:files") || s.Allows("present", "builtin:files") || s.Allows("send_mail", "plugin:mail") {
		t.Fatal("selection axes were not combined")
	}
	r := New(Standard, nil)
	if _, err := r.Register(Definition{ID: Standard}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Register(Definition{ID: Standard}); err == nil {
		t.Fatal("accepted duplicate")
	}
	if _, err := r.Register(Definition{ID: "bad id"}); err == nil {
		t.Fatal("accepted invalid id")
	}
	if err := r.SetDefault("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestPersonaTemplatesAreLiteralAndStrict(t *testing.T) {
	text, err := Render("Agent for {{ account.id }} in {{cwd}}", map[string]string{"account.id": "test", "cwd": "/tmp/{{literal}}"})
	if err != nil || text != "Agent for test in /tmp/{{literal}}" {
		t.Fatalf("%q %v", text, err)
	}
	for _, text := range []string{"{{unknown}}", "{{cwd", "{{1+1}}", "text }}"} {
		if _, err := Render(text, map[string]string{"cwd": "/tmp"}); err == nil {
			t.Fatalf("accepted %q", text)
		}
	}
}
