package app

import (
	"context"
	"errors"
	"fmt"

	"ups-warden/internal/collector"
	"ups-warden/internal/history"
	"ups-warden/internal/model"
	"ups-warden/internal/state"
)

// startPipeline 先恢复所有设备再开放采集；P0 只装配启动时配置，CRUD 接入留给 M4。
func (a *App) startPipeline() error {
	var err error
	a.history, err = history.New(a.db, history.Options{Logger: a.logger})
	if err != nil {
		return err
	}
	a.actors = map[int64]*state.Actor{}
	rows, err := a.db.Read().QueryContext(a.ctx, "SELECT id,config FROM devices WHERE enabled=1 ORDER BY id")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var configJSON string
		if err = rows.Scan(&id, &configJSON); err != nil {
			break
		}
		var cfg state.Config
		cfg, err = state.ParseConfig([]byte(configJSON))
		if err != nil {
			err = fmt.Errorf("设备 %d 状态配置: %w", id, err)
			break
		}
		var actor *state.Actor
		actor, err = state.NewActor(a.ctx, a.db, id, cfg, a.publish, a.logger)
		if err != nil {
			break
		}
		a.actors[id] = actor
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	stateCtx, stateCancel := context.WithCancel(context.WithoutCancel(a.ctx))
	a.stateCancel = stateCancel
	historyCtx, historyCancel := context.WithCancel(context.WithoutCancel(a.ctx))
	a.historyCancel = historyCancel
	if err = a.goWithContext(historyCtx, "history", a.history.Run, nil); err != nil {
		return err
	}
	for id, actor := range a.actors {
		done := make(chan struct{})
		if err = a.goWithContext(stateCtx, fmt.Sprintf("state-%d", id), func(ctx context.Context) error {
			err := actor.Run(ctx)
			if ctx.Err() != nil && err != nil {
				a.mu.Lock()
				a.stateErr = errors.Join(a.stateErr, err)
				a.mu.Unlock()
			}
			return err
		}, done); err != nil {
			return err
		}
		a.stateDone = append(a.stateDone, done)
	}
	a.collector = collector.New(a.db, a.cipher, func(ctx context.Context, r model.SourceResult) error {
		actor, ok := a.actors[r.DeviceID]
		if !ok {
			return errors.New("采集结果没有对应状态 actor")
		}
		return actor.Submit(ctx, r)
	}, collector.Options{Logger: a.logger, Go: func(name string, fn func(context.Context)) {
		// supervisor 在 Start/Reload 内拥有 worker context；关闭竞态拒绝受管启动时
		// 同步调用一次，使已取消 worker 仍可释放 supervisor 的等待计数。
		if err := a.Go(name, func(ctx context.Context) error { fn(ctx); return nil }); err != nil {
			fn(a.ctx)
		}
	}})
	return a.collector.Start(a.ctx)
}

func (a *App) publish(u model.DeviceUpdate) {
	if u.Sample {
		a.history.Enqueue(u.State)
	}
	a.bus.Publish(u)
}
