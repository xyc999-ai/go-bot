package host

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"

	"go-bot/internal/providersettings"
	webproviders "go-bot/providers/web"
	"go-bot/web"
)

type hostWebSearch struct {
	mu             sync.RWMutex
	provider       web.SearchProvider
	selection      providersettings.Selection
	defaultFactory web.Factory
}

func (s *hostWebSearch) configured() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.provider != nil
}

func (s *hostWebSearch) Search(ctx context.Context, request web.SearchRequest) (web.SearchResult, error) {
	s.mu.RLock()
	provider := s.provider
	s.mu.RUnlock()
	if provider == nil {
		return web.SearchResult{}, &web.Error{Code: "WEB_SEARCH_NOT_CONFIGURED",
			Message: "Web search is not configured. Add a search API key in Web search settings."}
	}
	return provider.Search(ctx, request)
}

func (s *hostWebSearch) handle(w http.ResponseWriter, r *http.Request) {
	settings, ok := decodeProviderSelection(w, r)
	if !ok {
		return
	}
	if settings.Provider == "" {
		settings.Provider = webproviders.Default
	}
	factory, supported := webproviders.Lookup(settings.Provider)
	if !supported || settings.Model != "" {
		http.Error(w, "invalid web search settings", http.StatusBadRequest)
		return
	}
	if settings.Provider == webproviders.Default {
		factory = s.defaultFactory
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.selection == settings {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var provider web.SearchProvider
	if settings.APIKey != "" || webproviders.KeyOptional(settings.Provider) {
		var err error
		provider, err = factory(web.Options{APIKey: settings.APIKey})
		if err != nil {
			http.Error(w, "invalid web search settings", http.StatusBadRequest)
			return
		}
	}
	s.provider, s.selection = provider, settings
	w.WriteHeader(http.StatusNoContent)
}

func decodeProviderSelection(w http.ResponseWriter, r *http.Request) (providersettings.Selection, bool) {
	var settings providersettings.Selection
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", http.MethodPut)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return settings, false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&settings) != nil || decoder.Decode(new(any)) != io.EOF || !providersettings.ValidAPIKey(settings.APIKey) {
		http.Error(w, "invalid provider settings", http.StatusBadRequest)
		return settings, false
	}
	return settings, true
}
