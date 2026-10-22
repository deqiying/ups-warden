package plan

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"ups-warden/internal/action"
	"ups-warden/internal/bus"
	"ups-warden/internal/host"
	"ups-warden/internal/model"
	"ups-warden/internal/notify"
	"ups-warden/internal/secret"
	"ups-warden/internal/store"
)

var (
	// ErrFinished 表示运行已结束，不能再取消。
	ErrFinished = errors.New("plan: 运行已结束")
	// ErrClosed 表示服务已关闭，不再接受取消。
	ErrClosed = errors.New("plan: 服务已关闭")
	// errNotWaiting 表示步骤或运行已被取消/结束，write-ahead 条件更新未命中，本次不执行。
	errNotWaiting = errors.New("plan: 步骤已不在等待状态")
)

// run 是活动运行的内存句柄；持久化状态以数据库为准，协程按数据库中的步骤状态推进。
type run struct {
	id        int64
	plan      Record
	device    *int64
	cycle     *int64
	dryRun    bool
	countdown *time.Time
	stop      chan struct{} // 取消或市电恢复后关闭，打断倒计时、阶段延迟和等待离线。
	// 以下字段受 Service.mu 保护。
	deviceName string
	state      model.DeviceState
	reason     string
	restoring  bool
}

// Service 拥有活动运行协程；必须在规则服务之后、动作服务之前 Close，
// 使在途步骤保持 running，重启后记为 unknown 而不是被误记为结束。
type Service struct {
	db      *store.Store
	cipher  *secret.Cipher
	actions *action.Service
	notify  *notify.Service
	bus     *bus.Bus
	logger  *slog.Logger
	// online 与 poll 仅供同包测试替换。
	online func(context.Context, host.Record) (bool, error)
	poll   time.Duration
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	runs   map[int64]*run
	closed bool
	wg     sync.WaitGroup
}

// New 校验已存计划，并恢复未结束的运行：中断时 running 的步骤记为 unknown 且绝不重放；
// 触发周期已结束的运行按市电恢复处理，其余按 on_restart 继续或终止，并发送通知。
func New(ctx context.Context, db *store.Store, cipher *secret.Cipher, actions *action.Service, notifications *notify.Service, b *bus.Bus, logger *slog.Logger) (*Service, error) {
	if ctx == nil || db == nil || cipher == nil || actions == nil || notifications == nil || b == nil || logger == nil {
		return nil, errors.New("plan: 缺少必需依赖")
	}
	rows, err := db.Read().QueryContext(ctx, "SELECT "+Columns+" FROM plans")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		if _, e := Scan(rows); e != nil {
			err = e
			break
		}
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	s := &Service{db: db, cipher: cipher, actions: actions, notify: notifications, bus: b, logger: logger, online: host.CheckOnline, poll: 5 * time.Second, ctx: runCtx, cancel: cancel, runs: map[int64]*run{}}
	if err := s.restore(ctx); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// Validate 检查结构与跨表引用：步骤只能引用 SSH / 脚本 / HTTP 动作，渠道须存在且启用。
// 配置问题返回包装 ErrInvalid 的错误，存储错误原样返回。
func (s *Service) Validate(ctx context.Context, r Record) error {
	if err := Validate(r); err != nil {
		return err
	}
	for _, st := range r.Stages {
		for _, step := range st.Steps {
			var kind string
			err := s.db.Read().QueryRowContext(ctx, "SELECT type FROM actions WHERE id=?", step.ActionID).Scan(&kind)
			if errors.Is(err, sql.ErrNoRows) || err == nil && !stepType(kind) {
				return invalid("步骤需引用已存在的 SSH、脚本或 HTTP 动作")
			}
			if err != nil {
				return err
			}
		}
	}
	for _, id := range r.NotifyChannelIDs {
		var enabled bool
		err := s.db.Read().QueryRowContext(ctx, "SELECT enabled FROM channels WHERE id=?", id).Scan(&enabled)
		if errors.Is(err, sql.ErrNoRows) || err == nil && !enabled {
			return invalid("通知渠道不存在或未启用")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Start 冻结计划快照，并在同一事务中检查单活动运行与停电周期幂等、写入运行和全部 waiting 步骤；
// accepted 与之同事务（手动执行审计）。设备与停电周期取自 req.State；非 dry-run 运行占用周期唯一约束。
// 服务关闭后仍接受已派发的触发（如规则排空时的 plan 动作）：运行照常落库但不启动协程，留给下次启动恢复，避免触发丢失。
func (s *Service) Start(ctx context.Context, planID int64, req action.Request, accepted func(*sql.Tx, int64) error) (int64, error) {
	p, err := Read(ctx, s.db, planID)
	if err != nil {
		return 0, err
	}
	snapshot, err := json.Marshal(p)
	if err != nil {
		return 0, err
	}
	trigger, err := json.Marshal(req.Trigger)
	if err != nil {
		return 0, err
	}
	// SSH 步骤记录受控主机，供等待离线与主机删除保护使用。
	hosts := make([][]*int64, len(p.Stages))
	for i, st := range p.Stages {
		hosts[i] = make([]*int64, len(st.Steps))
		for j, step := range st.Steps {
			a, err := action.Read(ctx, s.db, s.cipher, step.ActionID)
			if err != nil {
				return 0, err
			}
			if a.Type == "ssh" {
				id := a.Config.HostID
				hosts[i][j] = &id
			}
		}
	}
	r := &run{plan: p, dryRun: req.DryRun, deviceName: req.DeviceName, state: model.CloneState(req.State), stop: make(chan struct{})}
	if req.State.DeviceID > 0 {
		id := req.State.DeviceID
		r.device = &id
	}
	if req.State.OutageCycleID != nil {
		id := *req.State.OutageCycleID
		r.cycle = &id
	}
	now := time.Now()
	status := "running"
	var until any
	if p.CountdownS > 0 {
		status = "countdown"
		t := now.Add(time.Duration(p.CountdownS) * time.Second)
		r.countdown = &t
		until = t.UnixMilli()
	}
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var active bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM plan_runs WHERE plan_id=? AND status IN ('countdown','running'))", planID).Scan(&active); err != nil {
			return err
		}
		if active {
			return action.ErrPlanActive
		}
		if r.cycle != nil {
			var ended bool
			if err := tx.QueryRowContext(ctx, "SELECT ended_at IS NOT NULL FROM outage_cycles WHERE id=? AND device_id=?", *r.cycle, *r.device).Scan(&ended); err != nil {
				return err
			}
			if ended {
				return action.ErrPlanCycleEnded
			}
			var executed bool
			if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM plan_runs WHERE plan_id=? AND outage_cycle_id=? AND dry_run=0)", planID, *r.cycle).Scan(&executed); err != nil {
				return err
			}
			if executed && !r.dryRun {
				return action.ErrPlanExecuted
			}
		}
		res, err := tx.ExecContext(ctx, "INSERT INTO plan_runs(plan_id,device_id,outage_cycle_id,dry_run,status,trigger,countdown_until,plan_snapshot,started_at) VALUES(?,?,?,?,?,?,?,?,?)", planID, nullable(r.device), nullable(r.cycle), r.dryRun, status, string(trigger), until, string(snapshot), now.UnixMilli())
		if err != nil {
			return err
		}
		if r.id, err = res.LastInsertId(); err != nil {
			return err
		}
		for i, st := range p.Stages {
			for j, step := range st.Steps {
				if _, err := tx.ExecContext(ctx, "INSERT INTO plan_run_steps(run_id,stage,step,action_id,host_id,status) VALUES(?,?,?,?,?,'waiting')", r.id, i, j, step.ActionID, nullable(hosts[i][j])); err != nil {
					return err
				}
			}
		}
		if accepted != nil {
			return accepted(tx, r.id)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	detail := fmt.Sprintf("计划「%s」运行 #%d 已启动，%d 秒后执行第一阶段", p.Name, r.id, p.CountdownS)
	s.notifyRun(r, "plan_started", model.SeverityWarning, detail)
	s.spawn(r)
	s.publish(r.id)
	return r.id, nil
}

// Cancel 取消活动运行：同一事务把全部 waiting 步骤记为 cancelled 并执行 accepted（审计）。
// 已发出或执行中的步骤不中断，运行在其结果落库后结束。
func (s *Service) Cancel(ctx context.Context, id int64, accepted func(*sql.Tx) error) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return ErrClosed
	}
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		var status string
		if err := tx.QueryRowContext(ctx, "SELECT status FROM plan_runs WHERE id=?", id).Scan(&status); err != nil {
			return err
		}
		if status != "countdown" && status != "running" {
			return ErrFinished
		}
		if _, err := tx.ExecContext(ctx, "UPDATE plan_run_steps SET status='cancelled',ended_at=? WHERE run_id=? AND status='waiting'", time.Now().UnixMilli(), id); err != nil {
			return err
		}
		if accepted != nil {
			return accepted(tx)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// 提交后再取句柄，缩小与 Start 登记之间的窗口；仍未登记的运行步骤已取消，协程醒来后同样结束为 cancelled。
	s.mu.Lock()
	r := s.runs[id]
	s.mu.Unlock()
	if r != nil {
		s.stop(r, "用户取消")
	}
	s.publish(id)
	return nil
}

// Observe 接收状态 actor 已落库的更新：刷新运行使用的设备快照，并在触发周期结束（去抖后的市电恢复）时处理取消。
// 在 actor 协程中调用，只做内存操作，数据库写入交给受管协程。
func (s *Service) Observe(u model.DeviceUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	for _, r := range s.runs {
		if r.device == nil || *r.device != u.State.DeviceID {
			continue
		}
		r.state = u.State
		if r.cycle == nil || r.restoring || u.State.OutageCycleID != nil && *u.State.OutageCycleID == *r.cycle {
			continue
		}
		r.restoring = true
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.mainsRestored(r)
		}()
	}
}

// Close 停止全部运行协程；在途步骤保持 running，未开始的步骤保持 waiting，留给下次启动恢复。
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Service) restore(ctx context.Context) error {
	rows, err := s.db.Read().QueryContext(ctx, "SELECT id,device_id,outage_cycle_id,dry_run,status,countdown_until,plan_snapshot FROM plan_runs WHERE status IN ('countdown','running') ORDER BY id")
	if err != nil {
		return err
	}
	var runs []*run
	for rows.Next() {
		r := &run{stop: make(chan struct{})}
		var status, snapshot string
		var until sql.NullInt64
		if err = rows.Scan(&r.id, &r.device, &r.cycle, &r.dryRun, &status, &until, &snapshot); err != nil {
			break
		}
		if err = strictJSON([]byte(snapshot), &r.plan); err == nil {
			err = Validate(r.plan)
		}
		if err != nil {
			err = fmt.Errorf("plan: 运行 #%d 的计划快照无效: %w", r.id, err)
			break
		}
		if status == "countdown" {
			t := time.UnixMilli(until.Int64)
			r.countdown = &t
		}
		runs = append(runs, r)
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, r := range runs {
		// write-ahead 后未落结果的步骤可能已经执行，记为 unknown，宁可少执行也不重放。
		if _, err := s.db.Write().ExecContext(ctx, "UPDATE plan_run_steps SET status='unknown',ended_at=?,result=json_set(result,'$.error','服务中断，结果未知；未自动重放') WHERE run_id=? AND status='running'", time.Now().UnixMilli(), r.id); err != nil {
			return err
		}
		ended := false
		if r.device != nil {
			r.state.DeviceID = *r.device
			if err := s.db.Read().QueryRowContext(ctx, "SELECT name FROM devices WHERE id=?", *r.device).Scan(&r.deviceName); err != nil {
				return err
			}
		}
		if r.cycle != nil {
			if err := s.db.Read().QueryRowContext(ctx, "SELECT ended_at IS NOT NULL FROM outage_cycles WHERE id=?", *r.cycle).Scan(&ended); err != nil {
				return err
			}
		}
		// 周期已结束等同于市电恢复：倒计时中或 cancel_pending 时取消；continue 时与在线行为一致，按 on_restart 处理。
		switch {
		case ended && (r.countdown != nil || r.plan.OnMainsRestored == "cancel_pending"):
			r.reason = "重启时市电已恢复"
			if err := s.finish(ctx, r, ""); err != nil {
				return err
			}
		case r.plan.OnRestart == "abort":
			if err := s.finish(ctx, r, "aborted"); err != nil {
				return err
			}
		default:
			s.notifyRun(r, "plan_resumed", model.SeverityWarning, fmt.Sprintf("计划「%s」运行 #%d 在服务重启后继续；中断时执行中的步骤记为结果未知，不会重放", r.plan.Name, r.id))
			s.spawn(r)
		}
	}
	return nil
}

// spawn 登记并启动运行协程；服务已关闭时不启动，已提交的运行留给下次启动恢复。
func (s *Service) spawn(r *run) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.runs[r.id] = r
	s.wg.Add(1)
	go s.drive(r)
}

// drive 按数据库中的步骤状态推进运行，重启后从第一个未开始的步骤继续。
// 服务关闭时直接返回，不写终态。
func (s *Service) drive(r *run) {
	defer func() {
		s.mu.Lock()
		delete(s.runs, r.id)
		s.mu.Unlock()
		s.wg.Done()
	}()
	if r.countdown != nil {
		if !s.sleep(r, time.Until(*r.countdown)) {
			if s.ctx.Err() == nil {
				s.retry("finish", r.id, func(ctx context.Context) error { return s.finish(ctx, r, "") })
			}
			return
		}
		if !s.retry("countdown_end", r.id, func(ctx context.Context) error {
			_, err := s.db.Write().ExecContext(ctx, "UPDATE plan_runs SET status='running' WHERE id=? AND status='countdown'", r.id)
			return err
		}) {
			return
		}
		s.publish(r.id)
	}
	for i, st := range r.plan.Stages {
		if s.stopped(r) {
			break
		}
		var steps []model.PlanStep
		if !s.retry("stage", r.id, func(ctx context.Context) (err error) {
			if _, err = s.db.Write().ExecContext(ctx, "UPDATE plan_runs SET current_stage=? WHERE id=? AND status='running'", i, r.id); err != nil {
				return err
			}
			steps, err = readSteps(ctx, s.db, r.id, i)
			return err
		}) {
			return
		}
		s.publish(r.id)
		// 阶段已部分执行（重启恢复）时不重复延迟。
		waiting := true
		for _, step := range steps {
			waiting = waiting && step.Status == "waiting"
		}
		if waiting && st.DelayS > 0 && !s.sleep(r, time.Duration(st.DelayS)*time.Second) {
			if s.ctx.Err() != nil {
				return
			}
			break
		}
		if st.SelfShutdown {
			s.selfShutdown(r, st, steps[0])
			return
		}
		var wg sync.WaitGroup
		for _, step := range steps {
			if step.Status == "waiting" {
				wg.Go(func() { s.runStep(r, st, step) })
			}
		}
		wg.Wait()
		if st.OfflineWaitS > 0 && !r.dryRun {
			s.waitOffline(r, i, st)
		}
		if s.ctx.Err() != nil {
			return
		}
	}
	s.retry("finish", r.id, func(ctx context.Context) error { return s.finish(ctx, r, "") })
}

// request 构造步骤动作请求；步骤超时覆盖动作自身超时，dry-run 随运行。
func (s *Service) request(r *run, st Stage, step model.PlanStep) action.Request {
	s.mu.Lock()
	state, name := r.state, r.deviceName
	s.mu.Unlock()
	return action.Request{ActionID: step.ActionID, DryRun: r.dryRun, TimeoutS: st.StepTimeoutS, Trigger: action.Trigger{Type: "plan", PlanRunID: r.id}, DeviceName: name, State: state, Event: model.Event{DeviceID: state.DeviceID, Type: "plan_step", Severity: model.SeverityWarning, At: time.Now()}}
}

// stepAction 在执行前复查动作仍为可用于步骤的类型；计划保存后动作可能被修改。
func (s *Service) stepAction(r *run, step model.PlanStep) bool {
	a, err := action.Read(s.ctx, s.db, s.cipher, step.ActionID)
	if err == nil && stepType(a.Type) {
		return true
	}
	if s.ctx.Err() == nil {
		s.failWaiting(r, step, "动作不存在、配置无效或类型不再适用")
	}
	return false
}

// runStep 先在动作 write-ahead 事务中把步骤条件更新为 running，再执行动作；未命中说明已被取消，不执行。
func (s *Service) runStep(r *run, st Stage, step model.PlanStep) {
	if !s.stepAction(r, step) {
		return
	}
	runID, err := s.actions.SubmitWithAudit(s.ctx, s.request(r, st, step), func(tx *sql.Tx, runID int64) error {
		data, err := json.Marshal(model.PlanStepResult{ActionRunID: runID})
		if err != nil {
			return err
		}
		return claim(s.ctx, tx, "UPDATE plan_run_steps SET status='running',started_at=?,result=? WHERE run_id=? AND stage=? AND step=? AND status='waiting'", time.Now().UnixMilli(), string(data), r.id, step.Stage, step.Step)
	})
	switch {
	case errors.Is(err, errNotWaiting), errors.Is(err, action.ErrClosed), s.ctx.Err() != nil:
		// 已取消；或服务关闭，步骤保持 waiting 留给重启恢复。
		return
	case err != nil:
		s.failWaiting(r, step, "动作提交失败")
		return
	}
	s.publish(r.id)
	var status string
	// 服务关闭时停止等待，步骤保持 running，重启后记为 unknown。
	if !s.retry("wait_step", r.id, func(ctx context.Context) (err error) { status, err = s.actions.Wait(ctx, runID); return err }) {
		return
	}
	result := model.PlanStepResult{ActionRunID: runID, ActionStatus: status}
	next := "sent"
	if !succeeded(status) {
		next = "failed"
		result.Error = "动作结果：" + status + "，详情见 action-runs"
	}
	data, err := json.Marshal(result)
	if err != nil {
		s.logger.Error("计划步骤结果编码失败", "run_id", r.id, "error_type", fmt.Sprintf("%T", err))
		return
	}
	if !s.retry("step_result", r.id, func(ctx context.Context) error {
		_, err := s.db.Write().ExecContext(ctx, "UPDATE plan_run_steps SET status=?,ended_at=?,result=? WHERE run_id=? AND stage=? AND step=? AND status='running'", next, time.Now().UnixMilli(), string(data), r.id, step.Stage, step.Step)
		return err
	}) {
		return
	}
	if next == "failed" {
		s.notifyStep(r, step, result.Error)
	}
	s.publish(r.id)
}

// selfShutdown 关闭本机：先在同一事务中把步骤记为 sent、运行记为结束，再执行动作，
// 本机重启后不会继续该计划；动作失败时补记失败与 completed_with_errors。
func (s *Service) selfShutdown(r *run, st Stage, step model.PlanStep) {
	finish := func() { s.retry("finish", r.id, func(ctx context.Context) error { return s.finish(ctx, r, "") }) }
	if !s.stepAction(r, step) {
		if s.ctx.Err() == nil {
			finish()
		}
		return
	}
	var status string
	runID, err := s.actions.SubmitWithAudit(s.ctx, s.request(r, st, step), func(tx *sql.Tx, runID int64) error {
		data, err := json.Marshal(model.PlanStepResult{ActionRunID: runID})
		if err != nil {
			return err
		}
		now := time.Now().UnixMilli()
		if err := claim(s.ctx, tx, "UPDATE plan_run_steps SET status='sent',started_at=?,result=? WHERE run_id=? AND stage=? AND step=? AND status='waiting'", now, string(data), r.id, step.Stage, step.Step); err != nil {
			return err
		}
		if status, err = derive(s.ctx, tx, r.id); err != nil {
			return err
		}
		return claim(s.ctx, tx, "UPDATE plan_runs SET status=?,ended_at=? WHERE id=? AND status='running'", status, now, r.id)
	})
	switch {
	case errors.Is(err, action.ErrClosed), s.ctx.Err() != nil:
		return
	case errors.Is(err, errNotWaiting):
		finish()
		return
	case err != nil:
		s.failWaiting(r, step, "动作提交失败")
		finish()
		return
	}
	s.notifyEnd(r, status)
	s.publish(r.id)
	var outcome string
	if !s.retry("wait_step", r.id, func(ctx context.Context) (err error) { outcome, err = s.actions.Wait(ctx, runID); return err }) {
		return
	}
	result := model.PlanStepResult{ActionRunID: runID, ActionStatus: outcome}
	failed := !succeeded(outcome)
	if failed {
		result.Error = "动作结果：" + outcome + "，详情见 action-runs"
	}
	data, err := json.Marshal(result)
	if err != nil {
		s.logger.Error("计划步骤结果编码失败", "run_id", r.id, "error_type", fmt.Sprintf("%T", err))
		return
	}
	if !s.retry("self_shutdown_result", r.id, func(ctx context.Context) error {
		return s.db.WithTx(ctx, func(tx *sql.Tx) error {
			next := "sent"
			if failed {
				next = "failed"
				if _, err := tx.ExecContext(ctx, "UPDATE plan_runs SET status='completed_with_errors' WHERE id=? AND status='completed'", r.id); err != nil {
					return err
				}
			}
			_, err := tx.ExecContext(ctx, "UPDATE plan_run_steps SET status=?,ended_at=?,result=? WHERE run_id=? AND stage=? AND step=? AND status='sent'", next, time.Now().UnixMilli(), string(data), r.id, step.Stage, step.Step)
			return err
		})
	}) {
		return
	}
	if failed {
		s.notifyStep(r, step, result.Error)
	}
	s.publish(r.id)
}

// failWaiting 把尚未开始的步骤记为失败（不执行动作）；已被取消的步骤不覆盖。
func (s *Service) failWaiting(r *run, step model.PlanStep, message string) {
	data, err := json.Marshal(model.PlanStepResult{Error: message})
	if err != nil {
		s.logger.Error("计划步骤结果编码失败", "run_id", r.id, "error_type", fmt.Sprintf("%T", err))
		return
	}
	var claimed bool
	if !s.retry("step_failed", r.id, func(ctx context.Context) error {
		now := time.Now().UnixMilli()
		res, err := s.db.Write().ExecContext(ctx, "UPDATE plan_run_steps SET status='failed',started_at=?,ended_at=?,result=? WHERE run_id=? AND stage=? AND step=? AND status='waiting'", now, now, string(data), r.id, step.Stage, step.Step)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		claimed = n == 1
		return err
	}) || !claimed {
		return
	}
	s.notifyStep(r, step, message)
	s.publish(r.id)
}

// waitOffline 对本阶段已发送的 SSH 步骤并行做在线检测，直到离线或 offline_wait_s 超时。
func (s *Service) waitOffline(r *run, stage int, st Stage) {
	var steps []model.PlanStep
	if !s.retry("load_steps", r.id, func(ctx context.Context) (err error) { steps, err = readSteps(ctx, s.db, r.id, stage); return err }) {
		return
	}
	deadline := time.Now().Add(time.Duration(st.OfflineWaitS) * time.Second)
	var wg sync.WaitGroup
	for _, step := range steps {
		if step.Status == "sent" && step.HostID != nil && !step.Result.OfflineTimeout {
			wg.Go(func() { s.waitHost(r, step, deadline) })
		}
	}
	wg.Wait()
}

func (s *Service) waitHost(r *run, step model.PlanStep, deadline time.Time) {
	message := "等待离线超时"
	h, err := host.Read(s.ctx, s.db, s.cipher, *step.HostID)
	if err != nil {
		message = "受控主机加载失败，无法确认离线"
	}
	for err == nil {
		online, e := s.online(s.ctx, h)
		if e == nil && !online {
			if s.retry("step_offline", r.id, func(ctx context.Context) error {
				_, err := s.db.Write().ExecContext(ctx, "UPDATE plan_run_steps SET status='offline' WHERE run_id=? AND stage=? AND step=? AND status='sent'", r.id, step.Stage, step.Step)
				return err
			}) {
				s.publish(r.id)
			}
			return
		}
		if e != nil {
			// 检测错误（如 ICMP 权限）不等于离线，继续检测直到超时，并在超时原因中说明。
			message = "等待离线超时（在线检测失败：" + e.Error() + "）"
		}
		if !time.Now().Before(deadline) {
			break
		}
		if !s.sleep(r, min(s.poll, time.Until(deadline))) {
			return
		}
	}
	if s.ctx.Err() != nil || s.stopped(r) {
		return
	}
	// 命令已发出，步骤保持 sent；标记超时并通知，不阻塞后续阶段。
	step.Result.OfflineTimeout = true
	data, err := json.Marshal(step.Result)
	if err != nil {
		s.logger.Error("计划步骤结果编码失败", "run_id", r.id, "error_type", fmt.Sprintf("%T", err))
		return
	}
	if !s.retry("offline_timeout", r.id, func(ctx context.Context) error {
		_, err := s.db.Write().ExecContext(ctx, "UPDATE plan_run_steps SET result=? WHERE run_id=? AND stage=? AND step=? AND status='sent'", string(data), r.id, step.Stage, step.Step)
		return err
	}) {
		return
	}
	s.notifyStep(r, step, message)
	s.publish(r.id)
}

// mainsRestored 处理触发周期结束：倒计时期间总是取消；执行中按 on_mains_restored，已发出的命令不撤回。
func (s *Service) mainsRestored(r *run) {
	var cancelled bool
	if !s.retry("mains_restored", r.id, func(ctx context.Context) error {
		return s.db.WithTx(ctx, func(tx *sql.Tx) error {
			var status string
			if err := tx.QueryRowContext(ctx, "SELECT status FROM plan_runs WHERE id=?", r.id).Scan(&status); err != nil {
				return err
			}
			cancelled = status == "countdown" || status == "running" && r.plan.OnMainsRestored == "cancel_pending"
			if !cancelled {
				return nil
			}
			_, err := tx.ExecContext(ctx, "UPDATE plan_run_steps SET status='cancelled',ended_at=? WHERE run_id=? AND status='waiting'", time.Now().UnixMilli(), r.id)
			return err
		})
	}) || !cancelled {
		return
	}
	s.stop(r, "市电已恢复")
	s.publish(r.id)
}

// finish 结束运行：剩余 waiting 步骤记为 cancelled；explicit 为空时按步骤结果推导终态。
// 条件更新保证只有完成状态转换的调用方发送通知。
func (s *Service) finish(ctx context.Context, r *run, explicit string) error {
	status := explicit
	var changed bool
	err := s.db.WithTx(ctx, func(tx *sql.Tx) error {
		now := time.Now().UnixMilli()
		if _, err := tx.ExecContext(ctx, "UPDATE plan_run_steps SET status='cancelled',ended_at=? WHERE run_id=? AND status='waiting'", now, r.id); err != nil {
			return err
		}
		if explicit == "" {
			var err error
			if status, err = derive(ctx, tx, r.id); err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx, "UPDATE plan_runs SET status=?,ended_at=? WHERE id=? AND status IN ('countdown','running')", status, now, r.id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		changed = n == 1
		return err
	})
	if err != nil || !changed {
		return err
	}
	s.notifyEnd(r, status)
	s.publish(r.id)
	return nil
}

// derive 按步骤推导终态：有取消即 cancelled；有失败、结果未知或离线超时即 completed_with_errors。
func derive(ctx context.Context, tx *sql.Tx, runID int64) (string, error) {
	var cancelled, failed int
	err := tx.QueryRowContext(ctx, "SELECT COALESCE(SUM(status='cancelled'),0),COALESCE(SUM(status IN ('failed','unknown') OR json_extract(result,'$.offline_timeout')=1),0) FROM plan_run_steps WHERE run_id=?", runID).Scan(&cancelled, &failed)
	switch {
	case err != nil:
		return "", err
	case cancelled > 0:
		return "cancelled", nil
	case failed > 0:
		return "completed_with_errors", nil
	}
	return "completed", nil
}

func (s *Service) notifyEnd(r *run, status string) {
	prefix := fmt.Sprintf("计划「%s」运行 #%d ", r.plan.Name, r.id)
	switch status {
	case "completed":
		s.notifyRun(r, "plan_completed", model.SeverityInfo, prefix+"已完成")
	case "completed_with_errors":
		s.notifyRun(r, "plan_completed", model.SeverityWarning, prefix+"已结束，部分步骤失败、离线超时或结果未知")
	case "cancelled":
		s.mu.Lock()
		reason := r.reason
		s.mu.Unlock()
		if reason == "" {
			reason = "未开始的步骤已取消"
		}
		s.notifyRun(r, "plan_cancelled", model.SeverityInfo, prefix+"已取消："+reason+"；已发出的命令不可撤回")
	case "aborted":
		s.notifyRun(r, "plan_aborted", model.SeverityWarning, prefix+"在服务重启后按配置终止，剩余步骤未执行")
	}
}

func (s *Service) notifyStep(r *run, step model.PlanStep, message string) {
	s.notifyRun(r, "plan_step_failed", model.SeverityCritical, fmt.Sprintf("计划「%s」运行 #%d 阶段 %d 步骤 %d（动作 #%d）：%s", r.plan.Name, r.id, step.Stage+1, step.Step+1, step.ActionID, message))
}

// notifyRun 向计划配置的渠道发送运行通知；dry-run 不外发。入队失败只记录日志，不影响运行。
func (s *Service) notifyRun(r *run, event string, severity model.Severity, detail string) {
	if r.dryRun {
		return
	}
	s.mu.Lock()
	state, name := r.state, r.deviceName
	s.mu.Unlock()
	for _, id := range r.plan.NotifyChannelIDs {
		req := notify.Request{DeviceName: name, State: state, Event: model.Event{DeviceID: state.DeviceID, Type: event, Severity: severity, At: time.Now()}, Detail: detail}
		if err := s.notify.Enqueue(s.ctx, id, req); err != nil {
			s.logger.Warn("关机计划通知未入队", "run_id", r.id, "channel_id", id, "event_type", event, "error_type", fmt.Sprintf("%T", err))
		}
	}
}

// publish 在状态落库后读取完整运行视图并经总线推送 plan_progress。
func (s *Service) publish(id int64) {
	v, err := ReadRun(s.ctx, s.db, id)
	if err != nil {
		if s.ctx.Err() == nil {
			s.logger.Error("读取计划运行进度失败", "run_id", id, "error_type", fmt.Sprintf("%T", err))
		}
		return
	}
	s.bus.Publish(model.DeviceUpdate{PlanProgress: &v})
}

func (s *Service) stop(r *run, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.reason == "" {
		r.reason = reason
	}
	select {
	case <-r.stop:
	default:
		close(r.stop)
	}
}

func (s *Service) stopped(r *run) bool {
	select {
	case <-r.stop:
		return true
	default:
		return false
	}
}

// sleep 等待 d；被取消或服务关闭时返回 false。
func (s *Service) sleep(r *run, d time.Duration) bool {
	t := time.NewTimer(max(d, 0))
	defer t.Stop()
	select {
	case <-r.stop:
		return false
	case <-s.ctx.Done():
		return false
	case <-t.C:
	}
	return !s.stopped(r) && s.ctx.Err() == nil
}

// retry 重试存储操作直到成功或服务关闭：计划状态必须落库后才能推进，不能跳过失败继续执行。
func (s *Service) retry(op string, runID int64, fn func(context.Context) error) bool {
	for {
		err := fn(s.ctx)
		if err == nil {
			return true
		}
		if s.ctx.Err() != nil {
			return false
		}
		s.logger.Error("关机计划存储操作失败，1 秒后重试", "op", op, "run_id", runID, "error_type", fmt.Sprintf("%T", err))
		select {
		case <-time.After(time.Second):
		case <-s.ctx.Done():
			return false
		}
	}
}

func claim(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		err = errNotWaiting
	}
	return err
}

func stepType(kind string) bool { return kind == "ssh" || kind == "script" || kind == "http" }

// succeeded 把动作成功类终态映射为步骤 sent；HTTP/脚本 succeeded、SSH success/sent、dry-run 预览均算已发送。
func succeeded(status string) bool {
	switch status {
	case "sent", "success", "succeeded", "dry_run":
		return true
	}
	return false
}

func nullable(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}
