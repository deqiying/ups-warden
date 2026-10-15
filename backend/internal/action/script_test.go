package action

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"ups-warden/internal/model"
)

func writeScript(t *testing.T, dir, linux, windows string) string {
	t.Helper()
	name := "test.sh"
	body := linux
	if runtime.GOOS == "windows" {
		name = "test.ps1"
		body = windows
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	return name
}
func TestScriptEnvironmentArgumentsAndOutput(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("UPS_WARDEN_MASTER_KEY", "must-not-inherit")
	script := writeScript(t, dir,
		"printf '%s\\n' \"$UPS_DEVICE_ID|$UPS_DEVICE_NAME|$UPS_EVENT|$UPS_POWER_STATE|$UPS_BATTERY_PCT|$UPS_ON_BATTERY_SECONDS|$UPS_RUNTIME_ESTIMATE|$UPS_DRY_RUN|$UPS_WARDEN_MASTER_KEY\"\nprintf '%s\\n' \"$1\"\n",
		"[Console]::WriteLine(\"$env:UPS_DEVICE_ID|$env:UPS_DEVICE_NAME|$env:UPS_EVENT|$env:UPS_POWER_STATE|$env:UPS_BATTERY_PCT|$env:UPS_ON_BATTERY_SECONDS|$env:UPS_RUNTIME_ESTIMATE|$env:UPS_DRY_RUN|$env:UPS_WARDEN_MASTER_KEY\")\n[Console]::WriteLine($args[0])\n")
	req := Request{DeviceName: "fake", Event: model.Event{Type: "mains_lost"}, State: model.DeviceState{DeviceID: 42, PowerState: "on_battery", Fields: map[string]model.Field{"battery_capacity_pct": {Quality: model.Good, Num: 75}, "on_battery_elapsed": {Quality: model.Good, Num: 12}, "runtime_estimate": {Quality: model.Good, Num: 123}}}}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	v := executeScript(ctx, dir, Config{Script: script, Args: []string{"literal & not-shell"}}, req)
	if v.status != "succeeded" || v.code == nil || *v.code != 0 || !strings.Contains(v.stdout, "42|fake|mains_lost|on_battery|75|12|123|false|") || !strings.Contains(v.stdout, "literal & not-shell") || strings.Contains(v.stdout, "must-not-inherit") {
		t.Fatalf("脚本结果 %+v", v)
	}
	script = writeScript(t, dir, "i=0; while [ $i -lt 6000 ]; do printf x; printf y >&2; i=$((i+1)); done\nexit 7\n", "[Console]::Out.Write(('x' * 6000));[Console]::Error.Write(('y' * 6000));exit 7\n")
	v = executeScript(ctx, dir, Config{Script: script}, Request{})
	if v.status != "failed" || v.code == nil || *v.code != 7 || len(v.stdout) != 4096 || len(v.stderr) != 4096 {
		t.Fatalf("输出截断/退出码 %+v", v)
	}
}
func TestScriptServiceTimeoutAndClose(t *testing.T) {
	for _, closeService := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "close"}[closeService], func(t *testing.T) {
			s := fixture(t)
			script := writeScript(t, s.scriptDir, "sleep 30\n", "Start-Sleep -Seconds 30\n")
			id := add(t, s, Record{Name: "temporary harmless sleeper", Type: "script", TimeoutS: 1, Config: Config{Script: script}})
			run := submit(t, s, id, false)
			if closeService {
				time.Sleep(300 * time.Millisecond)
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			}
			v := await(t, s, run)
			want := "timeout"
			if closeService {
				want = "cancelled"
			}
			if v.status != want {
				t.Fatal(v)
			}
		})
	}
}
func TestWindowsBatchMetacharactersNeverExecute(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows 专用批处理参数边界")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "safe.cmd"), []byte("@echo off\r\necho %1\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, arg := range []string{"x&echo injected", "%UPS_WARDEN_MASTER_KEY%", "x|echo injected", "x y"} {
		v := executeScript(t.Context(), dir, Config{Script: "safe.cmd", Args: []string{arg}}, Request{})
		if v.status != "failed" || v.stdout != "" {
			t.Fatal("执行了不安全参数", v)
		}
	}
	v := executeScript(t.Context(), dir, Config{Script: "safe.cmd", Args: []string{"literal-123"}}, Request{})
	if v.status != "succeeded" || !strings.Contains(v.stdout, "literal-123") {
		t.Fatal(v)
	}
}

func TestScriptCloseTerminatesOwnProcessTree(t *testing.T) {
	s := fixture(t)
	childName := "child.sh"
	child := "printf ready > child-ready\nsleep 3\nprintf leaked > child-survived\n"
	parent := "/bin/sh child.sh &\nwait\n"
	if runtime.GOOS == "windows" {
		childName = "child.ps1"
		child = "[IO.File]::WriteAllText((Join-Path $PSScriptRoot 'child-ready'), 'ready')\nStart-Sleep -Seconds 3\n[IO.File]::WriteAllText((Join-Path $PSScriptRoot 'child-survived'), 'leaked')\n"
	}
	if err := os.WriteFile(filepath.Join(s.scriptDir, childName), []byte(child), 0700); err != nil {
		t.Fatal(err)
	}
	name := writeScript(t, s.scriptDir, parent, "$child = Join-Path $PSScriptRoot 'child.ps1'\n$p = Start-Process -FilePath (Get-Process -Id $PID).Path -ArgumentList @('-NoLogo','-NoProfile','-NonInteractive','-File', $child) -PassThru\n$p.WaitForExit()\n")
	id := add(t, s, Record{Name: "own process tree", Type: "script", TimeoutS: 10, Config: Config{Script: name}})
	run := submit(t, s, id, false)
	deadline := time.Now().Add(4 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(s.scriptDir, "child-ready")); err == nil {
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("自建子进程未就绪")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if v := await(t, s, run); v.status != "cancelled" {
		t.Fatal(v)
	}
	time.Sleep(3200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(s.scriptDir, "child-survived")); !os.IsNotExist(err) {
		t.Fatal("自建子进程在 Close 后继续运行", err)
	}
}
