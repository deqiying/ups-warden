// Command ups-warden 是 UPS 监控与联动服务入口；业务模块按里程碑逐步接入。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"ups-warden/internal/app"
	"ups-warden/internal/config"
	"ups-warden/internal/logging"
)

// version 由构建时通过 -ldflags "-X main.version=..." 注入。
var version = "dev"

func main() {
	if err := run(); err != nil {
		// run 会在关闭日志前记录运行期错误；stderr 保证初始化失败也可见。
		fmt.Fprintln(os.Stderr, "服务异常退出:", err)
		os.Exit(1)
	}
}

func run() (err error) {
	c, err := config.Parse(os.Args[1:], os.Getenv, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.Version {
		fmt.Println(version)
		return nil
	}
	logger, output, err := logging.New(c, os.Stderr)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, output.Close()) }()
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = app.Run(ctx, c, version, logger, os.Getenv("UPS_WARDEN_MASTER_KEY"))
	if err != nil {
		logger.Error("服务异常退出", "error", err)
	} else {
		logger.Info("服务已停止")
	}
	return err
}
