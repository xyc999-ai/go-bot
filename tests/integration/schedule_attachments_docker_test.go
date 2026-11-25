package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go-bot/harness/attachments"
	"go-bot/harness/schedule"
	"go-bot/internal/config"
	"go-bot/internal/modelsettings"
	dockerapi "go-bot/thirdparty/docker"
)

func TestDockerRestoresScheduleAndHostAttachments(t *testing.T) {
	if os.Getenv("RUN_DOCKER_INTEGRATION") != "1" {
		t.Skip("requires the built Host image")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var delivered atomic.Bool
	var sawImage atomic.Bool
	var sawFile atomic.Bool
	var usedScheduledModel atomic.Bool
	const uploadedText = "verbatim input"
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(uploadedText)))
	attachmentPath := "/root/harness/.dsh/attachments/v1/files/" + digest[:2] + "/" + digest + "/reference.txt"
	model := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fixtureTitleResponse(w, r) {
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if bytes.Contains(body, []byte("SCHEDULE REMINDER")) && bytes.Contains(body, []byte("persisted reminder")) {
			delivered.Store(true)
		}
		if bytes.Contains(body, []byte("Continue in the scheduled chat.")) {
			var request struct{ Model string }
			if json.Unmarshal(body, &request) == nil && request.Model == "fixture-alternate" {
				usedScheduledModel.Store(true)
			}
		}
		if bytes.Contains(body, []byte("Arm fixture schedule.")) {
			if bytes.Contains(body, []byte("data:image/png;base64,")) {
				sawImage.Store(true)
			}
			if bytes.Contains(body, []byte(attachmentPath)) {
				sawFile.Store(true)
			}
		}
		output := []map[string]any{{"type": "message", "id": "message-test", "role": "assistant",
			"content": []map[string]string{{"type": "output_text", "text": "OK"}}}}
		if bytes.Contains(body, []byte("Arm fixture schedule.")) && !bytes.Contains(body, []byte("function_call_output")) {
			output = []map[string]any{webCall("create-reminder", "schedule_create",
				`{"title":"Test reminder","prompt":"persisted reminder","rrule":"DTSTART:`+time.Now().UTC().Add(time.Hour).Format("20060102T150405Z")+`\nRRULE:FREQ=DAILY;COUNT=1","target":"new_once"}`)}
		}
		if bytes.Contains(body, []byte("Continue using the attachments.")) && !bytes.Contains(body, []byte("copy-attachment")) {
			args, _ := json.Marshal(map[string]string{
				"command": "cp " + attachmentPath + " delivered.txt && chmod u+w delivered.txt && printf ' edited' >> delivered.txt",
			})
			output = []map[string]any{webCall("copy-attachment", "shellexecute", string(args))}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		payload, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{
			"id": "response-test", "status": "completed", "output": output,
			"usage": map[string]int{"input_tokens": 10, "output_tokens": 5},
		}})
		fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", payload)
	}))
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	model.Listener = listener
	model.Start()
	defer model.Close()

	c := dockerFixtureConfig()
	c.Model.Main = config.MainModel{Provider: "custom", Name: "fixture-main", Protocol: "responses",
		BaseURL: fmt.Sprintf("http://host.docker.internal:%d", listener.Addr().(*net.TCPAddr).Port), Auth: "bearer",
		APIKey: "fixture-model-key", ContextWindow: config.ModelContextWindow}
	c.Web.Provider, c.Web.APIKey = "deepseek", "fixture-search-key"

	manager := newFixtureManager(t, c)
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		if err := manager.Delete(cleanup); err != nil {
			t.Error(err)
		}
	}()
	if err := manager.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(manager.Handler())
	defer server.Close()

	call := func(method, path, mediaType string, body io.Reader, want int) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, server.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		if mediaType != "" {
			req.Header.Set("Content-Type", mediaType)
		}
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, data)
		}
		return resp
	}
	create := func() string {
		t.Helper()
		resp := call("POST", "/v1/sessions", "", nil, 201)
		defer resp.Body.Close()
		var out struct{ SessionID string }
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.SessionID == "" {
			t.Fatalf("create Session: %v", err)
		}
		return out.SessionID
	}
	owner, other := create(), create()
	ownerURL := "/v1/sessions/" + owner

	source := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	source.Set(0, 0, color.NRGBA{R: 200, A: 100})
	var originalImage bytes.Buffer
	if err := png.Encode(&originalImage, source); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("content", "Arm fixture schedule."); err != nil {
		t.Fatal(err)
	}
	file, err := form.CreateFormFile("files", "reference.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, uploadedText); err != nil {
		t.Fatal(err)
	}
	imagePart, err := form.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="images"; filename="chart.png"`},
		"Content-Type":        {"image/png"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := imagePart.Write(originalImage.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	stream := call("GET", ownerURL+"/stream", "", nil, 200)
	admission := call("POST", ownerURL+"/messages", form.FormDataContentType(), &body, 202)
	var refs struct{ Files, Images []attachments.FileRef }
	err = json.NewDecoder(admission.Body).Decode(&refs)
	admission.Body.Close()
	if err != nil || len(refs.Files) != 1 || len(refs.Images) != 1 {
		t.Fatalf("missing committed attachments: %+v, %v", refs, err)
	}
	events := readWebTurn(t, stream.Body)
	stream.Body.Close()
	if !sawImage.Load() || !sawFile.Load() {
		t.Fatal("model did not receive the durable attachments")
	}
	const dsh = "/root/harness/.dsh"
	for _, item := range []struct{ dir, id string }{{"objects", refs.Images[0].ID}, {"file-objects", refs.Files[0].ID}} {
		digest := strings.TrimPrefix(item.id, "sha256:")
		manager.Exec(t, ctx, "test", "-f", dsh+"/attachments/v1/"+item.dir+"/"+digest[:2]+"/"+digest)
	}
	instance := manager.Instance(ctx)
	if instance == nil {
		t.Fatal("Host instance missing")
	}
	if err := manager.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	client := dockerapi.Client{}
	remote := instance.Ref.ID + ":" + dsh + "/storages/schedule.json"
	store := schedule.FileStore{Path: filepath.Join(t.TempDir(), "schedule.json")}
	if _, err := client.Run(ctx, nil, "cp", remote, store.Path); err != nil {
		t.Fatal(err)
	}
	document, err := store.Load(ctx)
	if err != nil || len(document.Tasks) != 1 || document.Tasks[0].Target == nil ||
		document.Tasks[0].Target.Kind != "new_once" || document.Tasks[0].Target.SessionID == "" {
		t.Fatalf("persisted schedule: %+v, %v", document, err)
	}
	reserved := document.Tasks[0].Target.SessionID
	document.Tasks[0].Record.ScheduledAt = time.Now().UTC().Add(-time.Minute).Format("2006-01-02T15:04:05.000Z")
	if err := store.Save(ctx, document); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Run(ctx, nil, "cp", store.Path, remote); err != nil {
		t.Fatal(err)
	}
	sawImage.Store(false)
	sawFile.Store(false)
	if err := manager.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		ref  attachments.FileRef
		want []byte
	}{{refs.Files[0], []byte(uploadedText)}, {refs.Images[0], originalImage.Bytes()}} {
		path := "/attachments/" + item.ref.ID
		response := call("GET", ownerURL+path, "", nil, 200)
		got, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || !bytes.Equal(got, item.want) {
			t.Fatalf("cold attachment download %s: %v", item.ref.ID, err)
		}
		call("GET", "/v1/sessions/"+other+path, "", nil, 404).Body.Close()
	}
	call("POST", ownerURL+"/messages", "application/json",
		strings.NewReader(`{"content":"Continue using the attachments."}`), 202).Body.Close()
	after := strconv.FormatUint(events[len(events)-1].Seq, 10)
	stream = call("GET", ownerURL+"/stream?after="+after, "", nil, 200)
	defer stream.Body.Close()
	readWebTurn(t, stream.Body)
	if !sawImage.Load() || !sawFile.Load() {
		t.Fatal("cold model request did not replay the durable attachments")
	}
	for path, want := range map[string]string{
		"/files/content?path=delivered.txt": uploadedText + " edited",
		"/attachments/" + refs.Files[0].ID:  uploadedText,
	} {
		response := call("GET", ownerURL+path, "", nil, 200)
		got, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || string(got) != want {
			t.Fatalf("workspace delivery %s = %q, %v", path, got, err)
		}
	}
	var receipt *schedule.Receipt
	for ctx.Err() == nil && (receipt == nil || !delivered.Load()) {
		if !delivered.Load() {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if _, err := client.Run(ctx, nil, "cp", remote, store.Path); err != nil {
			t.Fatal(err)
		}
		state, err := store.Load(ctx)
		if err != nil || len(state.Tasks) != 1 {
			t.Fatalf("loading recovered schedule: %+v, %v", state, err)
		}
		receipt = state.Tasks[0].LastDelivery
		if receipt == nil {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if !delivered.Load() || receipt == nil || receipt.TargetSessionID != reserved {
		t.Fatalf("reserved destination did not receive durable reminder: %q %+v", reserved, receipt)
	}
	modelPath := "/v1/sessions/" + reserved + "/model"
	assertSelection := func(want string) {
		t.Helper()
		resp := call(http.MethodGet, modelPath, "", nil, http.StatusOK)
		defer resp.Body.Close()
		var view struct{ Selection modelsettings.Selection }
		if err := json.NewDecoder(resp.Body).Decode(&view); err != nil || view.Selection.ModelID != want {
			t.Fatalf("scheduled chat selection = %+v, %v", view, err)
		}
	}
	assertSelection("fixture-main")
	call(http.MethodPatch, "/v1/settings/models/endpoints/fixture", "application/json",
		strings.NewReader(`{"models":["fixture-main","fixture-alternate"]}`), http.StatusOK).Body.Close()
	call(http.MethodPut, modelPath, "application/json",
		strings.NewReader(`{"endpointId":"fixture","modelId":"fixture-alternate"}`), http.StatusOK).Body.Close()
	if err := manager.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	assertSelection("fixture-alternate")
	state, err := manager.Store.LoadModels(ctx, manager.account)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := state.Sessions[reserved]; exists {
		t.Fatal("the scheduled chat model was copied to Redis")
	}
	call(http.MethodPost, "/v1/sessions/"+reserved+"/messages", "application/json",
		strings.NewReader(`{"content":"Continue in the scheduled chat."}`), http.StatusAccepted).Body.Close()
	for ctx.Err() == nil && !usedScheduledModel.Load() {
		time.Sleep(50 * time.Millisecond)
	}
	if !usedScheduledModel.Load() {
		t.Fatal("the replacement Host did not use the saved scheduled chat model")
	}

	// The scheduled Session has its own journal and attachments but keeps
	// the source workspace through replacement and after the source archives.
	resp := call(http.MethodGet, "/v1/sessions", "", nil, http.StatusOK)
	var listed struct {
		Sessions []struct{ SessionID, CWD, Title string }
	}
	if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	cwds := map[string]string{}
	title := ""
	for _, row := range listed.Sessions {
		cwds[row.SessionID] = row.CWD
		if row.SessionID == reserved {
			title = row.Title
		}
	}
	if cwds[owner] == "" || cwds[owner] != cwds[reserved] || cwds[other] == cwds[owner] || title != "Test reminder" {
		t.Fatalf("unexpected Session workspace bindings or title: %+v", listed)
	}

	hostSchedules := "/v1/host/schedules"
	createTask := func(destination string) schedule.Entry {
		t.Helper()
		payload := fmt.Sprintf(`{"title":"Future follow-up","prompt":"Reply with OK","timing":{"kind":"after","after_seconds":3600},"target":{"kind":"session","sessionId":%q}}`, destination)
		resp := call(http.MethodPost, hostSchedules, "application/json", strings.NewReader(payload), http.StatusCreated)
		defer resp.Body.Close()
		var task schedule.Entry
		if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
			t.Fatal(err)
		}
		return task
	}
	immediate := createTask(other)
	runPath := hostSchedules + "/" + immediate.Record.ID
	resp = call(http.MethodPost, runPath+"/run", "", nil, http.StatusOK)
	var ran schedule.Entry
	if err := json.NewDecoder(resp.Body).Decode(&ran); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if ran.Record.ScheduledAt != immediate.Record.ScheduledAt || ran.Status != "active" ||
		ran.LastDelivery == nil || ran.LastDelivery.TargetSessionID != other || ran.UpdatedAt == immediate.UpdatedAt {
		t.Fatalf("manual delivery moved the scheduled occurrence or lost its receipt: %+v", ran)
	}
	call(http.MethodPost, runPath+"/run", "", nil, http.StatusConflict).Body.Close()
	resp = call(http.MethodGet, runPath+"/history?limit=1", "", nil, http.StatusOK)
	var history schedule.HistoryPage
	if err := json.NewDecoder(resp.Body).Decode(&history); err != nil || len(history.Records) != 1 || history.Records[0].Prompt != "Reply with OK" {
		t.Fatalf("manual delivery history: %+v, %v", history, err)
	}
	resp.Body.Close()

	armed := createTask(owner)
	call(http.MethodDelete, ownerURL, "", nil, http.StatusConflict).Body.Close()
	call(http.MethodDelete, ownerURL+"?stopActivity=true", "", nil, http.StatusNoContent).Body.Close()
	call(http.MethodGet, hostSchedules+"/"+armed.Record.ID, "", nil, http.StatusNotFound).Body.Close()
	call(http.MethodGet, ownerURL+"/transcript?limit=1", "", nil, http.StatusNotFound).Body.Close()
	resp = call(http.MethodGet, "/v1/sessions/"+reserved+"/files/content?path=delivered.txt", "", nil, http.StatusOK)
	got, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || string(got) != uploadedText+" edited" {
		t.Fatalf("archive removed the shared workspace file: %q, %v", got, err)
	}
}
