package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"go-bot/extensions/catalog"
	"go-bot/internal/config"
	"go-bot/plugin"
)

func testPluginCatalog() []catalog.Entry {
	entries := []catalog.Entry{}
	for _, id := range []string{"agent-browser", "officecli"} {
		name := "@go-bot/plugin-" + id
		entries = append(entries, catalog.Entry{DefaultEnabled: true, AssetBase: "https://plugins.example/" + id + "-v0.1.0/", CatalogEntry: plugin.CatalogEntry{ID: id, Package: name, Name: id, Version: "0.1.0", Description: plugin.Localized{EN: id, ZH: id}, Icon: catalog.AssetPrefix + id + "/icon.png", License: "MIT", Maintainer: "Fixture"}, Source: plugin.Source{Type: "registry", Spec: name + "@0.1.0"}})
	}
	return entries
}

func TestRemoteMarketRefreshDiscoversPluginsWithoutChangingSelectedReleases(t *testing.T) {
	var updated atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		entries := testPluginCatalog()
		if updated.Load() {
			next := entries[0]
			next.Version = "0.2.0"
			next.Source.Spec = next.Package + "@0.2.0"
			added := entries[1]
			added.ID, added.Package, added.Name = "mail", "@fixture/mail", "Mail"
			added.Icon = catalog.AssetPrefix + "mail/icon.png"
			added.Source.Spec = "@fixture/mail@0.1.0"
			added.DefaultEnabled = false
			entries = append(entries, next, added)
		}
		_ = json.NewEncoder(w).Encode(entries)
	}))
	defer server.Close()
	store := &memoryStore{records: map[string]Account{}}
	manager := New(config.Default(), nil, store)
	manager.Catalog = catalog.NewRemote(server.URL)
	if err := manager.CreateAccount(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	get := func(path string) plugin.View {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set(UserHeader, "alice")
		response := httptest.NewRecorder()
		manager.Handler().ServeHTTP(response, request)
		var view plugin.View
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &view) != nil {
			t.Fatalf("market response: %d %s", response.Code, response.Body)
		}
		return view
	}
	if view := get("/v1/settings/plugins"); len(view.Catalog) != 2 || len(view.Settings.Packages) != 2 {
		t.Fatal("initial catalog or default selections are missing", view)
	}
	updated.Store(true)
	if view := get("/v1/settings/plugins"); len(view.Catalog) != 2 || requests.Load() != 1 {
		t.Fatal("ordinary reads bypassed the metadata cache", view)
	}
	view := get("/v1/settings/plugins?refresh=true")
	if len(view.Catalog) != 3 || view.Catalog[0].Version != "0.2.0" || view.Catalog[2].ID != "mail" || requests.Load() != 2 {
		t.Fatal("explicit refresh did not discover the new plugin and version", view)
	}
	resolved, err := manager.resolvePlugins(t.Context(), view.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Packages) != 2 || resolved.Packages["@go-bot/plugin-agent-browser"].Source.Spec != "@go-bot/plugin-agent-browser@0.1.0" {
		t.Fatal("refresh changed the installed selection or its exact download source", resolved)
	}
	for _, entry := range view.Catalog {
		if entry.Icon != pluginAssetURL(catalog.AssetPrefix+entry.ID+"/icon.png", entry.Version) {
			t.Fatal("display assets did not retain their release version", entry)
		}
	}
}
