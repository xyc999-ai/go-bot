package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestNonStreamingIdleTimeoutWaitsForProgressNotTotalDuration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[`))
		w.(http.Flusher).Flush()
		time.Sleep(130 * time.Millisecond)
		_, _ = w.Write([]byte(`{"type":"message","role":"assistant",`))
		w.(http.Flusher).Flush()
		time.Sleep(130 * time.Millisecond)
		_, _ = w.Write([]byte(`"content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer server.Close()
	client, err := NewClientWithNonStreamingIdleTimeout(Endpoint{BaseURL: server.URL, APIKey: "secret"}, "model", 220*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Generate(context.Background(), Request{})
	if err != nil || response == nil || response.Output[0].Content[0].Text != "ok" {
		t.Fatalf("progressive response: %+v, %v", response, err)
	}
}

func TestNonStreamingIdleTimeoutCoversHeadersAndBody(t *testing.T) {
	for _, phase := range []string{"headers", "body"} {
		t.Run(phase, func(t *testing.T) {
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if phase == "body" {
					_, _ = w.Write([]byte(`{"status":"completed","output":[`))
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer func() { close(release); server.Close() }()
			client, err := NewClientWithNonStreamingIdleTimeout(Endpoint{BaseURL: server.URL, APIKey: "secret"}, "model", 35*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Generate(context.Background(), Request{})
			if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				t.Fatalf("%s idle failure = %v", phase, err)
			}
			if classified := NormalizeModelError("endpoint", "model", err); classified.Code != ModelErrorUpstreamTimeout {
				t.Fatalf("%s idle classification = %s", phase, classified.Code)
			}
		})
	}
}

func TestNonStreamingCallerCancellationIsNotIdleTimeout(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer func() { close(release); server.Close() }()
	client, err := NewClientWithNonStreamingIdleTimeout(Endpoint{BaseURL: server.URL, APIKey: "secret"}, "model", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := client.Generate(ctx, Request{}); done <- err }()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("caller cancellation = %v", err)
	} else if classified := NormalizeModelError("endpoint", "model", err); classified.Code != ModelErrorCancelled {
		t.Fatalf("caller cancellation classification = %s", classified.Code)
	}
}

func TestResponsesKeepsOutputOrderAndNormalizesReasoning(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("unexpected request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"id":"resp-1","status":"completed","output":[{"type":"reasoning","id":"rs-1","summary":[{"type":"summary_text","text":"先查资料"}]},{"type":"message","id":"msg-1","role":"assistant","content":[{"type":"output_text","text":"我来查"}]},{"type":"function_call","id":"fc-1","call_id":"call-1","name":"lookup","arguments":"{broken"}],"usage":{"input_tokens":4,"output_tokens":8,"total_tokens":12}}`))
	}))
	defer server.Close()
	client, err := NewClient(Endpoint{BaseURL: server.URL + "/v1", APIKey: "secret"}, "qwen", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Generate(context.Background(), Request{Input: []Item{{Type: "message", Role: "user", Content: []ContentBlock{{Type: "text", Text: "你好"}}}}, Tools: []Tool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{response.Output[0].Type, response.Output[1].Type, response.Output[2].Type}; !reflect.DeepEqual(got, []string{"reasoning", "message", "tool_call"}) {
		t.Fatalf("order: %v", got)
	}
	if response.Output[0].Content[0].Text != "先查资料" || response.Output[2].Arguments != "{broken" || response.Output[2].CallID != "call-1" {
		t.Fatalf("lost output fields: %#v", response.Output)
	}
	if request["model"] != "qwen" || request["store"] != false {
		t.Fatalf("request: %#v", request)
	}
	if effort := request["reasoning"].(map[string]any)["effort"]; effort != "medium" {
		t.Fatalf("default reasoning effort: %v", effort)
	}
	if request["tool_choice"] != "auto" {
		t.Fatalf("default tool choice: %v", request["tool_choice"])
	}
	if _, ok := request["messages"]; ok {
		t.Fatal("default client used Chat Completions")
	}
}

func TestToolStrictIsExplicitForBothProtocols(t *testing.T) {
	strict := true
	tool := Tool{Name: "lookup", Strict: &strict}
	responses := encodeResponsesTools([]Tool{tool})[0].(map[string]any)
	chat := encodeChatTools([]Tool{tool})[0].(map[string]any)["function"].(map[string]any)
	if responses["strict"] != true || chat["strict"] != true {
		t.Fatalf("strict tool flag missing: Responses=%v Chat=%v", responses, chat)
	}
	without := encodeResponsesTools([]Tool{{Name: "lookup"}})[0].(map[string]any)
	if _, exists := without["strict"]; exists {
		t.Fatalf("unset strict flag should be omitted: %v", without)
	}
}

func TestResponsesContinuationUsesOrderedToolResultBlocks(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"status":"completed","output":[]}`))
	}))
	defer server.Close()
	client, err := NewClient(Endpoint{BaseURL: server.URL, APIKey: "secret"}, "qwen", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Generate(context.Background(), Request{ReasoningEffort: "high", Input: []Item{
		{Type: "reasoning", ID: "rs-1", Content: []ContentBlock{{Type: "text", Text: "先查资料"}}},
		{Type: "tool_call", ID: "fc-1", CallID: "call-1", Name: "lookup", Arguments: "{}"},
		{Type: "tool_result", CallID: "call-1", Content: []ContentBlock{{Type: "text", Text: "找到资料"}, {Type: "image", Source: &ImageSource{Type: "url", URL: "data:image/png;base64,AA=="}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	input := request["input"].([]any)
	if got := input[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"]; got != "先查资料" {
		t.Fatalf("reasoning content: %v", got)
	}
	output := input[2].(map[string]any)["output"].([]any)
	if len(output) != 2 || output[0].(map[string]any)["type"] != "input_text" || output[0].(map[string]any)["text"] != "找到资料" || output[1].(map[string]any)["type"] != "input_image" || output[1].(map[string]any)["image_url"] != "data:image/png;base64,AA==" {
		t.Fatalf("tool result: %v", output)
	}
	if got := input[1].(map[string]any)["call_id"]; got != "call-1" {
		t.Fatalf("call id: %v", got)
	}
	if effort := request["reasoning"].(map[string]any)["effort"]; effort != "high" {
		t.Fatalf("explicit reasoning effort: %v", effort)
	}
}

func TestResponsesToolResultKeepsBlockOrder(t *testing.T) {
	input, err := encodeResponsesInput([]Item{
		{Type: "tool_call", CallID: "call-1", Name: "read_image", Arguments: `{}`},
		{Type: "tool_result", CallID: "call-1", Content: []ContentBlock{
			{Type: "text", Text: "metadata"},
			{Type: "image", Source: &ImageSource{Type: "url", URL: "data:image/png;base64,AA=="}},
			{Type: "text", Text: "after image"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(input) != 2 {
		t.Fatalf("input item count = %d", len(input))
	}
	toolOutput := input[1].(map[string]any)
	if toolOutput["type"] != "function_call_output" || toolOutput["call_id"] != "call-1" {
		t.Fatalf("tool result lost its call identity: %v", toolOutput)
	}
	parts := toolOutput["output"].([]any)
	if got := []string{
		parts[0].(map[string]string)["type"],
		parts[1].(map[string]any)["type"].(string),
		parts[2].(map[string]string)["type"],
	}; !reflect.DeepEqual(got, []string{"input_text", "input_image", "input_text"}) {
		t.Fatalf("tool result block order changed: %v", got)
	}
}

func TestResponsesHistoricalTextToolResultUsesString(t *testing.T) {
	input, err := encodeResponsesInput([]Item{
		{Type: "tool_call", CallID: "call-1", Name: "lookup", Arguments: `{}`},
		{Type: "tool_result", CallID: "call-1", Content: []ContentBlock{{Type: "text", Text: "first"}, {Type: "text", Text: " second"}}},
		{Type: "message", Role: "user", Content: []ContentBlock{
			{Type: "text", Text: "generated image"},
			{Type: "image", Source: &ImageSource{Type: "url", URL: "data:image/jpeg;base64,AA=="}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := input[1].(map[string]any)["output"]; got != "first\n second" {
		t.Fatalf("historical text tool result = %#v", got)
	}
	if parts, ok := input[2].(map[string]any)["content"].([]any); !ok || len(parts) != 2 {
		t.Fatalf("generated image message lost its ordered blocks: %#v", input[2])
	}
}

func TestResponsesTextToolOutputsWorkWithStringOnlyProvider(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, test := range []struct {
			name    string
			content []ContentBlock
			want    string
		}{
			{name: "nil"},
			{name: "empty", content: []ContentBlock{}},
			{name: "empty text", content: []ContentBlock{{Type: "text"}}},
			{name: "escaped text", content: []ContentBlock{{Type: "text", Text: "搜索结果：\n\"quoted\" \\ path\tend"}}, want: "搜索结果：\n\"quoted\" \\ path\tend"},
			{name: "JSON array data", content: []ContentBlock{{Type: "text", Text: `[{"title":"Go","url":"https://go.dev/"}]`}}, want: `[{"title":"Go","url":"https://go.dev/"}]`},
			{name: "multiple blocks", content: []ContentBlock{{Type: "text", Text: "first"}, {Type: "text", Text: "second"}}, want: "first\nsecond"},
		} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, test.name), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Decode into a string field to model a provider that rejects arrays.
					var request struct {
						Input []struct {
							Type   string
							CallID string `json:"call_id"`
							Output string
						}
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Errorf("decode string-only tool output: %v", err)
						http.Error(w, `{"error":{"message":"invalid JSON body"}}`, http.StatusBadRequest)
						return
					}
					outputs := map[string]string{}
					for _, item := range request.Input {
						if item.Type == "function_call_output" {
							outputs[item.CallID] = item.Output
						}
					}
					if !reflect.DeepEqual(outputs, map[string]string{"old-call": "old\nresult", "new-call": test.want}) {
						t.Errorf("tool outputs = %#v", outputs)
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n")
					} else {
						fmt.Fprint(w, `{"status":"completed","output":[]}`)
					}
				}))
				defer server.Close()
				client, err := NewClient(Endpoint{BaseURL: server.URL}, "model", server.Client())
				if err != nil {
					t.Fatal(err)
				}
				request := Request{Input: []Item{
					{Type: "tool_call", CallID: "old-call", Name: "lookup", Arguments: `{}`},
					{Type: "tool_result", CallID: "old-call", Content: []ContentBlock{{Type: "text", Text: "old"}, {Type: "text", Text: "result"}}},
					{Type: "message", Role: "user", Content: []ContentBlock{{Type: "text", Text: "search again"}}},
					{Type: "tool_call", CallID: "new-call", Name: "lookup", Arguments: `{}`},
					{Type: "tool_result", CallID: "new-call", Content: test.content},
				}}
				if stream {
					_, err = client.Stream(context.Background(), request, nil)
				} else {
					_, err = client.Generate(context.Background(), request)
				}
				if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestChatCompletionsExplicitAndImageRole(t *testing.T) {
	var path string
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"id":"chat-1","choices":[{"message":{"role":"assistant","content":"看到图片","reasoning_content":"分析图像"}}]}`))
	}))
	defer server.Close()
	client, err := NewClient(Endpoint{BaseURL: server.URL, APIKey: "secret"}, "vl", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	image := ContentBlock{Type: "image", Source: &ImageSource{Type: "url", URL: "data:image/png;base64,AA=="}}
	if _, err := client.Generate(context.Background(), Request{Input: []Item{{Type: "message", Role: "assistant", Content: []ContentBlock{image}}}}); err == nil {
		t.Fatal("Responses accepted assistant input_image")
	}
	response, err := client.GenerateWithProtocol(context.Background(), ChatCompletions, Request{Input: []Item{{Type: "message", Role: "user", Content: []ContentBlock{image}}}, Tools: []Tool{{Name: "inspect"}}})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/chat/completions" || len(response.Output) != 2 || response.Output[0].Type != "reasoning" || response.Output[1].Type != "message" {
		t.Fatalf("chat response: path=%q output=%#v", path, response.Output)
	}
	if request["tool_choice"] != "auto" {
		t.Fatalf("Chat tool choice: %v", request["tool_choice"])
	}
}

func TestChatCompletionsRejectsTruncatedOutputWithUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"chat-truncated","choices":[{"finish_reason":"length","message":{"role":"assistant","content":"partial checkpoint"}}],"usage":{"prompt_tokens":12,"completion_tokens":8,"total_tokens":20}}`))
	}))
	defer server.Close()
	client, err := NewClient(Endpoint{BaseURL: server.URL, APIKey: "secret"}, "vl", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.GenerateWithProtocol(context.Background(), ChatCompletions, Request{Input: []Item{{Type: "message", Role: "user", Content: []ContentBlock{{Type: "text", Text: "summarize"}}}}})
	if err == nil || response == nil || response.Status != "length" || response.FinishReason != "length" || response.Usage == nil || response.Usage.TotalTokens != 20 || len(response.Output) != 1 || response.Output[0].Content[0].Text != "partial checkpoint" {
		t.Fatalf("truncated output was accepted or lost usage: response=%+v error=%v", response, err)
	}
}

func TestResponsesIncompleteCarriesReasonAndPartialOutput(t *testing.T) {
	response, err := decodeResponses([]byte(`{"id":"resp-truncated","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial"}]}],"usage":{"input_tokens":10,"output_tokens":5}}`))
	if err == nil || response == nil || response.Status != "incomplete" || response.FinishReason != "max_output_tokens" || response.Usage.OutputTokens != 5 || len(response.Output) != 1 || response.Output[0].Content[0].Text != "partial" {
		t.Fatalf("lost incomplete response facts: response=%+v err=%v", response, err)
	}
}

func TestOutputCapUsesProtocolSpecificWireField(t *testing.T) {
	client, err := NewClient(Endpoint{BaseURL: "http://example.test", APIKey: "secret"}, "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		protocol Protocol
		field    string
		other    string
	}{
		{Responses, "max_output_tokens", "max_completion_tokens"},
		{ChatCompletions, "max_completion_tokens", "max_output_tokens"},
	} {
		request := Request{Input: []Item{{Type: "message", Role: "user", Content: []ContentBlock{{Type: "text", Text: "summarize"}}}}, MaxOutputTokens: 2048, Purpose: "compaction"}
		_, raw, err := client.requestBody(test.protocol, request, false)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if body[test.field] != float64(2048) || body[test.other] != nil || body["purpose"] != nil {
			t.Fatalf("wrong %s auxiliary request: %+v", test.protocol, body)
		}
		request.MaxOutputTokens = 0
		_, raw, err = client.requestBody(test.protocol, request, false)
		if err != nil {
			t.Fatal(err)
		}
		body = map[string]any{}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if body[test.field] != nil {
			t.Fatalf("ordinary %s request unexpectedly has output cap: %+v", test.protocol, body)
		}
	}
}

func TestToolResultBusinessJSONUsesOrderedContentBlocks(t *testing.T) {
	data, err := json.Marshal(Item{Type: "tool_result", CallID: "call-1", Content: []ContentBlock{{Type: "text", Text: "ok"}, {Type: "image", Source: &ImageSource{Type: "url", URL: "data:image/png;base64,AA=="}}}})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"type":"tool_result","callId":"call-1","content":[{"type":"text","text":"ok"},{"type":"image","source":{"type":"url","url":"data:image/png;base64,AA=="}}]}` {
		t.Fatalf("JSON: %s", data)
	}
	var item Item
	if err := json.Unmarshal(data, &item); err != nil || len(item.Content) != 2 || item.Content[0].Text != "ok" || item.Content[1].Source == nil || item.Content[1].Source.URL != "data:image/png;base64,AA==" {
		t.Fatalf("round trip: %#v %v", item, err)
	}
}

func TestUnknownResponsesItemDoesNotRejectOutput(t *testing.T) {
	response, err := decodeResponses([]byte(`{"status":"completed","output":[{"type":"new_vendor_item","content":{"unexpected":true}},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 2 || response.Output[0].Type != "new_vendor_item" || response.Output[1].Content[0].Text != "ok" {
		t.Fatalf("output: %#v", response.Output)
	}
	encoded, err := json.Marshal(response.Output[0])
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip Item
	if err := json.Unmarshal(encoded, &roundTrip); err != nil || roundTrip.Type != "new_vendor_item" {
		t.Fatalf("unknown item round trip: %#v %v", roundTrip, err)
	}
}
