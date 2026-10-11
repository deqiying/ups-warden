// Command ups-warden 是 UPS 监控与联动服务的入口。
//
// 当前仅为脚手架：负责解析启动参数、初始化日志并托管 HTTP 服务的生命周期，
// 采集、规则、通知等业务模块尚未接入。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ups-warden/internal/server"
)

// version 由构建时通过 -ldflags "-X main.version=..." 注入。
var version = "dev"

// shutdownTimeout 是收到退出信号后等待进行中请求完成的最长时间。
const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("服务异常退出", "error", err)
		os.Exit(1)
	}
}

func run() error {
	listen := flag.String("listen", envOr("UPS_WARDEN_LISTEN", "127.0.0.1:8080"), "HTTP 监听地址（环境变量 UPS_WARDEN_LISTEN）")
	logLevel := flag.String("log-level", envOr("UPS_WARDEN_LOG_LEVEL", "info"), "日志级别：debug|info|warn|error（环境变量 UPS_WARDEN_LOG_LEVEL）")
	flag.Parse()

	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		return fmt.Errorf("无效的日志级别 %q: %w", *logLevel, err)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := server.New(*listen)
	errCh := make(chan error, 1)
	go func() {
		slog.Info("HTTP 服务启动", "listen", *listen, "version", version)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		// ListenAndServe 只会返回非 nil 错误；未经 Shutdown 的返回均视为启动或运行失败。
		return fmt.Errorf("HTTP 服务停止: %w", err)
	case <-ctx.Done():
		slog.Info("收到退出信号，开始优雅关闭")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("优雅关闭失败: %w", err)
	}
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("HTTP 服务停止: %w", err)
	}
	slog.Info("服务已停止")
	return nil
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
