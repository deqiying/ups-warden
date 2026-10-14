// Package history 只持久化字段目录允许的归一化历史值，不保存诊断或协议数据。
package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"ups-warden/internal/model"
	"ups-warden/internal/store"
)

const queueLimit = 1024
const batchSize = 200

// Options 配置保留期；零值分别使用 7、90、365 天，负值无效。
type Options struct {
	DetailRetention, AggregateRetention, LogRetention time.Duration
	Logger                                            *slog.Logger
}

type sample struct {
	device int64
	at     int64
	fields map[string]float64
}

// Writer 的队列（含失败待重试批次）最多 1024 行。Run 只能调用一次；
// Run 取消不丢弃队列，可用独立 context 调用 Close 重试刷新。
type Writer struct {
	db        *store.Store
	opts      Options
	mu        sync.Mutex
	queue     []sample
	closed    bool
	stop      chan struct{}
	wake      chan struct{}
	flushGate chan struct{}
	ran       atomic.Bool
	dropped   atomic.Uint64
}

// New 创建 writer，不启动后台任务，也不改变数据库 schema。
func New(db *store.Store, opts Options) (*Writer, error) {
	if db == nil || db.Read() == nil || db.Write() == nil {
		return nil, errors.New("history: require an open store")
	}
	if opts.DetailRetention < 0 || opts.AggregateRetention < 0 || opts.LogRetention < 0 {
		return nil, errors.New("history: retention must not be negative")
	}
	if opts.DetailRetention == 0 {
		opts.DetailRetention = 7 * 24 * time.Hour
	}
	if opts.AggregateRetention == 0 {
		opts.AggregateRetention = 90 * 24 * time.Hour
	}
	if opts.LogRetention == 0 {
		opts.LogRetention = 365 * 24 * time.Hour
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Writer{db: db, opts: opts, queue: make([]sample, 0, queueLimit), stop: make(chan struct{}), wake: make(chan struct{}, 1), flushGate: make(chan struct{}, 1)}, nil
}

func historyKey(key string) (model.FieldSpec, bool) {
	spec, ok := model.Lookup(key)
	return spec, ok && spec.History && (!spec.Phased || model.Phase(key) != 0) && (spec.Kind == model.Number || spec.Kind == model.Duration || spec.Kind == model.Bool)
}

// Enqueue 非阻塞接收状态；只复制 Good 且 History=true 的数值/布尔字段。
// 当前目录没有 History=true 的枚举或布尔字段；布尔在未来允许时以 0/1 存储。
// 满队列计入 Dropped；已关闭、非法设备/时间或无可存字段返回 false，不计丢弃。
func (w *Writer) Enqueue(state model.DeviceState) bool {
	if state.DeviceID <= 0 || state.At.IsZero() {
		return false
	}
	fields := make(map[string]float64)
	for key, field := range state.Fields {
		spec, ok := historyKey(key)
		if !ok || field.Quality != model.Good {
			continue
		}
		value := field.Num
		if spec.Kind == model.Bool {
			value = 0
			if field.Bool {
				value = 1
			}
		}
		if !math.IsNaN(value) && !math.IsInf(value, 0) {
			fields[key] = value
		}
	}
	if len(fields) == 0 {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return false
	}
	if len(w.queue) == queueLimit {
		w.dropped.Add(1)
		return false
	}
	w.queue = append(w.queue, sample{device: state.DeviceID, at: state.At.UnixMilli(), fields: fields})
	if len(w.queue) >= batchSize {
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
	return true
}

// Dropped 返回因队列满而拒绝的样本总数。
func (w *Writer) Dropped() uint64 { return w.dropped.Load() }

func (w *Writer) stopAccepting() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		w.closed = true
		close(w.stop)
	}
}

// Close 停止接收并同步刷新，不等待 Run 或要求取消其 context。
// 刷新失败保留队列；可再次 Close。应先停上游，再 Close，最后取消 Run。
func (w *Writer) Close(ctx context.Context) error {
	w.stopAccepting()
	return w.Flush(ctx)
}

// Flush 刷新调用取得串行锁时已有的队列，每个事务最多 200 行。
// 提交失败时原批次仍占队列容量，不会扩展额外无界缓冲。
func (w *Writer) Flush(ctx context.Context) error {
	select {
	case w.flushGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-w.flushGate }()
	w.mu.Lock()
	remaining := len(w.queue)
	w.mu.Unlock()
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(remaining, batchSize)
		w.mu.Lock()
		batch := append([]sample(nil), w.queue[:n]...)
		w.mu.Unlock()
		err := w.db.WithTx(ctx, func(tx *sql.Tx) error {
			stmt, err := tx.PrepareContext(ctx, `INSERT INTO history_sample(device_id,ts,data) VALUES(?,?,?) ON CONFLICT(device_id,ts) DO UPDATE SET data=excluded.data`)
			if err != nil {
				return err
			}
			defer stmt.Close()
			for _, s := range batch {
				data, err := json.Marshal(s.fields)
				if err != nil {
					return err
				}
				if _, err = stmt.ExecContext(ctx, s.device, s.at, string(data)); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			w.opts.Logger.Error("历史批量写入失败，保留待写队列", "rows", n, "error", err)
			return fmt.Errorf("history: flush: %w", err)
		}
		w.mu.Lock()
		copy(w.queue, w.queue[n:])
		clear(w.queue[len(w.queue)-n:])
		w.queue = w.queue[:len(w.queue)-n]
		w.mu.Unlock()
		remaining -= n
	}
	return nil
}

// Run 每秒或达到 200 行刷新，每分钟重算完整分钟，每小时清理。
// 失败通过日志报告并在后续周期重试；取消仅停止接收，保留未提交队列。
func (w *Writer) Run(ctx context.Context) error {
	if !w.ran.CompareAndSwap(false, true) {
		return errors.New("history: Run may only be called once")
	}
	defer w.stopAccepting()
	select {
	case <-w.stop:
		return nil
	default:
	}
	if err := w.Aggregate(ctx, time.Now()); err != nil && ctx.Err() == nil {
		w.opts.Logger.Error("历史启动补算失败", "error", err)
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	minute := time.NewTicker(time.Minute)
	defer minute.Stop()
	hour := time.NewTicker(time.Hour)
	defer hour.Stop()
	reportDrops := time.NewTicker(time.Minute)
	defer reportDrops.Stop()
	var reported uint64
	retry := false
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.stop:
			return nil
		case <-w.wake:
			if !retry {
				retry = w.Flush(ctx) != nil
			}
		case <-tick.C:
			retry = w.Flush(ctx) != nil
		case now := <-minute.C:
			if err := w.Aggregate(ctx, now); err != nil {
				w.opts.Logger.Error("历史聚合失败", "error", err)
			}
		case now := <-hour.C:
			if err := w.Aggregate(ctx, now); err != nil {
				w.opts.Logger.Error("历史清理前聚合失败", "error", err)
				continue
			}
			if err := w.Cleanup(ctx, now); err != nil {
				w.opts.Logger.Error("历史清理失败", "error", err)
			}
		case <-reportDrops.C:
			total := w.Dropped()
			if total != reported {
				w.opts.Logger.Warn("历史队列已满，样本被丢弃", "dropped", total-reported, "total", total)
				reported = total
			}
		}
	}
}
