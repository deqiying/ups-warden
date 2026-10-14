package app

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"
)

// Go 启动受管任务：正常返回即结束，错误或 panic 按 1..30 秒指数退避重启。
// 连续运行超过一分钟后重置退避。fn 必须响应 ctx 取消，不得调用 App.Close。
// 接受的任务至少调用一次，确保即使启动与取消同时发生也能完成资源清理。
func (a *App) Go(name string, fn func(context.Context) error) error {
	return a.goWithContext(a.ctx, name, fn, nil)
}

// goWithContext 允许分阶段关闭流水线；done 在整个重试循环退出后关闭。
func (a *App) goWithContext(ctx context.Context, name string, fn func(context.Context) error, done chan struct{}) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing || a.ctx.Err() != nil {
		return errors.New("应用正在关闭，拒绝启动后台任务")
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		if done != nil {
			defer close(done)
		}
		delay := time.Second
		for {
			start := time.Now()
			err := a.invoke(name, func(context.Context) error { return fn(ctx) })
			if err == nil || ctx.Err() != nil {
				return
			}
			if time.Since(start) >= time.Minute {
				delay = time.Second
			}
			a.logger.Error("后台任务失败，将重启", "worker", name, "error", err, "retry_after", delay)
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			if ctx.Err() != nil {
				return
			}
			delay = min(delay*2, 30*time.Second)
		}
	}()
	return nil
}

func (a *App) invoke(name string, fn func(context.Context) error) (err error) {
	defer func() {
		if value := recover(); value != nil {
			// panic 内容可能带凭据，只保留其类型与定位堆栈，不格式化原始值。
			err = fmt.Errorf("后台任务 panic（%T）", value)
			a.logger.Error("后台任务 panic，已隔离", "worker", name, "error", err, "stack", string(debug.Stack()))
		}
	}()
	return fn(a.ctx)
}
