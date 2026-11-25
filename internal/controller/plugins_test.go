package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go-bot/extensions/catalog"

	"go-bot/harness/agent"
	"go-bot/internal/config"
	"go-bot/internal/httpapi"
	"go-bot/internal/modeldata"
	"go-bot/internal/plugins"
	"go-bot/plugin"
)

type pluginFailureStore struct {
	*memoryStore
	fail bool
}

func TestPluginRestartDoesNotRequireFailedApplyToSucceed(t *testing.T) {
	settings := plugin.Settings{Packages: map[string]plugin.PackageSelection{"fixture": {CatalogID: "fixture", Version: "1.0.0", Enabled: true}}}

	restarts := 0
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut:
			t.Error("recovery tried to apply failed instances before restarting")
			http.Error(w, "previous load failed", http.StatusUnprocessableEntity)
		case r.Method == http.MethodPost && r.URL.Path == plugin.InternalPath+"/runtime/restart":
			var input struct{ Settings *plugin.RuntimeSettings }
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Settings == nil ||
				len(input.Settings.Instances) != 1 || input.Settings.Instances[0].ID != "fixture" || !input.Settings.Instances[0].Enabled {
				t.Errorf("restart did not carry saved settings: %v", err)
				http.Error(w, "missing settings", http.StatusBadRequest)
				return
			}
			restarts++
			writeJSON(w, 200, plugin.Status{Runtime: "running"})
		case r.Method == http.MethodGet:
			writeJSON(w, 200, plugin.Status{Runtime: "running"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer host.Close()
	store := &memoryStore{records: map[string]Account{"alice": {
		DesiredState: running, HostEndpoint: host.URL, HostToken: "fixture", Plugins: &settings,
	}}}
	manager := New(config.Default(), nil, store)
	manager.pluginCatalog = []catalog.Entry{{CatalogEntry: plugin.CatalogEntry{ID: "fixture", Package: "fixture", Version: "1.0.0"}, Source: plugin.Source{Type: "registry", Spec: "fixture@1.0.0"}}}

	req := httptest.NewRequest(http.MethodPost, "/v1/host/plugins/runtime/restart", nil)
	req.Header.Set(UserHeader, "alice")
	out := httptest.NewRecorder()
	manager.Handler().ServeHTTP(out, req)
	if out.Code != http.StatusOK || restarts != 1 {
		t.Fatalf("restart failed: %d %s, calls=%d", out.Code, out.Body, restarts)
	}
}

func (s *pluginFailureStore) SavePluginSettings(ctx context.Context, id string, value plugin.Settings) error {
	if s.fail {
		return errors.New("Redis write failed")
	}
	return s.memoryStore.SavePluginSettings(ctx, id, value)
}
func TestPluginHTTPPersistsPackageSelectionBeforeNativeCordisApply(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required")
	}
	main, _ := filepath.Abs("../../extensions/cordis/src/main.mjs")
	if _, err = os.Stat(filepath.Join(filepath.Dir(main), "../node_modules/@deepseek-ai/cordis/package.json")); err != nil {
		t.Skip("Install Cordis dependencies")
	}
	pluginDirectory := t.TempDir()
	extensions, err := plugins.NewManager(t.Context(), plugins.ManagerConfig{Directory: pluginDirectory, Runtime: plugins.RuntimeConfig{Node: node, Main: main, Workspace: t.TempDir()}, PackageMain: filepath.Join(filepath.Dir(main), "package-main.mjs")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = extensions.Close() })
	memory := &memoryStore{records: map[string]Account{"alice": {DesiredState: running, HostToken: "private-host-token"}}}
	store := &pluginFailureStore{memoryStore: memory}
	applied := 0
	private := extensions.Handler()
	host := httptest.NewServer(httpapi.Authenticate("private-host-token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == plugin.InternalPath+"/settings" {
			record, _ := store.Get(r.Context(), "alice")
			if record.Plugins == nil {
				t.Error("Host apply preceded Redis save")
			}
			applied++
		}
		private.ServeHTTP(w, r)
	})))
	defer host.Close()
	memory.records["alice"] = Account{DesiredState: running, HostToken: "private-host-token", HostEndpoint: host.URL}
	manager := New(config.Default(), nil, store)
	request := func(method, path string, input any, want int) []byte {
		t.Helper()
		data, _ := json.Marshal(input)
		r := httptest.NewRequest(method, path, strings.NewReader(string(data)))
		r.Header.Set(UserHeader, "alice")
		out := httptest.NewRecorder()
		manager.Handler().ServeHTTP(out, r)
		if out.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, out.Code, out.Body.String())
		}
		return out.Body.Bytes()
	}
	directory := t.TempDir()
	for name, body := range map[string]string{
		"package.json": `{"name":"@fixture/mail","version":"1.0.0","type":"module","exports":"./index.mjs","goBot":{"api":"1"}}`,
		"index.mjs":    `export const inject=['host'];export async function apply(ctx,config){await ctx.host.tools.register({name:'echo',description:'Echo',inputSchema:{type:'object'},async execute(){return{content:[{type:'text',text:'Hello'}]}}})}`,
	} {
		if err = os.WriteFile(filepath.Join(directory, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Only trusted test setup can inject a directory release. The public API
	// cannot accept this source, even when it points to a valid package.
	manager.pluginCatalog = []catalog.Entry{{CatalogEntry: plugin.CatalogEntry{ID: "mail", Package: "@fixture/mail", Version: "1.0.0"}, Source: plugin.Source{Type: "directory", Spec: directory}}}
	request("POST", plugin.SettingsPath+"/packages", map[string]any{"name": "@fixture/mail", "source": plugin.Source{Type: "directory", Spec: directory}}, 400)
	request("PUT", plugin.SettingsPath, plugin.Settings{}, 404)
	request("POST", plugin.SettingsPath+"/packages", map[string]string{"pluginId": "unknown"}, 404)
	installed := request("POST", plugin.SettingsPath+"/packages", map[string]string{"pluginId": "mail"}, 200)
	var view plugin.View
	if err := json.Unmarshal(installed, &view); err != nil || len(view.Settings.Packages) != 1 || view.Settings.Packages["@fixture/mail"].Enabled {
		t.Fatalf("install must save one disabled package selection: %s", installed)
	}
	if len(view.Catalog) != 1 || view.Settings.Packages["@fixture/mail"].CatalogID != "mail" {
		t.Fatal("missing catalog association", string(installed))
	}
	packagePath := plugin.SettingsPath + "/packages/%40fixture%2Fmail"
	request("PUT", packagePath, map[string]any{"source": plugin.Source{Type: "directory", Spec: directory}}, 400)
	request("POST", plugin.SettingsPath+"/instances", map[string]string{"package": "@fixture/mail"}, 404)
	request("PUT", packagePath, map[string]any{"enabled": true, "config": map[string]string{"label": "initial"}}, 400)
	request("PUT", packagePath, map[string]bool{"enabled": true}, 200)
	tools, err := extensions.Tools(t.Context(), "session")
	if err != nil || len(tools) != 1 {
		t.Fatal(tools, err)
	}
	if text, err := tools[0].Execute(t.Context(), json.RawMessage(`{}`)); err != nil || text != "Hello" {
		t.Fatal(text, err)
	}

	// A real package version update must preserve enablement and replace the
	// old Cordis callback, rather than merely saving a newer Redis selection.
	prior := tools[0]
	nextDirectory := t.TempDir()
	if err = os.WriteFile(filepath.Join(nextDirectory, "package.json"), []byte(`{"name":"@fixture/mail","version":"2.0.0","type":"module","exports":"./index.mjs","goBot":{"api":"1"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(nextDirectory, "index.mjs"), []byte(`export const inject=['host'];export async function apply(ctx){await ctx.host.tools.register({name:'echo',description:'Echo',inputSchema:{type:'object'},async execute(){return{content:[{type:'text',text:'Updated'}]}}})}`), 0600); err != nil {
		t.Fatal(err)
	}
	manager.pluginCatalog = append(manager.pluginCatalog, manager.pluginCatalog[0])
	manager.pluginCatalog[0].Version = "2.0.0"
	manager.pluginCatalog[0].Source.Spec = nextDirectory
	// A market refresh does not force an upgrade when the user only toggles
	// the installed release. This also exercises restore against a newer catalog.
	request("PUT", packagePath, map[string]bool{"enabled": false}, 200)
	request("PUT", packagePath, map[string]bool{"enabled": true}, 200)
	body := request("GET", plugin.SettingsPath, nil, 200)
	var beforeUpdate plugin.View
	if err = json.Unmarshal(body, &beforeUpdate); err != nil || beforeUpdate.Status.Packages[0].AvailableVersion != "2.0.0" {
		t.Fatalf("missing actual update status: %s", body)
	}
	body = request("POST", packagePath+"/update", map[string]string{"pluginId": "mail"}, 200)
	var updated plugin.View
	if err = json.Unmarshal(body, &updated); err != nil || updated.Settings.Packages["@fixture/mail"].Version != "2.0.0" || !updated.Settings.Packages["@fixture/mail"].Enabled || updated.Status.Packages[0].AvailableVersion != "" {
		t.Fatalf("update state: %s", body)
	}
	tools, err = extensions.Tools(t.Context(), "session")
	if err != nil || len(tools) != 1 {
		t.Fatal(tools, err)
	}
	if text, err := tools[0].Execute(t.Context(), json.RawMessage(`{}`)); err != nil || text != "Updated" {
		t.Fatal(text, err)
	}
	if _, err = prior.Execute(t.Context(), json.RawMessage(`{}`)); err == nil {
		t.Error("update retained the old callback")
	}
	store.fail = true
	before := applied
	request("PUT", packagePath, map[string]bool{"enabled": false}, 503)
	if applied != before {
		t.Fatal("failed Redis save still applied to Host")
	}
	store.fail = false
	saved, _ := store.Get(t.Context(), "alice")
	data, _ := json.Marshal(saved.Plugins)
	if strings.Contains(string(data), "source") || strings.Contains(string(data), "instances") || strings.Contains(string(data), "config") {
		t.Fatal("Redis selection contains runtime configuration", string(data))
	}
	if _, err := os.Stat(filepath.Join(pluginDirectory, "profile", "plugins.json")); err == nil {
		t.Fatal("unexpected plugin state file")
	}
	request("PUT", packagePath, map[string]bool{"enabled": false}, 200)
	request("DELETE", packagePath, nil, 200)
	if tools, err := extensions.Tools(t.Context(), "session"); err != nil || len(tools) != 0 {
		t.Fatal(tools, err)
	}
	// Ordinary core preparation is Go-only, even with no Node runtime.
	factory, err := extensions.ToolInvocations(t.Context(), "session")
	if err != nil {
		t.Fatal(err)
	}
	bodyCalls := 0
	invocation, err := factory(t.Context(), agent.ToolExecution{SessionID: "session", Name: "core_probe"}, func(context.Context) modeldata.ToolResult {
		bodyCalls++
		return modeldata.ToolResult{Content: "Go core remains available"}
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !invocation.Prepare().Dispatch {
		t.Fatal("core preparation required Node")
	}
	outcome := invocation.Complete(invocation.Dispatch())
	if outcome.IsError || bodyCalls != 1 {
		t.Fatalf("core outcome=%+v calls=%d", outcome, bodyCalls)
	}
}
