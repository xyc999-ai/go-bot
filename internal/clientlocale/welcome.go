// Package clientlocale contains the supported languages for initial client-facing
// welcome messages. It does not select a model or set a conversation language.
package clientlocale

import (
	_ "embed"
	"encoding/json"
)

//go:embed welcome.json
var welcomeJSON []byte

var welcomes = func() map[string]string {
	var values map[string]string
	if err := json.Unmarshal(welcomeJSON, &values); err != nil {
		panic(err)
	}
	return values
}()

func Supported(language string) bool {
	_, ok := welcomes[language]
	return ok
}

func Welcome(language string) string {
	if value, ok := welcomes[language]; ok {
		return value
	}
	return welcomes["en"]
}
