// Package memory stores an account's long-term memory and reads it for prompts.
package memory

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unicode"
	"unicode/utf8"

	"go-bot/internal/durablefs"
	"go-bot/internal/localstate"
)

const separator = "\n§\n"

type Store struct {
	root string
	mu   sync.Mutex
}

type Operation struct {
	Action  string `json:"action"`
	OldText string `json:"old_text,omitempty"`
	Content string `json:"content,omitempty"`
}

type Result struct {
	Changed    bool   `json:"-"`
	Target     string `json:"target"`
	Entries    int    `json:"entries"`
	Characters int    `json:"characters"`
	Capacity   int    `json:"capacity"`
}

type memoryFile struct {
	name  string
	label string
	limit int
}

var userFile = memoryFile{"USER.md", "User preferences", 1375}
var agentFile = memoryFile{"MEMORY.md", "Agent notes", 2200}

func NewStore(root string) *Store { return &Store{root: root} }

// Instruction follows Hermes Agent's build_memory_guidance for both stores
// with no skill-management tool. The Host refreshes memory each model step.
const Instruction = `You have persistent memory across sessions. The current saved entries are included in your context at each model step. The memory tool schema defines what to save and how to update it. Keep memory for facts that apply across tasks, such as who the user is, stable environment facts, and standing conventions. Put task-specific procedures, pitfalls, preferences, and corrections in skills. Leave short-lived facts in conversation history. Memory has a strict character limit. When it fills, replace or consolidate stale entries to make room. Phrase entries as declarative facts, for example "User prefers concise replies". Imperative wording can be mistaken for a new instruction in later sessions.`

// Prompt reads the current files together, serializing with writes from other
// sessions in this Host. An invalid file is omitted without hiding valid notes
// from the other file.
func (s *Store) Prompt() (string, error) {
	sections, err := s.PromptSections()
	var prompt strings.Builder
	for _, section := range sections {
		prompt.WriteString(section.Text)
	}
	return prompt.String(), err
}

type PromptSection struct{ Name, Text string }

// PromptSections reads the same account stores while retaining their source
// identities for Agent composition. It does not create per-preset memory.
func (s *Store) PromptSections() ([]PromptSection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var sections []PromptSection
	var problems []error
	for _, file := range []memoryFile{userFile, agentFile} {
		entries, err := s.read(file)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", file.name, err))
			continue
		}
		if len(entries) != 0 {
			body := strings.Join(entries, separator)
			name := "memory:user"
			if file.name == agentFile.name {
				name = "memory:notes"
			}
			sections = append(sections, PromptSection{Name: name, Text: fmt.Sprintf("\n\n%s (%d/%d characters):\n%s", file.label, utf8.RuneCountInString(body), file.limit, body)})
		}
	}
	return sections, errors.Join(problems...)
}

func (s *Store) Apply(target string, ops []Operation) (Result, error) {
	var file memoryFile
	switch target {
	case "user":
		file = userFile
	case "memory":
		file = agentFile
	default:
		return Result{}, errors.New("memory target must be user or memory")
	}
	if err := ValidateOperations(ops); err != nil {
		return Result{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.read(file)
	if err != nil {
		return Result{}, fmt.Errorf("read %s: %w", file.name, err)
	}
	original := strings.Join(entries, separator)
	for i, op := range ops {
		entries, err = apply(entries, op)
		if err != nil {
			return Result{}, fmt.Errorf("memory operation %d: %w", i+1, err)
		}
	}
	body := strings.Join(entries, separator)
	chars := utf8.RuneCountInString(body)
	if chars > file.limit {
		return Result{}, fmt.Errorf("%s would use %d of %d characters (currently %d). Shorten or remove an existing entry in the same batch. Current entries:\n%s",
			file.name, chars, file.limit, utf8.RuneCountInString(original), original)
	}
	changed := body != original
	if changed {
		if err := durablefs.EnsureDirectory(s.root); err != nil {
			return Result{}, err
		}
		if body != "" {
			body += "\n"
		}
		if err := localstate.WritePrivate(filepath.Join(s.root, file.name), []byte(body)); err != nil {
			return Result{}, err
		}
	}
	return Result{Target: target, Entries: len(entries), Characters: chars, Capacity: file.limit, Changed: changed}, nil
}

func (s *Store) read(file memoryFile) ([]string, error) {
	f, err := os.OpenRoot(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Lstat(file.name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("memory must be a regular file")
	}
	input, err := f.OpenFile(file.name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() {
		return nil, errors.New("memory must be a regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(input, int64(file.limit*utf8.UTFMax+2)))
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(raw) || len(raw) > file.limit*utf8.UTFMax+1 {
		return nil, errors.New("invalid UTF-8 or oversized memory file")
	}
	body := strings.TrimSpace(strings.ReplaceAll(string(raw), "\r\n", "\n"))
	if body == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(body) > file.limit {
		return nil, errors.New("memory file exceeds its character limit")
	}
	entries := strings.Split(body, separator)
	for _, entry := range entries {
		if err := validateEntry(entry); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

func ValidateOperations(ops []Operation) error {
	if len(ops) == 0 || len(ops) > 20 {
		return errors.New("memory requires between 1 and 20 operations")
	}
	for i, op := range ops {
		var err error
		switch op.Action {
		case "add":
			if op.OldText != "" {
				err = errors.New("add does not accept old_text")
			} else {
				err = validateEntry(strings.TrimSpace(op.Content))
			}
		case "patch":
			if strings.TrimSpace(op.OldText) == "" {
				err = errors.New("patch requires old_text")
			} else {
				err = validateEntry(strings.TrimSpace(op.Content))
			}
		case "remove":
			if strings.TrimSpace(op.OldText) == "" || op.Content != "" {
				err = errors.New("remove requires old_text and accepts no content")
			}
		default:
			err = errors.New("memory action must be add, patch, or remove")
		}
		if err != nil {
			return fmt.Errorf("memory operation %d: %w", i+1, err)
		}
	}
	return nil
}

func validateEntry(entry string) error {
	if strings.TrimSpace(entry) == "" || !utf8.ValidString(entry) {
		return errors.New("memory content must be nonempty UTF-8")
	}
	for _, line := range strings.Split(entry, "\n") {
		if strings.TrimSpace(line) == "§" {
			return errors.New("memory entry contains a reserved delimiter")
		}
	}
	for _, r := range entry {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return errors.New("memory entry contains control characters")
		}
	}
	lower := strings.ToLower(entry)
	for _, marker := range []string{"ignore all previous instructions", "ignore previous instructions", "<|im_start|>", "<|im_end|>", "[inst]", "</system>", "<system>", "<system-reminder>"} {
		if strings.Contains(lower, marker) {
			return errors.New("memory entry contains a disallowed instruction marker")
		}
	}
	return nil
}

func apply(entries []string, op Operation) ([]string, error) {
	if op.Action == "add" {
		content := strings.TrimSpace(op.Content)
		for _, entry := range entries {
			if entry == content {
				return entries, nil
			}
		}
		return append(entries, content), nil
	}
	match := -1
	for i, entry := range entries {
		if strings.Contains(entry, op.OldText) {
			if match >= 0 {
				return nil, errors.New("old_text matches multiple entries")
			}
			match = i
		}
	}
	if match < 0 {
		return nil, errors.New("old_text did not match an entry")
	}
	if op.Action == "remove" {
		return append(entries[:match], entries[match+1:]...), nil
	}
	entry := entries[match]
	first := strings.Index(entry, op.OldText)
	if strings.Contains(entry[first+1:], op.OldText) {
		return nil, errors.New("old_text repeats in the entry")
	}
	updated := strings.TrimSpace(strings.Replace(entry, op.OldText, op.Content, 1))
	if err := validateEntry(updated); err != nil {
		return nil, err
	}
	entries[match] = updated
	return entries, nil
}
