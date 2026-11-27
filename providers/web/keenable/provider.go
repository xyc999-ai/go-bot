package keenable

import (
	"context"
	"errors"
	"net/http"
	"time"

	"go-bot/internal/providersettings"
	"go-bot/providers/web/internal/standard"
	api "go-bot/thirdparty/keenable"
	"go-bot/thirdparty/providerhttp"
	"go-bot/web"
)

const ID = "keenable"
const name = "Keenable"

type Provider struct {
	endpoint, key string
	http          *http.Client
}

// New accepts an empty key: Keenable then uses its keyless endpoint. A saved
// key moves the requests to the keyed endpoint, which only raises rate limits.
func New(options web.Options) (web.SearchProvider, error) {
	if !providersettings.ValidAPIKey(options.APIKey) {
		return nil, errors.New("invalid Keenable API key")
	}
	endpoint := options.BaseURL
	if endpoint == "" {
		endpoint = api.PublicEndpoint
		if options.APIKey != "" {
			endpoint = api.Endpoint
		}
	}
	return &Provider{endpoint, options.APIKey, providerhttp.New(30 * time.Second)}, nil
}

func (*Provider) Name() string { return ID }

func (p *Provider) Search(ctx context.Context, request web.SearchRequest) (web.SearchResult, error) {
	query := web.CollapseSpace(request.Query)
	if query == "" {
		return web.SearchResult{}, &web.Error{Code: "WEB_INVALID_QUERY", Message: "Keenable requires a non-empty search query."}
	}
	count := standard.Count(request)
	response, status, err := api.Search(ctx, p.http, p.endpoint, p.key, query, count)
	if err := standard.Failed(name, status, err); err != nil {
		return web.SearchResult{}, err
	}
	if response.Results == nil {
		return web.SearchResult{}, standard.Invalid(name)
	}
	result := web.SearchResult{Sources: make([]web.SearchSource, 0, min(count, len(response.Results)))}
	for _, row := range response.Results {
		// Keenable's snippet is page text, often several thousand characters,
		// so it becomes the source summary, which the web_search tool bounds.
		if source, ok := standard.Source(row.URL, row.Title, "", row.Snippet, ""); ok {
			result.Sources = append(result.Sources, source)
			if len(result.Sources) == count {
				break
			}
		}
	}
	return result, nil
}
