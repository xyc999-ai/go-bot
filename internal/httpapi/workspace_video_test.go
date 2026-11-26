package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go-bot/internal/hostruntime"
)

func TestWorkspaceVideoRangeAndHead(t *testing.T) {
	root := t.TempDir()
	data := strings.Repeat("video-frame-", 200)
	path := filepath.Join(root, "clip #1+.MP4")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	handler := NewNativeHostHTTP(&hostruntime.NativeHost{WorkspaceForSession: func(sid string) string {
		if sid == id {
			return root
		}
		return ""
	}}, NativeWorkerHTTPOptions{})
	target := "/v1/sessions/" + id + "/files/content?" + url.Values{"path": {path}, "inline": {"1"}}.Encode()
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		request := httptest.NewRequest(method, target, nil)
		request.Header.Set("Range", "bytes=10-29")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 206 || response.Header().Get("Content-Range") != "bytes 10-29/2400" || response.Header().Get("Content-Type") != "video/mp4" || !strings.HasPrefix(response.Header().Get("Content-Disposition"), "inline;") {
			t.Fatalf("%s: %d %+v", method, response.Code, response.Header())
		}
		if method == http.MethodGet && response.Body.String() != data[10:30] {
			t.Fatal("incorrect range bytes")
		}
		if method == http.MethodHead && response.Body.Len() != 0 {
			t.Fatal("HEAD returned bytes")
		}
	}
	response := httptest.NewRecorder()
	serveWorkspaceFile(response, httptest.NewRequest("GET", "/?path="+url.QueryEscape(path), nil), root)
	if !strings.HasPrefix(response.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatal("download disposition changed")
	}
	for _, path := range []string{filepath.Join(root, "..", "outside.mp4"), filepath.Join(root, "missing.mp4")} {
		response := httptest.NewRecorder()
		serveWorkspaceFile(response, httptest.NewRequest("GET", "/?inline=1&path="+url.QueryEscape(path), nil), root)
		if response.Code != 404 {
			t.Fatalf("invalid file: %d", response.Code)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "page.html"), []byte("<script>bad()</script>"), 0600); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	serveWorkspaceFile(response, httptest.NewRequest("GET", "/?inline=1&path=page.html", nil), root)
	if !strings.HasPrefix(response.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatal("non-media became inline")
	}
}
