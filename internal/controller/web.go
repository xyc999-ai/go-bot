package controller

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"go-bot/internal/config"
	mcpconfig "go-bot/mcp"
)

type WebOptions struct {
	Files     fs.FS
	DevOrigin string
}

func ValidateWebDevOrigin(origin string) error {
	if origin == "" {
		return nil
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" ||
		u.ForceQuery || u.Fragment != "" || u.Port() == "" || !config.IsLoopbackHost(u.Hostname()) ||
		origin != "http://"+u.Host {
		return fmt.Errorf("web dev origin must be a loopback HTTP origin with an explicit port")
	}
	if _, err := config.ControllerListenAddress(u.Host); err != nil {
		return fmt.Errorf("invalid web dev origin: %w", err)
	}
	return nil
}

func controllerBrowserOrigin(w http.ResponseWriter, r *http.Request, devOrigin string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") != "cross-site" && r.Header.Get("Sec-Fetch-Site") != "same-site"
	}
	scheme, defaultPort := "http", ":80"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme, defaultPort = "https", ":443"
	}
	host := strings.TrimSuffix(r.Host, defaultPort)
	if strings.EqualFold(strings.TrimSuffix(origin, defaultPort), scheme+"://"+host) {
		return true
	}
	if devOrigin != "" && origin == devOrigin && config.IsLoopbackHost((&url.URL{Host: r.Host}).Hostname()) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Last-Event-ID, X-Client-Time-Zone, X-User-ID, "+ClientHeader+", "+mcpconfig.ControllerOriginHeader)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		if r.URL.Path == hostFilesPath {
			w.Header().Set("Access-Control-Expose-Headers", hostStorageHeader+", "+hostStorageSourceHeader)
		}
		return true
	}
	return false
}

func serveWeb(w http.ResponseWriter, r *http.Request, files fs.FS) bool {
	if files == nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		name := strings.TrimPrefix(r.URL.Path, "/")
		info, err := fs.Stat(files, name)
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return true
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if name == "assets/licenses.txt" {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.FileServerFS(files).ServeHTTP(w, r)
		return true
	}
	if r.URL.Path != "/" && r.URL.Path != "/settings/models" && r.URL.Path != "/settings/web-search" && r.URL.Path != "/settings/image-generation" && r.URL.Path != "/settings/proactive" && r.URL.Path != "/plugins" {
		id, chat := strings.CutPrefix(r.URL.Path, "/chat/")
		if !chat || len(id) != 36 || uuid.Validate(id) != nil {
			return false
		}
	}
	data, err := fs.ReadFile(files, "index.html")
	if err != nil {
		http.Error(w, "Web UI assets unavailable", http.StatusServiceUnavailable)
		return true
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' blob:; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(data))
	return true
}
