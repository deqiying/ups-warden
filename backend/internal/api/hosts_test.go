package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"ups-warden/internal/host"
)

func newHostAPI(t *testing.T) *apiFixture {
	t.Helper()
	f := newAPI(t)
	// 主代理接入 api.go 前后均可独立运行，不重复注册路由。
	_, pattern := f.s.mux.Handler(httptest.NewRequest("GET", "http://localhost/api/v1/hosts", nil))
	if pattern != "GET /api/v1/hosts" {
		f.s.hostRoutes()
	}
	return f
}
func createHost(t *testing.T, f *apiFixture, port int) int64 {
	t.Helper()
	w := f.req("POST", "/api/v1/hosts", map[string]any{"name": "loopback", "address": "127.0.0.1", "kind": "linux", "conn": map[string]any{"port": port, "username": "tester", "auth_method": "password"}, "password": "host-private-password", "online_check": map[string]any{"type": "tcp", "port": port}})
	status(t, w, 201)
	var h host.Record
	if err := json.Unmarshal(w.Body.Bytes(), &h); err != nil {
		t.Fatal(err)
	}
	return h.ID
}

type apiSSHFake struct {
	port        int
	fingerprint string
	auth        atomic.Int32
	commands    chan string
}

func newHostSSH(t *testing.T) *apiSSHFake {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(port)
	f := &apiSSHFake{port: p, fingerprint: ssh.FingerprintSHA256(signer.PublicKey()), commands: make(chan string, 16)}
	cfg := &ssh.ServerConfig{AuthLogCallback: func(_ ssh.ConnMetadata, _ string, _ error) { f.auth.Add(1) }, PasswordCallback: func(_ ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if string(p) != "host-private-password" {
			return nil, errors.New("wrong password")
		}
		return nil, nil
	}}
	cfg.AddHostKey(signer)
	var mu sync.Mutex
	conns := map[net.Conn]bool{}
	closing := false
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			if closing {
				mu.Unlock()
				_ = conn.Close()
				return
			}
			conns[conn] = true
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { _ = conn.Close(); mu.Lock(); delete(conns, conn); mu.Unlock() }()
				s, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				defer s.Close()
				go ssh.DiscardRequests(reqs)
				for next := range chans {
					ch, requests, err := next.Accept()
					if err != nil {
						return
					}
					for req := range requests {
						if req.Type != "exec" {
							_ = req.Reply(false, nil)
							continue
						}
						var command struct{ Command string }
						if ssh.Unmarshal(req.Payload, &command) != nil {
							_ = req.Reply(false, nil)
							continue
						}
						f.commands <- command.Command
						_ = req.Reply(true, nil)
						_, _ = io.WriteString(ch, "ups-warden-ok\n")
						_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{0}))
						_ = ch.Close()
						break
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		closing = true
		for conn := range conns {
			_ = conn.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return f
}
func fetchAPIHostKey(t *testing.T, f *apiFixture, path string) string {
	t.Helper()
	w := f.req("POST", path+"/hostkey/fetch", `{}`)
	status(t, w, 200)
	var result struct {
		Fingerprint string    `json:"fingerprint"`
		Expires     time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Fingerprint == "" || !result.Expires.After(time.Now()) {
		t.Fatal(w.Body.String())
	}
	return result.Fingerprint
}
func TestHostsCRUDEncryptionAuditAndRollback(t *testing.T) {
	f := newHostAPI(t)
	status(t, f.req("GET", "/api/v1/hosts", nil), 401)
	f.login(t)
	id := createHost(t, f, 22)
	path := fmt.Sprintf("/api/v1/hosts/%d", id)
	for _, endpoint := range []string{path, "/api/v1/hosts", "/api/v1/audit"} {
		w := f.req("GET", endpoint, nil)
		status(t, w, 200)
		if strings.Contains(w.Body.String(), "host-private-password") {
			t.Fatal("泄露凭据", endpoint)
		}
	}
	w := f.req("GET", path, nil)
	if !strings.Contains(w.Body.String(), `"password_set":true`) {
		t.Fatal(w.Body.String())
	}
	var encrypted []byte
	var cfg string
	if err := f.db.Read().QueryRow("SELECT conn,secret FROM hosts WHERE id=?", id).Scan(&cfg, &encrypted); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte("host-private-password")) || strings.Contains(cfg, "host-private-password") {
		t.Fatal("凭据未加密")
	}
	if _, err := f.s.opts.Cipher.Decrypt(encrypted, "hosts.secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.opts.Cipher.Decrypt(encrypted, "channels.secret"); err == nil {
		t.Fatal("缺少 AAD 绑定")
	}
	status(t, f.req("PUT", path, `{"name":"renamed","password":""}`), 200)
	h, err := host.Read(t.Context(), f.db, f.s.opts.Cipher, id)
	if err != nil || h.Credentials.Password.Reveal() != "host-private-password" {
		t.Fatal(err)
	}
	status(t, f.req("PUT", path, `{"password":"rotated-private-password"}`), 200)
	var diff string
	if err := f.db.Read().QueryRow("SELECT diff FROM audit_logs WHERE kind='host.update' ORDER BY id DESC LIMIT 1").Scan(&diff); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, `"secret":"已修改"`) || strings.Contains(diff, "rotated") {
		t.Fatal(diff)
	}
	csrf := f.csrf
	f.csrf = "wrong"
	status(t, f.req("PUT", path, `{"name":"denied"}`), 403)
	f.csrf = csrf
	for _, body := range []string{`{"conn":{"port":0}}`, `{"address":"127.0.0.1:22"}`, `{"kind":"unknown"}`, `{"clear_secrets":["unknown"]}`, `{"clear_secrets":["password","password"]}`, `{"clear_secrets":["password"],"password":"conflict"}`, `{"clear_secrets":["password"]}`, `{"online_check":{"type":"tcp","port":0}}`} {
		status(t, f.req("PUT", path, body), 422)
	}
	status(t, f.req("PUT", path, `{"hostkey_fingerprint":"SHA256:forged"}`), 400)
	status(t, f.req("PUT", path, `{"conn":{"unknown":true}}`), 400)
	status(t, f.req("GET", "/api/v1/hosts/0", nil), 400)
	for _, q := range []string{"limit=201", "before=-1"} {
		status(t, f.req("GET", "/api/v1/hosts?"+q, nil), 400)
	}
	createHost(t, f, 22)
	w = f.req("GET", "/api/v1/hosts?limit=1", nil)
	status(t, w, 200)
	if !strings.Contains(w.Body.String(), `"next_before":2`) {
		t.Fatal(w.Body.String())
	}
	w = f.req("GET", "/api/v1/hosts?before=2&limit=1", nil)
	status(t, w, 200)
	if !strings.Contains(w.Body.String(), `"next_before":0`) {
		t.Fatal(w.Body.String())
	}
	sqlExec(t, f.db, "CREATE TRIGGER reject_host_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT,'audit failure'); END")
	status(t, f.req("PUT", path, `{"password":"must-rollback","name":"must-rollback"}`), 409)
	h, err = host.Read(t.Context(), f.db, f.s.opts.Cipher, id)
	if err != nil || h.Name != "renamed" || h.Credentials.Password.Reveal() != "rotated-private-password" {
		t.Fatal(h.Name, err)
	}
	status(t, f.req("DELETE", path, `{"confirm":true}`), 409)
	sqlExec(t, f.db, "DROP TRIGGER reject_host_audit")
	status(t, f.req("DELETE", path, `{"confirm":false}`), 422)
	status(t, f.req("DELETE", path, `{"confirm":true}`), 200)
	status(t, f.req("GET", path, nil), 404)
}
func TestHostsKeyConfirmationFixedCommandAndAddressInvalidation(t *testing.T) {
	f := newHostAPI(t)
	f.login(t)
	fake := newHostSSH(t)
	id := createHost(t, f, fake.port)
	path := fmt.Sprintf("/api/v1/hosts/%d", id)
	status(t, f.req("POST", path+"/test-connection", `{}`), 409)
	status(t, f.req("POST", path+"/test-command", `{"confirm":false}`), 422)
	status(t, f.req("POST", path+"/test-command", `{"confirm":true,"command":"unsafe"}`), 400)
	confirmBody := map[string]any{"confirm": true, "fingerprint": fake.fingerprint}
	status(t, f.req("POST", path+"/hostkey/confirm", confirmBody), 409)
	fp := fetchAPIHostKey(t, f, path)
	if fp != fake.fingerprint || fake.auth.Load() != 0 || len(fake.commands) != 0 {
		t.Fatal("fetch 执行了认证或命令")
	}
	status(t, f.req("POST", path+"/hostkey/confirm", map[string]any{"confirm": true, "fingerprint": "SHA256:forged"}), 409)
	status(t, f.req("POST", path+"/hostkey/confirm", map[string]any{"confirm": false, "fingerprint": fp}), 422)
	status(t, f.req("POST", path+"/hostkey/confirm", confirmBody), 200)
	status(t, f.req("POST", path+"/hostkey/confirm", confirmBody), 409)
	other := newHostSSH(t)
	sqlExec(t, f.db, "UPDATE hosts SET hostkey_fingerprint=? WHERE id=?", other.fingerprint, id)
	status(t, f.req("POST", path+"/test-command", `{"confirm":true}`), 409)
	if fake.auth.Load() != 0 || len(fake.commands) != 0 {
		t.Fatal("错误指纹仍进行认证或执行")
	}
	sqlExec(t, f.db, "UPDATE hosts SET hostkey_fingerprint=? WHERE id=?", fp, id)
	status(t, f.req("POST", path+"/test-connection", `{}`), 200)
	if len(fake.commands) != 0 {
		t.Fatal("test-connection 执行了命令")
	}
	status(t, f.req("PUT", path, `{"conn":{"sudo":true}}`), 200)
	status(t, f.req("POST", path+"/test-command", `{"confirm":true}`), 200)
	select {
	case command := <-fake.commands:
		if command != "echo ups-warden-ok" {
			t.Fatal(command)
		}
	case <-time.After(time.Second):
		t.Fatal("未执行固定测试命令")
	}
	w := f.req("POST", path+"/online-check", `{}`)
	status(t, w, 200)
	if !strings.Contains(w.Body.String(), `"online":true`) {
		t.Fatal(w.Body.String())
	}
	fetchAPIHostKey(t, f, path)
	status(t, f.req("PUT", path, `{"address":"localhost"}`), 200)
	status(t, f.req("POST", path+"/hostkey/confirm", confirmBody), 409)
	h, err := host.Read(t.Context(), f.db, f.s.opts.Cipher, id)
	if err != nil || h.Fingerprint != "" {
		t.Fatal("地址改变未清除指纹", err)
	}
	status(t, f.req("PUT", path, `{"address":"127.0.0.1"}`), 200)
	fetchAPIHostKey(t, f, path)
	status(t, f.req("POST", path+"/hostkey/confirm", confirmBody), 200)
	status(t, f.req("PUT", path, map[string]any{"conn": map[string]any{"port": fake.port + 1}}), 200)
	h, err = host.Read(t.Context(), f.db, f.s.opts.Cipher, id)
	if err != nil || h.Fingerprint != "" {
		t.Fatal("SSH 端口改变未清除指纹", err)
	}
}
func TestHostsExpiredAndReboundCandidateAndConfirmAuditRollback(t *testing.T) {
	f := newHostAPI(t)
	f.login(t)
	fake := newHostSSH(t)
	id := createHost(t, f, fake.port)
	a := &hostAPI{s: f.s, pending: make(map[int64]fetchedHostKey)}
	request := func(handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost/api/v1/hosts/1/hostkey", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.SetPathValue("id", strconv.FormatInt(id, 10))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	status(t, request(a.fetchKey, `{}`), 200)
	candidate := a.pending[id]
	candidate.expires = time.Now().Add(-time.Second)
	a.pending[id] = candidate
	body := fmt.Sprintf(`{"confirm":true,"fingerprint":%q}`, fake.fingerprint)
	status(t, request(a.confirmKey, body), 409)
	candidate.expires = time.Now().Add(time.Minute)
	candidate.address = "localhost"
	a.pending[id] = candidate
	status(t, request(a.confirmKey, body), 409)
	status(t, request(a.fetchKey, `{}`), 200)
	sqlExec(t, f.db, "CREATE TRIGGER reject_host_confirm BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT,'audit failure'); END")
	status(t, request(a.confirmKey, body), 409)
	h, err := host.Read(t.Context(), f.db, f.s.opts.Cipher, id)
	if err != nil || h.Fingerprint != "" {
		t.Fatal("确认未随审计失败回滚", err)
	}
	sqlExec(t, f.db, "DROP TRIGGER reject_host_confirm")
	status(t, request(a.confirmKey, body), 200)
}
func TestHostsPrivateKeyCredentialsAndReferences(t *testing.T) {
	f := newHostAPI(t)
	f.login(t)
	id := createHost(t, f, 22)
	path := fmt.Sprintf("/api/v1/hosts/%d", id)
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(key, "api", []byte("private-key-passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	privateKey := string(pem.EncodeToMemory(block))
	w := f.req("PUT", path, map[string]any{"conn": map[string]any{"auth_method": "private_key"}, "private_key": privateKey, "passphrase": "private-key-passphrase"})
	status(t, w, 200)
	if strings.Contains(w.Body.String(), "BEGIN") || strings.Contains(w.Body.String(), "private-key-passphrase") || !strings.Contains(w.Body.String(), `"private_key_set":true`) {
		t.Fatal("私钥响应不安全")
	}
	h, err := host.Read(t.Context(), f.db, f.s.opts.Cipher, id)
	if err != nil || h.Credentials.PrivateKey.Reveal() != privateKey || h.Credentials.Password.Reveal() != "" {
		t.Fatal(err)
	}
	sqlExec(t, f.db, `INSERT INTO actions(name,type,config,timeout_s) VALUES('referenced','ssh',?,10)`, fmt.Sprintf(`{"host_id":%d,"command":"never"}`, id))
	status(t, f.req("DELETE", path, `{"confirm":true}`), 409)
	sqlExec(t, f.db, "DELETE FROM actions")
	sqlExec(t, f.db, `INSERT INTO plans(id,name,config) VALUES(1,'plan','{}')`)
	sqlExec(t, f.db, `INSERT INTO plan_runs(id,plan_id,dry_run,status,trigger,plan_snapshot,started_at) VALUES(1,1,1,'completed','{}','{}',1)`)
	sqlExec(t, f.db, `INSERT INTO plan_run_steps(run_id,stage,step,host_id,status) VALUES(1,0,0,?,'sent')`, id)
	status(t, f.req("DELETE", path, `{"confirm":true}`), 409)
	sqlExec(t, f.db, "DELETE FROM plan_run_steps")
	status(t, f.req("DELETE", path, `{"confirm":true}`), 200)
	w = f.req("GET", "/api/v1/audit", nil)
	status(t, w, 200)
	if strings.Contains(w.Body.String(), "private-key-passphrase") || strings.Contains(w.Body.String(), "BEGIN OPENSSH") {
		t.Fatal("审计泄露私钥")
	}
}
