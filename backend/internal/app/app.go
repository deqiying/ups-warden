// Package app 装配服务模块，并拥有数据目录、数据库及后台任务的生命周期。
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"ups-warden/internal/config"
	"ups-warden/internal/datalock"
	"ups-warden/internal/secret"
	"ups-warden/internal/server"
	"ups-warden/internal/store"
)

const shutdownTimeout = 10 * time.Second

// App 持有进程资源；业务模块只能在启动阶段装配，关闭后不接受新任务。
type App struct {
	ctx       context.Context
	cancel    context.CancelFunc
	logger    *slog.Logger
	db        *store.Store
	cipher    *secret.Cipher
	lock      *datalock.Lock
	wg        sync.WaitGroup
	mu        sync.Mutex
	closing   bool
	closeOnce sync.Once
	closeErr  error
}

// Run 按“锁 → 密钥 → 数据库 → HTTP”启动，ctx 取消后按反向顺序关闭。
// encodedKey 来自 UPS_WARDEN_MASTER_KEY，不得记录到日志或持久化配置。
func Run(ctx context.Context, c config.Config, version string, logger *slog.Logger, encodedKey string) (err error) {
	a, err := open(ctx, c, logger, encodedKey)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, a.Close()) }()
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return fmt.Errorf("监听 HTTP: %w", err)
	}
	logger.Info("HTTP 服务启动", "listen", listener.Addr().String(), "version", version)
	return a.serve(listener)
}

func open(ctx context.Context, c config.Config, logger *slog.Logger, encodedKey string) (_ *App, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	child, cancel := context.WithCancel(ctx)
	a := &App{ctx: child, cancel: cancel, logger: logger}
	defer func() {
		if err != nil {
			err = errors.Join(err, a.Close())
		}
	}()
	a.lock, err = datalock.Acquire(c.DataDir)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "windows" {
		logger.Warn("Windows 主密钥保护依赖数据目录 ACL，请仅允许服务账号访问")
	}
	a.cipher, err = secret.Load(c.DataDir, encodedKey)
	if err != nil {
		return nil, err
	}
	a.db, err = store.Open(child, filepath.Join(c.DataDir, "ups-warden.db"))
	if err != nil {
		return nil, err
	}
	if err := a.checkSecrets(); err != nil {
		return nil, err
	}
	return a, nil
}

// 启动时验证所有已存凭据，防止换错主密钥后仍将服务报告为可用。
func (a *App) checkSecrets() error {
	for _, table := range []string{"sources", "channels", "hosts", "actions"} {
		if err := a.checkTableSecrets(table); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) checkTableSecrets(table string) (err error) {
	// table 仅来自上方固定清单，不接受用户输入。
	rows, err := a.db.Read().QueryContext(a.ctx, "SELECT secret FROM "+table+" WHERE secret IS NOT NULL")
	if err != nil {
		return fmt.Errorf("检查 %s 凭据: %w", table, err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var encrypted []byte
		if err := rows.Scan(&encrypted); err != nil {
			return fmt.Errorf("读取 %s 凭据: %w", table, err)
		}
		plain, err := a.cipher.Decrypt(encrypted, table+".secret")
		if err != nil {
			return fmt.Errorf("检查 %s 凭据: %w", table, err)
		}
		clear(plain)
	}
	return rows.Err()
}

func (a *App) serve(listener net.Listener) (err error) {
	srv := server.New(listener.Addr().String())
	defer func() { err = errors.Join(err, srv.Close()) }()
	srv.BaseContext = func(net.Listener) context.Context { return a.ctx }
	srv.ErrorLog = slog.NewLogLogger(a.logger.Handler(), slog.LevelError)
	done := make(chan error, 1)
	if err := a.Go("http", func(context.Context) error {
		// HTTP 是进程入口，不做原地重启；panic 也必须通知生命周期退出。
		done <- a.invoke("http", func(context.Context) error { return srv.Serve(listener) })
		return nil
	}); err != nil {
		return errors.Join(err, listener.Close())
	}
	select {
	case err := <-done:
		// 未经本生命周期 Shutdown 就退出，即使是 ErrServerClosed 也需要报告。
		return fmt.Errorf("HTTP 服务意外停止: %w", err)
	case <-a.ctx.Done():
		a.logger.Info("开始优雅关闭")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := srv.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, srv.Close())
	}
	serveErr := <-done
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	if err := errors.Join(shutdownErr, serveErr); err != nil {
		return fmt.Errorf("关闭 HTTP 服务: %w", err)
	}
	return nil
}

// Close 停止并等待后台任务，随后关闭数据库并释放目录锁；可重复调用。
func (a *App) Close() error {
	a.closeOnce.Do(func() {
		a.mu.Lock()
		a.closing = true
		a.cancel()
		a.mu.Unlock()
		a.wg.Wait()
		if a.db != nil {
			a.closeErr = errors.Join(a.closeErr, a.db.Close())
		}
		if a.lock != nil {
			a.closeErr = errors.Join(a.closeErr, a.lock.Close())
		}
	})
	return a.closeErr
}
