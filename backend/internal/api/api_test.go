package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ups-warden/internal/action"
	"ups-warden/internal/auth"
	"ups-warden/internal/bus"
	"ups-warden/internal/collector"
	"ups-warden/internal/history"
	"ups-warden/internal/notify"
	"ups-warden/internal/plan"
	"ups-warden/internal/secret"
	"ups-warden/internal/store"
)

type apiFixture struct {
	s           *Server
	db          *store.Store
	token, csrf string
}

func newAPI(t *testing.T) *apiFixture {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	cipher, err := secret.Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	a, err := auth.New(db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := history.New(db, history.Options{Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	b := &bus.Bus{}
	t.Cleanup(b.Close)
	c := collector.New(db, cipher, nil, collector.Options{Logger: logger})
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	n, err := notify.New(context.Background(), db, cipher, b, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := n.Close(); err != nil {
			t.Error(err)
		}
	})
	executor, err := action.New(context.Background(), db, cipher, n, filepath.Join(dir, "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := executor.Close(); err != nil {
			t.Error(err)
		}
	})
	plans, err := plan.New(context.Background(), db, cipher, executor, n, b, logger)
	if err != nil {
		t.Fatal(err)
	}
	executor.SetPlans(plans)
	t.Cleanup(plans.Close)
	s, err := New(Options{DB: db, Cipher: cipher, Auth: a, Collector: c, History: h, Bus: b, Runtime: &snapshotRuntime{}, Notify: n, Actions: executor, Plans: plans, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	return &apiFixture{s: s, db: db}
}
func (f *apiFixture) req(method, path string, value any) *httptest.ResponseRecorder {
	var data []byte
	if text, ok := value.(string); ok {
		data = []byte(text)
	} else if value != nil {
		data, _ = json.Marshal(value)
	}
	r := httptest.NewRequest(method, "http://localhost"+path, bytes.NewReader(data))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Content-Type", "application/json")
	if f.token != "" {
		r.AddCookie(&http.Cookie{Name: cookieName, Value: f.token})
		r.Header.Set("X-CSRF-Token", f.csrf)
	}
	w := httptest.NewRecorder()
	f.s.ServeHTTP(w, r)
	return w
}
func status(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body.String())
	}
}
func (f *apiFixture) login(t *testing.T) {
	t.Helper()
	token := f.s.opts.Auth.SetupToken()
	status(t, f.req("POST", "/api/v1/setup", map[string]any{"setup_token": token, "username": "admin", "password": "test-password"}), 201)
	w := f.req("POST", "/api/v1/auth/login", map[string]any{"username": "admin", "password": "test-password"})
	status(t, w, 200)
	var v struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	f.csrf = v.CSRF
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/" {
		t.Fatal("cookie 不安全")
	}
	f.token = cookies[0].Value
	if strings.Contains(w.Body.String(), f.token) || strings.Contains(w.Body.String(), "test-password") {
		t.Fatal("认证响应泄密")
	}
}
func sqlExec(t *testing.T, db *store.Store, q string, args ...any) {
	t.Helper()
	if _, err := db.Write().Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}
func TestAuthenticationCSRFAndLimits(t *testing.T) {
	f := newAPI(t)
	for _, path := range []string{"/api/v1/devices", "/api/v1/snapshots", "/api/v1/stream", "/api/v1/unknown"} {
		status(t, f.req("GET", path, nil), 401)
	}
	status(t, f.req("POST", "/api/v1/setup", `{"setup_token":"wrong","username":"admin","password":"test-password"}`), 403)
	f.login(t)
	status(t, f.req("GET", "/api/v1/auth/session", nil), 200)
	status(t, f.req("POST", "/api/v1/setup", `{"setup_token":"anything","username":"other","password":"test-password"}`), 409)
	csrf := f.csrf
	f.csrf = "wrong"
	status(t, f.req("POST", "/api/v1/devices", `{"name":"test"}`), 403)
	f.csrf = csrf
	r := httptest.NewRequest("POST", "http://localhost/api/v1/devices", strings.NewReader(`{"name":"test"}`))
	r.AddCookie(&http.Cookie{Name: cookieName, Value: f.token})
	r.Header.Set("X-CSRF-Token", f.csrf)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	f.s.ServeHTTP(w, r)
	status(t, w, 403)
	for _, body := range []string{`{"name":"a","name":"b"}`, `{"name":null}`, `{"name":"a","unknown":1}`, `{} {}`, `[]`} {
		status(t, f.req("POST", "/api/v1/devices", body), 400)
	}
	status(t, f.req("GET", "/api/v1/devices?limit=201", nil), 400)
	status(t, f.req("POST", "/api/v1/auth/logout", `{}`), 200)
	status(t, f.req("GET", "/api/v1/devices", nil), 401)
	f.token = ""
	for i := 0; i < 5; i++ {
		w = f.req("POST", "/api/v1/auth/login", `{"username":"admin","password":"wrong-pass"}`)
		if w.Code != 401 && w.Code != 429 {
			t.Fatal(w.Code)
		}
	}
	status(t, f.req("POST", "/api/v1/auth/login", `{"username":"admin","password":"test-password"}`), 429)
}
func TestCookieSecureAndOrigin(t *testing.T) {
	f := newAPI(t)
	f.login(t)
	r := httptest.NewRequest("POST", "https://localhost/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"test-password"}`))
	r.TLS = &tls.ConnectionState{}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://localhost")
	w := httptest.NewRecorder()
	f.s.ServeHTTP(w, r)
	status(t, w, 200)
	if !w.Result().Cookies()[0].Secure {
		t.Fatal("HTTPS cookie 缺 Secure")
	}
}
func TestDeviceCRUDPaginationAndAuditRollback(t *testing.T) {
	f := newAPI(t)
	f.login(t)
	for _, name := range []string{"first", "second"} {
		status(t, f.req("POST", "/api/v1/devices", map[string]any{"name": name}), 201)
	}
	w := f.req("GET", "/api/v1/devices?limit=1", nil)
	status(t, w, 200)
	var result struct {
		Items []device `json:"items"`
		Next  int64    `json:"next_before"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if len(result.Items) != 1 || result.Next != 2 {
		t.Fatal(w.Body.String())
	}
	w = f.req("GET", "/api/v1/devices?before=2&limit=1", nil)
	status(t, w, 200)
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Items[0].ID != 1 || result.Next != 0 {
		t.Fatal(w.Body.String())
	}
	status(t, f.req("PATCH", "/api/v1/devices/1", `{"note":"changed","config":{"outage_confirm_s":0}}`), 200)
	status(t, f.req("PUT", "/api/v1/devices/1/enabled", `{"enabled":false}`), 200)
	status(t, f.req("DELETE", "/api/v1/devices/2", `{"confirm":false}`), 422)
	sqlExec(t, f.db, `CREATE TRIGGER reject_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT,'test audit failure'); END`)
	status(t, f.req("PATCH", "/api/v1/devices/1", `{"name":"must-rollback"}`), 409)
	var name string
	if err := f.db.Read().QueryRow("SELECT name FROM devices WHERE id=1").Scan(&name); err != nil || name != "first" {
		t.Fatal("未与审计回滚", err)
	}
	sqlExec(t, f.db, "DROP TRIGGER reject_audit")
	status(t, f.req("DELETE", "/api/v1/devices/2", `{"confirm":true}`), 200)
	status(t, f.req("GET", "/api/v1/devices/2", nil), 404)
	status(t, f.req("GET", "/api/v1/audit?limit=1", nil), 200)
}
func TestSourcesSecretsAndConflict(t *testing.T) {
	f := newAPI(t)
	f.login(t)
	status(t, f.req("POST", "/api/v1/devices", `{"name":"sim","enabled":false}`), 201)
	body := map[string]any{"protocol": "snmp", "host": "127.0.0.1", "port": 161, "profile_key": "et1k-snmp", "community": "private-test-community"}
	w := f.req("POST", "/api/v1/devices/1/sources", body)
	status(t, w, 201)
	if strings.Contains(w.Body.String(), "private-test-community") || !strings.Contains(w.Body.String(), `"community_set":true`) {
		t.Fatal(w.Body.String())
	}
	status(t, f.req("POST", "/api/v1/devices/1/sources", body), 409)
	var original []byte
	f.db.Read().QueryRow("SELECT secret FROM sources WHERE id=1").Scan(&original)
	if bytes.Contains(original, []byte("private-test-community")) {
		t.Fatal("凭据未加密")
	}
	status(t, f.req("PATCH", "/api/v1/sources/1", `{"community":"","timeout_ms":2000}`), 200)
	var preserved []byte
	f.db.Read().QueryRow("SELECT secret FROM sources WHERE id=1").Scan(&preserved)
	if !bytes.Equal(original, preserved) {
		t.Fatal("空串覆盖凭据")
	}
	status(t, f.req("PATCH", "/api/v1/sources/1", `{"community":"new-test-community"}`), 200)
	w = f.req("GET", "/api/v1/audit", nil)
	if strings.Contains(w.Body.String(), "new-test-community") || strings.Contains(w.Body.String(), "private-test-community") {
		t.Fatal("审计泄密")
	}
	status(t, f.req("PATCH", "/api/v1/sources/1", `{"clear_secrets":["community"]}`), 200)
	w = f.req("GET", "/api/v1/devices/1/sources", nil)
	status(t, w, 200)
	if !strings.Contains(w.Body.String(), `"community_set":false`) {
		t.Fatal(w.Body.String())
	}
	status(t, f.req("PATCH", "/api/v1/sources/1", `{"params":{"version":"2c","unit_id":1}}`), 422)
	status(t, f.req("PATCH", "/api/v1/sources/1", `{"host":"http://127.0.0.1"}`), 422)
	status(t, f.req("DELETE", "/api/v1/sources/1", `{"confirm":true}`), 200)
}
func TestProfilesCopyUpdateReferenceAndDelete(t *testing.T) {
	f := newAPI(t)
	f.login(t)
	w := f.req("GET", "/api/v1/profiles", nil)
	status(t, w, 200)
	status(t, f.req("DELETE", "/api/v1/profiles/et1k-modbus", `{"confirm":true}`), 409)
	w = f.req("POST", "/api/v1/profiles/et1k-modbus/copy", `{"key":"local-copy","name":"测试复制"}`)
	status(t, w, 201)
	var p profileView
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	w = f.req("GET", "/api/v1/profiles/local-copy/export", nil)
	status(t, w, 200)
	if !strings.Contains(w.Body.String(), "local-copy") {
		t.Fatal("导出内容错误")
	}
	status(t, f.req("POST", "/api/v1/profiles/validate", map[string]any{"content": p.Content}), 200)
	status(t, f.req("POST", "/api/v1/devices", `{"name":"sim","enabled":false}`), 201)
	status(t, f.req("POST", "/api/v1/devices/1/sources", `{"protocol":"modbus","host":"127.0.0.1","port":502,"profile_key":"local-copy"}`), 201)
	status(t, f.req("DELETE", "/api/v1/profiles/local-copy", `{"confirm":true}`), 409)
	p.Content = strings.Replace(p.Content, "version: 1", "version: 2", 1)
	status(t, f.req("PUT", "/api/v1/profiles/local-copy", map[string]any{"content": p.Content}), 200)
	var version int
	if err := f.db.Read().QueryRow("SELECT profile_version FROM sources WHERE id=1").Scan(&version); err != nil || version != 2 {
		t.Fatal("引用版本未更新", err)
	}
	status(t, f.req("DELETE", "/api/v1/sources/1", `{"confirm":true}`), 200)
	status(t, f.req("DELETE", "/api/v1/profiles/local-copy", `{"confirm":true}`), 200)
}
func TestUserManagement(t *testing.T) {
	f := newAPI(t)
	f.login(t)
	status(t, f.req("POST", "/api/v1/users", `{"username":"second","password":"second-password"}`), 201)
	status(t, f.req("PATCH", "/api/v1/users/2", `{"username":"renamed"}`), 200)
	status(t, f.req("PUT", "/api/v1/users/2/password", `{"password":"changed-password"}`), 200)
	status(t, f.req("GET", "/api/v1/users?limit=1", nil), 200)
	status(t, f.req("DELETE", "/api/v1/users/1", `{"confirm":true}`), 422)
	status(t, f.req("DELETE", "/api/v1/users/2", `{"confirm":true}`), 200)
}
func TestQueryFieldsAndEvents(t *testing.T) {
	f := newAPI(t)
	f.login(t)
	status(t, f.req("POST", "/api/v1/devices", `{"name":"test"}`), 201)
	now := time.Now().UTC()
	sqlExec(t, f.db, "INSERT INTO history_sample(device_id,ts,data) VALUES(1,?,?)", now.UnixMilli(), `{"battery_capacity_pct":80,"battery_voltage":25}`)
	sqlExec(t, f.db, "INSERT INTO events(device_id,type,severity,started_at,active) VALUES(1,'battery_low','warning',?,1)", now.UnixMilli())
	path := "/api/v1/devices/1/history?from=" + now.Add(-time.Second).Format(time.RFC3339Nano) + "&to=" + now.Add(time.Second).Format(time.RFC3339Nano) + "&fields=battery_capacity_pct"
	w := f.req("GET", path, nil)
	status(t, w, 200)
	if strings.Contains(w.Body.String(), "battery_voltage") || !strings.Contains(w.Body.String(), `"avg":80`) {
		t.Fatal(w.Body.String())
	}
	status(t, f.req("GET", path+",raw_register", nil), 422)
	status(t, f.req("GET", "/api/v1/events?device_id=1&type=battery_low&severity=warning", nil), 200)
	status(t, f.req("GET", "/api/v1/alerts", nil), 200)
	status(t, f.req("GET", "/api/v1/events?device_id=oops", nil), 400)
}
func FuzzStrictJSON(f *testing.F) {
	for _, s := range []string{`{}`, `{"x":1,"x":2}`, `{"x":[true,false]}`, `null`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 512*1024 {
			t.Skip()
		}
		if strictJSON(data) == nil && !json.Valid(data) {
			t.Fatal("接受非法JSON")
		}
	})
}
