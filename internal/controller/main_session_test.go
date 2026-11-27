package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go-bot/internal/modelsettings"
)

func TestNewAccountReservesMainAndDefaultPlugins(t *testing.T) {
	m := newTestManager(t, "docker", &fakeDriver{}, func(w http.ResponseWriter, r *http.Request) { controlReply(w, r) })
	if err := m.CreateAccount(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	a, _ := m.Store.Get(t.Context(), "first")
	if uuid.Validate(a.MainSessionID) != nil || len(a.Plugins.Packages) != 2 {
		t.Fatalf("account defaults: %+v", a)
	}
	for _, name := range []string{"@go-bot/plugin-officecli", "@go-bot/plugin-agent-browser"} {
		if !a.Plugins.Packages[name].Enabled {
			t.Fatal(name)
		}
	}
	removed := accountPlugins(a)
	delete(removed.Packages, "@go-bot/plugin-officecli")
	if err := m.Store.SavePluginSettings(t.Context(), "first", removed); err != nil {
		t.Fatal(err)
	}
	a, _ = m.Store.Get(t.Context(), "first")
	a.HostID = "replacement"
	if err := m.Store.Save(t.Context(), "first", a); err != nil {
		t.Fatal(err)
	}
	restored, _ := m.Store.Get(t.Context(), "first")
	if restored.MainSessionID != a.MainSessionID || len(restored.Plugins.Packages) != 1 {
		t.Fatal("Host change restored defaults or changed main identity")
	}
}

func TestMainStartWithoutLLMAndSendChecks(t *testing.T) {
	revision := int64(0)
	accepted := 0
	prepared := ""
	preparedLocale := ""
	m := newTestManager(t, "docker", &fakeDriver{}, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case modelsettings.InternalPath:
			if r.Method == "PUT" {
				var state modelsettings.Snapshot
				_ = json.NewDecoder(r.Body).Decode(&state)
				revision = state.Revision
				w.WriteHeader(204)
			} else {
				fmt.Fprintf(w, `{"revision":%d}`, revision)
			}
		case "/internal/host/sessions/main":
			var input struct{ SessionID, Locale string }
			_ = json.NewDecoder(r.Body).Decode(&input)
			prepared = input.SessionID
			preparedLocale = input.Locale
			w.WriteHeader(200)
		default:
			if strings.HasPrefix(r.URL.Path, modelsettings.ScheduledSelectionPrefix) {
				w.WriteHeader(404)
				return
			}
			if strings.HasSuffix(r.URL.Path, "/exists") {
				w.WriteHeader(204)
				return
			}
			if strings.HasSuffix(r.URL.Path, "/messages") {
				accepted++
				w.WriteHeader(202)
				return
			}
			if !controlReply(w, r) {
				w.WriteHeader(204)
			}
		}
	})
	if err := m.CreateAccount(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	a, _ := m.Store.Get(t.Context(), "first")
	out := controllerCall(m, "POST", "/v1/host/start", "first", `{"welcomeLocale":"zh-CN"}`)
	if out.Code != 200 || prepared != a.MainSessionID || !strings.Contains(out.Body.String(), a.MainSessionID) {
		t.Fatalf("start: %d %s", out.Code, out.Body)
	}
	for _, language := range []string{"en", "zh-CN", "zh-TW", "es", "ja", "pt-BR", "fr", "de", "ko"} {
		out := controllerCall(m, "POST", "/v1/host/start", "first", fmt.Sprintf(`{"welcomeLocale":%q}`, language))
		if out.Code != 200 || preparedLocale != language {
			t.Fatalf("locale %s was not forwarded: status %d, Host locale %s", language, out.Code, preparedLocale)
		}
	}
	if out := controllerCall(m, "POST", "/v1/host/start", "first", `{"welcomeLocale":"invalid"}`); out.Code != 400 {
		t.Fatalf("unsupported welcome locale: %d %s", out.Code, out.Body)
	}
	path := "/v1/sessions/" + a.MainSessionID + "/messages"
	if got := controllerCall(m, "POST", path, "first", `{"content":"draft"}`); got.Code != 409 || accepted != 0 {
		t.Fatalf("unconfigured sent: %d %s", got.Code, got.Body)
	}
	if got := controllerCall(m, "POST", "/v1/sessions", "first", ""); got.Code != 409 {
		t.Fatal("new chat accepted without default")
	}
	catalog := modelsettings.Catalog{DefaultEndpointID: "test", Endpoints: map[string]modelsettings.Endpoint{"test": {Protocol: "responses", Name: "Test", BaseURL: "https://models.example/v1", APIKey: "fixture", Models: []string{"first", "second"}}}}
	_, _ = m.Store.SaveModelCatalog(t.Context(), "first", catalog)
	state, _ := m.Store.LoadModels(t.Context(), "first")
	record, _ := m.Store.Get(t.Context(), "first")
	if err := m.syncModels(t.Context(), record, state); err != nil {
		t.Fatal(err)
	}
	if got := controllerCall(m, "POST", path, "first", `{"content":"hello"}`); got.Code != 202 || accepted != 1 {
		t.Fatalf("first send: %d %s", got.Code, got.Body)
	}
	state, _ = m.Store.LoadModels(t.Context(), "first")
	if state.Sessions[a.MainSessionID].ModelID != "first" {
		t.Fatal("main was not bound")
	}
	catalog.Endpoints["test"] = modelsettings.Endpoint{Protocol: "responses", BaseURL: "https://models.example/v1", APIKey: "fixture", Models: []string{"second"}}
	_, _ = m.Store.SaveModelCatalog(t.Context(), "first", catalog)
	state, _ = m.Store.LoadModels(t.Context(), "first")
	_ = m.syncModels(t.Context(), record, state)
	if got := controllerCall(m, "POST", path, "first", `{"content":"do not switch"}`); got.Code != 409 || accepted != 1 {
		t.Fatalf("invalid choice silently changed: %d %s", got.Code, got.Body)
	}
}
