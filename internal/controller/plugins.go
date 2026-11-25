package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"go-bot/extensions/catalog"

	mcpconfig "go-bot/mcp"

	"go-bot/plugin"
)

func accountPlugins(account Account) plugin.Settings {
	if account.Plugins != nil {
		return account.Plugins.Clone()
	}
	return plugin.Settings{}.Clone()
}

// Package preparation may take minutes. It is synchronous and cancellable,
// independently of the normal ten-second Host control request budget.
func (m *Manager) requestPlugins(ctx context.Context, record Account, method, suffix string, input, output any) error {
	return m.requestExtension(ctx, record, method, plugin.InternalPath+suffix, input, output)
}

type extensionHTTPError struct {
	Status  int
	Message string
}

func (e *extensionHTTPError) Error() string { return e.Message }

func (m *Manager) requestExtension(ctx context.Context, record Account, method, path string, input, output any) error {
	target, err := url.Parse(record.HostEndpoint)
	if err != nil || target.Scheme != "http" || target.Host == "" {
		return errHostUnavailable
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, record.HostEndpoint+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+record.HostToken)
	if redirect := oauthCallbackFromContext(ctx); redirect != "" {
		req.Header.Set(mcpconfig.OAuthCallbackHeader, redirect)
		if callback := pluginCallbackBase(redirect); callback != "" {
			req.Header.Set(plugin.AuthorizationCallbackHeader, callback)
		}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := *m.Client
	client.Timeout = 0
	res, err := client.Do(req)
	if err != nil {
		return errors.New("host_plugins_unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var failure struct{ Error, Message string }
		if decodeJSON(io.LimitReader(res.Body, 8<<10), &failure) == nil {
			if failure.Message != "" {
				return &extensionHTTPError{res.StatusCode, failure.Message}
			}
			if failure.Error != "" {
				return &extensionHTTPError{res.StatusCode, failure.Error}
			}
		}
		return &extensionHTTPError{res.StatusCode, "host_plugin_operation_failed"}
	}
	if output != nil {
		return decodeJSON(io.LimitReader(res.Body, 2<<20), output)
	}
	return nil
}

func (m *Manager) syncPlugins(ctx context.Context, id string, account Account) error {
	lock := m.pluginLock(id)
	// An explicit package operation is already applying the saved selection.
	// Opening the account must not wait for its downloads under accountLock.
	if !lock.TryLock() {
		return nil
	}
	defer lock.Unlock()
	current, err := m.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	account.Plugins = current.Plugins
	if account.Plugins == nil {
		return nil
	}
	resolved, err := m.resolvePlugins(ctx, *account.Plugins)
	if err != nil {
		return err
	}
	return m.requestPlugins(ctx, account, http.MethodPut, "/settings", resolved, nil)
}

// Resolve reviewed package sources only while sending private Host commands.
// Redis stores neither installation sources nor Cordis entry/config objects.
func (m *Manager) resolvePlugins(ctx context.Context, settings plugin.Settings) (plugin.RuntimeSettings, error) {
	resolved := plugin.RuntimeSettings{Packages: map[string]plugin.RuntimePackage{}, Instances: []plugin.InstanceConfig{}}
	if len(settings.Packages) == 0 {
		return resolved, nil
	}
	entries, err := m.marketEntries(ctx, false)
	if err != nil {
		return resolved, err
	}
	for name, selection := range settings.Packages {
		var release *catalog.Entry
		for i := range entries {
			entry := &entries[i]
			if entry.ID == selection.CatalogID && entry.Package == name && entry.Version == selection.Version {
				release = entry
				break
			}
		}
		if release == nil {
			return resolved, errors.New("selected_plugin_release_unavailable")
		}
		resolved.Packages[name] = plugin.RuntimePackage{Version: selection.Version, CatalogID: release.ID, Source: release.Source, Builds: release.Builds}
		resolved.Instances = append(resolved.Instances, plugin.InstanceConfig{ID: release.ID, Package: name, Enabled: selection.Enabled})
	}
	sort.Slice(resolved.Instances, func(i, j int) bool { return resolved.Instances[i].ID < resolved.Instances[j].ID })
	return resolved, nil
}

func (m *Manager) pluginView(ctx context.Context, record Account, settings plugin.Settings) plugin.View {
	status := plugin.Status{Runtime: "unavailable", Packages: []plugin.PackageInfo{}, Instances: []plugin.Instance{}}
	if record.DesiredState == running && record.HostEndpoint != "" {
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := m.requestPlugins(readCtx, record, http.MethodGet, "", nil, &status); err != nil {
			status.Runtime = "unavailable"
			status.Error = "host_plugins_unavailable"
		}
	}
	releases, _ := m.marketEntries(ctx, false)
	entries := make([]plugin.CatalogEntry, 0, len(releases))
	for _, entry := range catalog.Latest(releases) {
		display := entry.CatalogEntry
		display.Icon = pluginAssetURL(display.Icon, entry.Version)
		display.Tutorials = append([]plugin.Tutorial{}, entry.Tutorials...)
		for i := range display.Tutorials {
			display.Tutorials[i].Video = pluginAssetURL(display.Tutorials[i].Video, entry.Version)
			if display.Tutorials[i].Poster != "" {
				display.Tutorials[i].Poster = pluginAssetURL(display.Tutorials[i].Poster, entry.Version)
			}
		}
		entries = append(entries, display)
		for i := range status.Packages {
			actual := &status.Packages[i]
			if actual.Name == entry.Package && actual.Present {
				actual.AvailableVersion = plugin.NewerVersion(actual.Version, entry.Version)
			}
		}
	}
	return plugin.View{Catalog: entries, Settings: settings.Clone(), Status: status}
}

func (m *Manager) savePlugins(w http.ResponseWriter, r *http.Request, id string, record Account, settings plugin.Settings, operation, name string) {
	if err := settings.Validate(); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := m.Store.SavePluginSettings(r.Context(), id, settings); err != nil {
		httpError(w, http.StatusServiceUnavailable, "plugin_settings_save_failed")
		return
	}
	if record.DesiredState == running && record.HostEndpoint != "" {
		resolved, resolveErr := m.resolvePlugins(r.Context(), settings)
		if resolveErr != nil {
			httpError(w, 422, resolveErr.Error())
			return
		}
		var err error
		if operation != "" {
			err = m.requestPlugins(r.Context(), record, http.MethodPost, operation, map[string]any{"name": name, "settings": resolved}, nil)
		} else {
			err = m.requestPlugins(r.Context(), record, http.MethodPut, "/settings", resolved, nil)
		}
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": settingsSavedHostApplyFailed, "message": err.Error(), "view": m.pluginView(r.Context(), record, settings)})
			return
		}
	}
	writeJSON(w, http.StatusOK, m.pluginView(r.Context(), record, settings))
}

func (m *Manager) pluginsHTTP(w http.ResponseWriter, r *http.Request, id string) {
	releases, marketErr := m.marketEntries(r.Context(), r.URL.Query().Get("refresh") == "true")
	if marketErr != nil {
		httpError(w, 503, "plugin_catalog_unavailable")
		return
	}

	if r.Method != http.MethodGet {
		lock := m.pluginLock(id)
		lock.Lock()
		defer lock.Unlock()
	}
	record, err := m.Store.Get(r.Context(), id)
	if errors.Is(err, ErrAccountNotFound) {
		httpError(w, 404, "account not found")
		return
	}
	if err != nil {
		httpError(w, 503, "plugin_settings_unavailable")
		return
	}
	settings := accountPlugins(record)
	suffix := strings.TrimPrefix(r.URL.EscapedPath(), plugin.SettingsPath)
	if r.Method == http.MethodGet && suffix == "" {
		writeJSON(w, 200, m.pluginView(r.Context(), record, settings))
		return
	}
	decode := func(value any) bool {
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		d.DisallowUnknownFields()
		if d.Decode(value) != nil || !errors.Is(d.Decode(new(any)), io.EOF) {
			httpError(w, 400, "invalid_plugin_request")
			return false
		}
		return true
	}
	// Clients choose reviewed packages and enabled state only.
	if r.Method == http.MethodPost && (suffix == "/packages" || (strings.HasPrefix(suffix, "/packages/") && strings.HasSuffix(suffix, "/update"))) {
		var input struct {
			PluginID string `json:"pluginId"`
		}
		if !decode(&input) {
			return
		}
		var release *catalog.Entry
		latest := catalog.Latest(releases)
		for i := range latest {
			if latest[i].ID == input.PluginID {
				release = &latest[i]
				break
			}
		}
		if release == nil {
			httpError(w, 404, "plugin_not_in_catalog")
			return
		}
		updating := suffix != "/packages"
		installed, exists := settings.Packages[release.Package]
		if updating {
			name, err := url.PathUnescape(strings.TrimSuffix(strings.TrimPrefix(suffix, "/packages/"), "/update"))
			if err != nil || name != release.Package || !exists || installed.CatalogID != release.ID {
				httpError(w, 404, "installed_plugin_not_found")
				return
			}
		} else if exists {
			httpError(w, 409, "plugin_package_already_installed")
			return
		}
		if record.DesiredState != running || record.HostEndpoint == "" {
			httpError(w, 409, "host_not_running")
			return
		}
		var info plugin.PackageInfo
		if err = m.requestPlugins(r.Context(), record, http.MethodPost, "/packages/inspect", map[string]any{"name": release.Package, "source": release.Source}, &info); err != nil {
			httpError(w, 422, err.Error())
			return
		}
		if info.Name != release.Package || info.Version != release.Version || info.API != "1" {
			httpError(w, 422, "incompatible_plugin_package")
			return
		}
		settings.Packages[release.Package] = plugin.PackageSelection{CatalogID: release.ID, Version: release.Version, Enabled: installed.Enabled}
		operation := ""
		if updating {
			operation = "/packages/reinstall"
		}
		m.savePlugins(w, r, id, record, settings, operation, release.Package)
		return
	}
	if r.Method == http.MethodPut && strings.HasPrefix(suffix, "/packages/") {
		name, err := url.PathUnescape(strings.TrimPrefix(suffix, "/packages/"))
		selection, exists := settings.Packages[name]
		if err != nil || !exists {
			http.NotFound(w, r)
			return
		}
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		if !decode(&input) {
			return
		}
		if input.Enabled == nil {
			httpError(w, 400, "enabled_is_required")
			return
		}
		selection.Enabled = *input.Enabled
		settings.Packages[name] = selection
		m.savePlugins(w, r, id, record, settings, "", "")
		return
	}
	if r.Method == http.MethodDelete && strings.HasPrefix(suffix, "/packages/") {
		name, err := url.PathUnescape(strings.TrimPrefix(suffix, "/packages/"))
		if err != nil || !plugin.ValidPackageName(name) {
			httpError(w, 400, "invalid_package_name")
			return
		}
		if _, ok := settings.Packages[name]; !ok {
			http.NotFound(w, r)
			return
		}
		delete(settings.Packages, name)
		m.savePlugins(w, r, id, record, settings, "/packages/remove", name)
		return
	}
	http.NotFound(w, r)
}

func (m *Manager) restartPluginsHTTP(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	lock := m.pluginLock(id)
	lock.Lock()
	defer lock.Unlock()
	record, err := m.Store.Get(r.Context(), id)
	if errors.Is(err, ErrAccountNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil || record.DesiredState != running || record.HostEndpoint == "" {
		httpError(w, http.StatusConflict, "host_not_running")
		return
	}
	var status plugin.Status
	// Apply saved settings as part of the restart. Applying them first can
	// repeat a failed Cordis load and prevent the recovery command itself.
	input := struct {
		Settings plugin.RuntimeSettings `json:"settings"`
	}{}
	input.Settings, err = m.resolvePlugins(r.Context(), accountPlugins(record))
	if err != nil {
		httpError(w, 422, err.Error())
		return
	}
	if err = m.requestPlugins(r.Context(), record, http.MethodPost, "/runtime/restart", input, &status); err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m.pluginView(r.Context(), record, accountPlugins(record)))
}
