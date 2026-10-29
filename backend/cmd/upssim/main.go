// upssim 提供只监听 loopback 的三协议模拟 UPS，供本机开发和联调。
//
// 无子命令（或首个参数以 '-' 开头）时为单设备前台模式；子命令模式由后台守护进程
// 托管多台模拟设备，客户端经 loopback 控制面增删设备和切换状态。
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

// version 可由 -ldflags "-X main.version=..." 注入。
var version = "dev"

// 退出码约定：0 成功；1 操作失败；2 参数错误；3 守护进程未运行。
const (
	exitOK         = 0
	exitFailure    = 1
	exitUsage      = 2
	exitNotRunning = 3
)

// usageError 表示命令行参数错误，退出码 2。
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error { return usageError{fmt.Sprintf(format, args...)} }

// errNotRunning 表示状态目录中没有可达的守护进程，退出码 3。
var errNotRunning = errors.New("守护进程未运行（先执行 upssim daemon start）")

const usageText = `用法：
  upssim [--et1k] [--scenario FILE] [...]     单设备前台模式（upssim --help 查看参数）
  upssim daemon start|run|stop|status [--state-dir DIR]
  upssim add NAME [flags]                     追加模拟设备
  upssim set NAME [flags]                     修改模式、通信状态或物理参数
  upssim list [--json]                        列出设备
  upssim show NAME [--json]                   查看单台设备
  upssim remove NAME                          删除设备

每个子命令都接受 --state-dir DIR；默认依次取 UPSSIM_STATE_DIR、用户配置目录下的 ups-warden/upssim。
子命令后加 --help 查看参数。退出码：0 成功，1 操作失败，2 参数错误，3 守护进程未运行。
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := dispatch(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// dispatch 解析子命令并把错误映射为退出码；错误信息统一写入 errout。
func dispatch(ctx context.Context, args []string, out, errout io.Writer) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		if err := run(ctx, args, out, errout); err != nil {
			fmt.Fprintf(errout, "upssim: %v\n", err)
			return exitFailure
		}
		return exitOK
	}
	var err error
	switch args[0] {
	case "daemon":
		err = daemonCommand(ctx, args[1:], out, errout)
	case "add":
		err = addCommand(args[1:], out, errout)
	case "set":
		err = setCommand(args[1:], out, errout)
	case "list":
		err = listCommand(args[1:], out, errout)
	case "show":
		err = showCommand(args[1:], out, errout)
	case "remove":
		err = removeCommand(args[1:], out, errout)
	case "help":
		fmt.Fprint(out, usageText)
		return exitOK
	default:
		err = usagef("未知子命令 %q\n\n%s", args[0], usageText)
	}
	if err == nil || errors.Is(err, errHelp) {
		return exitOK
	}
	fmt.Fprintf(errout, "upssim: %v\n", err)
	var usage usageError
	switch {
	case errors.As(err, &usage):
		return exitUsage
	case errors.Is(err, errNotRunning):
		return exitNotRunning
	default:
		return exitFailure
	}
}

// resolveStateDir 按 --state-dir、UPSSIM_STATE_DIR、用户配置目录的顺序确定绝对路径。
// 默认目录按用户隔离，不写入仓库；不同状态目录对应相互独立的守护进程。
func resolveStateDir(flagValue string) (string, error) {
	dir := flagValue
	if dir == "" {
		dir = os.Getenv("UPSSIM_STATE_DIR")
	}
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("无法确定默认状态目录，请使用 --state-dir: %w", err)
		}
		dir = filepath.Join(base, "ups-warden", "upssim")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("解析状态目录: %w", err)
	}
	return abs, nil
}
