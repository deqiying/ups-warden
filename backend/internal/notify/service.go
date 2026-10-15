// Package notify 提供独立渠道队列、模板和有界重试，不订阅设备事件或替代规则引擎。
package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"ups-warden/internal/bus"
	"ups-warden/internal/model"
	"ups-warden/internal/secret"
	"ups-warden/internal/store"
)

// 明确区分未接受、已保存但运行失败和已关闭，调用方不可将这些状态当作发送成功。
var (
	ErrUnavailable = errors.New("notify: 渠道未启用、正在变更或不可用")
	ErrQueueFull   = errors.New("notify: 渠道队列已满")
	ErrClosed      = errors.New("notify: 服务已关闭")
	ErrReload      = errors.New("notify: 配置已保存但运行恢复失败")
)

// Request 只携带统一模型；Event.ID=0 表示无持久化事件的测试消息。
type Request struct {
	DeviceName string
	Event      model.Event
	State      model.DeviceState
}

type job struct {
	message  Message
	eventID  *int64
	fallback bool
}
type worker struct {
	record     ChannelRecord
	deliveries []Delivery
	queue      chan job
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	err        error // 只由工作协程写，关闭 done 后读取。
}

// Service 拥有渠道工作协程；Close 必须在数据库和总线之前调用。
type Service struct {
	db       *store.Store
	cipher   *secret.Cipher
	bus      *bus.Bus
	logger   *slog.Logger
	changes  sync.Mutex
	mu       sync.Mutex
	workers  map[int64]*worker
	closed   bool
	closeErr error
}

// New 加载并校验全部渠道；只启动本地队列，未入队前不外发。
func New(ctx context.Context, db *store.Store, cipher *secret.Cipher, b *bus.Bus, logger *slog.Logger) (*Service, error) {
	if db == nil || cipher == nil || b == nil {
		return nil, errors.New("notify: 缺少必需模块")
	}
	if logger == nil {
		logger = slog.Default()
	}
	s := &Service{db: db, cipher: cipher, bus: b, logger: logger, workers: map[int64]*worker{}}
	rows, err := db.Read().QueryContext(ctx, "SELECT "+ChannelColumns+" FROM channels ORDER BY id")
	if err != nil {
		return nil, err
	}
	var records []ChannelRecord
	for rows.Next() {
		c, e := ScanChannel(rows, cipher)
		if e != nil {
			err = e
			break
		}
		records = append(records, c)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return nil, err
	}
	for _, c := range records {
		if err = s.start(c); err != nil {
			return nil, errors.Join(err, s.Close())
		}
	}
	return s, nil
}
func (s *Service) start(c ChannelRecord) error {
	if !c.Enabled {
		return nil
	}
	deliveries, err := NewDeliveries(c.Type, c.Config, c.Credentials)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &worker{record: c, deliveries: deliveries, queue: make(chan job, 256), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	s.workers[c.ID] = w
	s.mu.Unlock()
	go s.run(w)
	return nil
}

// Change 暂停目标渠道并取消已接受的旧配置任务，然后执行调用方事务。
// apply 返回新建/变更 ID；失败后也从数据库恢复旧配置，恢复过程不受请求取消影响。
func (s *Service) Change(ctx context.Context, id int64, apply func() (int64, error)) error {
	s.changes.Lock()
	defer s.changes.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	old := s.workers[id]
	delete(s.workers, id)
	s.mu.Unlock()
	var stopErr error
	if old != nil {
		old.cancel()
		<-old.done
		stopErr = old.err
	}
	changedID := id
	err := stopErr
	if err == nil {
		if e := ctx.Err(); e != nil {
			err = e
		} else {
			changedID, err = apply()
		}
	}
	reloadID := changedID
	if err != nil {
		reloadID = id
	}
	reloadCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var reloadErr error
	if reloadID != 0 {
		var c ChannelRecord
		c, reloadErr = ReadChannel(reloadCtx, s.db, s.cipher, reloadID)
		if errors.Is(reloadErr, sql.ErrNoRows) {
			reloadErr = nil
		} else if reloadErr == nil {
			reloadErr = s.start(c)
		}
	}
	if reloadErr != nil {
		s.logger.Error("通知渠道恢复失败", "channel_id", reloadID, "error_type", fmt.Sprintf("%T", reloadErr))
		if err == nil {
			return ErrReload
		}
	}
	return errors.Join(err, reloadErr)
}

// Enqueue 渲染后非阻塞入队；nil 仅表示已接受，不表示已送达。
// 不持久化发送队列，重启不会重放。每个目标的最终结果查询 notify_logs。
func (s *Service) Enqueue(ctx context.Context, id int64, r Request) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// 短锁只取不可变配置；数据库访问和渲染不阻塞其他渠道。
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	w := s.workers[id]
	s.mu.Unlock()
	if w == nil {
		return ErrUnavailable
	}
	if !knownTemplateEvent(r.Event.Type) {
		return errors.New("notify: 事件类型无效")
	}
	if r.Event.ID < 0 {
		return errors.New("notify: 事件 ID 无效")
	}
	if r.Event.ID > 0 {
		var exists int
		if err := s.db.Read().QueryRowContext(ctx, "SELECT 1 FROM events WHERE id=? AND device_id=? AND type=?", r.Event.ID, r.Event.DeviceID, r.Event.Type).Scan(&exists); err != nil {
			return err
		}
	}
	vars := Variables(r.DeviceName, r.Event, r.State, time.Now())
	bodyTemplate := w.record.Config.BodyTemplate
	var err error
	if bodyTemplate == "" {
		bodyTemplate, err = LoadTemplate(ctx, s.db, r.Event.Type, w.record.Config.Format)
		if err != nil {
			return err
		}
	}
	body, err := Render(bodyTemplate, w.record.Config.Format, vars)
	fallback := err != nil
	if fallback {
		s.logger.Warn("通知模板渲染失败，使用默认模板", "channel_id", id, "event_type", r.Event.Type)
		bodyTemplate, err = DefaultTemplate(r.Event.Type, w.record.Config.Format)
		if err != nil {
			return err
		}
		body, err = Render(bodyTemplate, w.record.Config.Format, vars)
		if err != nil {
			return err
		}
	}
	j := job{message: Message{Severity: string(r.Event.Severity), Title: r.Event.Type, Body: body}, fallback: fallback}
	if r.Event.ID != 0 {
		id := r.Event.ID
		j.eventID = &id
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if s.workers[id] != w {
		return ErrUnavailable
	}
	select {
	case <-w.done:
		return ErrUnavailable
	default:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case w.queue <- j:
		return nil
	default:
		return ErrQueueFull
	}
}
func (s *Service) run(w *worker) {
	defer close(w.done)
	defer func() {
		for _, d := range w.deliveries {
			d.Channel.Close()
		}
	}()
	for {
		select {
		case <-w.ctx.Done():
			// 已接受任务不静默丢弃；关闭时只记取消，不再外发。
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for {
				select {
				case j := <-w.queue:
					if err := s.deliver(ctx, w, j, true); err != nil {
						w.err = err
						return
					}
				default:
					return
				}
			}
		case j := <-w.queue:
			if err := s.deliver(w.ctx, w, j, false); err != nil {
				w.err = err
				s.logger.Error("通知结果持久化失败，渠道已停止", "channel_id", w.record.ID, "error_type", fmt.Sprintf("%T", err))
				return
			}
		}
	}
}
func (s *Service) deliver(ctx context.Context, w *worker, j job, cancelled bool) error {
	for _, d := range w.deliveries {
		start := time.Now()
		result := model.NotificationResult{ChannelID: w.record.ID, EventID: j.eventID, Target: d.Target, Summary: SafeSummary(j.message.Body, w.record.Credentials), TemplateFallback: j.fallback}
		var sendErr error
		if cancelled {
			sendErr = context.Canceled
		} else {
			result.Attempts, sendErr = sendWithRetry(ctx, d.Channel, j.message)
		}
		result.OK = sendErr == nil
		if sendErr != nil {
			result.Error = sendErr.Error()
		}
		result.DurationMS = time.Since(start).Milliseconds()
		result.At = time.Now().UTC()
		// 发送上下文可能已取消，使用独立短期限保存其真实结果。
		saveCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if cancelled {
			cancel()
			saveCtx = ctx
			cancel = func() {}
		}
		err := s.saveResult(saveCtx, &result)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}
func sendWithRetry(ctx context.Context, c Channel, msg Message) (int, error) {
	attempts := 0
	var err error
	for attempts < 4 {
		if ctx.Err() != nil {
			return attempts, errors.New("notify: 发送已取消")
		}
		attempts++
		err = sendOnce(ctx, c, msg)
		if err == nil {
			return attempts, nil
		}
		if attempts == 4 {
			break
		}
		delay := time.Second * time.Duration(1<<attempts)
		if retry := RetryDelay(err); retry > delay {
			delay = retry
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return attempts, errors.New("notify: 发送已取消")
		case <-timer.C:
		}
	}
	return attempts, err
}
func sendOnce(ctx context.Context, c Channel, msg Message) (err error) {
	// 通知是受管副作用；panic 不得击穿进程，也不格式化可能含凭据的 panic 值。
	defer func() {
		if recover() != nil {
			err = errors.New("notify: 渠道发送 panic")
		}
	}()
	return c.Send(ctx, msg)
}
func (s *Service) saveResult(ctx context.Context, r *model.NotificationResult) error {
	result, err := s.db.Write().ExecContext(ctx, `INSERT INTO notify_logs(channel_id,event_id,at,summary,ok,duration_ms,error,target,attempts,template_fallback) VALUES(?,?,?,?,?,?,?,?,?,?)`, r.ChannelID, r.EventID, r.At.UnixMilli(), r.Summary, r.OK, r.DurationMS, r.Error, r.Target, r.Attempts, r.TemplateFallback)
	if err != nil {
		return err
	}
	r.ID, err = result.LastInsertId()
	if err != nil {
		return err
	}
	if !r.OK {
		s.bus.Publish(model.DeviceUpdate{NotifyFailed: r})
	}
	return nil
}

// Close 停止接收、取消外发并记录队列取消结果；可重复调用，返回所有工作协程的存储错误。
func (s *Service) Close() error {
	s.changes.Lock()
	defer s.changes.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return s.closeErr
	}
	s.closed = true
	workers := s.workers
	s.workers = map[int64]*worker{}
	for _, w := range workers {
		w.cancel()
	}
	s.mu.Unlock()
	for _, w := range workers {
		<-w.done
		s.closeErr = errors.Join(s.closeErr, w.err)
	}
	return s.closeErr
}
