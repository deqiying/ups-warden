package acceptance

import (
	"syscall"
	"time"
)

// processCPU 返回本进程累计的内核态与用户态 CPU 时间。
func processCPU() time.Duration {
	h, err := syscall.GetCurrentProcess()
	if err != nil {
		panic(err)
	}
	var created, exited, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		panic(err)
	}
	ticks := func(f syscall.Filetime) int64 { return int64(f.HighDateTime)<<32 | int64(f.LowDateTime) }
	return time.Duration((ticks(kernel) + ticks(user)) * 100)
}
