package webui

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandler(t *testing.T) {
	files := fstest.MapFS{
		"index.html":              {Data: []byte("<html>SPA</html>")},
		"assets/app-Ab12_cd3.js":  {Data: []byte("console.log('static')")},
		"assets/plain.css":        {Data: []byte("body{}")},
		"favicon.svg":             {Data: []byte("<svg/>")},
		".secret":                 {Data: []byte("private credential")},
		"assets/.hidden/file.txt": {Data: []byte("private credential")},
		"api/v1/missing":          {Data: []byte("private credential")},
		"healthz/sub":             {Data: []byte("private credential")},
	}
	handler := newHandler(files, "")
	for _, tc := range []struct {
		name, method, target     string
		status                   int
		body, contentType, cache string
	}{
		{"root", "GET", "/", 200, "<html>SPA</html>", "text/html", "no-cache"},
		{"index", "GET", "/index.html", 200, "<html>SPA</html>", "text/html", "no-cache"},
		{"fallback", "GET", "/devices/42?tab=history", 200, "<html>SPA</html>", "text/html", "no-cache"},
		{"trailing fallback", "GET", "/devices/42/", 200, "<html>SPA</html>", "text/html", "no-cache"},
		{"missing file fallback", "GET", "/missing.js", 200, "<html>SPA</html>", "text/html", "no-cache"},
		{"asset", "GET", "/assets/app-Ab12_cd3.js", 200, "console.log('static')", "javascript", "public, max-age=31536000, immutable"},
		{"plain asset", "GET", "/assets/plain.css", 200, "body{}", "text/css", "no-cache"},
		{"svg", "GET", "/favicon.svg", 200, "<svg/>", "image/svg+xml", "no-cache"},
		{"head fallback", "HEAD", "/devices/42", 200, "", "text/html", "no-cache"},
		{"head asset", "HEAD", "/assets/app-Ab12_cd3.js", 200, "", "javascript", "public, max-age=31536000, immutable"},
		{"api", "GET", "/api", 404, "Not Found\n", "text/plain", "no-cache"},
		{"api slash", "GET", "/api/", 404, "Not Found\n", "text/plain", "no-cache"},
		{"unknown api", "GET", "/api/v1/missing", 404, "Not Found\n", "text/plain", "no-cache"},
		{"health", "GET", "/healthz", 404, "Not Found\n", "text/plain", "no-cache"},
		{"health child", "GET", "/healthz/sub", 404, "Not Found\n", "text/plain", "no-cache"},
		{"head api", "HEAD", "/api/v1/missing", 404, "", "text/plain", "no-cache"},
		{"api prefix boundary", "GET", "/apiary", 200, "<html>SPA</html>", "text/html", "no-cache"},
		{"health prefix boundary", "GET", "/healthzone", 200, "<html>SPA</html>", "text/html", "no-cache"},
		{"directory", "GET", "/assets", 404, "Not Found\n", "text/plain", "no-cache"},
		{"directory slash", "GET", "/assets/", 404, "Not Found\n", "text/plain", "no-cache"},
		{"post", "POST", "/devices", 405, "Method Not Allowed\n", "text/plain", "no-cache"},
		{"options", "OPTIONS", "/", 405, "Method Not Allowed\n", "text/plain", "no-cache"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, httptest.NewRequest(tc.method, tc.target, nil))
			if rr.Code != tc.status || rr.Body.String() != tc.body {
				t.Fatalf("status/body = %d %q, want %d %q", rr.Code, rr.Body.String(), tc.status, tc.body)
			}
			if !strings.Contains(rr.Header().Get("Content-Type"), tc.contentType) || rr.Header().Get("Cache-Control") != tc.cache {
				t.Fatalf("unexpected headers: %v", rr.Header())
			}
			if rr.Header().Get("X-Content-Type-Options") != "nosniff" || rr.Header().Get("Location") != "" {
				t.Fatalf("unsafe headers: %v", rr.Header())
			}
			if tc.status == 405 && rr.Header().Get("Allow") != "GET, HEAD" {
				t.Fatalf("missing Allow: %v", rr.Header())
			}
		})
	}
	for _, target := range []string{"/.secret", "/.gitkeep", "/assets/.hidden/file.txt", "/../.secret", "/assets/../index.html", "/%2e%2e/.secret", "/assets/%2e%2e/index.html", "/assets%2f.hidden/file.txt", "/assets%5c..%5c.secret", "/assets/./plain.css", "/%00", "//api/v1/missing"} {
		t.Run(target, func(t *testing.T) {
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, httptest.NewRequest("GET", target, nil))
			if rr.Code != 404 || rr.Body.String() != "Not Found\n" {
				t.Fatalf("unsafe path returned %d %q", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestNotBuilt(t *testing.T) {
	handler := newHandler(fstest.MapFS{".gitkeep": {}}, "")
	for _, method := range []string{"GET", "HEAD"} {
		for _, target := range []string{"/", "/devices/42"} {
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, httptest.NewRequest(method, target, nil))
			if rr.Code != 503 || rr.Header().Get("Cache-Control") != "no-cache" {
				t.Fatalf("not-built response = %d %v", rr.Code, rr.Header())
			}
			if method == "HEAD" && rr.Body.Len() != 0 || method == "GET" && !strings.Contains(rr.Body.String(), "mise run webui:build") {
				t.Fatalf("not-built body = %q", rr.Body.String())
			}
		}
	}
}

type brokenFS struct{}

func (brokenFS) Open(string) (fs.File, error) {
	return nil, errors.New("private filesystem path and credential")
}

func TestFilesystemErrorDoesNotLeak(t *testing.T) {
	for _, target := range []string{"/", "/file.js"} {
		rr := httptest.NewRecorder()
		newHandler(brokenFS{}, "").ServeHTTP(rr, httptest.NewRequest("GET", target, nil))
		if rr.Code != 500 || rr.Body.String() != "Internal Server Error\n" {
			t.Fatalf("error leaked: %d %q", rr.Code, rr.Body.String())
		}
	}
}

func TestNewHandler(t *testing.T) {
	rr := httptest.NewRecorder()
	NewHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != 200 && rr.Code != 503 {
		t.Fatalf("embedded index or placeholder unavailable: %d", rr.Code)
	}
}
