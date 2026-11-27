// Package keenable contains the wire contract for Keenable Search.
package keenable

import (
	"context"
	"net/http"

	"go-bot/thirdparty/providerhttp"
)

// Endpoint is the keyed search API. PublicEndpoint serves the same search
// without a key, at a lower rate limit.
const (
	Endpoint       = "https://api.keenable.ai/v1/search"
	PublicEndpoint = "https://api.keenable.ai/v1/search/public"
)

// AppTitle names the calling app; the keyless endpoint requires it.
const AppTitle = "Go Bot"

type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

type Response struct {
	Results []Result `json:"results"`
}

// Search sends X-API-Key only when key is set.
func Search(ctx context.Context, client *http.Client, endpoint, key, query string, count int) (Response, int, error) {
	input := struct {
		Query string `json:"query"`
		Count int    `json:"max_results"`
	}{Query: query, Count: count}
	headers := map[string]string{"X-Keenable-Title": AppTitle}
	if key != "" {
		headers["X-API-Key"] = key
	}
	var output Response
	status, err := providerhttp.PostHeaders(ctx, client, endpoint, headers, input, &output, 4<<20)
	return output, status, err
}
