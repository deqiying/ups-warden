// Package app 装配服务模块，并拥有数据目录、数据库及后台任务的生命周期。
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"ups-warden/internal/api"
	"ups-warden/internal/auth"
	"ups-warden/internal/bus"
	"ups-warden/internal/collector"
	"ups-warden/internal/config"
	"ups-warden/internal/datalock"
	"ups-warden/internal/history"
	"ups-warden/internal/notify"
	"ups-warden/internal/profile"
	"ups-warden/internal/secret"
	"ups-warden/internal/server"
	"ups-warden/internal/state"
	"ups-warden/internal/store"
)

const shutdownTimeout = 10 * time.Second

// App 持有进程资源并串行协调动态设备变更；关闭后不接受新任务。
type App struct {
	ctx           context.Context
	cancel        context.CancelFunc
	logger        *slog.Logger
	db            *store.Store
	cipher        *secret.Cipher
	auth          *auth.Service
	api           *api.Server
	lock          *datalock.Lock
	profiles      []*profile.Profile
	collector     *collector.Supervisor
	history       *history.Writer
	notify        *notify.Service
	bus           bus.Bus
	actors        map[int64]*state.Actor
	stateCancel   context.CancelFunc
	historyCancel context.CancelFunc
	stateDone     map[int64]chan struct{}
	actorCancel   map[int64]context.CancelFunc
	actorErr      map[int64]error
	stateCtx      context.Context
	pipelineMu    sync.Mutex
	actorsMu      sync.RWMutex
	stateErr      error
	wg            sync.WaitGroup
	mu            sync.Mutex
	closing       bool
	closeOnce     sync.Once
	closeErr      error
}

// Run 按“锁 → 密钥 → 数据库 → Profile → 状态/历史/采集 → HTTP”启动。
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
	// 初始化口令只写进程 stderr 一次，不进入轮转日志、数据库或 HTTP 响应。
	if token := a.auth.SetupToken(); token != "" {
		fmt.Fprintf(os.Stderr, "首次初始化 setup token: %s\n", token)
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
	a.profiles, err = profile.Builtins()
	if err != nil {
		return nil, fmt.Errorf("加载内置 Profile: %w", err)
	}
	a.auth, err = auth.New(a.db, auth.Options{})
	if err != nil {
		return nil, err
	}
	a.notify, err = notify.New(child, a.db, a.cipher, &a.bus, a.logger)
	if err != nil {
		return nil, err
	}
	if err := a.startPipeline(); err != nil {
		return nil, err
	}
	a.api, err = api.New(api.Options{DB: a.db, Cipher: a.cipher, Auth: a.auth, Collector: a.collector, History: a.history, Bus: &a.bus, Runtime: a, Notify: a.notify, Logger: a.logger})
	if err != nil {
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
	srv := server.New(listener.Addr().String(), a.api)
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

// Close 依次停止采集、状态、历史和其他任务，再关闭数据库并释放目录锁。
func (a *App) Close() error {
	a.closeOnce.Do(func() {
		a.mu.Lock()
		a.closing = true
		a.cancel()
		a.mu.Unlock()
		a.pipelineMu.Lock()
		defer a.pipelineMu.Unlock()
		if a.collector != nil {
			a.closeErr = errors.Join(a.closeErr, a.collector.Close())
		}
		if a.stateCancel != nil {
			a.stateCancel()
		}
		for _, done := range a.stateDone {
			<-done
		}
		if a.history != nil {
			ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			a.closeErr = errors.Join(a.closeErr, a.stateErr)
			a.closeErr = errors.Join(a.closeErr, a.history.Close(ctx))
			cancel()
		}
		if a.historyCancel != nil {
			a.historyCancel()
		}
		if a.notify != nil {
			a.closeErr = errors.Join(a.closeErr, a.notify.Close())
		}
		a.bus.Close()
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
