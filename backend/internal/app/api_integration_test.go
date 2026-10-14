package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"ups-warden/internal/config"
	"ups-warden/internal/model"
	"ups-warden/internal/sim"
)

func TestAPIDynamicCollectionAndRollback(t *testing.T) {
	simulator, err := sim.Start(sim.Config{ET1K: true})
	if err != nil {
		t.Fatal(err)
	}
	defer simulator.Close()
	_, address, _ := simulator.Addresses()
	host, port := loopbackPort(t, address)
	a, err := open(context.Background(), config.Config{DataDir: t.TempDir()}, testLogger(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var token, csrf string
	request := func(method, path string, value any) *httptest.ResponseRecorder {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, "http://localhost/api/v1"+path, bytes.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.AddCookie(&http.Cookie{Name: "ups_warden_session", Value: token})
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		a.api.ServeHTTP(w, r)
		return w
	}
	expect := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
	}
	expect(request("POST", "/setup", map[string]any{"setup_token": a.auth.SetupToken(), "username": "admin", "password": "test-password"}), 201)
	w := request("POST", "/auth/login", map[string]any{"username": "admin", "password": "test-password"})
	expect(w, 200)
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	csrf = session.CSRF
	token = w.Result().Cookies()[0].Value
	ch, cancel := a.bus.Subscribe()
	defer cancel()
	await := func(id int64) {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case u := <-ch:
				if u.Sample && u.State.DeviceID == id && u.State.CommState == "ok" {
					return
				}
			case <-timer.C:
				t.Fatal("动态装配未采集")
			}
		}
	}
	expect(request("POST", "/devices", map[string]any{"name": "loopback"}), 201)
	source := map[string]any{"protocol": "modbus", "host": host, "port": port, "profile_key": "et1k-modbus", "poll_ms": 200, "timeout_ms": 100}
	expect(request("POST", "/devices/1/sources", source), 201)
	await(1)
	expect(request("GET", "/devices/1/snapshot", nil), 200)
	expect(request("GET", "/sources/1/raw", nil), 200)
	expect(request("POST", "/sources/1/test", map[string]any{}), 200)
	expect(request("POST", "/sources/test", source), 200)
	// 重载失败不能回显旧成功，也不能声称配置回滚。这里使用离线校验拒绝非法配置。
	expect(request("PATCH", "/sources/1", map[string]any{"host": "http://127.0.0.1"}), 422)
	pipelineSQL(t, a.db, `CREATE TRIGGER reject_api_audit BEFORE INSERT ON audit_logs WHEN NEW.kind='device.update' BEGIN SELECT RAISE(ABORT,'audit failed'); END`)
	expect(request("PATCH", "/devices/1", map[string]any{"name": "rollback"}), 409)
	pipelineSQL(t, a.db, "DROP TRIGGER reject_api_audit")
	// 丢弃变更前已进入订阅缓冲的更新，只等待新一轮成功事实。
	drain := true
	for drain {
		select {
		case <-ch:
		default:
			drain = false
		}
	}
	await(1)
	w = request("GET", "/devices/1", nil)
	expect(w, 200)
	if strings.Contains(w.Body.String(), "rollback") {
		t.Fatal("审计失败未回滚")
	}
	// 请求取消只能中止本次配置事务，不能永久停止原采集。
	cancelled, cancelRequest := context.WithCancel(context.Background())
	cancelRequest()
	if err := a.ChangeDevices(cancelled, []int64{1}, func() error { t.Fatal("取消后仍执行配置事务"); return nil }); err == nil {
		t.Fatal("取消未报告失败")
	}
	for len(ch) > 0 {
		<-ch
	}
	await(1)

	expect(request("PUT", "/devices/1/enabled", map[string]any{"enabled": false}), 200)
	if _, ok := a.collector.Status(1); ok {
		t.Fatal("禁用未停worker")
	}
	if len(a.Snapshots()) != 0 {
		t.Fatal("禁用保留活动actor")
	}
	expect(request("PUT", "/devices/1/enabled", map[string]any{"enabled": true}), 200)
	await(1)
	expect(request("DELETE", "/devices/1", map[string]any{"confirm": true}), 200)
	if _, ok := a.collector.Status(1); ok {
		t.Fatal("删除未停worker")
	}
	var count int
	if err = a.db.Read().QueryRow("SELECT count(*) FROM devices").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDynamicDeviceSnapshotIsolation(t *testing.T) {
	a, err := open(context.Background(), config.Config{DataDir: t.TempDir()}, testLogger(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	err = a.ChangeDevices(context.Background(), nil, func() error {
		_, err := a.db.Write().Exec("INSERT INTO devices(id,name) VALUES(1,'no source')")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshots := a.Snapshots()
	if len(snapshots) != 1 {
		t.Fatal("动态设备无快照")
	}
	snapshots[0].Fields["bad"] = model.Field{Str: "mutated"}
	if _, ok := a.Snapshots()[0].Fields["bad"]; ok {
		t.Fatal("共享快照")
	}
	for _, id := range []int64{2, 3} {
		err = a.ChangeDevices(context.Background(), nil, func() error {
			_, err := a.db.Write().Exec("INSERT INTO devices(id,name) VALUES(?,?)", id, strconv.FormatInt(id, 10))
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(a.Snapshots()) != 3 {
		t.Fatal("新增丢失既有actor")
	}
}
