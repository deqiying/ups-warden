package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ups-warden/internal/action"
	"ups-warden/internal/model"
)

func createActionAPI(t *testing.T, f *apiFixture, in any) int64 {
	t.Helper()
	w := f.req("POST", "/api/v1/actions", in)
	status(t, w, 201)
	var v struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v.ID
}
func actionHTTPInput(url string) map[string]any {
	return map[string]any{"name": "HTTP fake", "type": "http", "url": url, "headers": map[string]string{"Authorization": "action-private-header"}, "body_template": `{"private":"action-private-body","name":"{{device.name}}"}`}
}
func awaitActionAPI(t *testing.T, f *apiFixture, id int64) actionRunView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		items, err := f.s.actionRunRows(t.Context(), 0, 0, 200)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range items {
			if v.ID == id && v.Status != "running" {
				return v
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等待动作日志超时")
	return actionRunView{}
}
func actionRunID(t *testing.T, w *httptest.ResponseRecorder) int64 {
	t.Helper()
	status(t, w, 202)
	var v struct {
		ID int64 `json:"run_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v.ID
}
func TestActionsCRUDEncryptionConfirmationAndAudit(t *testing.T) {
	f := newAPI(t)
	for _, path := range []string{"/api/v1/actions", "/api/v1/scripts", "/api/v1/action-runs"} {
		status(t, f.req("GET", path, nil), 401)
	}
	f.login(t)
	id := createActionAPI(t, f, actionHTTPInput("http://127.0.0.1:1/action-private-url"))
	path := fmt.Sprintf("/api/v1/actions/%d", id)
	for _, endpoint := range []string{path, "/api/v1/actions", "/api/v1/audit"} {
		w := f.req("GET", endpoint, nil)
		status(t, w, 200)
		if strings.Contains(w.Body.String(), "action-private") {
			t.Fatal("响应或审计泄密", endpoint)
		}
	}
	var cfg string
	var encrypted []byte
	if err := f.db.Read().QueryRow("SELECT config,secret FROM actions WHERE id=?", id).Scan(&cfg, &encrypted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cfg, "action-private") || bytes.Contains(encrypted, []byte("action-private")) {
		t.Fatal("敏感配置未加密")
	}
	status(t, f.req("PUT", path, `{"name":"renamed","url":"","body_template":""}`), 200)
	c, err := action.Read(t.Context(), f.db, f.s.opts.Cipher, id)
	if err != nil || !strings.Contains(c.Credentials.URL.Reveal(), "action-private-url") || c.Credentials.BodyTemplate.Reveal() == "" {
		t.Fatal("空串未保留旧凭据", err)
	}
	status(t, f.req("PUT", path, `{"clear_secrets":["headers","body_template"]}`), 200)
	c, err = action.Read(t.Context(), f.db, f.s.opts.Cipher, id)
	if err != nil || len(c.Credentials.Headers) != 0 || c.Credentials.BodyTemplate.Reveal() != "" {
		t.Fatal("清除失败", err)
	}
	for _, body := range []string{`{}`, `{"dry_run":true}`, `{"confirm":false,"dry_run":true}`} {
		status(t, f.req("POST", path+"/run", body), 422)
	}
	for _, body := range []string{`{"type":"plan"}`, `{"timeout_s":0}`, `{"timeout_s":3601}`, `{"clear_secrets":["url"]}`, `{"clear_secrets":["headers","headers"]}`, `{"body_template":"{{bad}}"}`} {
		status(t, f.req("PUT", path, body), 422)
	}
	status(t, f.req("PUT", path, `{"config":{"unknown":1}}`), 400)
	csrf := f.csrf
	f.csrf = "wrong"
	status(t, f.req("POST", path+"/run", `{"confirm":true,"dry_run":true}`), 403)
	f.csrf = csrf
	runID := actionRunID(t, f.req("POST", path+"/run", `{"confirm":true,"dry_run":true}`))
	v := awaitActionAPI(t, f, runID)
	if v.Status != "dry_run" || v.Trigger.UserID == 0 || v.EndedAt == nil {
		t.Fatal(v)
	}
	audit := f.req("GET", "/api/v1/audit", nil).Body.String()
	if strings.Contains(audit, "action-private") || !strings.Contains(audit, "action.run") || !strings.Contains(audit, "accepted") {
		t.Fatal("运行接受审计缺失或泄密")
	}
	status(t, f.req("DELETE", path, `{"confirm":true}`), 409) // 历史日志引用不得删除。
	unused := createActionAPI(t, f, actionHTTPInput("http://127.0.0.1:1/unused"))
	unusedPath := fmt.Sprintf("/api/v1/actions/%d", unused)
	status(t, f.req("DELETE", unusedPath, `{"confirm":false}`), 422)
	status(t, f.req("DELETE", unusedPath, `{"confirm":true}`), 200)
	status(t, f.req("GET", unusedPath, nil), 404)
}
func TestActionAPIBusyAndPagination(t *testing.T) {
	f := newAPI(t)
	f.login(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		entered <- struct{}{}
		<-release
		w.WriteHeader(204)
	}))
	defer func() { close(release); server.Close() }()
	id := createActionAPI(t, f, actionHTTPInput(server.URL))
	path := fmt.Sprintf("/api/v1/actions/%d", id)
	first := actionRunID(t, f.req("POST", path+"/run", `{"confirm":true}`))
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("动作未到达本机假 HTTP")
	}
	for _, method := range []string{"PUT", "DELETE"} {
		body := `{"name":"new"}`
		if method == "DELETE" {
			body = `{"confirm":true}`
		}
		w := f.req(method, path, body)
		status(t, w, 409)
		if !strings.Contains(w.Body.String(), "action_busy") {
			t.Fatal(w.Body.String())
		}
	}
	second := actionRunID(t, f.req("POST", path+"/run", `{"confirm":true}`))
	if v := awaitActionAPI(t, f, second); v.Status != "skipped_concurrent" {
		t.Fatal(v)
	}
	w := f.req("GET", fmt.Sprintf("/api/v1/action-runs?action_id=%d&limit=1", id), nil)
	status(t, w, 200)
	var page struct {
		Items []actionRunView `json:"items"`
		Next  int64           `json:"next_before"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Next == 0 {
		t.Fatal(w.Body.String(), err)
	}
	w = f.req("GET", fmt.Sprintf("/api/v1/action-runs?before=%d", page.Next), nil)
	status(t, w, 200)
	if !strings.Contains(w.Body.String(), fmt.Sprintf(`"id":%d`, first)) {
		t.Fatal(w.Body.String())
	}
	for _, q := range []string{"action_id=0", "action_id=-1", "action_id=x", "before=0", "limit=201"} {
		status(t, f.req("GET", "/api/v1/action-runs?"+q, nil), 400)
	}
}
func TestActionAPIAuditRollbackAndGuard(t *testing.T) {
	f := newAPI(t)
	f.login(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	defer server.Close()
	id := createActionAPI(t, f, actionHTTPInput(server.URL))
	path := fmt.Sprintf("/api/v1/actions/%d", id)
	sqlExec(t, f.db, "CREATE TRIGGER reject_action_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT,'audit fail'); END")
	status(t, f.req("PUT", path, `{"name":"not-saved"}`), 409)
	status(t, f.req("POST", path+"/run", `{"confirm":true}`), 409)
	var name string
	var count int
	if err := f.db.Read().QueryRow("SELECT name FROM actions WHERE id=?", id).Scan(&name); err != nil || name == "not-saved" {
		t.Fatal(name, err)
	}
	if err := f.db.Read().QueryRow("SELECT count(*) FROM action_runs").Scan(&count); err != nil || count != 0 || calls.Load() != 0 {
		t.Fatal("审计失败仍接受或外发", count, err)
	}
	sqlExec(t, f.db, "DROP TRIGGER reject_action_audit")
	sqlExec(t, f.db, "INSERT INTO devices(id,name) VALUES(1,'fake')")
	sqlExec(t, f.db, "INSERT INTO sources(device_id,protocol,host,port,profile_key,profile_version,poll_ms,timeout_ms,enabled) VALUES(1,'fake','localhost',1,'fake',1,1000,1000,0)")
	status(t, f.req("POST", "/api/v1/actions", actionHTTPInput(server.URL)), 422)
	status(t, f.req("PUT", path, `{"name":"blocked"}`), 422)
	run := actionRunID(t, f.req("POST", path+"/run", `{"confirm":true}`))
	if v := awaitActionAPI(t, f, run); v.Status != "failed" {
		t.Fatal(v)
	}
	if calls.Load() != 0 {
		t.Fatal("执行时未重新检查护栏")
	}
}
func TestActionAPIReferencesAndScripts(t *testing.T) {
	f := newAPI(t)
	f.login(t)
	for _, kind := range []string{"rule", "recovery", "plan"} {
		t.Run(kind, func(t *testing.T) {
			id := createActionAPI(t, f, actionHTTPInput("http://127.0.0.1:1/unused"))
			path := fmt.Sprintf("/api/v1/actions/%d", id)
			switch kind {
			case "rule":
				sqlExec(t, f.db, "INSERT INTO rules(name,scope,trigger,actions) VALUES('fake','{}','{}',?)", fmt.Sprintf(`[{"action_id":%d}]`, id))
			case "recovery":
				sqlExec(t, f.db, "INSERT INTO rules(name,scope,trigger,actions,recovery_actions) VALUES('fake','{}','{}','[]',?)", fmt.Sprintf(`[{"action_id":%d}]`, id))
			case "plan":
				sqlExec(t, f.db, "INSERT INTO plans(name,config) VALUES('fake',?)", fmt.Sprintf(`{"stages":[{"steps":[{"action_id":%d}]}]}`, id))
			}
			status(t, f.req("DELETE", path, `{"confirm":true}`), 409)
		})
	}
	dir := f.s.opts.Actions.ScriptDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "harmless.sh"), []byte("exit 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	w := f.req("GET", "/api/v1/scripts", nil)
	status(t, w, 200)
	if !strings.Contains(w.Body.String(), "harmless.sh") {
		t.Fatal(w.Body.String())
	}
	status(t, f.req("POST", "/api/v1/actions", map[string]any{"name": "bad path", "type": "script", "config": map[string]any{"script": "../outside.sh"}}), 422)
	status(t, f.req("POST", "/api/v1/actions", map[string]any{"name": "missing channel", "type": "notify", "config": map[string]any{"channel_ids": []int64{999}}}), 422)
	status(t, f.req("POST", "/api/v1/actions", map[string]any{"name": "missing host", "type": "ssh", "config": map[string]any{"host_id": 999, "command": "echo fake"}}), 422)
}
func TestActionAPIDeviceSnapshotContext(t *testing.T) {
	f := newAPI(t)
	f.login(t)
	sqlExec(t, f.db, "INSERT INTO devices(id,name) VALUES(42,'fake snapshot device')")
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		received <- string(data)
		w.WriteHeader(204)
	}))
	defer server.Close()
	id := createActionAPI(t, f, actionHTTPInput(server.URL))
	path := fmt.Sprintf("/api/v1/actions/%d/run", id)
	status(t, f.req("POST", path, `{"confirm":true,"device_id":42}`), 409)
	f.s.opts.Runtime = &snapshotRuntime{states: []model.DeviceState{{DeviceID: 42, PowerState: "online"}}}
	run := actionRunID(t, f.req("POST", path, `{"confirm":true,"device_id":42}`))
	if v := awaitActionAPI(t, f, run); v.Status != "succeeded" {
		t.Fatal(v)
	}
	got := <-received
	if !strings.Contains(got, "fake snapshot device") {
		t.Fatal(got)
	}
}
