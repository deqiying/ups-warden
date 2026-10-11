// Package server 组装对外提供的 HTTP 服务。
package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// New 创建监听 addr 的 HTTP 服务。目前只注册健康检查，业务 API 后续挂载到 /api/v1。
func New(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           NewHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
}

// NewHandler 返回服务的根路由，单独导出便于测试直接调用。
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	return mux
}

// handleHealthz 只表示进程存活并能处理请求，不反映 UPS 设备状态；该端点无需鉴权。
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		slog.Warn("写入健康检查响应失败", "error", err)
	}
}
