package model

import (
	"encoding/json"
	"slices"
	"time"
)

// PlanStepResult 只记录步骤关联的动作结果与离线确认；命令输出和凭据留在 action_runs。
type PlanStepResult struct {
	ActionRunID    int64  `json:"action_run_id,omitempty"`
	ActionStatus   string `json:"action_status,omitempty"`
	Error          string `json:"error,omitempty"`
	OfflineTimeout bool   `json:"offline_timeout,omitempty"`
}

// PlanStep 是计划运行中单个步骤的持久化状态。
// Status 取 waiting / running / sent / offline / failed / cancelled / unknown；
// unknown 表示重启前已写入 running、结果未知且不会重放。
type PlanStep struct {
	Stage     int            `json:"stage"`
	Step      int            `json:"step"`
	ActionID  int64          `json:"action_id"`
	HostID    *int64         `json:"host_id"`
	Status    string         `json:"status"`
	StartedAt *time.Time     `json:"started_at"`
	EndedAt   *time.Time     `json:"ended_at"`
	Result    PlanStepResult `json:"result"`
}

// PlanRun 是关机计划运行视图，查询 API 与 SSE plan_progress 共用。
// CountdownUntil 为倒计时截止时刻，剩余秒数由客户端按当前时间计算。
type PlanRun struct {
	ID             int64           `json:"id"`
	PlanID         int64           `json:"plan_id"`
	PlanName       string          `json:"plan_name"`
	DeviceID       *int64          `json:"device_id"`
	OutageCycleID  *int64          `json:"outage_cycle_id"`
	DryRun         bool            `json:"dry_run"`
	Status         string          `json:"status"`
	Trigger        json.RawMessage `json:"trigger"`
	CountdownUntil *time.Time      `json:"countdown_until"`
	CurrentStage   int             `json:"current_stage"`
	StartedAt      time.Time       `json:"started_at"`
	EndedAt        *time.Time      `json:"ended_at"`
	Steps          []PlanStep      `json:"steps"`
}

// ClonePlanRun 复制切片字段；时间指针指向的值不会被修改，可共享。
func ClonePlanRun(r PlanRun) PlanRun {
	r.Trigger = slices.Clone(r.Trigger)
	r.Steps = slices.Clone(r.Steps)
	return r
}
