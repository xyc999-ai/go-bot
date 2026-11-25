package agentpreset

import (
	"fmt"
	"regexp"
	"strings"
)

var variableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// Render substitutes only registered names. Templates are not executable code.
func Render(text string, variables map[string]string) (string, error) {
	var out strings.Builder
	for {
		start := strings.Index(text, "{{")
		if start < 0 {
			if strings.Contains(text, "}}") {
				return "", fmt.Errorf("unmatched persona template closing delimiter")
			}
			out.WriteString(text)
			return out.String(), nil
		}
		out.WriteString(text[:start])
		rest := text[start+2:]
		end := strings.Index(rest, "}}")
		if end < 0 {
			return "", fmt.Errorf("unclosed persona template variable")
		}
		name := strings.TrimSpace(rest[:end])
		value, ok := variables[name]
		if !variableName.MatchString(name) || !ok {
			return "", fmt.Errorf("unknown or invalid persona variable %q", name)
		}
		out.WriteString(value)
		text = rest[end+2:]
	}
}
