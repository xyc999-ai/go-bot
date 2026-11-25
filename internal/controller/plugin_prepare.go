package controller

import (
	"go-bot/plugin"
	"net/http"
)

// Package preparation is a separate synchronous request. It never holds the
// account lock while downloading; navigation and model settings remain usable.
func (m *Manager) preparePluginsHTTP(w http.ResponseWriter, r *http.Request, id string) {
	lock := m.pluginLock(id)
	lock.Lock()
	defer lock.Unlock()
	record, err := m.Store.Get(r.Context(), id)
	if err != nil {
		httpError(w, 503, "plugin_settings_unavailable")
		return
	}
	if record.DesiredState != running || record.HostEndpoint == "" {
		httpError(w, 409, "host_not_running")
		return
	}
	settings := accountPlugins(record)
	resolved, err := m.resolvePlugins(r.Context(), settings)
	if err != nil {
		httpError(w, 422, err.Error())
		return
	}
	var status plugin.Status
	if err := m.requestPlugins(r.Context(), record, http.MethodPut, "/settings", resolved, &status); err != nil {
		httpError(w, 422, err.Error())
		return
	}
	writeJSON(w, 200, m.pluginView(r.Context(), record, settings))
}
