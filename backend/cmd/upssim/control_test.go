package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func controlTestServer(t *testing.T) (*httptest.Server, *registry) {
	t.Helper()
	reg := newTestRegistry(t)
	srv := httptest.NewServer(controlHandler(reg, "tok", func() statusView { return statusView{} }, func() {}))
	t.Cleanup(srv.Close)
	return srv, reg
}

func send(t *testing.T, srv *httptest.Server, method, path, token, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

func TestControlAuthAndBodyLimits(t *testing.T) {
	srv, _ := controlTestServer(t)
	for _, token := range []string{"", "wrong"} {
		if code, body := send(t, srv, http.MethodGet, "/v1/devices", token, ""); code != http.StatusUnauthorized || !strings.Contains(body, "unauthorized") {
			t.Fatalf("token %q 应返回 401: %d %s", token, code, body)
		}
	}
	if code, _ := send(t, srv, http.MethodGet, "/v1/devices", "tok", ""); code != http.StatusOK {
		t.Fatalf("正确 token 应通过: %d", code)
	}
	if code, body := send(t, srv, http.MethodPost, "/v1/devices", "tok", `{"name":"a","unknown":1}`); code != http.StatusUnprocessableEntity || !strings.Contains(body, "validation_failed") {
		t.Fatalf("未知字段应拒绝: %d %s", code, body)
	}
	if code, _ := send(t, srv, http.MethodPost, "/v1/devices", "tok", `{"name":"a"}{"name":"b"}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("尾随对象应拒绝: %d", code)
	}
	large := `{"name":"a","scenario_yaml":"` + strings.Repeat("x", maxControlBody) + `"}`
	if code, _ := send(t, srv, http.MethodPost, "/v1/devices", "tok", large); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超大请求体应返回 413: %d", code)
	}
}

func TestControlPatchAtomicAndRedacted(t *testing.T) {
	srv, _ := controlTestServer(t)
	code, body := send(t, srv, http.MethodPost, "/v1/devices", "tok", `{"name":"a","password":"fake-pass","community":"fake-community"}`)
	if code != http.StatusCreated {
		t.Fatalf("创建失败: %d %s", code, body)
	}
	if strings.Contains(body, "fake-") {
		t.Fatal("响应泄露凭据")
	}
	// 不可修改字段按未知字段拒绝。
	if code, _ := send(t, srv, http.MethodPatch, "/v1/devices/a", "tok", `{"ip":"127.0.0.2"}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("PATCH 不可修改字段应拒绝: %d", code)
	}
	// low 与大电量组合被拒绝，且负载修改不能部分生效。
	if code, _ := send(t, srv, http.MethodPatch, "/v1/devices/a", "tok", `{"mode":"low","battery_capacity_pct":50,"params":{"load_pct":80}}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("非法组合应拒绝: %d", code)
	}
	_, body = send(t, srv, http.MethodGet, "/v1/devices/a", "tok", "")
	var view deviceView
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatal(err)
	}
	if view.Mode != "mains" || view.Params.LoadPct != 30 || !view.PasswordSet || strings.Contains(body, "fake-") {
		t.Fatalf("PATCH 非原子或泄露凭据: %s", body)
	}
	if code, body := send(t, srv, http.MethodGet, "/v1/devices/missing", "tok", ""); code != http.StatusNotFound || !strings.Contains(body, "device_not_found") {
		t.Fatalf("不存在的设备应 404: %d %s", code, body)
	}
}

func TestDialControlSafety(t *testing.T) {
	dir := t.TempDir()
	if _, err := dialControl(dir); !errors.Is(err, errNotRunning) {
		t.Fatalf("缺少 control.json 应视为未运行: %v", err)
	}
	write := func(address string) {
		data, _ := json.Marshal(controlInfo{Schema: 1, Address: address, Token: "tok"})
		if err := os.WriteFile(filepath.Join(dir, controlFile), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("192.0.2.10:80")
	if _, err := dialControl(dir); err == nil || errors.Is(err, errNotRunning) {
		t.Fatalf("非 loopback 地址应拒绝连接: %v", err)
	}
	// 进程崩溃遗留的 control.json：地址不可达视为未运行。
	write("127.0.0.1:1")
	if _, err := dialControl(dir); !errors.Is(err, errNotRunning) {
		t.Fatalf("不可达应视为未运行: %v", err)
	}
}
