package controller

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"go-bot/internal/providersettings"
	"go-bot/internal/websearchsettings"
	imageproviders "go-bot/providers/image"
	webproviders "go-bot/providers/web"
)

const webSearchSettingsPath = "/v1/settings/web-search"
const imageSettingsPath = "/v1/settings/image-generation"

type providerSettingsRoute struct {
	defaultID, internalPath, redisField string
	catalog                             func() []providersettings.Descriptor
	field                               func(*Account) **providersettings.Settings
}

var searchSettings = providerSettingsRoute{webproviders.Default, websearchsettings.InternalPath, webSearchField, webproviders.Catalog,
	func(a *Account) **providersettings.Settings { return &a.WebSearch }}
var imageSettings = providerSettingsRoute{imageproviders.Default, imageproviders.InternalPath, imageGenerationField, imageproviders.Catalog,
	func(a *Account) **providersettings.Settings { return &a.ImageGeneration }}

func (m *Manager) syncProviderSettings(ctx context.Context, account Account, route providerSettingsRoute) error {
	settings := *route.field(&account)
	if settings == nil {
		return nil
	}
	code, err := m.requestHost(ctx, account, http.MethodPut, route.internalPath, settings.Current(route.defaultID), nil)
	if err != nil || code != http.StatusNoContent {
		return errors.New("host_provider_settings_unavailable")
	}
	return nil
}

func (m *Manager) providerSettingsHTTP(w http.ResponseWriter, r *http.Request, id string, route providerSettingsRoute) {
	ctx := r.Context()
	lock := m.accountLock(id)
	lock.Lock()
	defer lock.Unlock()
	account, err := m.Store.Get(ctx, id)
	switch {
	case errors.Is(err, ErrAccountNotFound):
		httpError(w, http.StatusNotFound, "account not found")
		return
	case err != nil:
		httpError(w, http.StatusServiceUnavailable, "provider_settings_unavailable")
		return
	}
	field := route.field(&account)
	var settings providersettings.Settings
	if *field != nil {
		settings = **field
	}
	catalog := route.catalog()
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut, http.MethodDelete:
		var change struct {
			Provider string  `json:"provider"`
			APIKey   *string `json:"apiKey"`
			Model    string  `json:"model"`
			Activate *bool   `json:"activate"`
		}
		change.Provider = r.URL.Query().Get("provider")
		if r.Method == http.MethodPut && decodeJSON(http.MaxBytesReader(w, r.Body, 8<<10), &change) != nil {
			httpError(w, http.StatusBadRequest, "invalid_provider_settings")
			return
		}
		if change.Provider == "" {
			change.Provider = route.defaultID
		}
		index := slices.IndexFunc(catalog, func(p providersettings.Descriptor) bool { return p.ID == change.Provider })
		if index < 0 {
			httpError(w, http.StatusBadRequest, "unsupported_provider")
			return
		}
		entry := settings.Entry(change.Provider, route.defaultID)
		activate := r.Method == http.MethodPut && (change.Activate == nil || *change.Activate)
		if r.Method == http.MethodDelete {
			entry.APIKey = ""
		} else {
			if catalog[index].DefaultModel != "" {
				if change.Model != "" || change.Activate == nil {
					entry.Model = strings.TrimSpace(change.Model)
				}
				if !providersettings.ValidModel(entry.Model) {
					httpError(w, http.StatusBadRequest, "invalid_image_model")
					return
				}
			} else if change.Model != "" || (change.APIKey == nil && entry.APIKey == "" && !catalog[index].KeyOptional) {
				httpError(w, http.StatusBadRequest, "invalid_web_search_key")
				return
			}
			if change.APIKey != nil {
				entry.APIKey = strings.TrimSpace(*change.APIKey)
				if entry.APIKey == "" || !providersettings.ValidAPIKey(entry.APIKey) {
					httpError(w, http.StatusBadRequest, "invalid_provider_key")
					return
				}
			}
		}
		if activate && change.Activate != nil && entry.APIKey == "" && !catalog[index].KeyOptional {
			httpError(w, http.StatusBadRequest, "provider_key_required")
			return
		}
		settings = settings.WithEntry(change.Provider, route.defaultID, entry, activate)
		*field = &settings
		if err := m.Store.SaveProviderSettings(ctx, id, route.redisField, settings); err != nil {
			httpError(w, http.StatusServiceUnavailable, "provider_settings_unavailable")
			return
		}
		if settings.Current(route.defaultID).Provider == change.Provider && account.DesiredState == running && account.HostEndpoint != "" && m.syncProviderSettings(ctx, account, route) != nil {
			httpError(w, http.StatusServiceUnavailable, settingsSavedHostApplyFailed)
			return
		}
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	entries := make(map[string]map[string]any)
	for _, p := range catalog {
		entry := settings.Entry(p.ID, route.defaultID)
		view := map[string]any{"hasApiKey": entry.APIKey != ""}
		if entry.Model != "" {
			view["model"] = entry.Model
		}
		if _, saved := settings.Entries[p.ID]; saved || entry.APIKey != "" {
			entries[p.ID] = view
		}
	}
	current := settings.Current(route.defaultID)
	writeJSON(w, http.StatusOK, map[string]any{
		"defaultProvider": route.defaultID,
		"provider":        current.Provider,
		"providers":       catalog,
		"entries":         entries,
		"hasApiKey":       current.APIKey != "",
	})
}
