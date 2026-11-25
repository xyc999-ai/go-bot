package controller

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"go-bot/internal/modelsettings"
	proactive "go-bot/internal/proactivesettings"
	mcpconfig "go-bot/mcp"
	"go-bot/plugin"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

const UserHeader = "X-User-ID"

const hostFilesPath = "/v1/host/files"
const hostStorageHeader = "X-Harness-Storage"
const hostStorageSourceHeader = "X-Harness-Storage-Source"

func decodeJSON(reader io.Reader, value any) error {
	dec := json.NewDecoder(reader)
	if err := dec.Decode(value); err != nil {
		return err
	}
	if dec.Decode(new(any)) != io.EOF {
		return errors.New("expected one JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func httpError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func (m *Manager) Handler() http.Handler {
	return m.HandlerWithWeb(WebOptions{})
}

func (m *Manager) HandlerWithWeb(web WebOptions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The provider redirects without an account header and from another
		// origin. Only this exact GET callback uses its one-time Host state;
		// ordinary APIs retain their existing browser-origin checks.
		if strings.HasPrefix(r.URL.Path, mcpconfig.OAuthCallbackPath) {
			m.mcpOAuthCallbackHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, plugin.AuthorizationCallbackPath) {
			m.pluginAuthorizationCallbackHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			w.Header().Add("Vary", "Origin")
			w.Header().Add("Vary", "Sec-Fetch-Site")
			if !controllerBrowserOrigin(w, r, web.DevOrigin) {
				httpError(w, http.StatusForbidden, "Controller only accepts same-origin browser requests")
				return
			}
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if m.servePluginAsset(w, r) {
			return
		}
		if serveWeb(w, r, web.Files) {
			return
		}
		if r.URL.Path == "/v1/accounts" && r.Method == http.MethodPost {
			m.createAccountHTTP(w, r)
			return
		}
		isHost := r.URL.Path == "/v1/host" || strings.HasPrefix(r.URL.Path, "/v1/host/") || strings.HasPrefix(r.URL.Path, "/v1/hosts/")
		isSession := r.URL.Path == "/v1/capabilities" || r.URL.Path == "/v1/sessions" || strings.HasPrefix(r.URL.Path, "/v1/sessions/")
		isPlugins := r.URL.Path == plugin.SettingsPath || strings.HasPrefix(r.URL.Path, plugin.SettingsPath+"/")
		isSettings := isPlugins || r.URL.Path == settingsPath || strings.HasPrefix(r.URL.Path, settingsPath+"/") || r.URL.Path == webSearchSettingsPath || r.URL.Path == imageSettingsPath || r.URL.Path == proactive.SettingsPath
		if !isHost && !isSession && !isSettings && r.URL.Path != "/v1/clients" {
			http.NotFound(w, r)
			return
		}
		id := r.Header.Get(UserHeader)
		if !userName.MatchString(id) {
			httpError(w, http.StatusBadRequest, "provide a valid X-User-ID")
			return
		}
		// Keep the actual, validated public callback with this request. Invalid
		// or ordinary non-loopback HTTP origins do not enable OAuth, but still
		// support normal Host operations and static credentials.
		redirect, _ := mcpOAuthRedirect(r, r.Header.Get(mcpconfig.ControllerOriginHeader), id)
		r = r.WithContext(withOAuthCallback(r.Context(), redirect))
		switch {
		case r.URL.Path == "/v1/clients":
			m.clientsHTTP(w, r, id)
		case r.URL.Path == plugin.AuthorizationPath || strings.HasPrefix(r.URL.Path, plugin.AuthorizationPath+"/"):
			m.authorizationHTTP(w, r, id)
		case r.URL.Path == mcpconfig.RuntimePath || strings.HasPrefix(r.URL.Path, mcpconfig.RuntimePath+"/"):
			m.mcpHTTP(w, r, id)
		case isPlugins:
			m.pluginsHTTP(w, r, id)
		case r.URL.Path == "/v1/host/plugins/runtime/restart":
			m.restartPluginsHTTP(w, r, id)
		case isHost:
			m.hostHTTP(w, r, id)
		case r.URL.Path == webSearchSettingsPath:
			m.providerSettingsHTTP(w, r, id, searchSettings)
		case r.URL.Path == proactive.SettingsPath:
			m.proactiveSettingsHTTP(w, r, id)
		case r.URL.Path == imageSettingsPath:
			m.providerSettingsHTTP(w, r, id, imageSettings)
		case isSettings:
			m.modelSettingsHTTP(w, r, id)
		case r.URL.Path == "/v1/sessions" && r.Method == http.MethodPost:
			m.createSessionHTTP(w, r, id)
		default:
			sessionID, action, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v1/sessions/"), "/")
			if action == "model" && uuid.Validate(sessionID) == nil {
				m.sessionModelHTTP(w, r, id, sessionID)
				return
			}
			m.forward(w, r, id)
		}
	})
}

func (m *Manager) createAccountHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct {
		UserID string `json:"userId"`
	}
	if err := decodeJSON(http.MaxBytesReader(w, r.Body, 16<<10), &input); err != nil {
		httpError(w, http.StatusBadRequest, "invalid account request")
		return
	}
	if err := m.CreateAccount(r.Context(), input.UserID); err != nil {
		switch {
		case errors.Is(err, ErrAccountExists):
			httpError(w, http.StatusConflict, "account already exists")
		case errors.Is(err, errInvalidAccount):
			httpError(w, http.StatusBadRequest, err.Error())
		default:
			httpError(w, http.StatusServiceUnavailable, "could not save account")
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"userId": input.UserID})
}

func (m *Manager) hostHTTP(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	var err error
	switch {
	case (r.URL.Path == hostFilesPath && r.Method == http.MethodGet) ||
		(r.URL.Path == "/v1/host/stream" && r.Method == http.MethodGet) ||
		r.URL.Path == "/v1/host/schedules" || strings.HasPrefix(r.URL.Path, "/v1/host/schedules/"):
		m.forward(w, r, id)
		return
	case r.URL.Path == "/v1/host" && r.Method == http.MethodGet:
		var status HostStatus
		status, err = m.Observe(ctx, id)
		if err == nil {
			writeJSON(w, http.StatusOK, status)
			return
		}
	case strings.HasPrefix(r.URL.Path, "/v1/hosts/") && r.Method == http.MethodGet:
		hostID := strings.TrimPrefix(r.URL.Path, "/v1/hosts/")
		var record Account
		record, err = m.Store.Get(ctx, id)
		if err == nil {
			if hostID == "" || record.HostID != hostID {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, http.StatusOK, m.hostStatus(ctx, record))
			return
		}
	case r.URL.Path == "/v1/host/plugins/prepare" && r.Method == http.MethodPost:
		m.preparePluginsHTTP(w, r, id)
		return
	case r.URL.Path == "/v1/host/start" && r.Method == http.MethodPost:
		var input struct {
			WelcomeLocale string `json:"welcomeLocale"`
		}
		if r.Body != nil {
			if decodeErr := decodeJSON(http.MaxBytesReader(w, r.Body, 1024), &input); decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
				httpError(w, 400, "invalid_host_start")
				return
			}
		}
		if input.WelcomeLocale != "" && input.WelcomeLocale != "en" && input.WelcomeLocale != "zh-CN" {
			httpError(w, 400, "invalid_welcome_locale")
			return
		}
		err = m.Ensure(ctx, id)
		if err == nil {
			record, readErr := m.Store.Get(ctx, id)
			if readErr != nil {
				err = readErr
			} else if record.MainSessionID != "" {
				code, prepareErr := m.requestHost(ctx, record, http.MethodPost, "/internal/host/sessions/main", map[string]string{"sessionId": record.MainSessionID, "locale": input.WelcomeLocale}, nil)
				if prepareErr != nil || code != http.StatusOK {
					err = errors.New("main_session_unavailable")
				} else {
					writeJSON(w, http.StatusOK, map[string]string{"mainSessionId": record.MainSessionID})
					return
				}
			} else {
				err = errors.New("main_session_unconfigured")
			}
		}
	case r.URL.Path == "/v1/host/stop" && r.Method == http.MethodPost:
		err = m.Stop(ctx, id)
	case r.URL.Path == "/v1/host/instance" && r.Method == http.MethodDelete:
		err = m.Delete(ctx, id)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrAccountNotFound):
			httpError(w, http.StatusNotFound, "account not found")
		case errors.Is(err, context.DeadlineExceeded):
			httpError(w, http.StatusGatewayTimeout, "Host operation could not be confirmed before the deadline")
		default:
			httpError(w, http.StatusServiceUnavailable, "Host operation could not be completed")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Manager) forward(w http.ResponseWriter, r *http.Request, id string) {
	var identity *ClientIdentity
	if r.Method == http.MethodGet && r.URL.Path == "/v1/host/stream" {
		var err error
		identity, err = clientIdentity(r)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
	}
	var stopPresence func()
	defer func() {
		if stopPresence != nil {
			stopPresence()
		}
	}()
	lock := m.accountLock(id)
	lock.Lock()
	record, err := m.Store.Get(r.Context(), id)
	if err == nil && (record.DesiredState != running || record.HostEndpoint == "" || record.HostID == "") {
		err = errHostUnavailable
	}
	if err == nil && r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/sessions/") {
		sessionID, action, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v1/sessions/"), "/")
		if action == "messages" {
			state, loadErr := m.Store.LoadModels(r.Context(), id)
			if loadErr != nil {
				lock.Unlock()
				httpError(w, http.StatusServiceUnavailable, "model_settings_unavailable")
				return
			}
			if !m.hostHasModelRevision(r.Context(), record, state.Revision) {
				lock.Unlock()
				httpError(w, http.StatusServiceUnavailable, "host_model_settings_unavailable")
				return
			}
			if uuid.Validate(sessionID) != nil {
				lock.Unlock()
				http.NotFound(w, r)
				return
			}
			choice, _, choiceErr := m.sessionModelSelection(r.Context(), record, state, sessionID)
			if choiceErr != nil {
				lock.Unlock()
				httpError(w, http.StatusServiceUnavailable, "session_model_unavailable")
				return
			}
			if choiceErr == nil && choice == (modelsettings.Selection{}) && sessionID == record.MainSessionID {
				choice = state.Catalog.Default()
				if _, valid := state.Catalog.Resolve(choice); valid == nil {
					revision, saveErr := m.Store.SaveSessionModel(r.Context(), id, sessionID, choice)
					if saveErr != nil {
						lock.Unlock()
						httpError(w, 503, "model_settings_write_failed")
						return
					}
					state.Sessions[sessionID], state.Revision = choice, revision
					if m.syncModels(r.Context(), record, state) != nil {
						lock.Unlock()
						httpError(w, 503, "host_model_settings_unavailable")
						return
					}
				}
			}
			if _, routeErr := state.Catalog.Resolve(choice); routeErr != nil {
				code, existsErr := m.requestHost(r.Context(), record, http.MethodGet, "/internal/host/sessions/"+sessionID+"/exists", nil, nil)
				lock.Unlock()
				if code == http.StatusNotFound {
					http.NotFound(w, r)
					return
				}
				if existsErr != nil || code != http.StatusNoContent {
					httpError(w, http.StatusServiceUnavailable, "session_unavailable")
					return
				}
				httpError(w, http.StatusConflict, routeErr.Error())
				return
			}
		}
	}

	lock.Unlock()
	if err != nil {
		if errors.Is(err, ErrAccountNotFound) {
			httpError(w, http.StatusNotFound, "account not found")
		} else {
			httpError(w, http.StatusServiceUnavailable, "Host is unavailable; reopen the account")
		}
		return
	}
	target, err := url.Parse(record.HostEndpoint)
	if err != nil || target.Scheme != "http" || target.Host == "" {
		httpError(w, http.StatusServiceUnavailable, "invalid Host endpoint")
		return
	}
	proxy := &httputil.ReverseProxy{
		FlushInterval: -1,
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(target)
			req.Out.Header.Del(UserHeader)
			req.Out.Header.Del(ClientHeader)
			req.Out.Header.Del(mcpconfig.ControllerOriginHeader)
			req.Out.Header.Del(mcpconfig.OAuthCallbackHeader)
			if redirect := oauthCallbackFromContext(req.In.Context()); redirect != "" {
				req.Out.Header.Set(mcpconfig.OAuthCallbackHeader, redirect)
			}
			req.Out.Header.Set("Authorization", "Bearer "+record.HostToken)
		},
		ModifyResponse: func(response *http.Response) error {
			if identity != nil && response.StatusCode == http.StatusOK && strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
				stopPresence = m.trackClient(r.Context(), id, *identity)
			}
			if r.URL.Path == hostFilesPath && response.StatusCode == http.StatusOK {
				kind, source := m.hostFileStorage(id, record)
				response.Header.Set(hostStorageHeader, kind)
				if source != "" {
					response.Header.Set(hostStorageSourceHeader, source)
				} else {
					response.Header.Del(hostStorageSourceHeader)
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, request *http.Request, _ error) {
			ctx := request.Context()
			if ctx.Err() == nil {
				lock.Lock()
				current, err := m.Store.Get(ctx, id)
				if err == nil && current.DesiredState == running && current.HostID == record.HostID &&
					current.HostEndpoint == record.HostEndpoint && m.probe(ctx, current.HostEndpoint, current.HostToken) != nil {
					current.HostEndpoint = ""
					_ = m.Store.Save(ctx, id, current)
				}
				lock.Unlock()
			}
			httpError(w, http.StatusBadGateway, "Host connection failed; the request was not replayed")
		},
	}
	proxy.ServeHTTP(w, r)
}
