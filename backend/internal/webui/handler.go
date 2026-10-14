// Package webui 提供内嵌静态资源和 SPA 路由回退，不处理业务 API。
package webui

import (
	"bytes"
	"embed"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
)

//go:embed all:dist
var embedded embed.FS

var hashedAsset = regexp.MustCompile(`^assets/.+-[A-Za-z0-9_-]{8,}\.[A-Za-z0-9]+$`)

const notBuiltPage = `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>WebUI 尚未构建</title><body><h1>WebUI 尚未构建</h1><p>请在仓库根目录执行 mise run webui:build，然后重新构建后端。</p></body></html>`

// NewHandler 返回 WebUI 处理器；仅接受 GET/HEAD，且不将 API 或健康检查路径回退到 SPA。
// 前端产物在编译时嵌入，更新后须重新构建后端；未构建时返回 503 提示页。
func NewHandler() http.Handler {
	return newHandler(embedded, "dist/")
}

// 使用 FS 接缝使测试不依赖本地是否已构建前端。
func newHandler(files fs.FS, prefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-cache")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeError(w, r, http.StatusMethodNotAllowed)
			return
		}
		if !safePath(r.URL.Path) || reservedPath(r.URL.Path) {
			writeError(w, r, http.StatusNotFound)
			return
		}
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if name != "" {
			info, err := fs.Stat(files, prefix+name)
			if err == nil {
				// 不暴露目录内容，也不由 FileServer 重定向或清理请求路径。
				if !info.Mode().IsRegular() {
					writeError(w, r, http.StatusNotFound)
					return
				}
				serveFile(w, r, files, prefix, name)
				return
			}
			if !errors.Is(err, fs.ErrNotExist) {
				writeError(w, r, http.StatusInternalServerError)
				return
			}
		}
		serveFile(w, r, files, prefix, "index.html")
	})
}

func reservedPath(p string) bool {
	return p == "/api" || strings.HasPrefix(p, "/api/") || p == "/healthz" || strings.HasPrefix(p, "/healthz/")
}

func safePath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00") || strings.Contains(p, "//") {
		return false
	}
	for _, part := range strings.Split(p[1:], "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

func serveFile(w http.ResponseWriter, r *http.Request, files fs.FS, prefix, name string) {
	content, err := fs.ReadFile(files, prefix+name)
	if err != nil {
		if name == "index.html" && errors.Is(err, fs.ErrNotExist) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			if r.Method != http.MethodHead {
				_, _ = w.Write([]byte(notBuiltPage))
			}
			return
		}
		writeError(w, r, http.StatusInternalServerError)
		return
	}
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if hashedAsset.MatchString(name) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(content))
}

func writeError(w http.ResponseWriter, r *http.Request, status int) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(http.StatusText(status) + "\n"))
	}
}
