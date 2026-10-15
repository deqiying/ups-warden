package rule

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"time"

	"ups-warden/internal/action"
	"ups-warden/internal/model"
	"ups-warden/internal/store"
)

// Dispatcher 只要求动作接受接口，规则不关心 SSH、HTTP 或脚本实现。
type Dispatcher interface {
	Submit(context.Context, action.Request) (int64, error)
}

// Service 顺序处理状态更新。队列有界且无损，不使用面向 SSE 的有损总线。
// 每秒评估由 state.Actor 的 DeviceUpdate tick 驱动，不重复维护设备时钟。
type Service struct {
	db          *store.Store
	actions     Dispatcher
	logger      *slog.Logger
	inbox       chan input
	stop        chan struct{}
	done        chan struct{}
	mu          sync.RWMutex
	closed      bool
	processMu   sync.Mutex
	closeOnce   sync.Once
	stopOnce    sync.Once
	completions []completion // 只补写已派发结果，不重放动作；由 processMu 保护。
	err         error
}

type input struct {
	update   model.DeviceUpdate
	resetIDs []int64
	ack      chan error
}

// New 验证已有规则并重置连续观测窗口；已消费边沿、冷却和配对事件仍保留。
func New(ctx context.Context, db *store.Store, actions Dispatcher, logger *slog.Logger) (*Service, error) {
	if db == nil || actions == nil {
		return nil, errors.New("rule: 缺少必需模块")
	}
	if logger == nil {
		logger = slog.Default()
	}
	rows, err := db.Read().QueryContext(ctx, "SELECT "+Columns+" FROM rules")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		if _, err = Scan(rows); err != nil {
			break
		}
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return nil, err
	}
	if _, err = db.Write().ExecContext(ctx, `UPDATE rule_state SET since=json_remove(since,'$.timers')`); err != nil {
		return nil, err
	}
	s := &Service{db: db, actions: actions, logger: logger, inbox: make(chan input, 256), stop: make(chan struct{}), done: make(chan struct{})}
	go s.run()
	return s, nil
}

// Enqueue 在有界队列满时反压，Close 时解除等待。调用前设备事件必须已落库。
func (s *Service) Enqueue(u model.DeviceUpdate) error {
	return s.enqueue(context.Background(), input{update: model.CloneUpdate(u)})
}

func (s *Service) enqueue(ctx context.Context, in input) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return errors.New("rule: 服务已关闭")
	}
	// 停机仍接受可以立即排入的事实，但队列满时不得卡住上游 actor 的退出。
	select {
	case s.inbox <- in:
		return nil
	default:
	}
	select {
	case s.inbox <- in:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.stop:
		return errors.New("rule: 服务正在关闭")
	}
}

// BeginClose 先解除满队列反压；生产者退出后仍须 Close 关闭输入并等待落库。
func (s *Service) BeginClose() { s.stopOnce.Do(func() { close(s.stop) }) }

// ResetTimers 排在设备最后一个更新之后清除未确认窗口，保留已消费边沿与冷却。
// app 必须先暂停这些设备的生产者，再调用本方法、修改配置并恢复采集。
func (s *Service) ResetTimers(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	in := input{resetIDs: append([]int64(nil), ids...), ack: make(chan error, 1)}
	if err := s.enqueue(ctx, in); err != nil {
		return err
	}
	select {
	case err := <-in.ack:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) processInput(ctx context.Context, in input) error {
	if in.ack == nil {
		return s.Process(ctx, in.update)
	}
	s.processMu.Lock()
	defer s.processMu.Unlock()
	return s.db.WithTx(ctx, func(tx *sql.Tx) error {
		for _, id := range in.resetIDs {
			if _, err := tx.ExecContext(ctx, `UPDATE rule_state SET since=json_remove(since,'$.timers') WHERE device_id=?`, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// Close 在生产者停止后调用，在独立五秒期限内处理已接受输入。
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.BeginClose()
		s.mu.Lock()
		s.closed = true
		close(s.inbox)
		s.mu.Unlock()
		<-s.done
	})
	return s.err
}
func (s *Service) run() {
	defer close(s.done)
	var pending *input
	for {
		select {
		case <-s.stop:
			s.finish(pending)
			return
		case u, ok := <-s.inbox:
			if !ok {
				return
			}
			pending = &u
		}
		for pending != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := s.processInput(ctx, *pending)
			cancel()
			if pending.ack != nil {
				pending.ack <- err
				pending = nil
				break
			}
			if err == nil {
				pending = nil
				break
			}
			s.logger.Error("规则处理失败，将重试存储而不重放已接受动作", "device_id", pending.update.State.DeviceID, "error_type", fmt.Sprintf("%T", err))
			timer := time.NewTimer(time.Second)
			select {
			case <-s.stop:
				timer.Stop()
				s.finish(pending)
				return
			case <-timer.C:
			}
		}
	}
}
func (s *Service) finish(pending *input) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	consume := func(in input) {
		err := s.processInput(ctx, in)
		if in.ack != nil {
			in.ack <- err
		}
		s.err = errors.Join(s.err, err)
	}
	if pending != nil {
		consume(*pending)
	}
	for in := range s.inbox {
		consume(in)
	}
	s.processMu.Lock()
	s.err = errors.Join(s.err, s.flushCompletions(ctx))
	s.processMu.Unlock()
}

type dispatch struct {
	logID    int64
	rule     Record
	decision decision
}

type completion struct {
	logID        int64
	ids          []int64
	kind, reason string
}

func (s *Service) flushCompletions(ctx context.Context) error {
	for len(s.completions) > 0 {
		c := s.completions[0]
		data, err := json.Marshal(c.ids)
		if err != nil {
			return err
		}
		if _, err = s.db.Write().ExecContext(ctx, "UPDATE rule_logs SET action_run_ids=?,kind=?,reason=? WHERE id=?", string(data), c.kind, c.reason, c.logID); err != nil {
			return err
		}
		s.completions = s.completions[1:]
	}
	return nil
}

// Process 同事务提交命中日志和实例状态，成功后才调用执行器。
// 提交后的派发失败不会撤销已消费边沿，也不在重启时重放外部动作。
func (s *Service) Process(ctx context.Context, u model.DeviceUpdate) error {
	s.processMu.Lock()
	defer s.processMu.Unlock()
	if err := s.flushCompletions(ctx); err != nil {
		return err
	}
	var jobs []dispatch
	var name string
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var tagsJSON string
		var enabled bool
		err := tx.QueryRowContext(ctx, "SELECT name,tags,enabled FROM devices WHERE id=?", u.State.DeviceID).Scan(&name, &tagsJSON, &enabled)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		} // 排队期间设备可能已被删除。
		if err != nil {
			return err
		}
		if !enabled {
			return nil
		}
		var tags []string
		if err = json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
			return errors.New("设备标签损坏")
		}
		rows, err := tx.QueryContext(ctx, "SELECT "+Columns+" FROM rules WHERE enabled=1 ORDER BY id")
		if err != nil {
			return err
		}
		var rules []Record
		for rows.Next() {
			r, e := Scan(rows)
			if e != nil {
				err = e
				break
			}
			rules = append(rules, r)
		}
		if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, r := range rules {
			if !r.Scope.matches(u.State.DeviceID, tags) {
				if _, err = tx.ExecContext(ctx, "DELETE FROM rule_state WHERE rule_id=? AND device_id=?", r.ID, u.State.DeviceID); err != nil {
					return err
				}
				continue
			}
			st, err := loadInstance(ctx, tx, r.ID, u.State.DeviceID)
			if err != nil {
				return err
			}
			beforeJSON, err := json.Marshal(st)
			if err != nil {
				return err
			}
			beforeFired, beforeAt := st.Fired, st.FiredAt
			decisions := evaluate(r, &st, u)
			afterJSON, err := json.Marshal(st)
			if err != nil {
				return err
			}
			if string(beforeJSON) != string(afterJSON) || beforeFired != st.Fired || !reflect.DeepEqual(beforeAt, st.FiredAt) {
				if err = saveInstance(ctx, tx, r.ID, u.State.DeviceID, st); err != nil {
					return err
				}
			}
			for _, d := range decisions {
				values, err := json.Marshal(d.Values)
				if err != nil {
					return err
				}
				res, err := tx.ExecContext(ctx, `INSERT INTO rule_logs(rule_id,device_id,at,kind,reason,"values") VALUES(?,?,?,?,?,?)`, r.ID, u.State.DeviceID, u.State.At.UnixMilli(), d.Kind, d.Reason, string(values))
				if err != nil {
					return err
				}
				id, err := res.LastInsertId()
				if err != nil {
					return err
				}
				if len(d.Actions) > 0 {
					jobs = append(jobs, dispatch{id, r, d})
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, job := range jobs {
		ids := []int64{}
		failed := false
		for _, ref := range job.decision.Actions {
			event := job.decision.Event
			if event.Type == "" {
				event.Type = "rule_triggered"
				if job.decision.Recovery {
					event.Type = "rule_recovered"
				}
				event.Severity = model.SeverityInfo
				event.At = u.State.At
				event.DeviceID = u.State.DeviceID
			}
			id, e := s.actions.Submit(ctx, action.Request{ActionID: ref.ActionID, DryRun: job.rule.DryRun, Trigger: action.Trigger{Type: "rule", RuleID: job.rule.ID, EventID: event.ID, Recovery: job.decision.Recovery}, DeviceName: name, State: u.State, Event: event})
			if e != nil {
				failed = true
				s.logger.Error("规则动作未被接受", "rule_id", job.rule.ID, "action_id", ref.ActionID, "error_type", fmt.Sprintf("%T", e))
				continue
			}
			ids = append(ids, id)
		}
		// 所有已提交命中都先派发完；补写失败不能跳过后续动作或丢失关联 ID。
		kind, reason := job.decision.Kind, job.decision.Reason
		if failed {
			kind = "dispatch_failed"
			reason += ";action_not_accepted"
		}
		s.completions = append(s.completions, completion{logID: job.logID, ids: ids, kind: kind, reason: reason})
	}
	return s.flushCompletions(ctx)
}
