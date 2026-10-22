package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"ups-warden/internal/action"
	"ups-warden/internal/audit"
	"ups-warden/internal/model"
	"ups-warden/internal/plan"
)

func (s *Server) planRoutes() {
	s.mux.HandleFunc("GET /api/v1/plans", s.plans)
	s.mux.HandleFunc("POST /api/v1/plans", s.createPlan)
	s.mux.HandleFunc("GET /api/v1/plans/{id}", s.getPlan)
	s.mux.HandleFunc("PUT /api/v1/plans/{id}", s.updatePlan)
	s.mux.HandleFunc("DELETE /api/v1/plans/{id}", s.deletePlan)
	s.mux.HandleFunc("POST /api/v1/plans/{id}/run", s.runPlan)
	s.mux.HandleFunc("GET /api/v1/plan-runs", s.planRuns)
	s.mux.HandleFunc("GET /api/v1/plan-runs/{id}", s.getPlanRun)
	s.mux.HandleFunc("POST /api/v1/plan-runs/{id}/cancel", s.cancelPlanRun)
}

// planInput 只替换提供的顶层字段；stages 与 notify_channel_ids 整体替换。
type planInput struct {
	Name             *string       `json:"name"`
	CountdownS       *int          `json:"countdown_s"`
	OnMainsRestored  *string       `json:"on_mains_restored"`
	OnRestart        *string       `json:"on_restart"`
	NotifyChannelIDs *[]int64      `json:"notify_channel_ids"`
	Stages           *[]plan.Stage `json:"stages"`
}

func (s *Server) plans(w http.ResponseWriter, r *http.Request) {
	before, limit, ok := pagination(w, r)
	if !ok {
		return
	}
	rows, err := s.opts.DB.Read().QueryContext(r.Context(), "SELECT "+plan.Columns+" FROM plans WHERE (?=0 OR id<?) ORDER BY id DESC LIMIT ?", before, before, limit+1)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer rows.Close()
	items := []plan.Record{}
	for rows.Next() {
		p, err := plan.Scan(rows)
		if err != nil {
			s.internal(w, err)
			return
		}
		items = append(items, p)
	}
	if err := rows.Err(); err != nil {
		s.internal(w, err)
		return
	}
	var next int64
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1].ID
	}
	page(w, items, next)
}
func (s *Server) getPlan(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	p, err := plan.Read(r.Context(), s.opts.DB, id)
	if err != nil {
		s.storageError(w, err, "plan")
		return
	}
	reply(w, 200, p)
}
func (s *Server) createPlan(w http.ResponseWriter, r *http.Request) { s.savePlan(w, r, 0) }
func (s *Server) updatePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if ok {
		s.savePlan(w, r, id)
	}
}

// savePlan 保存计划配置；正在运行的计划使用已冻结快照，修改只影响之后的运行。
func (s *Server) savePlan(w http.ResponseWriter, r *http.Request, id int64) {
	var in planInput
	if !decode(w, r, &in) {
		return
	}
	s.changes.Lock()
	defer s.changes.Unlock()
	p := plan.Record{Config: plan.DefaultConfig()}
	var before any
	if id != 0 {
		var err error
		if p, err = plan.Read(r.Context(), s.opts.DB, id); err != nil {
			s.storageError(w, err, "plan")
			return
		}
		before = p
	}
	if in.Name != nil {
		p.Name = *in.Name
	}
	if in.CountdownS != nil {
		p.CountdownS = *in.CountdownS
	}
	if in.OnMainsRestored != nil {
		p.OnMainsRestored = *in.OnMainsRestored
	}
	if in.OnRestart != nil {
		p.OnRestart = *in.OnRestart
	}
	if in.NotifyChannelIDs != nil {
		p.NotifyChannelIDs = *in.NotifyChannelIDs
	}
	if in.Stages != nil {
		p.Stages = *in.Stages
	}
	plan.Normalize(&p)
	if err := s.opts.Plans.Validate(r.Context(), p); err != nil {
		if errors.Is(err, plan.ErrInvalid) {
			fail(w, 422, "validation_failed", err.Error())
			return
		}
		s.internal(w, err)
		return
	}
	cfg, err := plan.Encode(p)
	if err != nil {
		s.internal(w, err)
		return
	}
	err = s.opts.DB.WithTx(r.Context(), func(tx *sql.Tx) error {
		kind := "plan.update"
		if id == 0 {
			kind = "plan.create"
			res, err := tx.ExecContext(r.Context(), "INSERT INTO plans(name,config) VALUES(?,?)", p.Name, cfg)
			if err != nil {
				return err
			}
			if p.ID, err = res.LastInsertId(); err != nil {
				return err
			}
		} else if _, err := tx.ExecContext(r.Context(), "UPDATE plans SET name=?,config=? WHERE id=?", p.Name, cfg, id); err != nil {
			return err
		}
		return logChange(r.Context(), tx, r, kind, strconv.FormatInt(p.ID, 10), before, p)
	})
	if err != nil {
		s.storageError(w, err, "plan")
		return
	}
	code := 200
	if id == 0 {
		code = 201
	}
	reply(w, code, p)
}

// deletePlan 拒绝删除被 plan 动作引用或已有运行记录的计划，保留执行证据。
func (s *Server) deletePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok || !confirm(w, r) {
		return
	}
	s.changes.Lock()
	defer s.changes.Unlock()
	old, err := plan.Read(r.Context(), s.opts.DB, id)
	if err != nil {
		s.storageError(w, err, "plan")
		return
	}
	err = s.opts.DB.WithTx(r.Context(), func(tx *sql.Tx) error {
		var referenced bool
		if err := tx.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM actions WHERE type='plan' AND json_extract(config,'$.plan_id')=?) OR EXISTS(SELECT 1 FROM plan_runs WHERE plan_id=?)", id, id).Scan(&referenced); err != nil {
			return err
		}
		if referenced {
			return errConflict
		}
		if _, err := tx.ExecContext(r.Context(), "DELETE FROM plans WHERE id=?", id); err != nil {
			return err
		}
		return logChange(r.Context(), tx, r, "plan.delete", strconv.FormatInt(id, 10), old, nil)
	})
	if err != nil {
		s.storageError(w, err, "plan")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}

// runPlan 手动执行计划，要求输入计划名称确认；device_id 指定时使用该设备快照，
// 若设备处于停电周期，非 dry-run 运行同样占用该周期的唯一执行。
func (s *Server) runPlan(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var in struct {
		ConfirmName string `json:"confirm_name"`
		DryRun      bool   `json:"dry_run"`
		DeviceID    int64  `json:"device_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.DeviceID < 0 {
		fail(w, 422, "validation_failed", "设备 ID 无效")
		return
	}
	p, err := plan.Read(r.Context(), s.opts.DB, id)
	if err != nil {
		s.storageError(w, err, "plan")
		return
	}
	if in.ConfirmName != p.Name {
		fail(w, 422, "confirmation_required", "需要输入与计划名称一致的 confirm_name，包括 dry-run")
		return
	}
	req := action.Request{DryRun: in.DryRun, Trigger: action.Trigger{Type: "manual", UserID: current(r).User.ID}, Event: model.Event{Type: "test", Severity: model.SeverityInfo, At: time.Now()}}
	if in.DeviceID > 0 {
		found := false
		for _, state := range s.opts.Runtime.Snapshots() {
			if state.DeviceID == in.DeviceID {
				req.State = state
				found = true
				break
			}
		}
		if !found {
			fail(w, 409, "snapshot_unavailable", "设备暂无运行快照")
			return
		}
		if err := s.opts.DB.Read().QueryRowContext(r.Context(), "SELECT name FROM devices WHERE id=?", in.DeviceID).Scan(&req.DeviceName); err != nil {
			s.storageError(w, err, "device")
			return
		}
	}
	runID, err := s.opts.Plans.Start(r.Context(), id, req, func(tx *sql.Tx, runID int64) error {
		return audit.Write(r.Context(), tx, audit.Record{UserID: current(r).User.ID, IP: remoteIP(r), Kind: "plan.run", Target: strconv.FormatInt(id, 10), Result: "accepted", After: map[string]any{"run_id": runID, "dry_run": in.DryRun, "device_id": in.DeviceID}})
	})
	switch {
	case err == nil:
		reply(w, 202, map[string]any{"accepted": true, "run_id": runID})
	case errors.Is(err, action.ErrPlanActive):
		fail(w, 409, "plan_run_active", "该计划已有倒计时或执行中的运行")
	case errors.Is(err, action.ErrPlanExecuted):
		fail(w, 409, "plan_cycle_executed", "该计划已在本停电周期执行过")
	case errors.Is(err, action.ErrPlanCycleEnded):
		fail(w, 409, "plan_cycle_ended", "设备快照中的停电周期已结束，请刷新后重试")
	default:
		s.storageError(w, err, "plan")
	}
}
func (s *Server) planRuns(w http.ResponseWriter, r *http.Request) {
	before, limit, ok := pagination(w, r)
	if !ok {
		return
	}
	var planID int64
	if raw := r.URL.Query().Get("plan_id"); raw != "" {
		var err error
		planID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || planID <= 0 {
			fail(w, 400, "invalid_filter", "plan_id 无效")
			return
		}
	}
	items, err := plan.ListRuns(r.Context(), s.opts.DB, planID, before, limit+1)
	if err != nil {
		s.internal(w, err)
		return
	}
	var next int64
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1].ID
	}
	page(w, items, next)
}
func (s *Server) getPlanRun(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	v, err := plan.ReadRun(r.Context(), s.opts.DB, id)
	if err != nil {
		s.storageError(w, err, "plan_run")
		return
	}
	reply(w, 200, v)
}

// cancelPlanRun 取消未开始的步骤；已发出或执行中的命令不撤回，运行在其结果落库后结束。
func (s *Server) cancelPlanRun(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok || !confirm(w, r) {
		return
	}
	err := s.opts.Plans.Cancel(r.Context(), id, func(tx *sql.Tx) error {
		return audit.Write(r.Context(), tx, audit.Record{UserID: current(r).User.ID, IP: remoteIP(r), Kind: "plan_run.cancel", Target: strconv.FormatInt(id, 10), Result: "accepted"})
	})
	switch {
	case err == nil:
		reply(w, 202, map[string]bool{"accepted": true})
	case errors.Is(err, plan.ErrFinished):
		fail(w, 409, "plan_run_finished", "运行已结束")
	case errors.Is(err, plan.ErrClosed):
		fail(w, 503, "plan_unavailable", "计划服务已关闭")
	default:
		s.storageError(w, err, "plan_run")
	}
}
