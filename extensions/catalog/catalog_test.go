package catalog

import (
	"encoding/json"
	"testing"

	"go-bot/plugin"
)

func reviewed() Entry {
	text := plugin.Localized{EN: "Mail", ZH: "邮件"}
	return Entry{CatalogEntry: plugin.CatalogEntry{ID: "mail", Package: "@fixture/mail", Version: "1.0.0", Name: "Mail", Description: text, Icon: "/assets/plugins/mail/icon.png", Maintainer: "Fixture", License: "MIT"}, AssetBase: "https://example.com/mail-v1.0.0/", Source: plugin.Source{Type: "registry", Spec: "@fixture/mail@1.0.0"}}
}

func TestCatalogRequiresReviewedPinnedBilingualReleases(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*Entry)
	}{
		{"floating-version", func(e *Entry) { e.Source.Spec = "@fixture/mail@latest" }},
		{"wrong-version", func(e *Entry) { e.Version = "2.0.0" }},
		{"floating-git", func(e *Entry) { e.Source = plugin.Source{Type: "git", Spec: "https://example.com/mail.git#main"} }},
		{"host-directory", func(e *Entry) { e.Source = plugin.Source{Type: "directory", Spec: "/tmp/mail"} }},
		{"untranslated", func(e *Entry) { e.Description.ZH = "" }},
		{"unreviewed-icon", func(e *Entry) { e.Icon = "https://example.com/icon.svg" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := reviewed()
			test.edit(&entry)
			data, _ := json.Marshal([]Entry{entry})
			if _, err := Parse(data); err == nil {
				t.Fatal("accepted invalid release")
			}
		})
	}
	data, _ := json.Marshal([]Entry{reviewed()})
	if entries, err := Parse(data); err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
	data, _ = json.Marshal([]Entry{reviewed(), reviewed()})
	if _, err := Parse(data); err == nil {
		t.Fatal("accepted duplicate identity")
	}
	// An empty real catalog is preferable to publishing demo placeholders.
	if entries, err := Parse([]byte(`[]`)); err != nil || entries == nil {
		t.Fatal("catalog must serialize as an array")
	}
}
