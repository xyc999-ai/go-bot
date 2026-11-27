package host

import (
	"context"
	"encoding/json"
	"go-bot/internal/clientlocale"
	"go-bot/internal/hostruntime"
	"go-bot/internal/httpapi"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	sessionformat "go-bot/harness/session"
)

func TestMainWelcomeUsesChosenLanguageOnlyWhenCreated(t *testing.T) {
	for _, language := range []string{"en", "zh-CN", "zh-TW", "es", "ja", "pt-BR", "fr", "de", "ko"} {
		t.Run(language, func(t *testing.T) {
			root, id := t.TempDir(), uuid.NewString()
			claims := (&sessionformat.ProcessClaims{}).Acquire
			if err := ensureMainJournal(t.Context(), root, id, language, claims); err != nil {
				t.Fatal(err)
			}
			store := sessionformat.FileStore{Root: sessionLogRoot(root), ProjectCWD: defaultSessionWorkspace(root, id)}
			artifact, err := store.ReadArtifact(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			var event struct {
				Message struct{ Content []struct{ Text string } }
			}
			if err := json.Unmarshal(artifact.Events[1].Data, &event); err != nil {
				t.Fatal(err)
			}
			if len(event.Message.Content) != 1 || event.Message.Content[0].Text != clientlocale.Welcome(language) {
				t.Fatalf("wrong welcome for %s: %+v", language, event)
			}
			file, _ := store.Path(id)
			before, _ := os.ReadFile(file)
			if err := ensureMainJournal(t.Context(), root, id, "en", claims); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(file)
			if string(before) != string(after) {
				t.Fatal("a later language choice rewrote the existing main chat")
			}
		})
	}
}

func TestMainJournalWithoutModelIsAtomicAndReused(t *testing.T) {
	root, id := t.TempDir(), uuid.NewString()
	if err := ensureMainJournal(t.Context(), root, id, "zh-CN", (&sessionformat.ProcessClaims{}).Acquire); err != nil {
		t.Fatal(err)
	}
	store := sessionformat.FileStore{Root: sessionLogRoot(root), ProjectCWD: defaultSessionWorkspace(root, id)}
	artifact, err := store.ReadArtifact(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Header.CWD != filepath.Join(root, "workspaces", id) {
		t.Fatalf("cwd: %s", artifact.Header.CWD)
	}
	if len(artifact.Events) != 3 || artifact.Events[0].Type != "system/message" || artifact.Events[1].Type != "assistant/message" {
		t.Fatalf("seed: %+v", artifact.Events)
	}
	var data struct {
		Message struct {
			ID      string
			Content []struct{ Text string }
		}
	}
	if err := json.Unmarshal(artifact.Events[1].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Message.ID == "" || !strings.Contains(data.Message.Content[0].Text, "你好，我是 Go Bot") {
		t.Fatalf("welcome: %+v", data)
	}
	path, _ := store.Path(id)
	before, _ := os.ReadFile(path)
	if err := ensureMainJournal(t.Context(), root, id, "en", (&sessionformat.ProcessClaims{}).Acquire); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("login rewrote persisted welcome")
	}
}

func TestMainWelcomeAppearsInColdTranscriptWithoutAgent(t *testing.T) {
	root, id := t.TempDir(), uuid.NewString()
	host := &hostruntime.NativeHost{PrepareMain: func(ctx context.Context, id, locale string) error {
		return ensureMainJournal(ctx, root, id, locale, (&sessionformat.ProcessClaims{}).Acquire)
	}}
	if err := host.EnsureMainSession(t.Context(), id, "en"); err != nil {
		t.Fatal(err)
	}
	store := sessionformat.FileStore{Root: sessionLogRoot(root), ProjectCWD: defaultSessionWorkspace(root, id)}
	host.ReadSession = func(ctx context.Context, id string) (sessionformat.PhysicalArtifact, error) {
		return store.ReadArtifact(ctx, id)
	}
	handler := httpapi.NewNativeHostHTTP(host, httpapi.NativeWorkerHTTPOptions{})
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, httptest.NewRequest("GET", "/v1/sessions/"+id+"/transcript", nil))
	if out.Code != 200 || !strings.Contains(out.Body.String(), "Hi, I'm Go Bot") || !strings.Contains(out.Body.String(), "blocks") {
		t.Fatalf("cold transcript: %d %s", out.Code, out.Body)
	}
	if err := host.ArchiveSession(t.Context(), id); err != hostruntime.ErrMainSessionArchive {
		t.Fatalf("archive: %v", err)
	}
}
