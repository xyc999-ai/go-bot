package controller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"go-bot/internal/modelsettings"
	proactive "go-bot/internal/proactivesettings"
)

func accountProactive(account Account) proactive.Settings {
	if account.Proactive == nil {
		return proactive.Settings{Enabled: false}
	}
	return *account.Proactive
}

func (m *Manager) syncProactive(ctx context.Context, account Account) error {
	if account.MainSessionID == "" {
		return nil // Main identity has not been provisioned for this account.
	}
	settings := accountProactive(account)
	code, err := m.requestHost(ctx, account, http.MethodPut, proactive.InternalPath, map[string]any{
		"enabled": settings.Enabled, "mainSessionId": account.MainSessionID,
	}, nil)
	if err != nil || code != http.StatusNoContent {
		return errors.New("host_proactive_settings_unavailable")
	}
	return nil
}

func (m *Manager) proactiveSettingsHTTP(w http.ResponseWriter, r *http.Request, id string) {
	lock := m.accountLock(id)
	lock.Lock()
	defer lock.Unlock()
	account, err := m.Store.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrAccountNotFound) {
			httpError(w, 404, "account not found")
		} else {
			httpError(w, 503, "proactive_settings_unavailable")
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut:
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) || input.Enabled == nil {
			httpError(w, 400, "invalid_proactive_settings")
			return
		}
		settings := proactive.Settings{Enabled: *input.Enabled}
		if settings.Enabled {
			state, err := m.Store.LoadModels(r.Context(), id)
			if err != nil {
				httpError(w, 503, "model_settings_unavailable")
				return
			}
			// Match the background Agent: an explicit main-chat selection wins.
			// Only an absent selection may use the account default.
			choice, exists := state.Sessions[account.MainSessionID]
			if !exists {
				choice = state.Catalog.Default()
			}
			if _, err := state.Catalog.Resolve(choice); err != nil {
				code := "model_selection_unavailable"
				if errors.Is(err, modelsettings.ErrUnconfigured) {
					code = "model_not_configured"
				}
				httpError(w, http.StatusConflict, code)
				return
			}
		}
		if err := m.Store.SaveProactiveSettings(r.Context(), id, settings); err != nil {
			httpError(w, 503, "proactive_settings_unavailable")
			return
		}
		account.Proactive = &settings
		if account.DesiredState == running && account.HostEndpoint != "" {
			if err := m.syncProactive(r.Context(), account); err != nil {
				httpError(w, 503, settingsSavedHostApplyFailed)
				return
			}
		}
	default:
		w.Header().Set("Allow", "GET, PUT")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	settings := accountProactive(account)
	status := proactive.Status{Enabled: settings.Enabled, Phase: "host-stopped"}
	if account.DesiredState == running && account.HostEndpoint != "" {
		code, err := m.requestHost(r.Context(), account, http.MethodGet, proactive.InternalPath, nil, &status)
		if err != nil || code != http.StatusOK {
			status = proactive.Status{Phase: "unavailable", Error: "host_state_unavailable"}
		}
	}
	status.Enabled = settings.Enabled
	writeJSON(w, http.StatusOK, status)
}
