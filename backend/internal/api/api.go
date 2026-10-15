// Package api 提供 /api/v1 管理接口；协议诊断只在显式诊断端点暴露。
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"

	"ups-warden/internal/auth"
	"ups-warden/internal/bus"
	"ups-warden/internal/collector"
	"ups-warden/internal/history"
	"ups-warden/internal/model"
	"ups-warden/internal/notify"
	"ups-warden/internal/secret"
	"ups-warden/internal/store"
)

// Runtime 将配置事务与受影响设备的生命周期串行协调；事务失败恢复旧配置。
type Runtime interface {
	ChangeDevices(context.Context, []int64, func() error) error
	Snapshots() []model.DeviceState
}

// Options 指定必需业务模块；仅 Logger 可省略。API 不拥有模块的关闭生命周期。
type Options struct {
	DB        *store.Store
	Cipher    *secret.Cipher
	Auth      *auth.Service
	Collector *collector.Supervisor
	History   *history.Writer
	Bus       *bus.Bus
	Runtime   Runtime
	Notify    *notify.Service
	Logger    *slog.Logger
}

// Server 包装业务路由与统一认证，配置操作串行以保证跨表校验一致性。
type Server struct {
	opts    Options
	mux     *http.ServeMux
	changes sync.Mutex
}

// New 创建 API handler；外层根路由须挂载 /api/，所有未知 API 也受认证拦截。
func New(opts Options) (*Server, error) {
	if opts.DB == nil || opts.Cipher == nil || opts.Auth == nil || opts.Collector == nil || opts.History == nil || opts.Bus == nil || opts.Runtime == nil || opts.Notify == nil {
		return nil, errors.New("api: 缺少必需模块")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	s := &Server{opts: opts, mux: http.NewServeMux()}
	s.routes()
	return s, nil
}
func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/v1/setup/status", s.setupStatus)
	s.mux.HandleFunc("POST /api/v1/setup", s.setup)
	s.mux.HandleFunc("POST /api/v1/auth/login", s.login)
	s.mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
	s.mux.HandleFunc("GET /api/v1/auth/session", s.session)
	s.mux.HandleFunc("GET /api/v1/users", s.users)
	s.mux.HandleFunc("POST /api/v1/users", s.createUser)
	s.mux.HandleFunc("PATCH /api/v1/users/{id}", s.renameUser)
	s.mux.HandleFunc("DELETE /api/v1/users/{id}", s.deleteUser)
	s.mux.HandleFunc("PUT /api/v1/users/{id}/password", s.password)
	s.mux.HandleFunc("GET /api/v1/devices", s.devices)
	s.mux.HandleFunc("POST /api/v1/devices", s.createDevice)
	s.mux.HandleFunc("GET /api/v1/devices/{id}", s.getDevice)
	s.mux.HandleFunc("PATCH /api/v1/devices/{id}", s.updateDevice)
	s.mux.HandleFunc("PUT /api/v1/devices/{id}/enabled", s.enableDevice)
	s.mux.HandleFunc("DELETE /api/v1/devices/{id}", s.deleteDevice)
	s.mux.HandleFunc("GET /api/v1/devices/{id}/sources", s.sources)
	s.mux.HandleFunc("POST /api/v1/devices/{id}/sources", s.createSource)
	s.mux.HandleFunc("PATCH /api/v1/sources/{id}", s.updateSource)
	s.mux.HandleFunc("DELETE /api/v1/sources/{id}", s.deleteSource)
	s.mux.HandleFunc("POST /api/v1/sources/test", s.testSource)
	s.mux.HandleFunc("POST /api/v1/sources/{id}/test", s.testSavedSource)
	s.mux.HandleFunc("GET /api/v1/sources/{id}/raw", s.raw)
	s.mux.HandleFunc("GET /api/v1/profiles", s.profiles)
	s.mux.HandleFunc("GET /api/v1/profiles/{key}", s.getProfile)
	s.mux.HandleFunc("GET /api/v1/profiles/{key}/export", s.exportProfile)
	s.mux.HandleFunc("POST /api/v1/profiles", s.createProfile)
	s.mux.HandleFunc("PUT /api/v1/profiles/{key}", s.updateProfile)
	s.mux.HandleFunc("DELETE /api/v1/profiles/{key}", s.deleteProfile)
	s.mux.HandleFunc("POST /api/v1/profiles/{key}/copy", s.copyProfile)
	s.mux.HandleFunc("POST /api/v1/profiles/validate", s.validateProfile)
	s.mux.HandleFunc("GET /api/v1/snapshots", s.snapshots)
	s.mux.HandleFunc("GET /api/v1/devices/{id}/snapshot", s.snapshot)
	s.mux.HandleFunc("GET /api/v1/devices/{id}/history", s.history)
	s.mux.HandleFunc("GET /api/v1/events", s.events)
	s.mux.HandleFunc("GET /api/v1/alerts", s.alerts)
	s.mux.HandleFunc("GET /api/v1/audit", s.audit)
	s.mux.HandleFunc("GET /api/v1/channels", s.channels)
	s.mux.HandleFunc("POST /api/v1/channels", s.createChannel)
	s.mux.HandleFunc("GET /api/v1/channels/{id}", s.getChannel)
	s.mux.HandleFunc("PUT /api/v1/channels/{id}", s.updateChannel)
	s.mux.HandleFunc("DELETE /api/v1/channels/{id}", s.deleteChannel)
	s.mux.HandleFunc("POST /api/v1/channels/{id}/test", s.testChannel)
	s.mux.HandleFunc("GET /api/v1/notify-templates", s.notifyTemplates)
	s.mux.HandleFunc("PUT /api/v1/notify-templates", s.saveNotifyTemplate)
	s.mux.HandleFunc("GET /api/v1/notify-logs", s.notifyLogs)
	s.mux.HandleFunc("GET /api/v1/stream", s.stream)
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "not_found", "接口不存在") })
}

type sessionKey struct{}

func current(r *http.Request) auth.Session {
	v, _ := r.Context().Value(sessionKey{}).(auth.Session)
	return v
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	defer func() {
		if v := recover(); v != nil {
			s.opts.Logger.Error("API panic，已隔离", "method", r.Method, "panic_type", fmt.Sprintf("%T", v), "stack", string(debug.Stack()))
			fail(w, 500, "internal_error", "内部错误")
		}
	}()
	public := (r.Method == "GET" && r.URL.Path == "/api/v1/setup/status") || (r.Method == "POST" && (r.URL.Path == "/api/v1/setup" || r.URL.Path == "/api/v1/auth/login"))
	if !safeMethod(r.Method) && !sameOrigin(r) {
		fail(w, 403, "origin_rejected", "请求来源不匹配")
		return
	}
	if !public {
		cookie, err := r.Cookie(cookieName)
		if err != nil {
			fail(w, 401, "unauthorized", "请先登录")
			return
		}
		session, err := s.opts.Auth.Authenticate(r.Context(), cookie.Value)
		if err != nil {
			s.authError(w, err)
			return
		}
		if session.User.Role != "admin" {
			fail(w, 403, "forbidden", "需要管理员权限")
			return
		}
		if !safeMethod(r.Method) && !equalSecret(r.Header.Get("X-CSRF-Token"), session.CSRF) {
			fail(w, 403, "csrf_invalid", "CSRF token 无效")
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), sessionKey{}, session))
	}
	s.mux.ServeHTTP(w, r)
}
func reply(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		fail(w, 500, "encoding_error", "响应编码失败")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(append(data, '\n')); err != nil {
		slog.Debug("客户端响应写入失败", "error_type", errorType(err))
	}
}
func fail(w http.ResponseWriter, status int, code, message string) {
	reply(w, status, map[string]any{"code": code, "message": message, "details": map[string]any{}})
}
func (s *Server) internal(w http.ResponseWriter, err error) {
	_, file, line, _ := runtime.Caller(1)
	s.opts.Logger.Error("API 存储或运行失败", "error_type", errorType(err), "caller", filepath.Base(file), "line", line)
	fail(w, 500, "internal_error", "操作失败，请检查服务日志")
}
func errorType(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	// 只输出类型及 SQLite 数字码，不格式化可能含凭据的原始错误文本。
	var code interface{ Code() int }
	if errors.As(err, &code) {
		return fmt.Sprintf("sqlite_%d", code.Code())
	}
	return fmt.Sprintf("%T", err)
}
