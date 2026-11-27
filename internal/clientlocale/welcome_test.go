package clientlocale

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestWelcomeLanguagesMatchClientPicker(t *testing.T) {
	data, err := os.ReadFile("../../frontend/src/i18n/languages.json")
	if err != nil {
		t.Fatal(err)
	}
	var languages []struct{ ID string }
	if err := json.Unmarshal(data, &languages); err != nil {
		t.Fatal(err)
	}
	if len(languages) != len(welcomes) {
		t.Fatalf("client languages: %d, welcome languages: %d", len(languages), len(welcomes))
	}
	seen := make(map[string]bool)
	for _, language := range languages {
		if !Supported(language.ID) {
			t.Fatalf("missing welcome: %s", language.ID)
		}
		text := Welcome(language.ID)
		if seen[text] || strings.Count(text, "\n\n") != 3 || !strings.Contains(text, "Go Bot") || !strings.Contains(text, "**Gemini API CLI**") {
			t.Fatalf("incomplete or duplicate welcome for %s: %s", language.ID, text)
		}
		seen[text] = true
	}
}

func TestWelcomeDefaultsToEnglish(t *testing.T) {
	for _, language := range []string{"", "invalid", "__proto__"} {
		if Supported(language) || Welcome(language) != Welcome("en") {
			t.Fatalf("invalid language: %q", language)
		}
	}
}
