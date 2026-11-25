package sessionformat

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

type TitleMessage struct {
	Seq  uint64 `json:"seq"`
	Text string `json:"text"`
}
type TitleSource struct {
	Kind     string          `json:"kind"`
	Provider string          `json:"provider,omitempty"`
	Model    *ModelSelection `json:"model,omitempty"`
}
type SessionTitle struct {
	Title       string      `json:"title"`
	MessageSeqs []uint64    `json:"messageSeqs"`
	Source      TitleSource `json:"source"`
}
type TitleState struct {
	Current *SessionTitle
	First   *TitleMessage
	Count   int
}

// Title metadata is log-only: neither event has a model-surface operation.
var titleDefinition = ProjectionDefinition[TitleState]{Key: "title", Version: 1,
	Init: func(Header, uint64) TitleState { return TitleState{} },
	Apply: func(s TitleState, e Event) (TitleState, error) {
		switch e.Type {
		case "session/title":
			var title SessionTitle
			if json.Unmarshal(e.Data, &title) == nil && title.Title != "" {
				s.Current = &title
			}
		case "user/message":
			var message struct {
				Source  struct{ Kind string }
				Content []struct{ Type, Text string }
			}
			if json.Unmarshal(e.Data, &message) != nil || message.Source.Kind != "user" {
				break
			}
			var parts []string
			for _, b := range message.Content {
				if b.Type == "text" {
					parts = append(parts, b.Text)
				}
			}
			text := strings.Join(parts, "\n")
			if NormalizeTitle(text, 80) != "" {
				s.Count++
				if s.First == nil {
					s.First = &TitleMessage{Seq: e.Seq, Text: text}
				}
			}
		}
		return s, nil
	},
	View: func(s TitleState) (json.RawMessage, error) {
		if s.Current == nil {
			return json.RawMessage(`null`), nil
		}
		return json.Marshal(s.Current.Title)
	},
}

func (s *LiveSession) TitleState() (TitleState, error) {
	h, err := RegisterProjection(s, &titleDefinition)
	if err != nil {
		return TitleState{}, err
	}
	return ReadProjection(h, LiveCut, func(v TitleState) TitleState {
		if v.Current != nil {
			c := *v.Current
			c.MessageSeqs = append([]uint64{}, c.MessageSeqs...)
			if c.Source.Model != nil {
				m := *c.Source.Model
				c.Source.Model = &m
			}
			v.Current = &c
		}
		if v.First != nil {
			c := *v.First
			v.First = &c
		}
		return v
	})
}

var titleCSI = regexp.MustCompile(`(?:\x1b\[|\x{009b})[0-?]*[ -/]*[@-~]`)
var titleESC = regexp.MustCompile(`\x1b[@-_]`)

func cleanTitle(input string) string {
	// OSC includes URLs/window titles and may have an unterminated tail.
	runes := []rune(input)
	var out strings.Builder
	for i := 0; i < len(runes); i++ {
		if runes[i] == 0x9d || runes[i] == 0x1b && i+1 < len(runes) && runes[i+1] == ']' {
			if runes[i] == 0x1b {
				i++
			}
			for i++; i < len(runes); i++ {
				if runes[i] == 7 {
					break
				}
				if runes[i] == 0x1b && i+1 < len(runes) && runes[i+1] == '\\' {
					i++
					break
				}
			}
			continue
		}
		out.WriteRune(runes[i])
	}
	text := titleESC.ReplaceAllString(titleCSI.ReplaceAllString(out.String(), ""), "")
	text = strings.Map(func(r rune) rune {
		if r < 32 && r != 9 && r != 10 && r != 13 || r >= 0x7f && r <= 0x9f || r == 0x200b || r == 0x200e || r == 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x2060 && r <= 0x2064 || r >= 0x2066 && r <= 0x206f || r == 0xfeff {
			return -1
		}
		return r
	}, text)
	return strings.Join(strings.Fields(text), " ")
}

func TruncateTitleUTF8(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	n := 0
	for _, r := range text {
		size := utf8.RuneLen(r)
		if n+size > limit {
			break
		}
		n += size
	}
	return text[:n]
}
func NormalizeTitle(text string, limit int) string {
	return strings.TrimSpace(TruncateTitleUTF8(cleanTitle(text), limit))
}
func FallbackTitle(text string) string {
	words := strings.Fields(cleanTitle(text))
	return strings.TrimSpace(TruncateTitleUTF8(strings.Join(words[:min(5, len(words))], " "), 40))
}
