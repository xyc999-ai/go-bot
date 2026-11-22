package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"testing"
	"time"

	"go-bot/harness/agent"
	"go-bot/harness/assembly"
	"go-bot/harness/attachments"
	"go-bot/harness/goal"
	"go-bot/harness/interaction"
	"go-bot/harness/plan"
	"go-bot/harness/session"
	"go-bot/internal/hostruntime"
	"go-bot/internal/testsupport/session"
)

type nativeHTTPWorker struct {
	messages   [][]byte
	events     []sessionformat.Event
	stream     chan agent.NativeAssistantStreamFrame
	baseline   agent.NativeAssistantStreamBaseline
	done       chan struct{}
	planActive bool
	goalView   *goal.NativeView
	compactErr error
}

type nativeImageObjects struct {
	objects map[string][]byte
}

func (o *nativeImageObjects) PutIfAbsent(_ context.Context, key, _ string, data []byte) error {
	if o.objects == nil {
		o.objects = make(map[string][]byte)
	}
	if _, found := o.objects[key]; !found {
		o.objects[key] = append([]byte(nil), data...)
	}
	return nil
}

func (o *nativeImageObjects) Read(_ context.Context, key string) (io.ReadCloser, error) {
	data, found := o.objects[key]
	if !found {
		return nil, errors.New("image object missing")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func nativeImageUpload(t *testing.T, form *multipart.Writer, name string, data []byte) {
	t.Helper()
	part, err := form.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="images"; filename="` + name + `"`},
		"Content-Type":        {"image/png"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
}

func nativeTinyPNG(t *testing.T) []byte {
	t.Helper()
	pixels := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	pixels.Set(0, 0, color.NRGBA{R: 255, A: 255})
	pixels.Set(1, 0, color.NRGBA{G: 255, A: 255})
	var output bytes.Buffer
	if err := png.Encode(&output, pixels); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func (w *nativeHTTPWorker) SubmitMessage(_ context.Context, message []byte) (string, error) {
	w.messages = append(w.messages, append([]byte(nil), message...))
	var record struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(message, &record)
	return record.ID, nil
}
func (w *nativeHTTPWorker) CompactNow(context.Context) (bool, error) {
	return w.compactErr == nil, w.compactErr
}
func (*nativeHTTPWorker) CancelTurn(_ context.Context, turnID string) (string, error) {
	if turnID == "1" {
		return "running", nil
	}
	return "", hostruntime.ErrNativeTurnNotFound
}
func (*nativeHTTPWorker) UpdateQueuedMessage(context.Context, string, bool) error {
	return agent.ErrQueuedMessageNotFound
}

func (w *nativeHTTPWorker) DurableSessionEvents() ([]sessionformat.Event, error) {
	return w.events, nil
}

func (w *nativeHTTPWorker) DurableEventsAfter(after *uint64, limit int) ([]sessionformat.Event, error) {
	var result []sessionformat.Event
	for _, event := range w.events {
		if after == nil || event.Seq > *after {
			result = append(result, event)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}
func (w *nativeHTTPWorker) SubscribeAssistantStreamSnapshot() (agent.NativeAssistantStreamBaseline, <-chan agent.NativeAssistantStreamFrame, func(), error) {
	if w.stream == nil {
		w.stream = make(chan agent.NativeAssistantStreamFrame, 32)
	}
	return w.baseline, w.stream, func() {}, nil
}
func (w *nativeHTTPWorker) Done() <-chan struct{} { return w.done }
func (*nativeHTTPWorker) LastError() error        { return nil }
func (w *nativeHTTPWorker) GetGoal() (goal.NativeView, bool, error) {
	if w.goalView == nil {
		return goal.NativeView{}, false, nil
	}
	return *w.goalView, true, nil
}
func (w *nativeHTTPWorker) Usage() (sessionformat.UsageSnapshot, error) {
	return sessionformat.ProjectUsage(w.events)
}
func (w *nativeHTTPWorker) ResumeGoal(_ context.Context, ref sessionformat.GoalRef) (goal.NativeView, error) {
	if w.goalView == nil {
		return goal.NativeView{}, goal.ErrNotFound
	}
	if ref.ID != w.goalView.Goal.ID || ref.Revision != w.goalView.Goal.Revision {
		return goal.NativeView{}, goal.ErrStaleRevision
	}
	if w.goalView.Goal.Phase == "complete" {
		return goal.NativeView{}, goal.ErrInvalidTransition
	}
	w.goalView.Goal.Revision++
	w.goalView.Goal.Phase = "active"
	w.goalView.Activation = "armed"
	return *w.goalView, nil
}
func (w *nativeHTTPWorker) GetPlan() (plan.View, error) { return plan.View{Active: w.planActive}, nil }
func (w *nativeHTTPWorker) SetPlan(_ context.Context, active bool) (plan.View, error) {
	w.planActive = active
	return plan.View{Active: active}, nil
}
func (*nativeHTTPWorker) PendingQuestions() ([]interaction.Pending, error) { return nil, nil }
func (*nativeHTTPWorker) AnswerQuestion(context.Context, string, interaction.Answer) error {
	return interaction.ErrNotPending
}
func (*nativeHTTPWorker) CancelQuestion(context.Context, string) error {
	return interaction.ErrNotPending
}
func (w *nativeHTTPWorker) Close() error {
	select {
	case <-w.done:
	default:
		close(w.done)
	}
	return nil
}

func TestNativeWorkerHTTPPlanAndQuestionEndpoints(t *testing.T) {
	worker := &nativeHTTPWorker{done: make(chan struct{})}
	handler := NewNativeWorkerHTTP(worker, assembly.NativeRuntimeIdentity{SessionID: "root", Workspace: t.TempDir()}, NativeWorkerHTTPOptions{})
	for _, check := range []struct {
		method, path, body string
		status             int
		contains           string
	}{
		{http.MethodGet, "/v1/sessions/root/plan", "", http.StatusOK, `"active":false`},
		{http.MethodPost, "/v1/sessions/root/plan", `{"active":true}`, http.StatusOK, `"active":true`},
		{http.MethodGet, "/v1/sessions/root/questions", "", http.StatusOK, `"questions":null`},
		{http.MethodGet, "/v1/sessions/root/usage", "", http.StatusOK, `"stepsStarted":0`},
		{http.MethodPost, "/v1/sessions/root/questions/missing/answer", `{"answers":[]}`, http.StatusNotFound, `question_not_pending`},
		{http.MethodPost, "/v1/sessions/root/questions/missing/cancel", `{}`, http.StatusNotFound, `question_not_pending`},
	} {
		request := httptest.NewRequest(check.method, check.path, strings.NewReader(check.body))
		if check.method == http.MethodPost {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != check.status || !strings.Contains(response.Body.String(), check.contains) {
			t.Errorf("%s %s = %d %s", check.method, check.path, response.Code, response.Body.String())
		}
	}
}

func TestNativeWorkerHTTPManualCompactEndpoint(t *testing.T) {
	worker := &nativeHTTPWorker{done: make(chan struct{})}
	handler := NewNativeWorkerHTTP(worker, assembly.NativeRuntimeIdentity{SessionID: "root", Workspace: t.TempDir()}, NativeWorkerHTTPOptions{})
	call := func(path string, wantStatus int, wantBody string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != wantStatus || !strings.Contains(response.Body.String(), wantBody) {
			t.Fatalf("compact %s = %d %s; want %d containing %q", path, response.Code, response.Body.String(), wantStatus, wantBody)
		}
	}
	call("/v1/sessions/root/commands/compact", http.StatusOK, `"compacted":true`)
	worker.compactErr = hostruntime.ErrNativeCompactionBusy
	call("/v1/sessions/root/commands/compact", http.StatusConflict, `"agent_busy"`)
	call("/v1/sessions/other/commands/compact", http.StatusNotFound, "")
}

func TestNativeWorkerHTTPGoalEndpoints(t *testing.T) {
	worker := &nativeHTTPWorker{done: make(chan struct{})}
	handler := NewNativeWorkerHTTP(worker, assembly.NativeRuntimeIdentity{SessionID: "root", Workspace: t.TempDir()}, NativeWorkerHTTPOptions{})
	call := func(method, path, body string, wantStatus int, wantBody string) {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if method == http.MethodPost {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != wantStatus || !strings.Contains(response.Body.String(), wantBody) {
			t.Errorf("%s %s = %d %s; want %d containing %q", method, path, response.Code, response.Body.String(), wantStatus, wantBody)
		}
	}
	base := "/v1/sessions/root/goal"
	call(http.MethodGet, base, "", http.StatusOK, `"goal":null`)
	call(http.MethodPost, base+"/resume", `{"goalId":"g","revision":1}`, http.StatusNotFound, `goal_not_found`)
	worker.goalView = &goal.NativeView{Goal: sessionformat.GoalSnapshot{ID: "g", Revision: 2, Phase: "paused"}, Activation: "disarmed"}
	call(http.MethodGet, base, "", http.StatusOK, `"phase":"paused"`)
	call(http.MethodPost, base+"/resume", `{"goalId":"g","revision":0}`, http.StatusBadRequest, `invalid_goal_resume_request`)
	call(http.MethodPost, base+"/resume", `{"goalId":"g","revision":1}`, http.StatusConflict, `goal_revision_stale`)
	call(http.MethodPost, base+"/resume", `{"goalId":"g","revision":2}`, http.StatusOK, `"activation":"armed"`)
	worker.goalView.Goal.Phase = "complete"
	call(http.MethodPost, base+"/resume", `{"goalId":"g","revision":3}`, http.StatusConflict, `goal_invalid_transition`)
}

func TestNativeWorkerHTTPListsAndDownloadsWorkspaceFiles(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(workspace+"/answer.txt", []byte("done"), 0o600); err != nil {
		t.Fatal(err)
	}
	worker := &nativeHTTPWorker{done: make(chan struct{})}
	handler := NewNativeWorkerHTTP(worker, assembly.NativeRuntimeIdentity{SessionID: "root", Workspace: workspace}, NativeWorkerHTTPOptions{})
	call := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	base := "/v1/sessions/root/files"
	if response := call(base); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "answer.txt") {
		t.Fatalf("native workspace list = %d %s", response.Code, response.Body.String())
	}
	if response := call(base + "/content?path=answer.txt"); response.Code != http.StatusOK || response.Body.String() != "done" {
		t.Fatalf("native download route = %d %s", response.Code, response.Body.String())
	}
	if response := call(base + "/content?path=../outside.txt"); response.Code != http.StatusBadRequest {
		t.Fatal("download escaped workspace")
	}
}

func TestNativeWorkerHTTPAdmitsMultipartAsDurableFileBlocks(t *testing.T) {
	workspace := t.TempDir()
	worker := &nativeHTTPWorker{done: make(chan struct{})}
	identity := assembly.NativeRuntimeIdentity{UserID: "alice", SessionID: "root", Workspace: workspace}
	handler := NewNativeWorkerHTTP(worker, identity, NativeWorkerHTTPOptions{Files: attachments.FileStore{Root: workspace}})
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("content", "Read the attached report"); err != nil {
		t.Fatal(err)
	}
	file, err := form.CreateFormFile("files", `C:\private\report.txt`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("verbatim file bytes")); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/sessions/root/messages", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || len(worker.messages) != 1 {
		t.Fatalf("multipart admission = %d %s", response.Code, response.Body.String())
	}
	var message struct {
		ID      string `json:"id"`
		Role    string `json:"role"`
		Content []struct {
			Type       string              `json:"type"`
			Text       string              `json:"text"`
			Attachment attachments.FileRef `json:"attachment"`
		} `json:"content"`
	}
	if err := json.Unmarshal(worker.messages[0], &message); err != nil {
		t.Fatal(err)
	}
	if message.ID == "" || message.Role != "user" || len(message.Content) != 2 || message.Content[1].Text != "Read the attached report" || message.Content[0].Type != "file" {
		t.Fatalf("invalid native file message: %s", worker.messages[0])
	}
	ref := message.Content[0].Attachment
	if ref.Name != "report.txt" || !strings.HasPrefix(ref.ID, "sha256:") || ref.Bytes != int64(len("verbatim file bytes")) {
		t.Fatalf("invalid native file ref: %+v", ref)
	}
	saved, err := os.ReadFile((attachments.FileStore{Root: workspace}).Path(ref))
	if err != nil || string(saved) != "verbatim file bytes" {
		t.Fatalf("native file was not committed before inbox: %q, %v", saved, err)
	}
	if strings.Contains(string(worker.messages[0]), "/workspace/") || strings.Contains(string(worker.messages[0]), "C:\\private") {
		t.Fatalf("native file block leaked a path: %s", worker.messages[0])
	}
	var reply map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil || reply["messageId"] != message.ID || reply["turnId"] != nil {
		t.Fatalf("native HTTP admission reply = %v, %v", reply, err)
	}
}

func TestNativeWorkerHTTPPreservesClientTimeZoneOnSameMessage(t *testing.T) {
	for _, check := range []struct {
		multipartInput bool
		zone           string
		canonical      string
	}{
		{zone: "Asia/Shanghai", canonical: "Asia/Shanghai"},
		{multipartInput: true, zone: "Asia/Shanghai", canonical: "Asia/Shanghai"},
		{zone: "Asia/Chongqing", canonical: "Asia/Shanghai"},
	} {
		workspace := t.TempDir()
		worker := &nativeHTTPWorker{done: make(chan struct{})}
		handler := NewNativeWorkerHTTP(worker, assembly.NativeRuntimeIdentity{SessionID: "root", Workspace: workspace}, NativeWorkerHTTPOptions{})
		var body bytes.Buffer
		contentType := "application/json"
		if check.multipartInput {
			form := multipart.NewWriter(&body)
			if err := form.WriteField("content", "remind me tomorrow"); err != nil {
				t.Fatal(err)
			}
			if err := form.Close(); err != nil {
				t.Fatal(err)
			}
			contentType = form.FormDataContentType()
		} else {
			body.WriteString(`{"content":"remind me tomorrow"}`)
		}
		request := httptest.NewRequest(http.MethodPost, "/v1/sessions/root/messages", &body)
		request.Header.Set("Content-Type", contentType)
		request.Header.Set("X-Client-Time-Zone", check.zone)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted || len(worker.messages) != 1 {
			t.Fatalf("time zone admission = %d %s", response.Code, response.Body.String())
		}
		var message struct {
			ID     string `json:"id"`
			Source struct {
				Kind           string `json:"kind"`
				RPCID          string `json:"rpcId"`
				ClientTimeZone string `json:"clientTimeZone"`
			} `json:"source"`
		}
		if err := json.Unmarshal(worker.messages[0], &message); err != nil {
			t.Fatal(err)
		}
		if message.ID == "" || message.Source.Kind != "user" || message.Source.RPCID != message.ID || message.Source.ClientTimeZone != check.canonical {
			t.Fatalf("time zone provenance = %s", worker.messages[0])
		}
	}
}

func TestNativeWorkerHTTPRejectsInvalidClientTimeZoneBeforeAdmission(t *testing.T) {
	worker := &nativeHTTPWorker{done: make(chan struct{})}
	handler := NewNativeWorkerHTTP(worker, assembly.NativeRuntimeIdentity{SessionID: "root", Workspace: t.TempDir()}, NativeWorkerHTTPOptions{})
	request := httptest.NewRequest(http.MethodPost, "/v1/sessions/root/messages", strings.NewReader(`{"content":"remind me tomorrow"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Client-Time-Zone", "../other-user")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_client_time_zone") || len(worker.messages) != 0 {
		t.Fatalf("invalid zone admission = %d %s, inbox=%d", response.Code, response.Body.String(), len(worker.messages))
	}
}

func TestNativeWorkerHTTPAdmitsMixedImageAndFileBlocks(t *testing.T) {
	workspace := t.TempDir()
	objects := &nativeImageObjects{}
	imageStore := attachments.Store{API: objects, Prefix: "agents/"}
	worker := &nativeHTTPWorker{done: make(chan struct{})}
	handler := NewNativeWorkerHTTP(worker, assembly.NativeRuntimeIdentity{SessionID: "root", Workspace: workspace}, NativeWorkerHTTPOptions{Images: imageStore, Files: attachments.FileStore{Root: workspace}, ImageInputEnabled: true})
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("content", "Compare these attachments"); err != nil {
		t.Fatal(err)
	}
	imageSource := nativeTinyPNG(t)
	nativeImageUpload(t, form, "chart.png", imageSource)
	file, err := form.CreateFormFile("files", "numbers.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("year,value\n2026,1\n")); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/sessions/root/messages", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || len(worker.messages) != 1 {
		t.Fatalf("mixed admission = %d %s", response.Code, response.Body.String())
	}
	var message struct {
		Content []struct {
			Type       string          `json:"type"`
			Text       string          `json:"text"`
			Attachment json.RawMessage `json:"attachment"`
		} `json:"content"`
	}
	if err := json.Unmarshal(worker.messages[0], &message); err != nil {
		t.Fatal(err)
	}
	if len(message.Content) != 3 || message.Content[0].Type != "image" || message.Content[1].Type != "file" || message.Content[2].Type != "text" || message.Content[2].Text != "Compare these attachments" {
		t.Fatalf("multipart order was not preserved: %s", worker.messages[0])
	}
	var imageRef struct {
		AttachmentID string `json:"attachmentId"`
		MediaType    string `json:"mediaType"`
		Bytes        int64  `json:"bytes"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		Name         string `json:"name"`
	}
	if err := json.Unmarshal(message.Content[0].Attachment, &imageRef); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(imageRef.AttachmentID, "sha256:") || imageRef.MediaType != "image/png" || imageRef.Bytes != int64(len(imageSource)) || imageRef.Width != 2 || imageRef.Height != 1 || imageRef.Name != "chart.png" {
		t.Fatalf("invalid v4 image reference: %+v", imageRef)
	}
	storedImage, err := imageStore.LoadImage(context.Background(), session.ImageAttachment{ID: imageRef.AttachmentID, MIMEType: imageRef.MediaType, Bytes: imageRef.Bytes, Width: imageRef.Width, Height: imageRef.Height})
	if err != nil {
		t.Fatalf("image was not durable before inbox admission: %v", err)
	}
	if !bytes.Equal(storedImage, imageSource) {
		t.Fatal("clean image changed before inbox admission")
	}
	if strings.Contains(string(worker.messages[0]), "readPath") || strings.Contains(string(worker.messages[0]), "originalDimensions") {
		t.Fatalf("image block contained non-upstream metadata: %s", worker.messages[0])
	}
	if !strings.Contains(response.Body.String(), `"images":[{`) || !strings.Contains(response.Body.String(), `"files":[{`) {
		t.Fatalf("admission omitted image or file refs: %s", response.Body.String())
	}
}

func TestNativeWorkerHTTPRejectsUnavailableOrInvalidImagesBeforeInbox(t *testing.T) {
	for _, check := range []struct {
		name    string
		enabled bool
		data    []byte
		want    string
	}{
		{name: "capability disabled", data: nativeTinyPNG(t), want: "image_input_unavailable"},
		{name: "invalid pixels", enabled: true, data: []byte("not an image"), want: "invalid_image"},
	} {
		t.Run(check.name, func(t *testing.T) {
			workspace := t.TempDir()
			worker := &nativeHTTPWorker{done: make(chan struct{})}
			handler := NewNativeWorkerHTTP(worker, assembly.NativeRuntimeIdentity{SessionID: "root", Workspace: workspace}, NativeWorkerHTTPOptions{Images: attachments.Store{API: &nativeImageObjects{}}, ImageInputEnabled: check.enabled})
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			if err := form.WriteField("content", "Inspect the image"); err != nil {
				t.Fatal(err)
			}
			nativeImageUpload(t, form, "image.png", check.data)
			if err := form.Close(); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/sessions/root/messages", &body)
			request.Header.Set("Content-Type", form.FormDataContentType())
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), check.want) || len(worker.messages) != 0 {
				t.Fatalf("rejection = %d %s, inbox=%d", response.Code, response.Body.String(), len(worker.messages))
			}
		})
	}
}

func TestNativeWorkerHTTPUsesZeroBasedEventCursorAndFixedIdentity(t *testing.T) {
	worker := &nativeHTTPWorker{done: make(chan struct{}), events: []sessionformat.Event{
		{Type: "turn/start", Seq: 0, Time: 1, Data: json.RawMessage(`{"turn":1}`)},
		{Type: "turn/end", Seq: 1, Time: 2, Data: json.RawMessage(`{"turn":1,"reason":{"kind":"completed"}}`)},
	}}
	handler := NewNativeWorkerHTTP(worker, assembly.NativeRuntimeIdentity{UserID: "alice", SessionID: "root", Workspace: t.TempDir()}, NativeWorkerHTTPOptions{})
	for path, want := range map[string]int{
		"/v1/sessions/root/turns/1/cancel": http.StatusAccepted,
		"/v1/sessions/root/turns/2/cancel": http.StatusNotFound,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != want {
			t.Errorf("POST %s = %d, want %d: %s", path, response.Code, want, response.Body.String())
		}
	}
	for path, expected := range map[string]int{
		"/v1/sessions/root/events?limit=1":       http.StatusOK,
		"/v1/sessions/root/events?after=0":       http.StatusOK,
		"/v1/sessions/another/events?limit=1":    http.StatusNotFound,
		"/v1/sessions/root/events?after=invalid": http.StatusBadRequest,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != expected {
			t.Errorf("GET %s = %d, want %d", path, response.Code, expected)
		}
		if path == "/v1/sessions/root/events?limit=1" && !strings.Contains(response.Body.String(), `"seq":0`) {
			t.Errorf("first native event page omitted seq 0: %s", response.Body.String())
		}
		if path == "/v1/sessions/root/events?after=0" && !strings.Contains(response.Body.String(), `"seq":1`) {
			t.Errorf("native cursor did not advance: %s", response.Body.String())
		}
	}
	close(worker.done)
	stream := httptest.NewRecorder()
	handler.ServeHTTP(stream, httptest.NewRequest(http.MethodGet, "/v1/sessions/root/stream", nil))
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "id: 0\n") || !strings.Contains(stream.Body.String(), "id: 1\n") {
		t.Fatalf("native stream omitted zero-based events: %d %s", stream.Code, stream.Body.String())
	}
	resume := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/sessions/root/stream", nil)
	request.Header.Set("Last-Event-ID", "0")
	handler.ServeHTTP(resume, request)
	if strings.Contains(resume.Body.String(), "id: 0\n") || !strings.Contains(resume.Body.String(), "id: 1\n") {
		t.Fatalf("native stream reconnect cursor is wrong: %s", resume.Body.String())
	}
}

func TestNativeWorkerHTTPDoesNotExposeSystemOrModelRequestFacts(t *testing.T) {
	worker := &nativeHTTPWorker{done: make(chan struct{}), events: []sessionformat.Event{
		{Type: "system/message", Seq: 0, Time: 1, Data: json.RawMessage(`{"message":{"role":"system","content":[{"type":"text","text":"private prompt"}]}}`)},
		{Type: "request/header", Seq: 1, Time: 2, Data: json.RawMessage(`{"secret":"private request"}`)},
		{Type: "user/message", Seq: 2, Time: 3, Data: json.RawMessage(`{"id":"u","role":"user","source":{"kind":"user"},"content":[{"type":"text","text":"hello"}]}`)},
	}}
	handler := NewNativeWorkerHTTP(worker, assembly.NativeRuntimeIdentity{UserID: "alice", SessionID: "root", Workspace: t.TempDir()}, NativeWorkerHTTPOptions{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/sessions/root/events", nil))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "private") || !strings.Contains(response.Body.String(), `"seq":2`) || !strings.Contains(response.Body.String(), `"nextCursor":2`) {
		t.Fatalf("native public event filter failed: %d %s", response.Code, response.Body.String())
	}
}

func TestNativePublicProjectionOmitsReasoningAndSyntheticUserCheckpoint(t *testing.T) {
	checkpoint := sessionformat.Event{Type: "user/message", Data: json.RawMessage(`{"id":"checkpoint","role":"user","source":{"kind":"compaction"},"content":[{"type":"text","text":"internal summary"}]}`)}
	if _, ok := publicNativeEvent(checkpoint); ok {
		t.Fatal("synthetic compaction checkpoint was exposed as a user message")
	}
	assistant := sessionformat.Event{Type: "assistant/message", Data: json.RawMessage(`{"turn":1,"step":1,"interrupted":true,"message":{"id":"a","role":"assistant","content":[{"type":"reasoning","text":"private reasoning"},{"type":"text","text":"visible answer"}]},"usage":{"inputTokens":9},"stream":[{"type":"chunk"}]}`)}
	projected, ok := publicNativeEvent(assistant)
	if !ok || strings.Contains(string(projected.Data), "private reasoning") || strings.Contains(string(projected.Data), "inputTokens") || strings.Contains(string(projected.Data), "stream") || !strings.Contains(string(projected.Data), "visible answer") || !strings.Contains(string(projected.Data), `"interrupted":true`) {
		t.Fatalf("assistant projection leaked internals: %s", projected.Data)
	}
}

func TestNativePublicAssistantFramesRedactUsageAndProviderFailureDetails(t *testing.T) {
	for _, raw := range []agent.ModelStreamChunk{
		{"type": "usage", "usage": map[string]any{"inputTokens": 20, "reasoningTokens": 12}},
		{"type": "finish", "reason": map[string]any{"kind": "error", "failure": map[string]any{"message": "private provider detail"}}},
	} {
		projected, ok := publicNativeAssistantFrame(agent.NativeAssistantStreamFrame{Type: "chunk", Chunk: raw})
		encoded, err := json.Marshal(projected)
		if !ok || err != nil || strings.Contains(string(encoded), "inputTokens") ||
			strings.Contains(string(encoded), "private provider detail") || !strings.Contains(string(encoded), `"type":"`) {
			t.Fatalf("public frame leaked internal details: %s, %v", encoded, err)
		}
	}
}

func TestNativeWorkerHTTPDropsOrphanedAssistantChunksAfterReconnect(t *testing.T) {
	stream := make(chan agent.NativeAssistantStreamFrame, 6)
	four, five, zero := 4, 5, 0
	stream <- agent.NativeAssistantStreamFrame{Type: "chunk", AttemptID: "old-attempt", Revision: 5, Index: &four,
		Chunk: agent.ModelStreamChunk{"type": "text-delta", "index": 0, "text": "orphaned"}}
	stream <- agent.NativeAssistantStreamFrame{Type: "end", AttemptID: "old-attempt", Revision: 6, Index: &five,
		Outcome: map[string]any{"kind": "committed", "eventType": "assistant/message", "seq": uint64(3)}}
	stream <- agent.NativeAssistantStreamFrame{Type: "start", AttemptID: "new-attempt", Revision: 7, Turn: 2, Step: 1}
	stream <- agent.NativeAssistantStreamFrame{Type: "chunk", AttemptID: "new-attempt", Revision: 8, Index: &zero,
		Chunk: agent.ModelStreamChunk{"type": "text-delta", "index": 0, "text": "visible"}}
	worker := &nativeHTTPWorker{done: make(chan struct{}), stream: stream}
	server := httptest.NewServer(NewNativeWorkerHTTP(worker,
		assembly.NativeRuntimeIdentity{UserID: "alice", SessionID: "root", Workspace: t.TempDir()}, NativeWorkerHTTPOptions{}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/sessions/root/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	var seen []string
	for len(seen) < 3 {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("reconnected stream ended before the new attempt: %v, %v", seen, err)
		}
		if strings.HasPrefix(line, "data: ") {
			seen = append(seen, line)
		}
	}
	if !strings.Contains(seen[0], `"revision":0`) || !strings.Contains(seen[1], `"attemptId":"new-attempt"`) ||
		!strings.Contains(seen[1], `"type":"start"`) || !strings.Contains(seen[2], `"text":"visible"`) ||
		strings.Contains(strings.Join(seen, ""), "orphaned") {
		t.Fatalf("reconnected stream exposed orphaned attempt: %v", seen)
	}
}

func TestNativeWorkerHTTPStreamsLiveAssistantFramesWithoutLeakingReasoning(t *testing.T) {
	stream := make(chan agent.NativeAssistantStreamFrame, 4)
	stream <- agent.NativeAssistantStreamFrame{Type: "start", AttemptID: "root:1", Revision: 1, Turn: 1, Step: 1}
	zero, one := 0, 1
	stream <- agent.NativeAssistantStreamFrame{Type: "chunk", AttemptID: "root:1", Revision: 2, Index: &zero,
		Chunk: agent.ModelStreamChunk{"type": "reasoning-delta", "index": 0, "text": "private thought"}}
	stream <- agent.NativeAssistantStreamFrame{Type: "chunk", AttemptID: "root:1", Revision: 3, Index: &one,
		Chunk: agent.ModelStreamChunk{"type": "text-delta", "index": 1, "text": "visible"}}
	worker := &nativeHTTPWorker{done: make(chan struct{}), stream: stream}
	server := httptest.NewServer(NewNativeWorkerHTTP(worker,
		assembly.NativeRuntimeIdentity{UserID: "alice", SessionID: "root", Workspace: t.TempDir()}, NativeWorkerHTTPOptions{}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/sessions/root/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	var seen []string
	for len(seen) < 4 {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("live stream ended before two visible frames: %v, %v", seen, err)
		}
		if strings.HasPrefix(line, "data: ") {
			seen = append(seen, line)
		}
	}
	if !strings.Contains(seen[0], `"revision":0`) || !strings.Contains(seen[1], `"type":"start"`) ||
		!strings.Contains(seen[2], `"type":"redacted"`) || !strings.Contains(seen[3], `"text":"visible"`) ||
		strings.Contains(strings.Join(seen, ""), "private thought") {
		t.Fatalf("incorrect public live stream: %v", seen)
	}
}

func TestNativeWorkerHTTPReplaysActiveAssistantBaselineWithoutReasoningText(t *testing.T) {
	two := 2
	worker := &nativeHTTPWorker{done: make(chan struct{}),
		baseline: agent.NativeAssistantStreamBaseline{Revision: 4, ActiveAttempt: &agent.NativeAssistantStreamAttemptBaseline{
			AttemptID: "root:1", StartedAfterSeq: 7, Turn: 1, Step: 1, NextIndex: 2,
			Stream: []any{map[string]any{"type": "reasoning-chunks", "time0": float64(10), "index": float64(0),
				"dt": []any{}, "texts": []any{"private thought"}},
				map[string]any{"type": "text-chunks", "time0": float64(11), "index": float64(1),
					"dt": []any{}, "texts": []any{"visible prefix"}}},
		}},
		stream: make(chan agent.NativeAssistantStreamFrame, 1),
	}
	worker.stream <- agent.NativeAssistantStreamFrame{Type: "chunk", AttemptID: "root:1", Revision: 5, Index: &two,
		Chunk: agent.ModelStreamChunk{"type": "text-delta", "index": 1, "text": " suffix"}}
	server := httptest.NewServer(NewNativeWorkerHTTP(worker,
		assembly.NativeRuntimeIdentity{UserID: "alice", SessionID: "root", Workspace: t.TempDir()}, NativeWorkerHTTPOptions{}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/sessions/root/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	var seen []string
	for len(seen) < 2 {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("active baseline ended prematurely: %v, %v", seen, err)
		}
		if strings.HasPrefix(line, "data: ") {
			seen = append(seen, line)
		}
	}
	if !strings.Contains(seen[0], `"nextIndex":2`) || !strings.Contains(seen[0], `"visible prefix"`) ||
		strings.Contains(seen[0], "private thought") || !strings.Contains(seen[1], `"text":" suffix"`) {
		t.Fatalf("active baseline or suffix differs: %v", seen)
	}
}
