// Package plan 编排关机计划：倒计时、阶段串行/阶段内并行、取消、幂等与重启恢复。
// 计划只引用已保存的 SSH / 脚本 / HTTP 动作，不读取协议数据，也不向 UPS 发送任何指令。
package plan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"ups-warden/internal/store"
)

// ErrInvalid 标记计划配置校验失败（保存时返回 422）；未包装它的错误属于存储或内部错误，不能当作校验失败。
var ErrInvalid = errors.New("计划配置无效")

func invalid(msg string) error { return fmt.Errorf("%w：%s", ErrInvalid, msg) }

// Step 引用一个已保存的 SSH、脚本或 HTTP 动作。
type Step struct {
	ActionID int64 `json:"action_id"`
}

// Stage 内步骤并行执行，阶段之间串行。时长单位均为秒。
// SelfShutdown 阶段关闭 ups-warden 所在主机，必须是最后一个阶段且只含一个步骤。
type Stage struct {
	DelayS       int    `json:"delay_s"`
	StepTimeoutS int    `json:"step_timeout_s"`
	OfflineWaitS int    `json:"offline_wait_s"`
	SelfShutdown bool   `json:"self_shutdown,omitempty"`
	Steps        []Step `json:"steps"`
}

// Config 是 plans.config 的持久化内容。
type Config struct {
	CountdownS       int     `json:"countdown_s"`
	OnMainsRestored  string  `json:"on_mains_restored"`
	OnRestart        string  `json:"on_restart"`
	NotifyChannelIDs []int64 `json:"notify_channel_ids"`
	Stages           []Stage `json:"stages"`
}

// Record 是计划配置；运行开始时整体冻结为 plan_snapshot，之后修改不影响该次运行。
type Record struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Config
}

// DefaultConfig 返回新建计划的默认值：倒计时 60 秒、市电恢复取消未开始阶段、重启后继续。
func DefaultConfig() Config {
	return Config{CountdownS: 60, OnMainsRestored: "cancel_pending", OnRestart: "resume", NotifyChannelIDs: []int64{}}
}

// Normalize 为省略的单步超时赋默认 30 秒，并保证渠道列表编码为数组。
func Normalize(r *Record) {
	if r.NotifyChannelIDs == nil {
		r.NotifyChannelIDs = []int64{}
	}
	for i := range r.Stages {
		if r.Stages[i].StepTimeoutS == 0 {
			r.Stages[i].StepTimeoutS = 30
		}
	}
}

// Validate 在保存与加载边界检查结构和业务上限；跨表引用由 Service.Validate 检查。
func Validate(r Record) error {
	if strings.TrimSpace(r.Name) == "" || len(r.Name) > 128 || strings.ContainsRune(r.Name, 0) {
		return invalid("计划名称无效")
	}
	if r.CountdownS < 0 || r.CountdownS > 3600 {
		return invalid("倒计时需在 0～3600 秒")
	}
	if r.OnMainsRestored != "cancel_pending" && r.OnMainsRestored != "continue" {
		return invalid("on_mains_restored 只支持 cancel_pending / continue")
	}
	if r.OnRestart != "resume" && r.OnRestart != "abort" {
		return invalid("on_restart 只支持 resume / abort")
	}
	if len(r.NotifyChannelIDs) > 16 {
		return invalid("通知渠道最多 16 个")
	}
	channels := map[int64]bool{}
	for _, id := range r.NotifyChannelIDs {
		if id <= 0 || channels[id] {
			return invalid("通知渠道 ID 无效或重复")
		}
		channels[id] = true
	}
	if len(r.Stages) == 0 || len(r.Stages) > 16 {
		return invalid("计划需包含 1～16 个阶段")
	}
	actions := map[int64]bool{}
	for i, st := range r.Stages {
		if st.DelayS < 0 || st.DelayS > 3600 || st.StepTimeoutS < 1 || st.StepTimeoutS > 3600 || st.OfflineWaitS < 0 || st.OfflineWaitS > 3600 {
			return invalid("阶段延迟、单步超时或等待离线时间超出范围")
		}
		if len(st.Steps) == 0 || len(st.Steps) > 32 {
			return invalid("每个阶段需包含 1～32 个步骤")
		}
		if st.SelfShutdown && (i != len(r.Stages)-1 || len(st.Steps) != 1 || st.OfflineWaitS != 0) {
			return invalid("self_shutdown 阶段必须是最后一个阶段，只含一个步骤且不等待离线")
		}
		// 同一动作出现两次会被动作互斥跳过，直接拒绝以免误以为执行了两次。
		for _, step := range st.Steps {
			if step.ActionID <= 0 || actions[step.ActionID] {
				return invalid("步骤动作 ID 无效或在计划内重复")
			}
			actions[step.ActionID] = true
		}
	}
	return nil
}

// Columns 固定 plans 的读取列顺序。
const Columns = "id,name,config"

// Scan 在存储信任边界严格解码并校验。
func Scan(row interface{ Scan(...any) error }) (Record, error) {
	var r Record
	var cfg string
	if err := row.Scan(&r.ID, &r.Name, &cfg); err != nil {
		return r, err
	}
	if err := strictJSON([]byte(cfg), &r.Config); err != nil {
		return r, errors.New("plan: 计划配置解码失败")
	}
	Normalize(&r)
	return r, Validate(r)
}

// Read 读取计划；不存在时返回 sql.ErrNoRows。
func Read(ctx context.Context, db *store.Store, id int64) (Record, error) {
	return Scan(db.Read().QueryRowContext(ctx, "SELECT "+Columns+" FROM plans WHERE id=?", id))
}

// Encode 返回 plans.config 列内容。
func Encode(r Record) (string, error) {
	data, err := json.Marshal(r.Config)
	return string(data), err
}

func strictJSON(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
