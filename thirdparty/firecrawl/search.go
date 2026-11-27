// Package firecrawl contains the wire contract for Firecrawl Search.
package firecrawl

import (
	"context"
	"net/http"

	"go-bot/thirdparty/providerhttp"
)

const Endpoint = "https://api.firecrawl.dev/v2/search"

type Result struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type Data struct {
	Web []Result `json:"web"`
}

type Response struct {
	Data *Data `json:"data"`
}

func Search(ctx context.Context, client *http.Client, endpoint, key, query string, count int) (Response, int, error) {
	input := struct {
		Query   string   `json:"query"`
		Limit   int      `json:"limit"`
		Sources []string `json:"sources"`
		Origin  string   `json:"origin"`
	}{Query: query, Limit: count, Sources: []string{"web"}, Origin: "go-bot"}
	var output Response
	status, err := providerhttp.Post(ctx, client, endpoint, "Authorization", "Bearer "+key, input, &output, 4<<20)
	return output, status, err
}
