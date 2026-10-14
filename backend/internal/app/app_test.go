package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"ups-warden/internal/config"
	"ups-warden/internal/datalock"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestStartupHealthAndRestart(t *testing.T) {
	c := config.Config{DataDir: filepath.Join(t.TempDir(), "data"), Listen: "127.0.0.1:0"}
	a, err := open(t.Context(), c, testLogger(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, name := range []string{"master.key", "ups-warden.db", "ups-warden.lock"} {
		if _, err := os.Stat(filepath.Join(c.DataDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if second, err := open(t.Context(), c, testLogger(), ""); !errors.Is(err, datalock.ErrLocked) {
		if second != nil {
			second.Close()
		}
		t.Fatalf("第二实例未被拒绝: %v", err)
	}
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.serve(listener) }()
	// 失败路径也先触发 Shutdown，再等待 Close，不能让测试清理等待仍在 Serve 的任务。
	defer a.cancel()
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get("http://" + listener.Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatal(errors.Join(readErr, closeErr))
	}
	if response.StatusCode != http.StatusOK || string(body) != "{\"status\":\"ok\"}\n" {
		t.Fatalf("健康检查变更: %d %q", response.StatusCode, body)
	}
	a.cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("服务未停止")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := open(t.Context(), c, testLogger(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStartupFailuresReleaseLock(t *testing.T) {
	c := config.Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0"}
	if err := Run(t.Context(), c, "test", testLogger(), "invalid-base64"); err == nil {
		t.Fatal("错误主密钥应阻止启动")
	}
	assertUnlocked := func() {
		t.Helper()
		lock, err := datalock.Acquire(c.DataDir)
		if err != nil {
			t.Fatal(err)
		}
		if err := lock.Close(); err != nil {
			t.Fatal(err)
		}
	}
	assertUnlocked()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	c.Listen = listener.Addr().String()
	if err := Run(t.Context(), c, "test", testLogger(), ""); err == nil {
		t.Fatal("监听冲突应失败")
	}
	assertUnlocked()
	// 非 SQLite 文件不应被覆盖，迁移失败后仍释放锁。
	if err := os.WriteFile(filepath.Join(c.DataDir, "ups-warden.db"), []byte("invalid database"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := open(t.Context(), c, testLogger(), ""); err == nil {
		t.Fatal("损坏数据库应失败")
	}
	assertUnlocked()
}

func TestWrongMasterKeyRejectsExistingCredentials(t *testing.T) {
	c := config.Config{DataDir: t.TempDir()}
	a, err := open(t.Context(), c, testLogger(), "")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := a.cipher.Encrypt([]byte("test-token"), "channels.secret")
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	_, err = a.db.Write().ExecContext(t.Context(), "INSERT INTO channels(name,type,config,secret,enabled) VALUES ('test','webhook','{}',?,1)", encrypted)
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	wrong := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	if a, err := open(t.Context(), c, testLogger(), wrong); err == nil {
		a.Close()
		t.Fatal("错误主密钥不应启动")
	} else if strings.Contains(err.Error(), "test-token") {
		t.Fatal("错误泄漏凭据")
	}
	restored, err := open(t.Context(), c, testLogger(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerRestartAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		var logs bytes.Buffer
		a := &App{ctx: ctx, cancel: cancel, logger: slog.New(slog.NewTextHandler(&logs, nil))}
		calls := 0
		if err := a.Go("test", func(ctx context.Context) error {
			calls++
			if calls == 1 {
				panic("sensitive-panic-content")
			}
			if calls == 2 {
				return errors.New("retry-test")
			}
			<-ctx.Done()
			return ctx.Err()
		}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if calls != 1 {
			t.Fatalf("首次调用次数: %d", calls)
		}
		if strings.Contains(logs.String(), "sensitive-panic-content") || !strings.Contains(logs.String(), "stack=") {
			t.Fatal("panic 日志未安全记录堆栈")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if calls != 2 {
			t.Fatalf("第一次重启: %d", calls)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if calls != 3 {
			t.Fatalf("第二次重启: %d", calls)
		}
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if calls != 3 {
			t.Fatal("取消后仍重启")
		}
		if err := a.Go("late", func(context.Context) error { return nil }); err == nil {
			t.Fatal("关闭后仍接受任务")
		}
	})
}

func TestWorkerBackoffCapAndNormalExit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		a := &App{ctx: ctx, cancel: cancel, logger: testLogger()}
		defer a.Close()
		times := make(chan time.Time, 10)
		if err := a.Go("retry", func(context.Context) error { times <- time.Now(); return errors.New("retry") }); err != nil {
			t.Fatal(err)
		}
		previous := <-times
		for _, seconds := range []int{1, 2, 4, 8, 16, 30, 30} {
			next := <-times
			if next.Sub(previous) != time.Duration(seconds)*time.Second {
				t.Fatalf("退避间隔 %v", next.Sub(previous))
			}
			previous = next
		}
		normalCalls := 0
		if err := a.Go("normal", func(context.Context) error { normalCalls++; return nil }); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		cancel()
		synctest.Wait()
		if normalCalls != 1 {
			t.Fatalf("正常任务调用次数 %d", normalCalls)
		}
	})
}

func TestAlreadyCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c := config.Config{DataDir: filepath.Join(t.TempDir(), "unused")}
	if err := Run(ctx, c, "test", testLogger(), ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消错误: %v", err)
	}
	if _, err := os.Stat(c.DataDir); !os.IsNotExist(err) {
		t.Fatal("取消后的启动不应创建数据目录")
	}
}
