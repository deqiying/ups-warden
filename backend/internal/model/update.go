package model

import "time"

// SourceResult 是采集层交给状态引擎的事实，不包含协议数据或自由文本错误。
// Failed 表示本轮不可用于状态更新；At 是保留单调读数的完成时刻，只在存储/输出边界转 UTC。
// Latency 不含排队后的业务处理。
type SourceResult struct {
	DeviceID int64
	SourceID int64
	At       time.Time
	Snapshot SourceSnapshot
	Failed   bool
	Latency  time.Duration
}

// Event 是业务事件；纯引擎产生候选，actor 提交后填充 ID。Clears 标识原活动类型。
type Event struct {
	ID       int64            `json:"id"`
	DeviceID int64            `json:"device_id"`
	Type     string           `json:"type"`
	Severity Severity         `json:"severity"`
	At       time.Time        `json:"at"`
	Active   bool             `json:"active"`
	Clears   string           `json:"clears,omitempty"`
	Summary  map[string]Field `json:"summary"`
}

// DeviceUpdate 在关键状态与事件成功落库后发布；Sample 仅在成功采集时为真。
// tick 推进计时，但不伪造新的历史测量点。
type DeviceUpdate struct {
	State         DeviceState         `json:"state"`
	Events        []Event             `json:"events,omitempty"`
	Sample        bool                `json:"-"`
	AlertsChanged bool                `json:"-"`
	Resync        bool                `json:"-"`
	NotifyFailed  *NotificationResult `json:"-"`
}

// CloneState 复制含指针和字段 map 的状态，防止订阅者修改 actor 内部数据。
func CloneState(s DeviceState) DeviceState {
	s.Fields = CloneFields(s.Fields)
	if s.PendingSince != nil {
		v := *s.PendingSince
		s.PendingSince = &v
	}
	if s.OnBatterySince != nil {
		v := *s.OnBatterySince
		s.OnBatterySince = &v
	}
	if s.OutageCycleID != nil {
		v := *s.OutageCycleID
		s.OutageCycleID = &v
	}
	return s
}

// CloneFields 返回独立字段 map；Field 仅含值类型。
func CloneFields(fields map[string]Field) map[string]Field {
	out := make(map[string]Field, len(fields))
	for k, v := range fields {
		out[k] = v
	}
	return out
}

// CloneUpdate 隔离发布者和每个订阅者的可变数据。
func CloneUpdate(u DeviceUpdate) DeviceUpdate {
	u.State = CloneState(u.State)
	if u.NotifyFailed != nil {
		v := *u.NotifyFailed
		if v.EventID != nil {
			id := *v.EventID
			v.EventID = &id
		}
		u.NotifyFailed = &v
	}
	events := make([]Event, len(u.Events))
	for i, e := range u.Events {
		events[i] = e
		events[i].Summary = CloneFields(e.Summary)
	}
	u.Events = events
	return u
}
