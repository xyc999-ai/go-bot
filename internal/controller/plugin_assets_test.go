package controller

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-bot/extensions/catalog"
	"go-bot/plugin"
)

func assetFixture(t *testing.T) http.Handler {
	t.Helper()
	var icon bytes.Buffer
	_ = png.Encode(&icon, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	video := make([]byte, 128)
	copy(video[4:8], "ftyp")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := icon.Bytes()
		if strings.HasSuffix(r.URL.Path, ".mp4") {
			data = video
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, r.URL.Path, time.Time{}, bytes.NewReader(data))
	}))
	t.Cleanup(server.Close)
	entry := testPluginCatalog()[0]
	entry.AssetBase = server.URL + "/"
	entry.Tutorials = []plugin.Tutorial{{Language: "en", Title: "Setup", Video: "/assets/plugins/agent-browser/setup.en.mp4"}}
	m := &Manager{pluginCatalog: []catalog.Entry{entry}, Catalog: catalog.NewRemote(server.URL)}
	return m.Handler()
}
func TestPluginManifestIconsAreServedWithoutAHost(t *testing.T) {
	handler := assetFixture(t)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, "/assets/plugins/agent-browser/icon.png?v=0.1.0", nil))
		if response.Code != 200 || response.Header().Get("Content-Type") != "image/png" {
			t.Fatal(response.Code, response.Header())
		}
		if method == http.MethodGet {
			if _, err := png.Decode(response.Body); err != nil {
				t.Fatal(err)
			}
		} else if response.Body.Len() != 0 {
			t.Fatal("HEAD returned a body")
		}
	}
	for _, path := range []string{"/assets/plugins/missing/icon.png", "/assets/plugins/agent-browser/../package.json", "/assets/plugins/agent-browser/private.json", "/assets/plugins/agent-browser/icon.png?v=9.0.0"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 404 {
			t.Fatal(path, response.Code)
		}
	}
}
func TestPluginTutorialVideoSupportsNativePlayerRequests(t *testing.T) {
	handler := assetFixture(t)
	request := httptest.NewRequest("GET", "/assets/plugins/agent-browser/setup.en.mp4?v=0.1.0", nil)
	request.Header.Set("Range", "bytes=0-31")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 206 || response.Body.Len() != 32 || response.Header().Get("Content-Range") != "bytes 0-31/128" || response.Header().Get("Content-Type") != "video/mp4" {
		t.Fatal(response.Code, response.Header())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("HEAD", "/assets/plugins/agent-browser/setup.en.mp4?v=0.1.0", nil))
	if response.Code != 200 || response.Body.Len() != 0 || response.Header().Get("Content-Length") != "128" {
		t.Fatal(response.Code, response.Header())
	}
}
