package action

import (
	"bytes"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ups-warden/internal/bus"
	"ups-warden/internal/model"
	"ups-warden/internal/notify"
	"ups-warden/internal/secret"
	"ups-warden/internal/store"
)

func fixture(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(dir, "actions.db"))
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
	b := &bus.Bus{}
	t.Cleanup(b.Close)
	n, err := notify.New(t.Context(), db, cipher, b, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := n.Close(); err != nil {
			t.Error(err)
		}
	})
	s, err := New(t.Context(), db, cipher, n, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func execSQL(t *testing.T, s *Service, q string, args ...any) {
	t.Helper()
	if _, err := s.db.Write().ExecContext(t.Context(), q, args...); err != nil {
		t.Fatal(err)
	}
}
func add(t *testing.T, s *Service, r Record) int64 {
	t.Helper()
	Normalize(&r)
	if err := Validate(r); err != nil {
		t.Fatal(err)
	}
	cfg, encrypted, err := Encode(r, s.cipher)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.db.Write().ExecContext(t.Context(), "INSERT INTO actions(name,type,timeout_s,config,secret) VALUES(?,?,?,?,?)", r.Name, r.Type, r.TimeoutS, cfg, encrypted)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func submit(t *testing.T, s *Service, id int64, dry bool) int64 {
	t.Helper()
	runID, err := s.Submit(t.Context(), Request{ActionID: id, DryRun: dry, Trigger: Trigger{Type: "manual"}, Event: model.Event{Type: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	return runID
}
func await(t *testing.T, s *Service, id int64) result {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var v result
		err := s.db.Read().QueryRowContext(t.Context(), "SELECT status,exit_code,stdout,stderr,error FROM action_runs WHERE id=?", id).Scan(&v.status, &v.code, &v.stdout, &v.stderr, &v.message)
		if err != nil {
			t.Fatal(err)
		}
		if v.status != "running" {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等待动作超时")
	return result{}
}
func httpRecord(url string) Record {
	return Record{Name: "HTTP", Type: "http", Credentials: Credentials{URL: secret.NewString(url), Headers: map[string]secret.String{"Authorization": secret.NewString("private-header")}, BodyTemplate: secret.NewString(`{"name":"{{device.name}}","private":"private-body"}`)}}
}
func TestDryRunHasNoHTTPOrDNSAndSecretsEncrypted(t *testing.T) {
	s := fixture(t)
	r := httpRecord("http://dry-run.invalid/private-path")
	id := add(t, s, r)
	run := submit(t, s, id, true)
	v := await(t, s, run)
	if v.status != "dry_run" || !strings.Contains(v.stdout, "未解析 DNS") {
		t.Fatal(v)
	}
	var cfg string
	var encrypted []byte
	if err := s.db.Read().QueryRow("SELECT config,secret FROM actions WHERE id=?", id).Scan(&cfg, &encrypted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cfg, "private") || bytes.Contains(encrypted, []byte("private")) {
		t.Fatal("秘密未加密")
	}
	loaded, err := Read(t.Context(), s.db, s.cipher, id)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Credentials.URL.Reveal() != r.Credentials.URL.Reveal() {
		t.Fatal("解密错误")
	}
	for _, secret := range []string{"private-path", "private-header", "private-body"} {
		if strings.Contains(v.stdout+v.stderr+v.message, secret) {
			t.Fatal("dry-run 泄密")
		}
	}
	if _, err = s.cipher.Decrypt(encrypted, "channels.secret"); err == nil {
		t.Fatal("AAD 未隔离")
	}
}
func TestHTTPWriteAheadMutexChangeAndClose(t *testing.T) {
	s := fixture(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		var running int
		if err := s.db.Read().QueryRow("SELECT count(*) FROM action_runs WHERE status='running'").Scan(&running); err != nil || running == 0 {
			t.Error("外发之前未持久化 running", err)
		}
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(204)
	}))
	defer func() { close(release); server.Close() }()
	id := add(t, s, httpRecord(server.URL))
	first := submit(t, s, id, false)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("未执行")
	}
	second := submit(t, s, id, false)
	if v := await(t, s, second); v.status != "skipped_concurrent" {
		t.Fatal(v)
	}
	changed := false
	if err := s.Change(t.Context(), id, func() error { changed = true; return nil }); !errors.Is(err, ErrBusy) || changed {
		t.Fatal("未阻止配置变更", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if v := await(t, s, first); v.status != "cancelled" {
		t.Fatal(v)
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	if _, err := s.Submit(t.Context(), Request{ActionID: id}); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}
func TestWriteAheadAndAuditFailureHaveNoSideEffects(t *testing.T) {
	s := fixture(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	defer server.Close()
	id := add(t, s, httpRecord(server.URL))
	req := Request{ActionID: id, Trigger: Trigger{Type: "manual"}}
	execSQL(t, s, "CREATE TRIGGER reject_run BEFORE INSERT ON action_runs BEGIN SELECT RAISE(ABORT,'write failed'); END")
	if _, err := s.Submit(t.Context(), req); err == nil {
		t.Fatal("write-ahead 失败被忽略")
	}
	execSQL(t, s, "DROP TRIGGER reject_run")
	if _, err := s.SubmitWithAudit(t.Context(), req, func(*sql.Tx, int64) error { return errors.New("audit failed") }); err == nil {
		t.Fatal("审计失败被忽略")
	}
	if calls.Load() != 0 {
		t.Fatal("持久化失败仍外发")
	}
	var count int
	if err := s.db.Read().QueryRow("SELECT count(*) FROM action_runs").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
func TestRestartMarksUnknownWithoutReplay(t *testing.T) {
	s := fixture(t)
	id := add(t, s, httpRecord("http://127.0.0.1:1/never"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	execSQL(t, s, "INSERT INTO action_runs(action_id,trigger,dry_run,status,started_at) VALUES(?,'{}',0,'running',1)", id)
	restarted, err := New(t.Context(), s.db, s.cipher, s.notifications, s.scriptDir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	var status string
	var ended sql.NullInt64
	if err := s.db.Read().QueryRow("SELECT status,ended_at FROM action_runs").Scan(&status, &ended); err != nil || status != "unknown" || !ended.Valid {
		t.Fatal(status, ended, err)
	}
	if len(restarted.busy) != 0 {
		t.Fatal("重启重放了动作")
	}
}
func addSource(t *testing.T, s *Service, host string) {
	execSQL(t, s, "INSERT OR IGNORE INTO devices(id,name) VALUES(1,'fake')")
	execSQL(t, s, "INSERT INTO sources(device_id,protocol,host,port,profile_key,profile_version,poll_ms,timeout_ms,enabled) VALUES(1,'fake',?,1,'fake',1,1000,1000,0)", host)
}
func TestHTTPGuardAtSaveAndExecutionAndSetting(t *testing.T) {
	s := fixture(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	defer server.Close()
	r := httpRecord(server.URL)
	Normalize(&r)
	if err := s.Validate(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	id := add(t, s, r)
	addSource(t, s, "localhost") // 不同 hostname，且数据源已停用，仍按解析后的 IP 阻止。
	if err := s.Validate(t.Context(), r); err == nil {
		t.Fatal("保存未检查 UPS 主机")
	}
	if v := await(t, s, submit(t, s, id, false)); v.status != "failed" {
		t.Fatal(v)
	}
	if calls.Load() != 0 {
		t.Fatal("护栏失败仍外发")
	}
	execSQL(t, s, "INSERT INTO settings(key,value) VALUES('http_action_guard_ups_hosts','false')")
	if v := await(t, s, submit(t, s, id, false)); v.status != "succeeded" {
		t.Fatal(v)
	}
	execSQL(t, s, "UPDATE settings SET value='true'")
	if v := await(t, s, submit(t, s, id, false)); v.status != "failed" {
		t.Fatal(v)
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
}
func TestHTTPTemplateRedirectProxyAndTimeout(t *testing.T) {
	s := fixture(t)
	var targetCalls, proxyCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1); w.WriteHeader(204) }))
	defer target.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxyCalls.Add(1); w.WriteHeader(500) }))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, target.URL, 302)
		case "/timeout":
			_, _ = io.Copy(io.Discard, r.Body)
			<-r.Context().Done()
		default:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			if r.Header.Get("Authorization") != "private-header" || !bytes.Contains(body, []byte(`quoted\"`)) {
				t.Error("模板或 Header 错误", string(body))
			}
			w.WriteHeader(201)
		}
	}))
	defer server.Close()
	r := httpRecord(server.URL)
	r.Config.ExpectedStatus = []int{201}
	id := add(t, s, r)
	run, err := s.Submit(t.Context(), Request{ActionID: id, DeviceName: `quoted"`, Trigger: Trigger{Type: "manual"}})
	if err != nil {
		t.Fatal(err)
	}
	if v := await(t, s, run); v.status != "succeeded" {
		t.Fatal(v)
	}
	redirected := add(t, s, httpRecord(server.URL+"/redirect"))
	if v := await(t, s, submit(t, s, redirected, false)); v.status != "failed" {
		t.Fatal(v)
	}
	if targetCalls.Load() != 0 || proxyCalls.Load() != 0 {
		t.Fatal("绕过了重定向/代理限制")
	}
	r = httpRecord(server.URL + "/timeout")
	r.TimeoutS = 1
	timed := add(t, s, r)
	if v := await(t, s, submit(t, s, timed, false)); v.status != "timeout" {
		t.Fatal(v)
	}
}
func TestDependenciesAndInvalidConfig(t *testing.T) {
	if _, err := New(t.Context(), nil, nil, nil, ""); err == nil {
		t.Fatal("必需依赖未 fail-fast")
	}
	for _, r := range []Record{{Name: "plan", Type: "plan", TimeoutS: 30}, {Name: "script", Type: "script", TimeoutS: 30, Config: Config{Script: "../bad"}}, {Name: "http", Type: "http", TimeoutS: 3601}, {Name: "http", Type: "http", TimeoutS: 30, Config: Config{Method: "POST"}, Credentials: Credentials{URL: secret.NewString("http://user:password@localhost/")}}} {
		if Validate(r) == nil {
			t.Fatalf("接受无效配置 %+v", r)
		}
	}
}
func TestDryRunScriptAndNotifyDoNotExecute(t *testing.T) {
	s := fixture(t)
	script := add(t, s, Record{Name: "missing script", Type: "script", Config: Config{Script: "does-not-exist.sh"}})
	execSQL(t, s, "INSERT INTO channels(id,name,type,config,enabled) VALUES(1,'disabled','webhook','{}',0)")
	notification := add(t, s, Record{Name: "notify", Type: "notify", Config: Config{ChannelIDs: []int64{1}}})
	ssh := add(t, s, Record{Name: "missing host dry-run", Type: "ssh", Config: Config{HostID: 999, Command: "echo harmless"}})
	for _, id := range []int64{script, notification, ssh} {
		if v := await(t, s, submit(t, s, id, true)); v.status != "dry_run" {
			t.Fatal(v)
		}
	}
	var count int
	if err := s.db.Read().QueryRow("SELECT count(*) FROM notify_logs").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
func TestWorkerPanicIsFailedAndDoesNotLeak(t *testing.T) {
	s := fixture(t)
	execSQL(t, s, "INSERT INTO channels(id,name,type,config,enabled) VALUES(1,'fake','webhook','{}',1)")
	id := add(t, s, Record{Name: "notify", Type: "notify", Config: Config{ChannelIDs: []int64{1}}})
	s.notifications = nil // 在已校验引用之后，唯一执行协程必须隔离依赖 panic。
	v := await(t, s, submit(t, s, id, false))
	if v.status != "failed" || !strings.Contains(v.message, "panic") {
		t.Fatal(v)
	}
}
func TestResultWriteFailureIsReturnedByClose(t *testing.T) {
	s := fixture(t)
	id := add(t, s, httpRecord("http://127.0.0.1:1/never"))
	execSQL(t, s, "CREATE TRIGGER reject_result BEFORE UPDATE ON action_runs BEGIN SELECT RAISE(ABORT,'result failed'); END")
	submit(t, s, id, true)
	s.wg.Wait()
	if err := s.Close(); err == nil {
		t.Fatal("结果写入失败被吞掉")
	}
	// 避免 fixture 的重复 Close 将预期错误计作测试失败。
	s.mu.Lock()
	s.closeErr = nil
	s.mu.Unlock()
}
func TestBatchArgumentsAndOutputCap(t *testing.T) {
	for _, v := range []string{"a&echo", "%PATH%", "!VAR!", "x|y", "x\"y", "a b", "x\ny", "(x)", ""} {
		if safeBatchArgument(v) {
			t.Fatal("接受 cmd 元字符", v)
		}
	}
	if !safeBatchArgument(`C:\scripts\test.cmd`) || !safeBatchArgument("abc-123") {
		t.Fatal("安全参数被拒绝")
	}
	var b cappedBuffer
	n, err := io.Copy(&b, strings.NewReader(strings.Repeat("x", 20000)))
	if err != nil || n != 20000 || len(b.String()) != 4096 {
		t.Fatal(n, err, len(b.String()))
	}
}
func TestScriptPathAndListing(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.sh"), []byte("exit 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "bad.sh"), []byte("exit 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../bad.sh", filepath.Join(outside, "bad.sh"), "a/../ok.sh", "C:/bad.sh"} {
		if _, err := scriptPath(dir, name); err == nil {
			t.Fatal("路径逃逸", name)
		}
	}
	if err := os.Symlink(filepath.Join(outside, "bad.sh"), filepath.Join(dir, "escape.sh")); err == nil {
		if _, err := scriptPath(dir, "escape.sh"); err == nil {
			t.Fatal("符号链接逃逸")
		}
	}
	items, err := Scripts(dir)
	if err != nil || len(items) != 1 || items[0] != "ok.sh" {
		t.Fatal(items, err)
	}
}

func TestNotifyUsesExistingQueueAndReportsQueued(t *testing.T) {
	s := fixture(t)
	received := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		received <- struct{}{}
		<-release
		w.WriteHeader(204)
	}))
	defer func() { close(release); server.Close() }()
	c := notify.ChannelRecord{Name: "local fake", Type: "webhook", Enabled: true, Credentials: notify.Credentials{URL: server.URL}}
	notify.NormalizeConfig(c.Type, &c.Config)
	cfg, encrypted, err := notify.EncodeChannel(c, s.cipher)
	if err != nil {
		t.Fatal(err)
	}
	var channelID int64
	err = s.notifications.Change(t.Context(), 0, func() (int64, error) {
		r, err := s.db.Write().ExecContext(t.Context(), "INSERT INTO channels(name,type,config,secret,enabled) VALUES(?,?,?,?,1)", c.Name, c.Type, cfg, encrypted)
		if err != nil {
			return 0, err
		}
		channelID, err = r.LastInsertId()
		return channelID, err
	})
	if err != nil {
		t.Fatal(err)
	}
	id := add(t, s, Record{Name: "notify", Type: "notify", Config: Config{ChannelIDs: []int64{channelID}}})
	if err := s.Validate(t.Context(), Record{Name: "notify", Type: "notify", TimeoutS: 30, Config: Config{ChannelIDs: []int64{channelID}}}); err != nil {
		t.Fatal(err)
	}
	run := submit(t, s, id, false)
	if v := await(t, s, run); v.status != "queued" || !strings.Contains(v.stdout, "不表示发送成功") {
		t.Fatal(v)
	}
	select {
	case <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("通知未到达本机假渠道")
	}
	var count int
	if err := s.db.Read().QueryRow("SELECT count(*) FROM notify_logs").Scan(&count); err != nil || count != 0 {
		t.Fatal("尚未发送成功却记录成功", count, err)
	}
}

func TestGlobalInFlightLimitIsRecorded(t *testing.T) {
	s := fixture(t)
	id := add(t, s, httpRecord("http://127.0.0.1:1/never"))
	s.mu.Lock()
	for i := int64(1000); i < 1032; i++ {
		s.busy[i] = true
	}
	s.mu.Unlock()
	if v := await(t, s, submit(t, s, id, false)); v.status != "skipped_capacity" {
		t.Fatal(v)
	}
}
