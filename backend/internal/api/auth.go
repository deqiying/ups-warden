package api

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ups-warden/internal/auth"
)

const cookieName = "ups_warden_session"

func safeMethod(method string) bool { return method == http.MethodGet || method == http.MethodHead }
func equalSecret(a, b string) bool {
	return a != "" && b != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return u.Scheme == scheme && strings.EqualFold(u.Host, r.Host)
}
func (s *Server) authError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		fail(w, 401, "invalid_credentials", "用户名或密码错误，或会话已失效")
	case errors.Is(err, auth.ErrRateLimited):
		w.Header().Set("Retry-After", "900")
		fail(w, 429, "rate_limited", "请求过于频繁，请稍后重试")
	case errors.Is(err, auth.ErrSetupClosed):
		fail(w, 409, "setup_closed", "初始化已完成")
	case errors.Is(err, auth.ErrInvalidSetupToken):
		fail(w, 403, "invalid_setup_token", "初始化 token 无效")
	case errors.Is(err, auth.ErrValidation):
		fail(w, 422, "validation_failed", "用户名或密码不符合要求")
	case errors.Is(err, auth.ErrConflict), errors.Is(err, auth.ErrLastAdmin):
		fail(w, 409, "user_conflict", "用户名冲突或不能删除该管理员")
	case errors.Is(err, auth.ErrNotFound):
		fail(w, 404, "user_not_found", "用户不存在")
	default:
		s.internal(w, err)
	}
}
func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	needed, err := s.opts.Auth.NeedsSetup(r.Context())
	if err != nil {
		s.internal(w, err)
		return
	}
	reply(w, 200, map[string]bool{"setup_required": needed})
}
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token    string `json:"setup_token"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := s.opts.Auth.Setup(r.Context(), in.Token, in.Username, in.Password, remoteIP(r))
	if err != nil {
		s.authError(w, err)
		return
	}
	reply(w, 201, u)
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	session, err := s.opts.Auth.Login(r.Context(), in.Username, in.Password, remoteIP(r), r.UserAgent())
	if err != nil {
		s.authError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: session.Token, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, Expires: session.AbsoluteExpiresAt})
	reply(w, 200, map[string]any{"user": session.User, "csrf_token": session.CSRF, "expires_at": session.ExpiresAt})
}
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	v := current(r)
	reply(w, 200, map[string]any{"user": v.User, "csrf_token": v.CSRF, "expires_at": v.ExpiresAt})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.opts.Auth.Logout(r.Context(), current(r).Token, remoteIP(r)); err != nil {
		s.authError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/", Value: "", MaxAge: -1, Expires: time.Unix(1, 0), HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
	reply(w, 200, map[string]bool{"ok": true})
}
func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	before, limit, ok := pagination(w, r)
	if !ok {
		return
	}
	items, err := s.opts.Auth.Users(r.Context(), before, limit+1)
	if err != nil {
		s.authError(w, err)
		return
	}
	next := int64(0)
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1].ID
	}
	page(w, items, next)
}
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := s.opts.Auth.CreateUser(r.Context(), current(r).User.ID, remoteIP(r), in.Username, in.Password)
	if err != nil {
		s.authError(w, err)
		return
	}
	reply(w, 201, u)
}
func (s *Server) renameUser(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var in struct {
		Username string `json:"username"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := s.opts.Auth.RenameUser(r.Context(), current(r).User.ID, id, remoteIP(r), in.Username)
	if err != nil {
		s.authError(w, err)
		return
	}
	reply(w, 200, u)
}
func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok || !confirm(w, r) {
		return
	}
	if err := s.opts.Auth.DeleteUser(r.Context(), current(r).User.ID, id, remoteIP(r)); err != nil {
		s.authError(w, err)
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
func (s *Server) password(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	v := current(r)
	if err := s.opts.Auth.ChangePassword(r.Context(), v.User.ID, id, v.Token, remoteIP(r), in.Password); err != nil {
		s.authError(w, err)
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
