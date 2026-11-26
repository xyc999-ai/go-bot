package controller

import (
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInlineVideoRoutesAccountAndPreservesRange(t *testing.T) {
	m := newTestManager(t, "docker", &fakeDriver{}, func(w http.ResponseWriter, r *http.Request) {
		if controlReply(w, r) {
			return
		}
		if r.URL.Query().Has("userId") || r.Header.Get(UserHeader) != "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Error("account routing leaked or Host authorization missing")
		}
		if r.Header.Get("Range") != "bytes=10-19" || r.Header.Get("If-Range") != "video-version" {
			t.Error("range request changed")
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Range", "bytes 10-19/100")
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", "10")
		w.WriteHeader(206)
		if r.Method != "HEAD" {
			_, _ = w.Write([]byte("0123456789"))
		}
	})
	if err := m.CreateAccount(t.Context(), "video-user"); err != nil {
		t.Fatal(err)
	}
	if err := m.Ensure(t.Context(), "video-user"); err != nil {
		t.Fatal(err)
	}
	path := "/v1/sessions/" + uuid.NewString() + "/files/content?inline=1&path=clip.mp4&userId=video-user"
	for _, method := range []string{"GET", "HEAD"} {
		request := httptest.NewRequest(method, path, nil)
		request.Header.Set("Range", "bytes=10-19")
		request.Header.Set("If-Range", "video-version")
		response := httptest.NewRecorder()
		m.Handler().ServeHTTP(response, request)
		if response.Code != 206 || response.Header().Get("Content-Range") != "bytes 10-19/100" || response.Header().Get("Accept-Ranges") != "bytes" {
			t.Fatalf("%s: %d %s", method, response.Code, response.Body)
		}
		if method == "HEAD" && response.Body.Len() != 0 {
			t.Fatal("HEAD body")
		}
	}
	for _, target := range []string{"/v1/sessions?userId=video-user&inline=1", strings.Replace(path, "inline=1", "inline=0", 1)} {
		response := httptest.NewRecorder()
		m.Handler().ServeHTTP(response, httptest.NewRequest("GET", target, nil))
		if response.Code != 400 {
			t.Fatalf("query account escaped file read: %d", response.Code)
		}
	}
	request := httptest.NewRequest("GET", path, nil)
	request.Header.Set(UserHeader, "another-account")
	response := httptest.NewRecorder()
	m.Handler().ServeHTTP(response, request)
	if response.Code != 400 {
		t.Fatal("conflicting accounts accepted")
	}
}
