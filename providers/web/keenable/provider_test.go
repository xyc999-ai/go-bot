package keenable

import (
	"testing"

	api "go-bot/thirdparty/keenable"
	"go-bot/web"
)

func TestNewPicksTheEndpointByKey(t *testing.T) {
	for _, tc := range []struct{ key, endpoint string }{
		{"", api.PublicEndpoint},
		{"fixture-secret", api.Endpoint},
	} {
		provider, err := New(web.Options{APIKey: tc.key})
		if err != nil {
			t.Fatal(err)
		}
		if got := provider.(*Provider).endpoint; got != tc.endpoint {
			t.Errorf("key %q: endpoint %q, want %q", tc.key, got, tc.endpoint)
		}
	}
	if api.PublicEndpoint != "https://api.keenable.ai/v1/search/public" || api.Endpoint != "https://api.keenable.ai/v1/search" {
		t.Fatal("Keenable endpoints changed")
	}
}
