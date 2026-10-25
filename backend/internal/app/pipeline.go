package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"ups-warden/internal/collector"
	"ups-warden/internal/history"
	"ups-warden/internal/model"
	"ups-warden/internal/settings"
	"ups-warden/internal/state"
)

func (a *App) startPipeline() error {
	current, err := settings.Load(a.ctx, a.db.Read())
	if err != nil {
		return err
	}
	a.history, err = history.New(a.db, history.Options{Logger: a.logger, DetailRetention: settings.Days(current.DetailRetentionDays), AggregateRetention: settings.Days(current.AggregateRetentionDays), LogRetention: settings.Days(current.LogRetentionDays)})
	if err != nil {
		return err
	}
	a.actors = map[int64]*state.Actor{}
	a.stateDone = map[int64]chan struct{}{}
	a.actorCancel = map[int64]context.CancelFunc{}
	a.actorErr = map[int64]error{}
	a.stateCtx, a.stateCancel = context.WithCancel(context.WithoutCancel(a.ctx))
	historyCtx, cancel := context.WithCancel(context.WithoutCancel(a.ctx))
	a.historyCancel = cancel
	if err = a.goWithContext(historyCtx, "history", a.history.Run, nil); err != nil {
		return err
	}
	if _, err = a.startMissingActors(a.ctx); err != nil {
		return err
	}
	a.collector = collector.New(a.db, a.cipher, func(ctx context.Context, r model.SourceResult) error {
		a.actorsMu.RLock()
		actor, ok := a.actors[r.DeviceID]
		a.actorsMu.RUnlock()
		if !ok {
			return errors.New("采集结果没有对应状态 actor")
		}
		return actor.Submit(ctx, r)
	}, collector.Options{Logger: a.logger, Go: func(name string, fn func(context.Context)) {
		if err := a.Go(name, func(ctx context.Context) error { fn(ctx); return nil }); err != nil {
			fn(a.ctx)
		}
	}})
	return a.collector.Start(a.ctx)
}

// startMissingActors 必须在 pipelineMu 下或开放 HTTP 前调用，先读完配置再占其他读连接。
func (a *App) startMissingActors(ctx context.Context) ([]int64, error) {
	rows, err := a.db.Read().QueryContext(ctx, "SELECT id,config FROM devices WHERE enabled=1 ORDER BY id")
	if err != nil {
		return nil, err
	}
	configs := map[int64]state.Config{}
	for rows.Next() {
		var id int64
		var content string
		if err = rows.Scan(&id, &content); err != nil {
			break
		}
		a.actorsMu.RLock()
		exists := a.actors[id] != nil
		a.actorsMu.RUnlock()
		if exists {
			continue
		}
		var cfg state.Config
		cfg, err = state.ParseConfig([]byte(content))
		if err != nil {
			err = fmt.Errorf("设备 %d 状态配置: %w", id, err)
			break
		}
		configs[id] = cfg
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return nil, err
	}
	started := []int64{}
	for id, cfg := range configs {
		actor, err := state.NewActor(ctx, a.db, id, cfg, a.publish, a.logger)
		if err != nil {
			return started, err
		}
		actorCtx, cancel := context.WithCancel(a.stateCtx)
		done := make(chan struct{})
		a.actorsMu.Lock()
		a.actors[id] = actor
		a.actorsMu.Unlock()
		if err = a.goWithContext(actorCtx, fmt.Sprintf("state-%d", id), func(ctx context.Context) error {
			err := actor.Run(ctx)
			if ctx.Err() != nil && err != nil {
				a.mu.Lock()
				a.stateErr = errors.Join(a.stateErr, err)
				a.mu.Unlock()
			}
			a.actorsMu.Lock()
			a.actorErr[id] = err
			a.actorsMu.Unlock()
			return err
		}, done); err != nil {
			cancel()
			a.actorsMu.Lock()
			delete(a.actors, id)
			a.actorsMu.Unlock()
			return started, err
		}
		a.stateDone[id] = done
		a.actorCancel[id] = cancel
		started = append(started, id)
	}
	return started, nil
}
func (a *App) stopDevice(ctx context.Context, id int64) error {
	if err := a.collector.StopDevice(ctx, id); err != nil {
		return err
	}
	cancel := a.actorCancel[id]
	if cancel == nil {
		return nil
	}
	cancel()
	// actor 自带独立的 5 秒关闭期限；必须等它结束才允许删除数据库记录。
	<-a.stateDone[id]
	a.actorsMu.Lock()
	err := a.actorErr[id]
	delete(a.actors, id)
	delete(a.actorErr, id)
	a.actorsMu.Unlock()
	delete(a.actorCancel, id)
	delete(a.stateDone, id)
	return err
}

// ChangeDevices 排空受影响设备，执行配置事务，再从提交状态恢复运行；失败不伪装回滚。
func (a *App) ChangeDevices(ctx context.Context, ids []int64, change func() error) (err error) {
	a.pipelineMu.Lock()
	defer a.pipelineMu.Unlock()
	a.mu.Lock()
	closing := a.closing
	a.mu.Unlock()
	if closing || a.ctx.Err() != nil {
		return errors.New("应用正在关闭")
	}
	affected := map[int64]bool{}
	for _, id := range ids {
		affected[id] = true
	}
	defer func() {
		// 客户端取消不应永久停采，恢复受应用上下文及独立限额约束。
		recovery, cancel := context.WithTimeout(a.ctx, 10*time.Second)
		defer cancel()
		added, e := a.startMissingActors(recovery)
		err = errors.Join(err, e)
		for _, id := range added {
			affected[id] = true
		}
		for id := range affected {
			rows, e := a.db.Read().QueryContext(recovery, "SELECT id FROM sources WHERE device_id=?", id)
			if e != nil {
				err = errors.Join(err, e)
				continue
			}
			sources := []int64{}
			for rows.Next() {
				var source int64
				if e = rows.Scan(&source); e != nil {
					break
				}
				sources = append(sources, source)
			}
			e = errors.Join(e, rows.Err(), rows.Close())
			if e != nil {
				err = errors.Join(err, e)
				continue
			}
			for _, source := range sources {
				err = errors.Join(err, a.collector.Reload(recovery, source))
			}
		}
		a.bus.Publish(model.DeviceUpdate{Resync: true})
	}()
	for id := range affected {
		if err = a.stopDevice(ctx, id); err != nil {
			return err
		}
	}
	if err = a.rules.ResetTimers(ctx, ids); err != nil {
		return err
	}
	if err = a.history.Flush(ctx); err != nil {
		return err
	}
	return change()
}

// Snapshots 返回当前运行设备的独立快照，按设备 ID 排序。
func (a *App) Snapshots() []model.DeviceState {
	a.actorsMu.RLock()
	actors := make([]*state.Actor, 0, len(a.actors))
	for _, actor := range a.actors {
		actors = append(actors, actor)
	}
	a.actorsMu.RUnlock()
	result := make([]model.DeviceState, 0, len(actors))
	for _, actor := range actors {
		result = append(result, actor.Snapshot())
	}
	sort.Slice(result, func(i, j int) bool { return result[i].DeviceID < result[j].DeviceID })
	return result
}
func (a *App) publish(u model.DeviceUpdate) {
	if u.Sample {
		a.history.Enqueue(u.State)
	}
	a.plans.Observe(u)
	if err := a.rules.Enqueue(u); err != nil {
		a.logger.Error("规则输入未被接受", "device_id", u.State.DeviceID, "error_type", fmt.Sprintf("%T", err))
		a.mu.Lock()
		a.stateErr = errors.Join(a.stateErr, fmt.Errorf("设备 %d 的规则输入未被接受: %w", u.State.DeviceID, err))
		a.mu.Unlock()
	}
	a.bus.Publish(u)
}
