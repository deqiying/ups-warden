package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ups-warden/internal/audit"
)

var errValidation = errors.New("invalid input")
var errConflict = errors.New("conflict")

// decode 限制请求体、深度、未知字段、重复键与 null，避免 PATCH 意外清空或覆盖。
func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		fail(w, 415, "json_required", "需要 application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 512*1024)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		fail(w, 413, "body_too_large", "请求体超过限制")
		return false
	}
	if len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' || strictJSON(data) != nil {
		fail(w, 400, "invalid_json", "JSON 格式无效、含重复键或空值")
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(out) != nil {
		fail(w, 400, "invalid_json", "JSON 字段或类型无效")
		return false
	}
	return true
}
func strictJSON(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 24 {
			return errValidation
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		if token == nil {
			return errValidation
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		if delim == '{' {
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				k, ok := key.(string)
				if !ok || seen[k] {
					return errValidation
				}
				seen[k] = true
				if err = walk(depth + 1); err != nil {
					return err
				}
			}
		} else if delim == '[' {
			for d.More() {
				if err = walk(depth + 1); err != nil {
					return err
				}
			}
		} else {
			return errValidation
		}
		_, err = d.Token()
		return err
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errValidation
	}
	return nil
}
func idParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(w, 400, "invalid_id", "ID 必须为正整数")
		return 0, false
	}
	return id, true
}
func pagination(w http.ResponseWriter, r *http.Request) (int64, int, bool) {
	before := int64(0)
	limit := 50
	var err error
	if v := r.URL.Query().Get("before"); v != "" {
		before, err = strconv.ParseInt(v, 10, 64)
		if err != nil || before <= 0 {
			fail(w, 400, "invalid_cursor", "分页游标无效")
			return 0, 0, false
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, err = strconv.Atoi(v)
		if err != nil || limit < 1 || limit > 200 {
			fail(w, 400, "invalid_limit", "limit 必须在 1～200")
			return 0, 0, false
		}
	}
	return before, limit, true
}
func page(w http.ResponseWriter, items any, next int64) {
	reply(w, 200, map[string]any{"items": items, "next_before": next})
}
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "unknown"
	}
	return ip.String()
}
func validText(s string, max int, required bool) bool {
	return (!required || strings.TrimSpace(s) != "") && len(s) <= max && !strings.ContainsRune(s, 0)
}
func object(v any) (map[string]any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	err = json.Unmarshal(data, &result)
	return result, err
}
func logChange(ctx context.Context, tx *sql.Tx, r *http.Request, kind, target string, before, after any) error {
	previous, err := object(before)
	if err != nil {
		return err
	}
	next, err := object(after)
	if err != nil {
		return err
	}
	return audit.Write(ctx, tx, audit.Record{UserID: current(r).User.ID, IP: remoteIP(r), Kind: kind, Target: target, Result: "success", Before: previous, After: next})
}
func (s *Server) storageError(w http.ResponseWriter, err error, kind string) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		fail(w, 404, kind+"_not_found", "资源不存在")
	case errors.Is(err, errValidation):
		fail(w, 422, "validation_failed", "配置校验失败")
	case errors.Is(err, errConflict):
		fail(w, 409, "resource_conflict", "资源被引用、名称冲突或启用数据源冲突")
	default:
		var code interface{ Code() int }
		if errors.As(err, &code) && code.Code()&255 == 19 {
			fail(w, 409, "resource_conflict", "资源被引用或存在约束冲突")
			return
		}
		s.internal(w, err)
	}
}
func (s *Server) change(r *http.Request, ids []int64, fn func(*sql.Tx) error) error {
	committed := false
	apply := func() error { err := s.opts.DB.WithTx(r.Context(), fn); committed = err == nil; return err }
	err := s.opts.Runtime.ChangeDevices(r.Context(), ids, apply)
	if err != nil && committed {
		s.opts.Logger.Error("配置已保存，恢复运行失败", "error_type", errorType(err), "device_ids", ids)
		return savedRuntimeError{}
	}
	return err
}

type savedRuntimeError struct{}

func (savedRuntimeError) Error() string { return "configuration saved but runtime reload failed" }
func (s *Server) changed(w http.ResponseWriter, err error, kind string) bool {
	if err == nil {
		return true
	}
	var saved savedRuntimeError
	if errors.As(err, &saved) {
		fail(w, 503, "configuration_saved_runtime_error", "配置已保存，但采集恢复失败；请检查连接配置后重试")
		return false
	}
	s.storageError(w, err, kind)
	return false
}
func confirm(w http.ResponseWriter, r *http.Request) bool {
	var in struct {
		Confirm bool `json:"confirm"`
	}
	if !decode(w, r, &in) {
		return false
	}
	if !in.Confirm {
		fail(w, 422, "confirmation_required", "需要 confirm=true")
		return false
	}
	return true
}
func timeQuery(r *http.Request, key string) (time.Time, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	return t.UTC(), err
}
