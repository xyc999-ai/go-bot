package host

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-bot/harness/agent"
	"go-bot/internal/modelsettings"
	"go-bot/providers/model/mainmodel"
)

func TestTitleAuxiliaryRequestUsesSelectedProtocolWithoutTools(t *testing.T) {
	for _, protocol := range []string{modelsettings.Responses, modelsettings.AnthropicMessages} {
		t.Run(protocol, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					http.Error(w, "invalid request", 400)
					return
				}
				if body["model"] != "selected-model" {
					t.Error("wrong model", body["model"])
				}
				if tools, ok := body["tools"].([]any); ok && len(tools) > 0 {
					t.Error("auxiliary request had tools")
				}
				if body["stream"] == true {
					t.Error("title request should not create a chat stream")
				}
				w.Header().Set("Content-Type", "application/json")
				switch protocol {
				case modelsettings.AnthropicMessages:
					if r.Header.Get("x-api-key") != "fixture" || body["max_tokens"] != float64(4096) {
						t.Error("incorrect Anthropic options")
					}
					_, _ = w.Write([]byte(`{"id":"title","type":"message","role":"assistant","content":[{"type":"text","text":"A useful title"}],"stop_reason":"end_turn"}`))

				default:
					if body["max_output_tokens"] != float64(4096) {
						t.Error("missing title token limit")
					}
					_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"A useful title"}]}]}`))
				}
			}))
			defer server.Close()
			models := hostModels{base: mainmodel.Model{Transport: server.Client().Transport}, snapshot: modelsettings.Snapshot{Catalog: modelsettings.Catalog{DefaultEndpointID: "p", Endpoints: map[string]modelsettings.Endpoint{"p": {BaseURL: server.URL, APIKey: "fixture", Protocol: protocol, Models: []string{"selected-model"}}}}}}
			value, err := models.generateTitle(t.Context(), agent.ModelRoute{Provider: "p", Model: "selected-model"}, "title input")
			if err != nil || value != "A useful title" {
				t.Fatal(value, err)
			}
		})
	}
}
