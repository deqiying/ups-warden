// Package model 定义与设备协议无关的统一字段和状态，供业务模块共同使用。
package model

import "time"

// SchemaVersion 是统一字段模型版本；新增字段向后兼容，不改变既有字段语义。
const SchemaVersion = 1

// Kind 描述归一化后字段的类型。
type Kind string

const (
	Number   Kind = "number"
	Bool     Kind = "bool"
	Enum     Kind = "enum"
	Text     Kind = "text"
	Duration Kind = "duration"
)

// Quality 是字段可信度；业务判断只能读取 Good。
type Quality string

const (
	Good        Quality = "good"
	Invalid     Quality = "invalid"
	Unsupported Quality = "unsupported"
	Stale       Quality = "stale"
)

// 原因码随字段返回；协议地址与原始值只保留在诊断 Trace 中。
const (
	ReasonPlaceholder       = "placeholder"
	ReasonInvalidValue      = "invalid_value"
	ReasonOutOfRange        = "out_of_range"
	ReasonNotMapped         = "not_mapped"
	ReasonDeviceUnsupported = "device_unsupported"
	ReasonPhaseInactive     = "phase_inactive"
	ReasonDecodeError       = "decode_error"
	ReasonEnumUnmapped      = "enum_unmapped"
	ReasonCommStale         = "comm_stale"
)

// Field 保存一个统一值及质量；按 FieldSpec.Kind 读取 Num、Bool 或 Str。
// 非 Good 数值不能被当作零参与计算；枚举映射失败时 Str 为 unknown。
type Field struct {
	Num     float64 `json:"num,omitempty"`
	Bool    bool    `json:"bool,omitempty"`
	Str     string  `json:"str,omitempty"`
	Quality Quality `json:"quality"`
	Reason  string  `json:"reason,omitempty"`
}

// PowerHints 只使用统一字段名声明可信的供电判定依据及优先级。
type PowerHints struct {
	Indicators []string `json:"indicators,omitempty" yaml:"indicators,omitempty"`
}

// SourceSnapshot 是单数据源的归一化结果；At 为 UTC 采集完成时间。
type SourceSnapshot struct {
	SourceID int64            `json:"source_id"`
	At       time.Time        `json:"at"`
	Fields   map[string]Field `json:"fields"`
	Hints    PowerHints       `json:"power_hints"`
}

// DeviceState 是业务层消费的设备视图，不携带任何协议原始数据。
// on_battery_elapsed 由 OnBatterySince 与消费时刻计算，失联时仍可延续计时。
type DeviceState struct {
	DeviceID       int64            `json:"device_id"`
	SchemaVersion  int              `json:"schema_version"`
	At             time.Time        `json:"at"`
	Fields         map[string]Field `json:"fields"`
	PowerStateRaw  string           `json:"power_state_raw"`
	PowerState     string           `json:"power_state"`
	PendingSince   *time.Time       `json:"pending_since,omitempty"`
	OnBatterySince *time.Time       `json:"on_battery_since,omitempty"`
	OutageCycleID  *int64           `json:"outage_cycle_id,omitempty"`
	CommState      string           `json:"comm_state"`
	LastOKAt       time.Time        `json:"last_ok_at"`
	RuntimeSource  string           `json:"runtime_source"`
}

// Severity 是事件的统一级别。
type Severity string

const (
	SeverityInfo           Severity = "info"
	SeverityWarning        Severity = "warning"
	SeverityCritical       Severity = "critical"
	EventMainsLost                  = "mains_lost"
	EventMainsRestored              = "mains_restored"
	EventBatteryLow                 = "battery_low"
	EventBatteryLowCleared          = "battery_low_cleared"
	EventBatteryDepleted            = "battery_depleted"
	EventCommLost                   = "comm_lost"
	EventCommRestored               = "comm_restored"
	EventOverload                   = "overload"
	EventOverloadCleared            = "overload_cleared"
	EventOnBypass                   = "on_bypass"
	EventBypassCleared              = "bypass_cleared"
	EventUPSFault                   = "ups_fault"
	EventUPSShuttingDown            = "ups_shutting_down"
	EventTemperatureHigh            = "temperature_high"
	EventSelfTestResult             = "self_test_result"
)

// EventSeverity 返回已知事件的默认级别；未知类型不猜测默认值。
func EventSeverity(event string) (Severity, bool) {
	switch event {
	case EventMainsLost, EventBatteryLow, EventBatteryLowCleared, EventBatteryDepleted, EventUPSFault, EventUPSShuttingDown:
		return SeverityCritical, true
	case EventCommLost, EventCommRestored, EventOverload, EventOverloadCleared, EventOnBypass, EventBypassCleared, EventTemperatureHigh:
		return SeverityWarning, true
	case EventMainsRestored, EventSelfTestResult:
		return SeverityInfo, true
	default:
		return "", false
	}
}
