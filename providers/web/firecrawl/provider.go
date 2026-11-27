package firecrawl

import (
	"context"
	"errors"
	"net/http"
	"time"

	"go-bot/internal/providersettings"
	"go-bot/providers/web/internal/standard"
	api "go-bot/thirdparty/firecrawl"
	"go-bot/thirdparty/providerhttp"
	"go-bot/web"
)

const ID = "firecrawl"
const name = "Firecrawl"

type Provider struct {
	endpoint, key string
	http          *http.Client
}

func New(options web.Options) (web.SearchProvider, error) {
	if options.APIKey == "" || !providersettings.ValidAPIKey(options.APIKey) {
		return nil, errors.New("invalid Firecrawl API key")
	}
	endpoint := options.BaseURL
	if endpoint == "" {
		endpoint = api.Endpoint
	}
	return &Provider{endpoint, options.APIKey, providerhttp.New(30 * time.Second)}, nil
}

func (*Provider) Name() string { return ID }

func (p *Provider) Search(ctx context.Context, request web.SearchRequest) (web.SearchResult, error) {
	query := web.CollapseSpace(request.Query)
	if query == "" {
		return web.SearchResult{}, &web.Error{Code: "WEB_INVALID_QUERY", Message: "Firecrawl requires a non-empty search query."}
	}
	count := standard.Count(request)
	response, status, err := api.Search(ctx, p.http, p.endpoint, p.key, query, count)
	if err := standard.Failed(name, status, err); err != nil {
		return web.SearchResult{}, err
	}
	if response.Data == nil {
		return web.SearchResult{}, standard.Invalid(name)
	}
	result := web.SearchResult{Sources: make([]web.SearchSource, 0, min(count, len(response.Data.Web)))}
	for _, row := range response.Data.Web {
		if source, ok := standard.Source(row.URL, row.Title, row.Description, "", ""); ok {
			result.Sources = append(result.Sources, source)
			if len(result.Sources) == count {
				break
			}
		}
	}
	return result, nil
}
