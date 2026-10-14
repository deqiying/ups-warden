// Package server 组装对外提供的 HTTP 服务。
package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
	"ups-warden/internal/webui"
)

// New 创建 HTTP 服务；API 由 app 注入，WriteTimeout 留空以支持 SSE。
func New(addr string, api ...http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           NewHandler(api...),
		ReadHeaderTimeout: 5 * time.Second,
	}
}

// NewHandler 返回服务的根路由，单独导出便于测试直接调用。
func NewHandler(api ...http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	if len(api) > 0 && api[0] != nil {
		mux.Handle("/api/", api[0])
		mux.Handle("/api", api[0])
	}
	mux.Handle("/", webui.NewHandler())
	return mux
}

// handleHealthz 只表示进程存活并能处理请求，不反映 UPS 设备状态；该端点无需鉴权。
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		slog.Warn("写入健康检查响应失败", "error", err)
	}
}
