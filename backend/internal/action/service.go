package action

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"ups-warden/internal/host"
	"ups-warden/internal/model"
	"ups-warden/internal/notify"
	"ups-warden/internal/secret"
	"ups-warden/internal/store"
)

var (
	// ErrBusy 表示动作正在执行，配置变更必须返回冲突而非取消旧执行。
	ErrBusy = errors.New("action: 动作正在执行")
	// ErrClosed 表示服务已停止接受任务。
	ErrClosed = errors.New("action: 服务已关闭")
	// ErrPlanActive、ErrPlanExecuted 与 ErrPlanCycleEnded 由计划服务返回，plan 动作据此记录为跳过而非失败。
	ErrPlanActive     = errors.New("action: 关机计划已有活动运行")
	ErrPlanExecuted   = errors.New("action: 关机计划已在本停电周期执行")
	ErrPlanCycleEnded = errors.New("action: 触发时的停电周期已结束")
)

// Trigger 记录触发来源，不包含配置或凭据。
type Trigger struct {
	Type      string `json:"type"`
	RuleID    int64  `json:"rule_id,omitempty"`
	EventID   int64  `json:"event_id,omitempty"`
	UserID    int64  `json:"user_id,omitempty"`
	PlanRunID int64  `json:"plan_run_id,omitempty"`
	Recovery  bool   `json:"recovery,omitempty"`
}

// Request 只携带动作 ID 和统一设备模型。TimeoutS>0 时覆盖动作自身超时（计划步骤超时）。
type Request struct {
	ActionID   int64             `json:"action_id"`
	DryRun     bool              `json:"dry_run"`
	TimeoutS   int               `json:"timeout_s,omitempty"`
	Trigger    Trigger           `json:"trigger"`
	DeviceName string            `json:"device_name"`
	State      model.DeviceState `json:"state"`
	Event      model.Event       `json:"event"`
}

// PlanStarter 由关机计划服务实现；plan 动作只负责启动运行，阶段编排由计划服务负责。
// accepted 与运行记录同事务执行，返回错误时不创建运行。
type PlanStarter interface {
	Start(ctx context.Context, planID int64, req Request, accepted func(*sql.Tx, int64) error) (int64, error)
}

// Service 拥有在途任务；必须在通知服务与数据库之前 Close。
type Service struct {
	db            *store.Store
	cipher        *secret.Cipher
	notifications *notify.Service
	plans         PlanStarter
	scriptDir     string
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.Mutex // Submit 与配置事务共用，保证 Busy 检查和变更之间没有竞态。
	busy          map[int64]bool
	done          map[int64]chan struct{} // 在途执行记录 ID → 结果落库后关闭。
	closed        bool
	wg            sync.WaitGroup
	closeErr      error
}

// New 校验已有配置并将遗留 running 标为 unknown；绝不自动重放。
func New(ctx context.Context, db *store.Store, cipher *secret.Cipher, notifications *notify.Service, scriptDir string) (*Service, error) {
	if ctx == nil || db == nil || cipher == nil || notifications == nil || scriptDir == "" {
		return nil, errors.New("action: 缺少必需依赖或脚本目录")
	}
	rows, err := db.Read().QueryContext(ctx, "SELECT "+Columns+" FROM actions")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		if _, e := Scan(rows, cipher); e != nil {
			err = e
			break
		}
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return nil, err
	}
	if _, err = db.Write().ExecContext(ctx, "UPDATE action_runs SET status='unknown',ended_at=?,error='服务中断，结果未知；未自动重放' WHERE status='running'", time.Now().UnixMilli()); err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	return &Service{db: db, cipher: cipher, notifications: notifications, scriptDir: scriptDir, ctx: runCtx, cancel: cancel, busy: map[int64]bool{}, done: map[int64]chan struct{}{}}, nil
}

// SetPlans 注入计划服务；计划服务依赖本服务执行步骤，因此只能在两者构造后、开始接受触发前调用一次。
func (s *Service) SetPlans(p PlanStarter) { s.plans = p }

// Change 在与 Submit 相同的锁下执行配置事务；运行中的动作不允许修改或删除。
func (s *Service) Change(ctx context.Context, id int64, apply func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return ErrClosed
	}
	if s.busy[id] {
		return ErrBusy
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return apply()
}

// Validate 检查保存时的真实安全边界（引用、脚本实路径和 HTTP 主机护栏）。
func (s *Service) Validate(ctx context.Context, r Record) error {
	if err := Validate(r); err != nil {
		return err
	}
	if err := s.references(ctx, r); err != nil {
		return err
	}
	switch r.Type {
	case "script":
		_, err := scriptPath(s.scriptDir, r.Config.Script)
		return err
	case "http":
		_, err := s.httpTargets(ctx, r.Credentials.URL.Reveal())
		return err
	}
	return nil
}
func (s *Service) references(ctx context.Context, r Record) error {
	for _, id := range r.Config.ChannelIDs {
		var enabled bool
		if err := s.db.Read().QueryRowContext(ctx, "SELECT enabled FROM channels WHERE id=?", id).Scan(&enabled); err != nil || !enabled {
			return errors.New("action: 通知渠道不存在或未启用")
		}
	}
	if r.Type == "ssh" {
		if _, err := host.Read(ctx, s.db, s.cipher, r.Config.HostID); err != nil {
			return errors.New("action: 受控主机不存在或配置无效")
		}
	}
	if r.Type == "plan" {
		var exists int
		if err := s.db.Read().QueryRowContext(ctx, "SELECT 1 FROM plans WHERE id=?", r.Config.PlanID).Scan(&exists); err != nil {
			return errors.New("action: 关机计划不存在")
		}
	}
	return nil
}

// Submit 先提交 write-ahead 日志再异步执行；返回的 run ID 不代表执行成功。
// 同动作重入和超过 32 个在途任务都持久化跳过结果，不排无界队列。
func (s *Service) Submit(ctx context.Context, req Request) (int64, error) {
	return s.SubmitWithAudit(ctx, req, nil)
}

// SubmitWithAudit 将调用方的接受记录（手动审计或计划步骤 write-ahead）与动作 write-ahead 日志放在同一事务中；事务失败不执行。
func (s *Service) SubmitWithAudit(ctx context.Context, req Request, accepted func(*sql.Tx, int64) error) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return 0, ErrClosed
	}
	if req.ActionID <= 0 || (req.Trigger.Type != "manual" && req.Trigger.Type != "rule" && req.Trigger.Type != "plan") || req.Trigger.RuleID < 0 || req.Trigger.UserID < 0 || req.Trigger.EventID < 0 || req.Trigger.PlanRunID < 0 || req.TimeoutS < 0 || req.TimeoutS > 3600 {
		return 0, errors.New("action: 触发请求无效")
	}
	r, err := Read(ctx, s.db, s.cipher, req.ActionID)
	if err != nil {
		return 0, err
	}
	trigger, err := json.Marshal(req.Trigger)
	if err != nil {
		return 0, err
	}
	status := "running"
	var ended any
	if s.busy[r.ID] {
		status = "skipped_concurrent"
	} else if len(s.busy) >= 32 {
		status = "skipped_capacity"
	}
	if status != "running" {
		ended = time.Now().UnixMilli()
	}
	var id int64
	err = s.db.WithTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, "INSERT INTO action_runs(action_id,trigger,dry_run,status,started_at,ended_at) VALUES(?,?,?,?,?,?)", r.ID, string(trigger), req.DryRun, status, time.Now().UnixMilli(), ended)
		if err != nil {
			return err
		}
		id, err = result.LastInsertId()
		if err != nil {
			return err
		}
		if accepted != nil {
			return accepted(tx, id)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if status != "running" {
		return id, nil
	}
	// 调用方随后可继续更新快照，任务保留提交时字段副本。
	req.State = model.CloneState(req.State)
	req.Event.Summary = model.CloneFields(req.Event.Summary)
	s.busy[r.ID] = true
	s.done[id] = make(chan struct{})
	s.wg.Add(1)
	go s.run(id, r, req)
	return id, nil
}

// Wait 等待执行记录结束并返回最终状态；ctx 结束只停止等待，不取消动作本身。
// 跳过或已结束的记录直接返回数据库中的状态。
func (s *Service) Wait(ctx context.Context, id int64) (string, error) {
	s.mu.Lock()
	done := s.done[id]
	s.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	var status string
	err := s.db.Read().QueryRowContext(ctx, "SELECT status FROM action_runs WHERE id=?", id).Scan(&status)
	return status, err
}

type result struct {
	status                  string
	code                    *int
	stdout, stderr, message string
}

func (s *Service) run(id int64, r Record, req Request) {
	out := result{status: "failed"}
	defer func() {
		if recover() != nil {
			out = result{status: "failed", message: "动作执行 panic，已隔离"}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := s.db.Write().ExecContext(ctx, "UPDATE action_runs SET status=?,ended_at=?,exit_code=?,stdout=?,stderr=?,error=? WHERE id=?", out.status, time.Now().UnixMilli(), out.code, first4K(out.stdout), first4K(out.stderr), first4K(out.message), id)
		s.mu.Lock()
		delete(s.busy, r.ID)
		close(s.done[id])
		delete(s.done, id)
		s.closeErr = errors.Join(s.closeErr, err)
		s.mu.Unlock()
		s.wg.Done()
	}()
	timeout := time.Duration(r.TimeoutS) * time.Second
	if req.TimeoutS > 0 {
		timeout = time.Duration(req.TimeoutS) * time.Second
	}
	ctx, cancel := context.WithTimeout(s.ctx, timeout)
	defer cancel()
	defer func() {
		if !req.DryRun && ctx.Err() != nil {
			out.status = "cancelled"
			out.message = "动作已取消"
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				out.status = "timeout"
				out.message = "动作超时"
			}
		}
	}()
	// plan 动作的 dry-run 由计划服务创建演练运行，步骤同样只做 dry-run。
	if req.DryRun && r.Type != "plan" {
		out.status = "dry_run"
		out.stdout = preview(r, req)
		return
	}
	if err := ctx.Err(); err != nil {
		out.message = "动作已取消"
		out.status = "cancelled"
		return
	}
	if err := s.references(ctx, r); err != nil {
		out.message = err.Error()
		return
	}
	switch r.Type {
	case "notify":
		for _, channelID := range r.Config.ChannelIDs {
			err := s.notifications.Enqueue(ctx, channelID, notify.Request{DeviceName: req.DeviceName, State: req.State, Event: req.Event})
			if err != nil {
				out.message = "通知未全部入队，请检查渠道状态和通知日志"
				return
			}
		}
		out.status = "queued"
		out.stdout = "通知已入队，不表示发送成功；结果见 notify_logs"
	case "http":
		out = s.executeHTTP(ctx, r, req)
	case "script":
		out = executeScript(ctx, s.scriptDir, r.Config, req)
	case "ssh":
		h, err := host.Read(ctx, s.db, s.cipher, r.Config.HostID)
		if err != nil {
			out.message = "受控主机加载失败"
			return
		}
		v, err := host.Execute(ctx, h, r.Config.Command, r.Config.Shutdown)
		out = result{status: v.Status, code: v.ExitCode, stdout: v.Stdout, stderr: v.Stderr}
		if err != nil {
			out.status = "failed"
			out.message = "SSH 执行失败，请检查主机配置或指纹"
		}
	case "plan":
		runID, err := s.plans.Start(ctx, r.Config.PlanID, req, nil)
		switch {
		case errors.Is(err, ErrPlanActive):
			out = result{status: "skipped_active", message: "关机计划已有活动运行，本次触发已跳过"}
		case errors.Is(err, ErrPlanExecuted):
			out = result{status: "skipped_executed", message: "关机计划已在本停电周期执行，本次触发已跳过"}
		case errors.Is(err, ErrPlanCycleEnded):
			out = result{status: "skipped_restored", message: "停电周期已结束，本次触发已跳过"}
		case err != nil:
			out.message = "关机计划启动失败"
		default:
			out = result{status: "started", stdout: fmt.Sprintf("已启动计划运行 #%d；进度见 plan-runs", runID)}
		}
	}
}
func preview(r Record, req Request) string {
	switch r.Type {
	case "notify":
		return fmt.Sprintf("将通知渠道 %v；dry-run 未入队", r.Config.ChannelIDs)
	case "http":
		if _, err := renderBody(r, req); err != nil {
			return "HTTP JSON 模板渲染失败；dry-run 未外发"
		}
		return fmt.Sprintf("将执行 HTTP %s；URL、Header、Body 已脱敏；dry-run 未解析 DNS 或外发", r.Config.Method)
	case "script":
		return fmt.Sprintf("将执行目录内脚本 %s（%d 个参数）；dry-run 未启动进程", r.Config.Script, len(r.Config.Args))
	case "ssh":
		return fmt.Sprintf("将向主机 %d 执行已配置命令；dry-run 未连接", r.Config.HostID)
	}
	return ""
}
func first4K(s string) string {
	if len(s) > 4096 {
		return s[:4096]
	}
	return s
}

// ScriptDir 返回配置的只读脚本目录，API 仅用于列出可选文件。
func (s *Service) ScriptDir() string { return s.scriptDir }

// Close 取消并等待全部在途执行与结果持久化；写日志失败通过返回值报告。
func (s *Service) Close() error {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeErr
}
