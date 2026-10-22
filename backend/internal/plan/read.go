package plan

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"ups-warden/internal/model"
	"ups-warden/internal/store"
)

const runColumns = "id,plan_id,json_extract(plan_snapshot,'$.name'),device_id,outage_cycle_id,dry_run,status,trigger,countdown_until,current_stage,started_at,ended_at"

// ReadRun 读取运行及全部步骤；不存在时返回 sql.ErrNoRows。
func ReadRun(ctx context.Context, db *store.Store, id int64) (model.PlanRun, error) {
	runs, err := queryRuns(ctx, db, "WHERE id=?", id)
	if err != nil {
		return model.PlanRun{}, err
	}
	if len(runs) == 0 {
		return model.PlanRun{}, sql.ErrNoRows
	}
	return runs[0], nil
}

// ListRuns 按 ID 倒序分页读取运行；planID=0 表示全部计划，before=0 表示从最新开始。
func ListRuns(ctx context.Context, db *store.Store, planID, before int64, limit int) ([]model.PlanRun, error) {
	return queryRuns(ctx, db, "WHERE (?=0 OR plan_id=?) AND (?=0 OR id<?) ORDER BY id DESC LIMIT ?", planID, planID, before, before, limit)
}

// ActiveRuns 返回倒计时或执行中的运行，供 SSE 全量同步。
func ActiveRuns(ctx context.Context, db *store.Store) ([]model.PlanRun, error) {
	return queryRuns(ctx, db, "WHERE status IN ('countdown','running') ORDER BY id")
}

func queryRuns(ctx context.Context, db *store.Store, where string, args ...any) ([]model.PlanRun, error) {
	rows, err := db.Read().QueryContext(ctx, "SELECT "+runColumns+" FROM plan_runs "+where, args...)
	if err != nil {
		return nil, err
	}
	items := []model.PlanRun{}
	for rows.Next() {
		var v model.PlanRun
		var trigger string
		var started int64
		var until, ended sql.NullInt64
		if err = rows.Scan(&v.ID, &v.PlanID, &v.PlanName, &v.DeviceID, &v.OutageCycleID, &v.DryRun, &v.Status, &trigger, &until, &v.CurrentStage, &started, &ended); err != nil {
			break
		}
		v.Trigger = json.RawMessage(trigger)
		v.CountdownUntil = timePtr(until)
		v.StartedAt = time.UnixMilli(started).UTC()
		v.EndedAt = timePtr(ended)
		items = append(items, v)
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].Steps, err = readSteps(ctx, db, items[i].ID, -1); err != nil {
			return nil, err
		}
	}
	return items, nil
}

// readSteps 读取运行步骤；stage<0 表示全部阶段。
func readSteps(ctx context.Context, db *store.Store, runID int64, stage int) ([]model.PlanStep, error) {
	rows, err := db.Read().QueryContext(ctx, "SELECT stage,step,action_id,host_id,status,started_at,ended_at,result FROM plan_run_steps WHERE run_id=? AND (?<0 OR stage=?) ORDER BY stage,step", runID, stage, stage)
	if err != nil {
		return nil, err
	}
	steps := []model.PlanStep{}
	for rows.Next() {
		var v model.PlanStep
		var started, ended sql.NullInt64
		var result string
		if err = rows.Scan(&v.Stage, &v.Step, &v.ActionID, &v.HostID, &v.Status, &started, &ended, &result); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(result), &v.Result); err != nil {
			break
		}
		v.StartedAt = timePtr(started)
		v.EndedAt = timePtr(ended)
		steps = append(steps, v)
	}
	return steps, errors.Join(err, rows.Err(), rows.Close())
}

func timePtr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMilli(v.Int64).UTC()
	return &t
}
