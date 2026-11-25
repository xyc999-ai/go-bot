package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go-bot/internal/config"
)

func TestDockerWebChatQueueAndQuestions(t *testing.T) {
	if os.Getenv("RUN_DOCKER_INTEGRATION") != "1" {
		t.Skip("requires the built Host image")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	var calls atomic.Int32
	model := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fixtureTitleResponse(w, r) {
			return
		}
		input, _ := io.ReadAll(r.Body)
		call := calls.Add(1)
		var output []map[string]any
		if call == 1 {
			output = []map[string]any{
				webCall("bad-delivery", "present", `{"files":[{"path":"missing.txt"}]}`),
				webCall("delivery", "present", `{"files":[{"path":"report.txt","description":"Prepared report"}]}`),
				webCall("question", "ask_user_question",
					`{"questions":[{"id":"choice","question":"Which option?","options":[{"label":"Yes"}]}]}`),
			}
		} else {
			if call == 2 && (!strings.Contains(string(input), "Steer this") || !strings.Contains(string(input), "Yes")) {
				t.Errorf("steering and answer did not reach the next model step")
			}
			if strings.Contains(string(input), "Delete this") || (call == 2 && strings.Contains(string(input), "Next turn")) {
				t.Error("a message entered the wrong turn")
			}
			output = []map[string]any{{"type": "message", "id": fmt.Sprintf("reply-%d", call), "role": "assistant",
				"content": []map[string]string{{"type": "output_text", "text": "[Report](./report.txt)"}}}}
		}
		payload, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{
			"id": fmt.Sprintf("response-%d", call), "status": "completed", "output": output,
		}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", payload)
	}))
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	model.Listener = listener
	model.Start()
	defer model.Close()

	c := config.Default()
	if image := os.Getenv("HARNESS_TEST_HOST_IMAGE"); image != "" {
		c.Runtime.Image = image
	}
	c.Model.Main = config.MainModel{Provider: "custom", Name: "chat-fixture", Protocol: "responses",
		BaseURL: fmt.Sprintf("http://host.docker.internal:%d", listener.Addr().(*net.TCPAddr).Port),
		Auth:    "bearer", APIKey: "fixture-model-key", ContextWindow: config.ModelContextWindow}
	c.Web.Provider, c.Web.APIKey = "deepseek", "fixture-search-key"
	manager := newFixtureManager(t, c)
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
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

	request := func(method, path, body string, want int) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
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
	read := func(method, path, body string, want int) string {
		t.Helper()
		resp := request(method, path, body, want)
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	var session struct{ SessionID string }
	if err := json.Unmarshal([]byte(read("POST", "/v1/sessions", "", 201)), &session); err != nil || session.SessionID == "" {
		t.Fatalf("missing Session ID: %v", err)
	}
	sessionPath := "/v1/sessions/" + session.SessionID
	manager.Exec(t, ctx, "sh", "-ec", `printf 'report contents' > "workspaces/$1/report.txt"`, "sh", session.SessionID)
	stream := request("GET", sessionPath+"/stream?view=chat", "", 200)
	defer stream.Body.Close()
	send := func(text string) string {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"content": text})
		var ack struct{ MessageID string }
		if err := json.Unmarshal([]byte(read("POST", sessionPath+"/messages", string(body), 202)), &ack); err != nil || ack.MessageID == "" {
			t.Fatalf("missing message ID: %v", err)
		}
		return ack.MessageID
	}
	scanner := bufio.NewScanner(stream.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	wait := func(predicate func(string, string) bool) {
		t.Helper()
		var event string
		for scanner.Scan() {
			line := scanner.Text()
			if kind, ok := strings.CutPrefix(line, "event: "); ok {
				event = kind
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok && predicate(event, data) {
				return
			}
		}
		t.Fatalf("chat stream ended before expected event: %v", scanner.Err())
	}

	send("First turn")
	var deliveryFile string
	wait(func(event, data string) bool {
		if event != "session" {
			return false
		}
		if strings.Contains(data, `"type":"turn/end"`) || strings.Contains(data, `"type":"deliverables/presented"`) {
			t.Fatalf("unexpected event before the delivery card: %s", data)
		}
		if !strings.Contains(data, `"delivery":true`) {
			return false
		}
		var delivered struct {
			Type string
			Data struct {
				Message struct {
					Blocks []struct {
						File struct{ Source, Path string }
					}
				}
			}
		}
		if err := json.Unmarshal([]byte(data), &delivered); err != nil || delivered.Type != "assistant/message" ||
			!strings.Contains(data, `"type":"file"`) || !strings.Contains(data, "Prepared report") ||
			strings.Contains(data, "missing.txt") || len(delivered.Data.Message.Blocks) != 1 {
			t.Fatalf("unexpected delivery card: %s (%v)", data, err)
		}
		ref := delivered.Data.Message.Blocks[0].File
		if ref.Source != "presented" || ref.Path != "/root/harness/workspaces/"+session.SessionID+"/report.txt" {
			t.Fatalf("unexpected delivered file reference: %+v", ref)
		}
		deliveryFile = ref.Path
		return true
	})
	deliveryPath := sessionPath + "/files/content?" + url.Values{"path": {deliveryFile}}.Encode()
	if file := read("GET", deliveryPath, "", 200); file != "report contents" {
		t.Fatalf("live delivery is %q", file)
	}
	read("GET", sessionPath+"/files/content?path=%2Fetc%2Fpasswd", "", 404)
	wait(func(event, data string) bool {
		return event == "questions.state" && strings.Contains(data, "Which option?")
	})
	steerID := send("Steer this")
	deleteID := send("Delete this")
	send("Next turn")
	wait(func(event, data string) bool {
		return event == "queue.state" && strings.Contains(data, "Steer this") && strings.Contains(data, "Delete this") && strings.Contains(data, "Next turn")
	})
	read("POST", sessionPath+"/queue/"+steerID+"/steer", "", 204)
	read("DELETE", sessionPath+"/queue/"+deleteID, "", 204)
	wait(func(event, data string) bool {
		return event == "queue.state" && strings.Contains(data, "steering") && !strings.Contains(data, "Delete this")
	})
	read("POST", sessionPath+"/questions/question/answer", `{"answers":[{"id":"choice","selected":["Yes"]}]}`, 204)
	var ended int
	wait(func(event, data string) bool {
		if event == "session" && strings.Contains(data, `"type":"turn/end"`) {
			ended++
		}
		return ended == 2
	})
	stream.Body.Close()
	if calls.Load() != 3 {
		t.Fatalf("expected three model requests across two turns, got %d", calls.Load())
	}
	var page struct {
		Before     *uint64
		ThroughSeq *uint64
	}
	latest := read("GET", sessionPath+"/transcript?limit=1", "", 200)
	if err := json.Unmarshal([]byte(latest), &page); err != nil || page.Before == nil || page.ThroughSeq == nil ||
		strings.Contains(latest, "First turn") || !strings.Contains(latest, "Next turn") ||
		!strings.Contains(latest, "[Report](./report.txt)") || strings.Contains(latest, `"type":"file"`) {
		t.Fatalf("unexpected latest transcript: %s (%v)", latest, err)
	}
	older := read("GET", fmt.Sprintf("%s/transcript?before=%d&limit=1", sessionPath, *page.Before), "", 200)
	if !strings.Contains(older, "First turn") || !strings.Contains(older, "Steer this") ||
		strings.Contains(older, "Delete this") || strings.Contains(older, "Which option?") ||
		!strings.Contains(older, "Prepared report") || strings.Contains(older, `"type":"deliverables/presented"`) {
		t.Fatalf("unexpected earlier transcript: %s", older)
	}
	manager.Exec(t, ctx, "sh", "-ec", `printf 'updated contents' > "workspaces/$1/report.txt"`, "sh", session.SessionID)
	if err := manager.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	cold := read("GET", sessionPath+"/transcript?limit=1", "", 200)
	var warmPage, coldPage map[string]any
	if err := json.Unmarshal([]byte(latest), &warmPage); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(cold), &coldPage); err != nil {
		t.Fatal(err)
	}
	// A loaded runtime includes its transient stream baseline; a cold session
	// has no live stream. Persisted events, cursors and interaction state match.
	delete(warmPage, "baseline")
	delete(coldPage, "baseline")
	if !reflect.DeepEqual(coldPage, warmPage) {
		t.Fatalf("cold transcript differs: warm=%s cold=%s", latest, cold)
	}
	if file := read("GET", deliveryPath, "", 200); file != "updated contents" {
		t.Fatalf("cold delivery is %q", file)
	}
	if file := read("GET", sessionPath+"/files/content?path=report.txt", "", 200); file != "updated contents" {
		t.Fatalf("unexpected cold workspace file: %q", file)
	}
	manager.Exec(t, ctx, "sh", "-ec", `rm "workspaces/$1/report.txt"; ln -s /etc/passwd "workspaces/$1/report.txt"`, "sh", session.SessionID)
	read("GET", deliveryPath, "", 404)
	stream = request("GET", fmt.Sprintf("%s/stream?view=chat&after=%d", sessionPath, *page.ThroughSeq), "", 200)
	defer stream.Body.Close()
	scanner = bufio.NewScanner(stream.Body)
	wait(func(event, _ string) bool { return event == "chat.idle" })
	send("Resume this session")
	wait(func(event, data string) bool {
		return event == "session" && strings.Contains(data, `"type":"turn/end"`)
	})
	if calls.Load() != 4 {
		t.Fatalf("cold stream did not attach to the resumed Agent: %d model requests", calls.Load())
	}
}
