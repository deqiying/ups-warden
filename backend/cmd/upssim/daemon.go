package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"ups-warden/internal/datalock"
)

const (
	logFile        = "upssim.log"
	maxLogBytes    = 5 << 20
	startTimeout   = 5 * time.Second
	stopTimeout    = 10 * time.Second
	logTailOnError = 20
)

// errHelp 表示已输出帮助信息，退出码 0。
var errHelp = flag.ErrHelp

func newFlagSet(name string, errout io.Writer) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("upssim "+name, flag.ContinueOnError)
	fs.SetOutput(errout)
	stateDir := fs.String("state-dir", "", "状态目录（默认 UPSSIM_STATE_DIR 或用户配置目录下的 ups-warden/upssim）")
	return fs, stateDir
}

// parseFlags 统一把 flag 包的解析错误映射为参数错误（退出码 2）。
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errHelp
		}
		return usageError{err.Error()}
	}
	return nil
}

func daemonCommand(ctx context.Context, args []string, out, errout io.Writer) error {
	if len(args) == 0 {
		return usagef("用法：upssim daemon start|run|stop|status [--state-dir DIR]")
	}
	fs, stateFlag := newFlagSet("daemon "+args[0], errout)
	controlPort := 0
	if args[0] == "run" || args[0] == "start" {
		fs.IntVar(&controlPort, "control-port", 0, "控制面 loopback 端口，0 自动分配")
	}
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usagef("不接受位置参数: %v", fs.Args())
	}
	if controlPort < 0 || controlPort > 65535 {
		return usagef("control-port 必须在 0..65535 内")
	}
	dir, err := resolveStateDir(*stateFlag)
	if err != nil {
		return err
	}
	switch args[0] {
	case "run":
		return runDaemon(ctx, dir, controlPort, errout, nil)
	case "start":
		return startDaemon(dir, controlPort, out)
	case "stop":
		return stopDaemon(dir, out)
	case "status":
		c, err := dialControl(dir)
		if err != nil {
			return err
		}
		var st statusView
		if err := c.do(http.MethodGet, "/v1/status", nil, &st); err != nil {
			return err
		}
		printStatus(out, st)
		return nil
	default:
		return usagef("未知 daemon 子命令 %q", args[0])
	}
}

func printStatus(out io.Writer, st statusView) {
	fmt.Fprintf(out, "守护进程运行中：PID=%d 控制地址=%s 启动于 %s 版本=%s\n状态目录：%s\n设备：%d 台（失败 %d 台）\n",
		st.PID, st.Address, st.StartedAt.Local().Format(time.DateTime), st.Version, st.StateDir, st.Devices, st.Failed)
}

// runDaemon 在前台运行守护进程，直到 ctx 取消或收到 /v1/shutdown。
// ready 非 nil 时在控制面就绪后收到 control.json 内容，供进程内测试使用。
func runDaemon(ctx context.Context, dir string, controlPort int, errout io.Writer, ready func(controlInfo)) (result error) {
	lock, err := datalock.Acquire(dir)
	if errors.Is(err, datalock.ErrLocked) {
		return fmt.Errorf("状态目录 %s 已有守护进程在运行", dir)
	}
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	logger := log.New(errout, "upssim ", log.LstdFlags)
	records, err := loadRecords(dir)
	if err != nil {
		return err
	}
	reg := newRegistry(dir, logger.Printf)
	reg.restore(records)
	defer func() {
		if err := reg.closeAll(); err != nil {
			logger.Printf("关闭设备: %v", err)
			result = errors.Join(result, err)
		}
	}()
	token, err := newToken()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(controlPort)))
	if err != nil {
		return fmt.Errorf("控制面监听: %w", err)
	}
	info := controlInfo{Schema: 1, PID: os.Getpid(), Address: listener.Addr().String(), Token: token,
		StartedAt: time.Now().Truncate(time.Second), Version: version}
	shutdownCh := make(chan struct{})
	var once sync.Once
	status := func() statusView {
		total, failed := reg.counts()
		return statusView{PID: info.PID, Address: info.Address, StartedAt: info.StartedAt, Version: version, StateDir: dir, Devices: total, Failed: failed}
	}
	server := &http.Server{
		Handler:           controlHandler(reg, token, status, func() { once.Do(func() { close(shutdownCh) }) }),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
		ErrorLog: logger,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	controlPath := filepath.Join(dir, controlFile)
	data, err := jsonMarshal(info)
	if err == nil {
		err = writeFileAtomic(controlPath, data)
	}
	if err != nil {
		return errors.Join(err, server.Close())
	}
	defer func() {
		if err := os.Remove(controlPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}()
	total, failed := reg.counts()
	logger.Printf("守护进程已启动：PID=%d 控制地址=%s 状态目录=%s 设备=%d 失败=%d", info.PID, info.Address, dir, total, failed)
	if ready != nil {
		ready(info)
	}
	select {
	case <-ctx.Done():
		logger.Printf("收到退出信号")
	case <-shutdownCh:
		logger.Printf("收到 shutdown 请求")
	case err := <-serveErr:
		return fmt.Errorf("控制面异常退出: %w", err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		result = errors.Join(result, fmt.Errorf("关闭控制面: %w", err))
	}
	logger.Printf("守护进程已退出")
	return result
}

// startDaemon 以脱离终端的子进程运行 daemon run，等待控制面就绪后返回。
func startDaemon(dir string, controlPort int, out io.Writer) error {
	if c, err := dialControl(dir); err == nil {
		var st statusView
		if err := c.do(http.MethodGet, "/v1/status", nil, &st); err != nil {
			return err
		}
		fmt.Fprintln(out, "守护进程已在运行。")
		printStatus(out, st)
		return nil
	} else if !errors.Is(err, errNotRunning) {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位可执行文件: %w", err)
	}
	// go run 的临时二进制会在退出后删除，后台进程无法继续使用；前台 daemon run 不受影响。
	if strings.Contains(filepath.ToSlash(exe), "/go-build") {
		return fmt.Errorf("不能从 go run 启动后台守护进程，请先执行 mise run sim:build 后使用构建产物，或改用前台 daemon run")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("创建状态目录: %w", err)
	}
	logPath := filepath.Join(dir, logFile)
	// 日志量很小（只有生命周期与设备变更）；每次启动前超过 5 MiB 时保留一份旧日志。
	if info, err := os.Stat(logPath); err == nil && info.Size() > maxLogBytes {
		if err := os.Rename(logPath, logPath+".1"); err != nil {
			return fmt.Errorf("轮转日志: %w", err)
		}
	}
	logOut, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("打开日志: %w", err)
	}
	defer logOut.Close()
	cmd, jobBound, err := startDetached(func() *exec.Cmd {
		c := exec.Command(exe, "daemon", "run", "--state-dir", dir, "--control-port", strconv.Itoa(controlPort))
		c.Stdout, c.Stderr = logOut, logOut
		return c
	})
	if err != nil {
		return fmt.Errorf("启动守护进程: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.After(startTimeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-exited:
			return fmt.Errorf("守护进程提前退出（%v），日志末尾：\n%s", err, tailFile(logPath, logTailOnError))
		case <-deadline:
			return fmt.Errorf("等待守护进程就绪超时（PID=%d 仍在运行，可查看 %s），日志末尾：\n%s", cmd.Process.Pid, logPath, tailFile(logPath, logTailOnError))
		case <-ticker.C:
			c, err := dialControl(dir)
			if err != nil || c.info.PID != cmd.Process.Pid {
				continue
			}
			var st statusView
			if err := c.do(http.MethodGet, "/v1/status", nil, &st); err != nil {
				continue
			}
			fmt.Fprintln(out, "守护进程已启动。")
			printStatus(out, st)
			fmt.Fprintf(out, "日志：%s\n", logPath)
			if jobBound {
				fmt.Fprintln(out, "注意：当前终端所在的 Windows Job 不允许子进程脱离，关闭该终端或宿主程序会结束守护进程。")
			}
			return nil
		}
	}
}

// stopDaemon 请求优雅退出并以状态目录锁可获取作为完成判据；不按 PID 强制结束进程，避免误杀复用 PID 的无关进程。
func stopDaemon(dir string, out io.Writer) error {
	c, err := dialControl(dir)
	if err != nil {
		return err
	}
	if err := c.do(http.MethodPost, "/v1/shutdown", nil, nil); err != nil {
		return err
	}
	deadline := time.Now().Add(stopTimeout)
	for time.Now().Before(deadline) {
		if lock, err := datalock.Acquire(dir); err == nil {
			if err := lock.Close(); err != nil {
				return err
			}
			fmt.Fprintf(out, "守护进程已停止（PID=%d）。\n", c.info.PID)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("等待守护进程退出超时（PID=%d），请查看 %s 后手动处理", c.info.PID, filepath.Join(dir, logFile))
}

func tailFile(path string, lines int) string {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Sprintf("（无法读取日志: %v）", err)
	}
	defer f.Close()
	var all []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		all = append(all, scanner.Text())
		if len(all) > lines {
			all = all[1:]
		}
	}
	return strings.Join(all, "\n")
}
