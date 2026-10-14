package state

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"ups-warden/internal/model"
	"ups-warden/internal/store"
)

type input struct {
	result *model.SourceResult
	config *Config
	ack    chan error
}

// Actor 拥有一个设备的串行状态。Run 由 app.Go 管理；提交使用有界无损队列。
type Actor struct {
	db       *store.Store
	mu       sync.Mutex
	engine   *Engine
	inbox    chan input
	publish  func(model.DeviceUpdate)
	logger   *slog.Logger
	running  atomic.Bool
	stopped  chan struct{}
	stopOnce sync.Once
}

// NewActor 在启动 worker 前恢复持久化状态和告警，不访问设备。
func NewActor(ctx context.Context, db *store.Store, id int64, cfg Config, publish func(model.DeviceUpdate), logger *slog.Logger) (*Actor, error) {
	if id <= 0 || db == nil {
		return nil, errors.New("状态 actor 缺少有效设备或存储")
	}
	e, err := restore(ctx, db, id, cfg)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Actor{db: db, engine: e, inbox: make(chan input, 64), publish: publish, logger: logger, stopped: make(chan struct{})}, nil
}

// Submit 等待队列消费和关键状态落库；调用 context 取消可中止等待。
// 一旦入队，调用者取消不撤销已经采集的事实，也不应重发同一结果。
func (a *Actor) Submit(ctx context.Context, r model.SourceResult) error {
	r.Snapshot.Fields = model.CloneFields(r.Snapshot.Fields)
	r.Snapshot.Hints.Indicators = append([]string(nil), r.Snapshot.Hints.Indicators...)
	return a.enqueue(ctx, input{result: &r, ack: make(chan error, 1)})
}

// Configure 在 actor 队列内修改阈值；不重放旧样本，重新开始未确认窗口。
func (a *Actor) Configure(ctx context.Context, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	return a.enqueue(ctx, input{config: &cfg, ack: make(chan error, 1)})
}
func (a *Actor) enqueue(ctx context.Context, in input) error {
	select {
	case <-a.stopped:
		return errors.New("状态 actor 已停止")
	default:
	}
	select {
	case a.inbox <- in:
	case <-ctx.Done():
		return ctx.Err()
	case <-a.stopped:
		return errors.New("状态 actor 已停止")
	}
	select {
	case err := <-in.ack:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-a.stopped:
		return errors.New("状态 actor 已停止")
	}
}

// Snapshot 返回最后已提交的独立设备视图。
func (a *Actor) Snapshot() model.DeviceState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.engine.Snapshot()
}

// Process 是同步的事务入口，供离线回放/测试使用；运行中的采集应使用 Submit。
func (a *Actor) Process(ctx context.Context, r model.SourceResult) (model.DeviceUpdate, error) {
	return a.process(ctx, &r, nil, time.Time{}, false)
}

// Advance 推进纯时钟输入，持久化确认为停电/恢复的关键变化。
func (a *Actor) Advance(ctx context.Context, at time.Time) (model.DeviceUpdate, error) {
	return a.process(ctx, nil, nil, at, false)
}
func (a *Actor) process(ctx context.Context, r *model.SourceResult, cfg *Config, at time.Time, force bool) (model.DeviceUpdate, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	next := a.engine.clone()
	var u model.DeviceUpdate
	if r != nil {
		u = next.Apply(*r)
	} else if cfg != nil {
		if next.view.CommState == "lost" && next.config.LostPolicy == "ignore" {
			next.view.PowerState = next.heldPower
		}
		next.config = *cfg
		next.cancelPending()
		u = next.Tick(time.Now())
	} else {
		u = next.Tick(at)
	}
	if force || len(u.Events) > 0 || criticalChanged(a.engine, next) {
		if err := save(ctx, a.db, next, &u); err != nil {
			return model.DeviceUpdate{}, err
		}
	}
	a.engine = next
	if a.publish != nil {
		a.publish(model.CloneUpdate(u))
	}
	return u, nil
}

// Run 每秒推进一次状态；落库失败保留当前输入并退避重试，不能丢失事件。
// 取消时在独立期限内提交已接受事实；collector 的 Close 必须先于 Run 的取消。
func (a *Actor) Run(ctx context.Context) error {
	if !a.running.CompareAndSwap(false, true) {
		return errors.New("状态 actor 不可并行 Run")
	}
	defer a.running.Store(false)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		var in input
		var at time.Time
		select {
		case <-ctx.Done():
			return a.finish(nil)
		case in = <-a.inbox:
		case at = <-ticker.C:
		}
		for {
			_, err := a.process(ctx, in.result, in.config, at, false)
			if err == nil {
				if in.ack != nil {
					in.ack <- nil
				}
				break
			}
			a.logger.Error("设备状态持久化失败，将重试", "device_id", a.Snapshot().DeviceID)
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return a.finish(&in)
			case <-timer.C:
			}
		}
	}
}
func (a *Actor) finish(pending *input) error {
	a.stopOnce.Do(func() { close(a.stopped) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	consume := func(in input) error {
		if in.result == nil && in.config == nil {
			return nil
		}
		_, err := a.process(ctx, in.result, in.config, time.Time{}, false)
		if in.ack != nil {
			in.ack <- err
		}
		return err
	}
	if pending != nil {
		if err := consume(*pending); err != nil {
			return err
		}
	}
	draining := true
	for draining {
		select {
		case in := <-a.inbox:
			if err := consume(in); err != nil {
				return err
			}
		default:
			draining = false
		}
	}
	// 不在停机时凭旧观测完成去抖；仅保存最后已提交状态与最后成功时间。
	a.mu.Lock()
	defer a.mu.Unlock()
	next := a.engine.clone()
	u := model.DeviceUpdate{State: next.Snapshot()}
	if next.view.At.IsZero() {
		next.view.At = time.Now().UTC()
	}
	return save(ctx, a.db, next, &u)
}
