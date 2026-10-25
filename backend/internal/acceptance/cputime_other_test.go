//go:build !windows

package acceptance

import (
	"syscall"
	"time"
)

// processCPU 返回本进程累计的内核态与用户态 CPU 时间。
func processCPU() time.Duration {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		panic(err)
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}
