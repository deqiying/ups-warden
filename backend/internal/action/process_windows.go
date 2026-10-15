package action

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

func configureProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		killer := exec.CommandContext(ctx, filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe"), "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
		killer.Env = []string{"SystemRoot=" + os.Getenv("SystemRoot")}
		if err := killer.Run(); err != nil {
			return errors.Join(errors.New("action: 终止进程树失败"), cmd.Process.Kill())
		}
		return nil
	}
	cmd.WaitDelay = time.Second
}
