package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go-bot/harness/assembly"
	"go-bot/harness/interaction"
	"go-bot/harness/session"
	"go-bot/internal/hostruntime"
)

func indexedHTTPFixture(t *testing.T) (*hostruntime.NativeHost, *sessionformat.LiveSession) {
	t.Helper()
	id := uuid.NewString()
	store := sessionformat.FileStore{Root: filepath.Join(t.TempDir(), "sessions"), ProjectCWD: "/workspace/indexed", Claim: func(context.Context, string) (func(), error) { return func() {}, nil }}
	header := sessionformat.Header{Type: "session", Version: 4, ID: id, CreatedAt: 1, CWD: store.ProjectCWD}
	journal, err := store.Create(t.Context(), header)
	if err != nil {
		t.Fatal(err)
	}
	live, err := sessionformat.NewLiveSession(header, journal)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Close() })
	digest := "sha256:" + strings.Repeat("a", 64)
	for turn := 1; turn <= 3; turn++ {
		for _, input := range []sessionformat.EventInput{
			{Type: "turn/start", Data: json.RawMessage(fmt.Sprintf(`{"turn":%d}`, turn))},
			{Type: "step/start", Data: json.RawMessage(fmt.Sprintf(`{"turn":%d,"step":1}`, turn))},
			{Type: "user/message", Data: json.RawMessage(fmt.Sprintf(`{"id":"u%d","role":"user","source":{"kind":"user"},"content":[{"type":"text","text":"message %d"},{"type":"file","attachment":{"attachmentId":"%s","name":"notes.txt","bytes":1}}]}`, turn, turn, digest)), SurfaceOp: json.RawMessage(`"append"`)},
			{Type: "step/end", Data: json.RawMessage(fmt.Sprintf(`{"turn":%d,"step":1}`, turn))},
			{Type: "turn/end", Data: json.RawMessage(fmt.Sprintf(`{"turn":%d,"reason":{"kind":"completed"}}`, turn))},
		} {
			if _, err := live.Append(input); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := live.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	reader := &sessionformat.FileHistoryReader{Configure: func(s *sessionformat.LiveSession) error {
		_, err := interaction.RegisterQuestionProjection(s)
		return err
	}}
	host := &hostruntime.NativeHost{
		ReadSession: func(context.Context, string) (sessionformat.PhysicalArtifact, error) {
			t.Error("indexed read loaded a full artifact through the Host")
			return sessionformat.PhysicalArtifact{}, fmt.Errorf("unexpected full read")
		},
		ReadHistory: func(ctx context.Context, sessionID string, q sessionformat.HistoryRequest) (sessionformat.HistoryWindow, error) {
			return reader.Read(ctx, store, sessionID, q)
		},
		ReadSessionHeader: func(ctx context.Context, sessionID string) (sessionformat.Header, error) {
			return store.ReadHeader(ctx, sessionID)
		},
		WorkspaceForSession: func(string) string { return store.ProjectCWD },
		Build: func(string, bool) (*hostruntime.NativeRuntime, error) {
			t.Error("history activated Agent")
			return nil, fmt.Errorf("unexpected activation")
		},
	}
	return host, live
}
func TestIndexedTranscriptAndEventsDoNotLoadFullHostArtifact(t *testing.T) {
	host, live := indexedHTTPFixture(t)
	handler := NewNativeHostHTTP(host, NativeWorkerHTTPOptions{})
	id := live.Header().ID
	get := func(suffix string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+id+suffix, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", suffix, w.Code, w.Body.String())
		}
		return w
	}
	first := get("/transcript?limit=1")
	var page chatTranscriptPage
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Before == nil || *page.Before != 10 || page.ThroughSeq == nil || *page.ThroughSeq != 14 || page.Permission == nil {
		t.Fatalf("bad first window: %+v", page)
	}
	older := get("/transcript?limit=1&before=10")
	var old chatTranscriptPage
	if err := json.Unmarshal(older.Body.Bytes(), &old); err != nil {
		t.Fatal(err)
	}
	if old.ThroughSeq != nil || old.Before == nil || *old.Before != 5 {
		t.Fatalf("bad previous window: %+v", old)
	}
	_ = get("/events?after=9&limit=2")
	_ = get("/permissions")
	_ = get("/questions")
	bad := httptest.NewRecorder()
	handler.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/v1/sessions/"+id+"/transcript?before=11", nil))
	if bad.Code != http.StatusBadRequest {
		t.Fatal("accepted cursor inside a turn")
	}
	if _, active := host.Session(id); active {
		t.Fatal("cold read activated Agent")
	}
}
func TestIndexedAttachmentLookupKeepsSessionAndNameMembership(t *testing.T) {
	host, live := indexedHTTPFixture(t)
	id := live.Header().ID
	digest := "sha256:" + strings.Repeat("a", 64)
	ref, found, err := sessionAttachmentReference(t.Context(), host, id, digest, "notes.txt")
	if err != nil || !found || ref.Type != "file" || ref.Attachment.Bytes != 1 {
		t.Fatalf("indexed attachment: %+v %v %v", ref, found, err)
	}
	if _, found, err := sessionAttachmentReference(t.Context(), host, id, digest, "other.txt"); err != nil || found {
		t.Fatal("wrong name gained attachment access")
	}
	if _, found, err := sessionAttachmentReference(t.Context(), host, id, "sha256:"+strings.Repeat("b", 64), ""); err != nil || found {
		t.Fatal("unrecorded digest gained attachment access")
	}
	if _, found, err := sessionAttachmentReference(t.Context(), host, uuid.NewString(), digest, ""); err != nil || found {
		t.Fatal("another session gained attachment access")
	}
}

type windowHTTPWorker struct {
	*nativeHTTPWorker
	opening sessionformat.HistoryWindow
}

func (w *windowHTTPWorker) HistoryWindow(context.Context, sessionformat.HistoryRequest) (sessionformat.HistoryWindow, error) {
	return w.opening, nil
}
func (w *windowHTTPWorker) DurableSessionEvents() ([]sessionformat.Event, error) {
	panic("stream copied entire history")
}
func TestChatStreamUsesOpeningQueueCutWithoutReapplyingOldSplices(t *testing.T) {
	worker := &windowHTTPWorker{nativeHTTPWorker: &nativeHTTPWorker{done: make(chan struct{}), events: []sessionformat.Event{
		{Type: "agent/inbox/spliced", Seq: 0, Data: json.RawMessage(`{"target":"next-turn","start":0,"inserted":[{"id":"q","role":"user","source":{"kind":"user"},"content":[{"type":"text","text":"queued"}]}]}`)},
		{Type: "agent/inbox/spliced", Seq: 1, Data: json.RawMessage(`{"target":"next-turn","start":0,"removedCount":1,"inserted":[]}`)},
	}}, opening: sessionformat.HistoryWindow{Count: 2, Inbox: sessionformat.InboxState{}}}
	server := httptest.NewServer(NewNativeWorkerHTTP(worker, assembly.NativeRuntimeIdentity{SessionID: "root", Workspace: t.TempDir()}, NativeWorkerHTTPOptions{}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/sessions/root/stream?view=chat&after=0", nil)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	queueEvent := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "event: queue.state" {
			queueEvent = true
		}
		if queueEvent && strings.HasPrefix(line, "data: ") {
			if !strings.Contains(line, `"queue":[]`) {
				t.Fatalf("wrong opening queue: %s", line)
			}
			return
		}
	}
	t.Fatalf("no queue snapshot: %v status=%d", scanner.Err(), response.StatusCode)
}
