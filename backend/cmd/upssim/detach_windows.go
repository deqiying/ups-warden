//go:build windows

package main

import (
	"errors"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// startDetached 让子进程脱离当前控制台并进入独立进程组，关闭终端或 Ctrl+C 不会结束它。
// IDE 终端、自动化工具等宿主常把子进程放进 Job，宿主退出时整棵进程树会被结束；
// 因此优先脱离 Job。所在 Job 不允许脱离时退回普通分离，并通过 jobBound 提示调用者。
func startDetached(newCmd func() *exec.Cmd) (cmd *exec.Cmd, jobBound bool, err error) {
	base := uint32(windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP)
	cmd = newCmd()
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: base | windows.CREATE_BREAKAWAY_FROM_JOB, HideWindow: true}
	if err = cmd.Start(); err == nil || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return cmd, false, err
	}
	cmd = newCmd()
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: base, HideWindow: true}
	return cmd, true, cmd.Start()
}
