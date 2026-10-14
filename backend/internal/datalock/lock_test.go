package datalock

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestExclusiveAndRelease(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new-data")
	first, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := first.Close(); err != nil {
			t.Error(err)
		}
	})
	if second, err := Acquire(dir); !errors.Is(err, ErrLocked) {
		if second != nil {
			second.Close()
		}
		t.Fatalf("第二实例应失败: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ups-warden.lock")); err != nil {
		t.Fatal("不应删除锁文件", err)
	}
}

func TestProcessExitReleasesLock(t *testing.T) {
	if os.Getenv("UPS_WARDEN_LOCK_TEST_CHILD") == "1" {
		lock, err := Acquire(os.Getenv("UPS_WARDEN_LOCK_TEST_DIR"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Println("ready")
		var one [1]byte
		_, _ = os.Stdin.Read(one[:])
		// 故意不调用 Close，验证由操作系统回收；引用 lock 保持句柄存活。
		_ = lock.file.Name()
		os.Exit(0)
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProcessExitReleasesLock$")
	cmd.Env = append(os.Environ(), "UPS_WARDEN_LOCK_TEST_CHILD=1", "UPS_WARDEN_LOCK_TEST_DIR="+dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		in.Close()
		if !waited {
			cancel()
			cmd.Wait()
		}
	}()
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || (line != "ready\n" && line != "ready\r\n") {
		t.Fatalf("子进程未就绪: %q %v %s", line, err, stderr.String())
	}
	if lock, err := Acquire(dir); !errors.Is(err, ErrLocked) {
		if lock != nil {
			lock.Close()
		}
		t.Fatalf("跨进程独占失败: %v", err)
	}
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err != nil {
		t.Fatalf("子进程退出: %v %s", err, stderr.String())
	}
	lock, err := Acquire(dir)
	if err != nil {
		t.Fatal("进程退出后仍占用", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("not-a-directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path); err == nil {
		t.Fatal("不应接受普通文件为数据目录")
	}
}
