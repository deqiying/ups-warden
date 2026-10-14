// Package state 只消费统一模型，串行维护供电、通信、事件和停电周期。
package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"time"
)

// Config 对应 devices.config；时间单位为秒，缺省值由 ParseConfig 填充。
// 默认值先填充再覆盖，因此显式零值与省略可区分；配置不含协议定位或凭据。
type Config struct {
	OutageConfirmSeconds   float64 `json:"outage_confirm_s"`
	RecoveryConfirmSeconds float64 `json:"recovery_confirm_s"`
	InputThreshold         float64 `json:"input_voltage_threshold"`
	Failures               int     `json:"comm_failures"`
	LostPolicy             string  `json:"comm_lost_policy"`
	AssumeAfterSeconds     float64 `json:"assume_on_battery_after_s"`
	BatteryLow             float64 `json:"battery_low_pct"`
	BatteryHysteresis      float64 `json:"battery_hysteresis_pct"`
	Overload               float64 `json:"overload_pct"`
	LoadHysteresis         float64 `json:"load_hysteresis_pct"`
}

// DefaultConfig 返回 P0 默认阈值和失联策略。
func DefaultConfig() Config { return Config{10, 60, 50, 3, "hold", 300, 30, 5, 90, 5} }

// ParseConfig 严格读取单个 JSON 配置；显式零确认时长合法，其他约束由 Validate 执行。
func ParseConfig(data []byte) (Config, error) {
	c := DefaultConfig()
	if len(data) > 16*1024 {
		return c, errors.New("设备配置超过 16 KiB")
	}
	// 单层配置逐键检查，拒绝 JSON 默认的重复键覆盖与 null 忽略行为。
	check := json.NewDecoder(bytes.NewReader(data))
	if token, err := check.Token(); err != nil || token != json.Delim('{') {
		return c, errors.New("设备配置必须是对象")
	}
	seen := map[string]bool{}
	for check.More() {
		token, err := check.Token()
		if err != nil {
			return c, errors.New("设备配置键无效")
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return c, errors.New("设备配置含重复键")
		}
		seen[key] = true
		var value json.RawMessage
		if err := check.Decode(&value); err != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return c, errors.New("设备配置字段不得为空")
		}
	}
	if _, err := check.Token(); err != nil {
		return c, errors.New("设备配置对象未闭合")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, errors.New("设备状态配置格式无效或含未知字段")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return c, errors.New("设备状态配置含尾随数据")
	}
	return c, c.Validate()
}

// Validate 校验有限数、回差和失联策略，不修改配置。
func (c Config) Validate() error {
	for _, n := range []float64{c.OutageConfirmSeconds, c.RecoveryConfirmSeconds, c.InputThreshold, c.AssumeAfterSeconds, c.BatteryLow, c.BatteryHysteresis, c.Overload, c.LoadHysteresis} {
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
			return errors.New("设备阈值须为有限非负数")
		}
	}
	if c.OutageConfirmSeconds > 86400 || c.RecoveryConfirmSeconds > 86400 || c.InputThreshold > 10000 || c.AssumeAfterSeconds > 86400*365 || c.Failures < 1 || c.Failures > 1000 || c.BatteryLow > 100 || c.BatteryLow+c.BatteryHysteresis > 100 || c.Overload > 100 || c.LoadHysteresis > c.Overload {
		return errors.New("设备阈值超出范围")
	}
	switch c.LostPolicy {
	case "hold", "ignore":
	case "assume_on_battery_after":
		if c.AssumeAfterSeconds <= 0 {
			return errors.New("失联假定时长须大于零")
		}
	default:
		return errors.New("未知失联策略")
	}
	return nil
}
func seconds(n float64) time.Duration { return time.Duration(n * float64(time.Second)) }
