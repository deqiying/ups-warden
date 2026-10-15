//go:build !linux && !windows

package action

import (
	"os/exec"
	"time"
)

func configureProcess(cmd *exec.Cmd) { cmd.WaitDelay = time.Second }
