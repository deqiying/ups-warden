package collector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"sync"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/model"
	"ups-warden/internal/normalize"
	"ups-warden/internal/secret"
	"ups-warden/internal/store"
)

// Options 提供测试接缝与应用生命周期注册；Go 必须异步执行一次传入函数。
type Options struct {
	Factory func(Source, driver.ReadPlan) (driver.Driver, error)
	Resolve func(context.Context, string) ([]net.IPAddr, error)
	Jitter  func(time.Duration) time.Duration
	Go      func(string, func(context.Context))
	Logger  *slog.Logger
}

// Status 是最近一次采集的安全状态；ErrorCode 永远不含驱动原始错误。
type Status struct {
	LastOKAt  time.Time
	Latency   time.Duration
	Failures  int
	ErrorCode string
}

type diagnostics struct {
	raw   driver.RawData
	trace []normalize.Trace
}
type worker struct {
	source Source
	driver driver.Driver
	cancel context.CancelFunc
	done   chan struct{}
	gate   chan struct{}
}

// Supervisor 管理 worker 与临时连接测试。Start/Reload/Close 串行，查询可并发。
// sink 必须遵守 context，且不得从回调中重入 Start/Reload/Close。
type Supervisor struct {
	db              *store.Store
	cipher          *secret.Cipher
	sink            func(context.Context, model.SourceResult) error
	opts            Options
	op              sync.Mutex
	mu              sync.Mutex
	workers         map[int64]*worker
	statuses        map[int64]Status
	cache           map[int64]diagnostics
	gates           map[string]chan struct{}
	life            context.Context
	cancel          context.CancelFunc
	runCtx          context.Context
	started, closed bool
	wg              sync.WaitGroup
	closeErr        error
}

// New 不联网；Start 从 DB 装配启用的设备与数据源。
func New(db *store.Store, cipher *secret.Cipher, sink func(context.Context, model.SourceResult) error, opts Options) *Supervisor {
	if opts.Factory == nil {
		opts.Factory = defaultFactory
	}
	if opts.Resolve == nil {
		opts.Resolve = net.DefaultResolver.LookupIPAddr
	}
	if opts.Jitter == nil {
		opts.Jitter = func(d time.Duration) time.Duration {
			if d <= 1 {
				return 0
			}
			return time.Duration(rand.Int64N(int64(d)))
		}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	life, cancel := context.WithCancel(context.Background())
	return &Supervisor{db: db, cipher: cipher, sink: sink, opts: opts, life: life, cancel: cancel, workers: map[int64]*worker{}, statuses: map[int64]Status{}, cache: map[int64]diagnostics{}, gates: map[string]chan struct{}{}}
}

func (s *Supervisor) prepare(ctx context.Context, source Source) (*worker, error) {
	plan, err := validate(source)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, source.Timeout)
	defer cancel()
	source.Host, err = s.canonical(ctx, source.Host)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	d, err := makeDriver(s.opts.Factory, source, plan)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	gate := s.gates[source.Host]
	if gate == nil {
		gate = make(chan struct{}, 1)
		s.gates[source.Host] = gate
	}
	s.mu.Unlock()
	return &worker{source: source, driver: d, done: make(chan struct{}), gate: gate}, nil
}

func makeDriver(factory func(Source, driver.ReadPlan) (driver.Driver, error), source Source, plan driver.ReadPlan) (d driver.Driver, err error) {
	defer func() {
		if recover() != nil {
			d = nil
			err = ErrPanic
		}
	}()
	d, err = factory(source, plan)
	if err != nil || d == nil {
		if d != nil {
			_ = closeDriver(d)
		}
		return nil, ErrConfig
	}
	return d, nil
}

// Start 完整校验后才启动 worker；P0 拒绝一个设备有多个启用源。
func (s *Supervisor) Start(ctx context.Context) error {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	closed, started := s.closed, s.started
	s.mu.Unlock()
	if closed {
		return ErrClosed
	}
	if started {
		return ErrConfig
	}
	if s.sink == nil {
		return ErrConfig
	}
	sources, err := s.load(ctx)
	if err != nil {
		return err
	}
	prepared := make([]*worker, 0, len(sources))
	for _, source := range sources {
		w, e := s.prepare(ctx, source)
		if e != nil {
			for _, old := range prepared {
				s.recordClose(old.driver)
			}
			return e
		}
		prepared = append(prepared, w)
	}
	if ctx.Err() != nil {
		for _, old := range prepared {
			s.recordClose(old.driver)
		}
		return ctx.Err()
	}
	s.mu.Lock()
	s.started = true
	s.runCtx = ctx
	s.mu.Unlock()
	for _, w := range prepared {
		s.launch(w)
	}
	return nil
}

func (s *Supervisor) launch(w *worker) {
	ctx, cancel := context.WithCancel(s.runCtx)
	w.cancel = cancel
	stopLife := context.AfterFunc(s.life, cancel)
	s.mu.Lock()
	s.workers[w.source.ID] = w
	s.statuses[w.source.ID] = Status{}
	s.wg.Add(1)
	s.mu.Unlock()
	run := func(appCtx context.Context) {
		stopApp := context.AfterFunc(appCtx, cancel)
		defer func() {
			if recover() != nil {
				s.mu.Lock()
				st := s.statuses[w.source.ID]
				st.Failures++
				st.ErrorCode = "worker_panic"
				s.statuses[w.source.ID] = st
				s.closeErr = errors.Join(s.closeErr, ErrPanic)
				s.mu.Unlock()
				s.opts.Logger.Error("采集 worker 异常", "source_id", w.source.ID, "code", "worker_panic")
			}
			cancel()
			stopLife()
			stopApp()
			if w.driver != nil {
				s.recordClose(w.driver)
			}
			close(w.done)
			s.wg.Done()
		}()
		s.pollLoop(ctx, w)
	}
	if s.opts.Go != nil {
		s.opts.Go(fmt.Sprintf("collector-%d", w.source.ID), run)
	} else {
		go run(s.life)
	}
}

// Reload 先取消并等待旧 worker（包括 sink），再读取提交后的配置。
// source 被禁用/删除时仅停止；失败时不会恢复旧配置，也不会让旧结果进入新 worker。
func (s *Supervisor) Reload(ctx context.Context, sourceID int64) error {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	closed, started := s.closed, s.started
	old := s.workers[sourceID]
	s.mu.Unlock()
	if closed {
		return ErrClosed
	}
	if !started {
		return ErrNotStarted
	}
	if old != nil {
		old.cancel()
		select {
		case <-old.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		s.mu.Lock()
		delete(s.workers, sourceID)
		delete(s.statuses, sourceID)
		delete(s.cache, sourceID)
		s.mu.Unlock()
	}
	sources, err := s.load(ctx)
	if err != nil {
		return err
	}
	for _, source := range sources {
		if source.ID != sourceID {
			continue
		}
		// DB 中源迁移设备时，也不能与未 Reload 的正在运行源发生冲突。
		s.mu.Lock()
		conflict := false
		for id, w := range s.workers {
			if id != sourceID && w.source.DeviceID == source.DeviceID {
				conflict = true
			}
		}
		s.mu.Unlock()
		if conflict {
			return ErrConflict
		}
		w, err := s.prepare(ctx, source)
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			s.recordClose(w.driver)
			return ctx.Err()
		}
		s.launch(w)
		break
	}
	return nil
}

// Close 取消并等待所有 worker/连接测试，再清空状态、诊断和信号量；可重复调用。
func (s *Supervisor) Close() error {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	s.closed = true
	s.cancel()
	for _, w := range s.workers {
		w.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.workers)
	clear(s.statuses)
	clear(s.cache)
	clear(s.gates)
	return s.closeErr
}

func closeDriver(d driver.Driver) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrPanic
		}
	}()
	if d.Close() != nil {
		return ErrDriver
	}
	return nil
}
func (s *Supervisor) recordClose(d driver.Driver) {
	if err := closeDriver(d); err != nil {
		s.mu.Lock()
		s.closeErr = errors.Join(s.closeErr, err)
		s.mu.Unlock()
		s.opts.Logger.Error("关闭采集驱动失败", "code", errorCode(err))
	}
}

// Status 返回状态的值副本；停止/关闭后返回 false。
func (s *Supervisor) Status(id int64) (Status, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.statuses[id]
	return st, ok
}

// Diagnostics 返回最近尝试的独立深拷贝；只有内存缓存，不进行 DB/设备访问。
func (s *Supervisor) Diagnostics(id int64) (driver.RawData, []normalize.Trace, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.cache[id]
	if !ok {
		return driver.RawData{}, nil, false
	}
	return cloneRaw(d.raw), cloneTrace(d.trace), true
}

func errorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrPasswordWrong):
		return "password_wrong"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, ErrPanic):
		return "driver_panic"
	case errors.Is(err, ErrNoObservation):
		return "no_observation"
	case errors.Is(err, ErrBusy):
		return "busy"
	default:
		return "driver_failed"
	}
}
