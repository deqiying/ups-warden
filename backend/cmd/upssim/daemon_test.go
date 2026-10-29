package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/driver/ayi9"
	"ups-warden/internal/driver/guard"
	"ups-warden/internal/driver/snmp"
	"ups-warden/internal/model"
	"ups-warden/internal/normalize"
	"ups-warden/internal/profile"
	"ups-warden/internal/secret"
)

// lockedBuffer 收集守护进程日志；日志与测试 goroutine 并发写读。
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startTestDaemon 在进程内以 daemon run 入口启动守护进程，返回等待退出的通道。
func startTestDaemon(t *testing.T, dir string, logs *lockedBuffer) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- runDaemon(ctx, dir, 0, logs, func(controlInfo) { close(ready) }) }()
	select {
	case <-ready:
	case err := <-done:
		cancel()
		t.Fatalf("守护进程启动失败: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("守护进程启动超时")
	}
	return cancel, done
}

type cliResult struct {
	code        int
	out, errout string
}

func cli(dir string, args ...string) cliResult {
	var out, errout bytes.Buffer
	code := dispatch(context.Background(), append(args, "--state-dir", dir), &out, &errout)
	return cliResult{code, out.String(), errout.String()}
}

// pollFields 用真实只读驱动与内置 Profile 采集一次并归一化，模拟 ups-warden 的采集链路。
func pollFields(t *testing.T, protocol, address, profileKey string) (map[string]model.Field, error) {
	t.Helper()
	p, err := profile.Builtin(profileKey)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.ReadPlan()
	if err != nil {
		t.Fatal(err)
	}
	cfg := guard.Config{Address: address, Timeout: 200 * time.Millisecond}
	var client driver.Driver
	switch protocol {
	case "snmp":
		client, err = snmp.New(snmp.Config{Connection: cfg, Community: secret.NewString("sim-public")}, *plan.SNMP)
	case "ayi9":
		client, err = ayi9.New(ayi9.Config{Connection: cfg, Password: secret.NewString("fake-pass")}, *plan.AYI9)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	raw, err := client.Poll(ctx)
	if err != nil {
		return nil, err
	}
	snapshot, _, err := normalize.Normalize(1, raw, p)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.Fields, nil
}

func TestDaemonEndToEnd(t *testing.T) {
	dir := t.TempDir()
	logs := &lockedBuffer{}
	cancel, done := startTestDaemon(t, dir, logs)
	var outputs strings.Builder
	run := func(args ...string) cliResult {
		r := cli(dir, args...)
		outputs.WriteString(r.out + r.errout)
		return r
	}
	r := run("add", "ups-1", "--et1k", "--ip", "127.0.0.3", "--password", "fake-pass", "--community", "sim-public", "--json")
	if r.code != exitOK {
		t.Fatalf("add 失败: %+v", r)
	}
	var view deviceView
	if err := json.Unmarshal([]byte(r.out), &view); err != nil {
		t.Fatal(err)
	}
	check := func(wantSource string, wantMains, wantLow bool) {
		t.Helper()
		fields, err := pollFields(t, "snmp", view.Endpoints.SNMP, *view.Profiles.SNMP)
		if err != nil {
			t.Fatal(err)
		}
		if got := fields["output_source"]; got.Quality != model.Good || got.Str != wantSource {
			t.Fatalf("SNMP output_source=%+v，期望 %s", got, wantSource)
		}
		fields, err = pollFields(t, "ayi9", view.Endpoints.AYI9, *view.Profiles.AYI9)
		if err != nil {
			t.Fatal(err)
		}
		if fields["mains_ok"].Bool != wantMains || fields["low_battery"].Bool != wantLow {
			t.Fatalf("AYI9 mains_ok=%+v low_battery=%+v", fields["mains_ok"], fields["low_battery"])
		}
	}
	check("normal", true, false)
	if r := run("set", "ups-1", "--mode", "battery"); r.code != exitOK {
		t.Fatalf("set battery: %+v", r)
	}
	check("battery", false, false)
	if r := run("set", "ups-1", "--mode", "low"); r.code != exitOK {
		t.Fatalf("set low: %+v", r)
	}
	check("battery", false, true)
	if r := run("set", "ups-1", "--link", "down"); r.code != exitOK {
		t.Fatalf("set link down: %+v", r)
	}
	if _, err := pollFields(t, "snmp", view.Endpoints.SNMP, *view.Profiles.SNMP); err == nil {
		t.Fatal("失联时采集应失败")
	}
	// 退出码约定。
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"set", "missing", "--mode", "battery"}, exitFailure},
		{[]string{"set", "ups-1", "--ip", "127.0.0.9"}, exitUsage},
		{[]string{"set", "ups-1"}, exitUsage},
		{[]string{"add", "ups-1"}, exitFailure},
		{[]string{"bogus"}, exitUsage},
	} {
		if r := run(tc.args...); r.code != tc.code {
			t.Fatalf("%v 退出码 %d，期望 %d: %s", tc.args, r.code, tc.code, r.errout)
		}
	}
	if r := run("set", "ups-1", "--ip", "127.0.0.9"); !strings.Contains(r.errout, "remove 后重新 add") {
		t.Fatalf("不可修改参数缺少提示: %s", r.errout)
	}
	if r := run("list"); r.code != exitOK || !strings.Contains(r.out, "ups-1") || !strings.Contains(r.out, "127.0.0.3") {
		t.Fatalf("list 输出错误: %+v", r)
	}
	if r := run("daemon", "status"); r.code != exitOK || !strings.Contains(r.out, "设备：1 台") {
		t.Fatalf("status 输出错误: %+v", r)
	}
	if r := run("daemon", "stop"); r.code != exitOK {
		t.Fatalf("stop 失败: %+v", r)
	}
	if err := <-done; err != nil {
		t.Fatalf("守护进程退出错误: %v", err)
	}
	cancel()
	if _, err := os.Stat(filepath.Join(dir, controlFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("退出后应删除 control.json")
	}
	if r := run("list"); r.code != exitNotRunning {
		t.Fatalf("停止后应返回退出码 3: %+v", r)
	}
	// 重启后按原端口恢复手动状态。
	cancel, done = startTestDaemon(t, dir, logs)
	defer func() { cancel(); <-done }()
	r = run("show", "ups-1", "--json")
	var restored deviceView
	if err := json.Unmarshal([]byte(r.out), &restored); err != nil {
		t.Fatal(err, r.errout)
	}
	if restored.Endpoints != view.Endpoints || restored.Mode != "low" || restored.Link != "down" {
		t.Fatalf("重启恢复错误: %+v", restored)
	}
	if r := run("set", "ups-1", "--mode", "mains", "--link", "up"); r.code != exitOK {
		t.Fatalf("恢复市电失败: %+v", r)
	}
	// 从低电恢复市电后电量仍 ≤ 20%，低电标志保持到回充超过 20%；显式设置电量可立即清除。
	check("normal", true, true)
	if r := run("set", "ups-1", "--capacity", "100"); r.code != exitOK {
		t.Fatalf("设置电量失败: %+v", r)
	}
	check("normal", true, false)
	// 命令输出、守护进程日志均不得包含凭据或 token。
	data, err := os.ReadFile(filepath.Join(dir, controlFile))
	if err != nil {
		t.Fatal(err)
	}
	var info controlInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	all := outputs.String() + logs.String()
	for _, secretText := range []string{"fake-pass", info.Token} {
		if strings.Contains(all, secretText) {
			t.Fatalf("输出或日志泄露敏感值")
		}
	}
}

func TestDaemonRejectsSecondInstance(t *testing.T) {
	dir := t.TempDir()
	cancel, done := startTestDaemon(t, dir, &lockedBuffer{})
	defer func() { cancel(); <-done }()
	err := runDaemon(context.Background(), dir, 0, &lockedBuffer{}, nil)
	if err == nil || !strings.Contains(err.Error(), "已有守护进程") {
		t.Fatalf("同一状态目录应拒绝第二个实例: %v", err)
	}
}

func TestDaemonRefusesCorruptDevicesFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, devicesFile), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runDaemon(context.Background(), dir, 0, &lockedBuffer{}, nil); err == nil {
		t.Fatal("损坏的 devices.json 应拒绝启动")
	}
	data, err := os.ReadFile(filepath.Join(dir, devicesFile))
	if err != nil || string(data) != "{broken" {
		t.Fatal("拒绝启动时不得覆盖原文件")
	}
}
