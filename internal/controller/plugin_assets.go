package controller

import (
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"go-bot/extensions/catalog"
)

func pluginAssetURL(path, version string) string { return path + "?v=" + url.QueryEscape(version) }

// Resources are available before installation. Clients can select only an
// asset declared by a reviewed release, never an arbitrary download URL.
func (m *Manager) servePluginAsset(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, catalog.AssetPrefix) {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(405)
		return true
	}
	id, _, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, catalog.AssetPrefix), "/")
	if !ok {
		http.NotFound(w, r)
		return true
	}
	entries, err := m.marketEntries(r.Context(), false)
	if err != nil {
		httpError(w, 503, "plugin_catalog_unavailable")
		return true
	}
	if r.URL.Query().Get("v") == "" {
		entries = catalog.Latest(entries)
	}
	for _, entry := range entries {
		if entry.ID != id || r.URL.Query().Get("v") != "" && entry.Version != r.URL.Query().Get("v") {
			continue
		}
		allowed := r.URL.Path == entry.Icon
		for _, tutorial := range entry.Tutorials {
			allowed = allowed || r.URL.Path == tutorial.Video || r.URL.Path == tutorial.Poster
		}
		if !allowed {
			continue
		}
		name := strings.TrimPrefix(r.URL.Path, catalog.AssetPrefix+id+"/")
		if strings.ContainsAny(name, "/\\?#") || name == "" || name == "." || name == ".." {
			continue
		}
		request, e := http.NewRequestWithContext(r.Context(), r.Method, entry.AssetBase+url.PathEscape(name), nil)
		if e != nil {
			httpError(w, 502, "plugin_asset_unavailable")
			return true
		}
		for _, header := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
			if v := r.Header.Get(header); v != "" {
				request.Header.Set(header, v)
			}
		}
		client := http.DefaultClient
		if m.Catalog != nil && m.Catalog.Client != nil {
			client = m.Catalog.Client
		}
		assetClient := *client
		assetClient.Timeout = 2 * time.Minute
		response, e := assetClient.Do(request)
		if e != nil {
			httpError(w, 502, "plugin_asset_unavailable")
			return true
		}
		defer response.Body.Close()
		if response.StatusCode != 200 && response.StatusCode != 206 && response.StatusCode != 304 && response.StatusCode != 416 {
			http.NotFound(w, r)
			return true
		}
		for _, header := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
			if v := response.Header.Get(header); v != "" {
				w.Header().Set(header, v)
			}
		}
		if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		w.WriteHeader(response.StatusCode)
		if r.Method == http.MethodGet {
			_, _ = io.Copy(w, io.LimitReader(response.Body, 100<<20))
		}
		return true
	}
	http.NotFound(w, r)
	return true
}
