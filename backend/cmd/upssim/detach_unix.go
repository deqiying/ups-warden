//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// startDetached 让子进程进入新会话，关闭启动它的终端后不会收到 SIGHUP。
func startDetached(newCmd func() *exec.Cmd) (*exec.Cmd, bool, error) {
	cmd := newCmd()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd, false, cmd.Start()
}
