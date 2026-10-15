package action

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"ups-warden/internal/notify"
)

func scriptPath(dir, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) || filepath.VolumeName(name) != "" || strings.ContainsAny(name, "\x00\\:") {
		return "", errors.New("action: 脚本路径无效")
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", errors.New("action: 脚本路径不能包含 ..")
		}
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", errors.New("action: 脚本目录不可用")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		return "", errors.New("action: 脚本不存在")
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", errors.New("action: 脚本越出允许目录")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("action: 脚本不是普通文件")
	}
	return path, nil
}

// Scripts 只列出目录内经过实路径约束的普通文件，不提供在线编辑。
func Scripts(dir string) ([]string, error) {
	items := []string{}
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return items, nil
	}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		if _, err := scriptPath(dir, name); err == nil {
			items = append(items, name)
		}
		return nil
	})
	sort.Strings(items)
	return items, err
}

// cappedBuffer 丢弃超过 4 KiB 的输出但仍报告完整写入，避免子进程因输出管道阻塞。
type cappedBuffer struct{ buffer bytes.Buffer }

func (b *cappedBuffer) String() string { return b.buffer.String() }
func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if left := 4096 - b.buffer.Len(); left > 0 {
		if len(p) > left {
			p = p[:left]
		}
		_, _ = b.buffer.Write(p)
	}
	return n, nil
}

func scriptEnvironment(req Request) []string {
	vars := notify.Variables(req.DeviceName, req.Event, req.State, time.Now())
	scalar := func(key string) string {
		v := vars[key]
		if v == nil {
			return ""
		}
		switch x := v.(type) {
		case string:
			return x
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64)
		case int64:
			return strconv.FormatInt(x, 10)
		case int:
			return strconv.Itoa(x)
		default:
			return ""
		}
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "UPS_DEVICE_ID=" + strconv.FormatInt(req.State.DeviceID, 10), "UPS_DEVICE_NAME=" + req.DeviceName, "UPS_EVENT=" + req.Event.Type, "UPS_POWER_STATE=" + req.State.PowerState, "UPS_BATTERY_PCT=" + scalar("battery_capacity_pct"), "UPS_ON_BATTERY_SECONDS=" + scalar("on_battery_elapsed"), "UPS_RUNTIME_ESTIMATE=" + scalar("runtime_estimate"), "UPS_DRY_RUN=" + strconv.FormatBool(req.DryRun)}
	if runtime.GOOS == "windows" {
		for _, key := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP"} {
			if v := os.Getenv(key); v != "" {
				env = append(env, key+"="+v)
			}
		}
	}
	return env
}
func scriptCommand(ctx context.Context, path string, args []string) (*exec.Cmd, error) {
	if runtime.GOOS == "windows" {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".ps1":
			interpreter, err := exec.LookPath("pwsh")
			if err != nil {
				interpreter, err = exec.LookPath("powershell.exe")
			}
			if err != nil {
				return nil, errors.New("action: PowerShell 不可用")
			}
			return exec.CommandContext(ctx, interpreter, append([]string{"-NoLogo", "-NoProfile", "-NonInteractive", "-File", path}, args...)...), nil
		case ".cmd", ".bat":
			// cmd 的二次解析不能复用通用 argv 引号规则；对批处理路径及参数只接受无需转义的字符。
			for _, v := range append([]string{path}, args...) {
				if !safeBatchArgument(v) {
					return nil, errors.New("action: 批处理路径或参数包含不安全元字符或空白")
				}
			}
			return exec.CommandContext(ctx, filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), append([]string{"/d", "/s", "/c", path}, args...)...), nil
		}
	} else if strings.ToLower(filepath.Ext(path)) == ".sh" {
		return exec.CommandContext(ctx, "/bin/sh", append([]string{path}, args...)...), nil
	}
	return exec.CommandContext(ctx, path, args...), nil
}
func safeBatchArgument(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_-.\\/:=+,", c)) {
			return false
		}
	}
	return true
}
func executeScript(ctx context.Context, dir string, c Config, req Request) result {
	out := result{status: "failed"}
	path, err := scriptPath(dir, c.Script)
	if err != nil {
		out.message = err.Error()
		return out
	}
	cmd, err := scriptCommand(ctx, path, c.Args)
	if err != nil {
		out.message = err.Error()
		return out
	}
	var stdout, stderr cappedBuffer
	cmd.Env = scriptEnvironment(req)
	cmd.Dir = filepath.Dir(path)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	configureProcess(cmd)
	err = cmd.Run()
	out.stdout = stdout.String()
	out.stderr = stderr.String()
	if cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		out.code = &code
	}
	if err != nil {
		out.message = "本地脚本执行失败"
		return out
	}
	out.status = "succeeded"
	return out
}
