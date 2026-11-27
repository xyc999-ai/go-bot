package host

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebSearchSettingsSwitchLiveProviders(t *testing.T) {
	var host hostWebSearch
	apply := func(body string, status int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/internal/host/web-search", strings.NewReader(body))
		out := httptest.NewRecorder()
		host.handle(out, req)
		if out.Code != status {
			t.Fatalf("apply settings: got HTTP %d, want %d", out.Code, status)
		}
	}
	apply(`{"provider":"zhipu","apiKey":"fixture-key"}`, 204)
	if host.provider == nil || host.provider.Name() != "zhipu" {
		t.Fatal("Zhipu provider was not installed")
	}
	apply(`{"provider":"doubao","apiKey":"fixture-key"}`, 204)
	if host.provider == nil || host.provider.Name() != "doubao" {
		t.Fatal("Doubao did not replace Zhipu")
	}
	apply(`{"provider":"unknown","apiKey":"fixture-key"}`, 400)
	if host.provider.Name() != "doubao" {
		t.Fatal("invalid settings replaced the selected provider")
	}
	apply(`{"provider":"doubao","apiKey":""}`, 204)
	if host.configured() {
		t.Fatal("removing the active key kept a live search client")
	}
}

func TestWebSearchSettingsInstallKeyOptionalProviderWithoutKey(t *testing.T) {
	var host hostWebSearch
	apply := func(body string, status int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/internal/host/web-search", strings.NewReader(body))
		out := httptest.NewRecorder()
		host.handle(out, req)
		if out.Code != status {
			t.Fatalf("apply settings: got HTTP %d, want %d", out.Code, status)
		}
	}
	apply(`{"provider":"keenable","apiKey":""}`, 204)
	if host.provider == nil || host.provider.Name() != "keenable" {
		t.Fatal("Keenable was not installed without a key")
	}
	apply(`{"provider":"tavily","apiKey":""}`, 204)
	if host.configured() {
		t.Fatal("a provider that needs a key was installed without one")
	}
}
