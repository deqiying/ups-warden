// Package rule 在统一设备模型上评估规则，不读取协议数据，也不直接执行外部动作。
package rule

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

import "ups-warden/internal/model"

// Scope 按设备、任一标签或全部设备展开；只评估启用的设备。
type Scope struct {
	Type      string   `json:"type"`
	DeviceIDs []int64  `json:"device_ids,omitempty"`
	Tags      []string `json:"tags,omitempty"`
}

// Condition 只支持统一字段与六种比较运算，ForS 的单位为秒。
type Condition struct {
	Field string          `json:"field"`
	Op    string          `json:"op"`
	Value json.RawMessage `json:"value"`
	ForS  int             `json:"for_s,omitempty"`
}

// Group 是一组 AND 条件；Trigger.AnyOf 将各组以 OR 连接。
type Group struct {
	AllOf []Condition `json:"all_of"`
}

// Trigger 是事件或条件二选一，不支持递归表达式。
type Trigger struct {
	Type   string   `json:"type"`
	Events []string `json:"events,omitempty"`
	AnyOf  []Group  `json:"any_of,omitempty"`
}

// ActionRef 引用已保存动作；执行顺序为列表顺序，完成顺序由各动作耗时决定。
type ActionRef struct {
	ActionID int64 `json:"action_id"`
}

// Record 是规则持久化与 API 的共同结构；新建规则默认 dry-run 由 API 设置。
type Record struct {
	ID              int64       `json:"id"`
	Name            string      `json:"name"`
	Enabled         bool        `json:"enabled"`
	DryRun          bool        `json:"dry_run"`
	Scope           Scope       `json:"scope"`
	Trigger         Trigger     `json:"trigger"`
	Actions         []ActionRef `json:"actions"`
	RecoveryActions []ActionRef `json:"recovery_actions"`
	CooldownS       int         `json:"cooldown_s"`
}

// Validate 在保存和数据库加载边界校验类型与业务上限；不处理跨表引用。
func Validate(r Record) error {
	if strings.TrimSpace(r.Name) == "" || len(r.Name) > 128 || strings.ContainsRune(r.Name, 0) || r.CooldownS < 0 || r.CooldownS > 604800 {
		return errors.New("规则名称或冷却时间无效")
	}
	switch r.Scope.Type {
	case "all":
		if len(r.Scope.DeviceIDs) > 0 || len(r.Scope.Tags) > 0 {
			return errors.New("全部作用范围不能指定设备或标签")
		}
	case "device":
		if len(r.Scope.DeviceIDs) == 0 || len(r.Scope.DeviceIDs) > 100 || len(r.Scope.Tags) > 0 {
			return errors.New("设备作用范围无效")
		}
		seen := map[int64]bool{}
		for _, id := range r.Scope.DeviceIDs {
			if id <= 0 || seen[id] {
				return errors.New("设备 ID 无效或重复")
			}
			seen[id] = true
		}
	case "tags":
		if len(r.Scope.Tags) == 0 || len(r.Scope.Tags) > 32 || len(r.Scope.DeviceIDs) > 0 {
			return errors.New("标签作用范围无效")
		}
		seen := map[string]bool{}
		for _, tag := range r.Scope.Tags {
			if strings.TrimSpace(tag) == "" || len(tag) > 64 || strings.ContainsRune(tag, 0) || seen[tag] {
				return errors.New("标签无效或重复")
			}
			seen[tag] = true
		}
	default:
		return errors.New("未知规则作用范围")
	}
	if len(r.Actions) == 0 || len(r.Actions) > 32 || len(r.RecoveryActions) > 32 {
		return errors.New("规则需包含 1～32 个动作，恢复动作最多 32 个")
	}
	for _, refs := range [][]ActionRef{r.Actions, r.RecoveryActions} {
		seen := map[int64]bool{}
		for _, ref := range refs {
			if ref.ActionID <= 0 || seen[ref.ActionID] {
				return errors.New("动作 ID 无效或重复")
			}
			seen[ref.ActionID] = true
		}
	}
	switch r.Trigger.Type {
	case "event":
		if len(r.Trigger.Events) == 0 || len(r.Trigger.Events) > 16 || len(r.Trigger.AnyOf) > 0 {
			return errors.New("事件触发配置无效")
		}
		seen := map[string]bool{}
		for _, e := range r.Trigger.Events {
			if _, ok := model.EventSeverity(e); !ok || seen[e] {
				return errors.New("事件类型无效或重复")
			}
			seen[e] = true
		}
	case "condition":
		if len(r.Trigger.Events) > 0 || len(r.Trigger.AnyOf) == 0 || len(r.Trigger.AnyOf) > 16 {
			return errors.New("条件触发配置无效")
		}
		for _, g := range r.Trigger.AnyOf {
			if len(g.AllOf) == 0 || len(g.AllOf) > 16 {
				return errors.New("每个条件组需包含 1～16 个条件")
			}
			for _, c := range g.AllOf {
				if err := validateCondition(c); err != nil {
					return err
				}
			}
		}
	default:
		return errors.New("未知规则触发类型")
	}
	return nil
}
func validateCondition(c Condition) error {
	spec, ok := model.Lookup(c.Field)
	if !ok || !slices.Contains([]string{"eq", "ne", "gt", "gte", "lt", "lte"}, c.Op) || c.ForS < 0 || c.ForS > 604800 {
		return errors.New("条件字段、运算符或持续时间无效")
	}
	switch spec.Kind {
	case model.Number, model.Duration:
		var n float64
		if strings.TrimSpace(string(c.Value)) == "null" || json.Unmarshal(c.Value, &n) != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return errors.New("数值条件必须使用有限数字")
		}
	case model.Bool:
		var b bool
		if strings.TrimSpace(string(c.Value)) == "null" || json.Unmarshal(c.Value, &b) != nil || c.Op != "eq" && c.Op != "ne" {
			return errors.New("布尔条件只支持 eq/ne 与布尔值")
		}
	case model.Enum, model.Text:
		var text string
		if strings.TrimSpace(string(c.Value)) == "null" || json.Unmarshal(c.Value, &text) != nil || len(text) > 256 || strings.ContainsRune(text, 0) || c.Op != "eq" && c.Op != "ne" {
			return errors.New("文本条件只支持 eq/ne 与字符串")
		}
		if spec.Kind == model.Enum && !slices.Contains(spec.Enum, text) {
			return fmt.Errorf("字段 %s 的枚举值无效", c.Field)
		}
	}
	return nil
}
func (s Scope) matches(id int64, tags []string) bool {
	switch s.Type {
	case "all":
		return true
	case "device":
		return slices.Contains(s.DeviceIDs, id)
	case "tags":
		for _, tag := range tags {
			if slices.Contains(s.Tags, tag) {
				return true
			}
		}
	}
	return false
}
