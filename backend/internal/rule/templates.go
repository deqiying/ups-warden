package rule

import "encoding/json"

// Template 是前端预填数据；Actions 留空要求用户显式选择，绝不自动创建或启用。
type Template struct {
	Key                 string `json:"key"`
	Rule                Record `json:"rule"`
	SuggestedActionType string `json:"suggested_action_type"`
}

// Templates 返回五个需求模板；关机模板建议引用 plan 动作，由用户显式选择计划。
func Templates() []Template {
	items := []Template{}
	for _, v := range []struct{ key, name, event string }{
		{"mains_lost", "停电通知", "mains_lost"},
		{"mains_restored", "市电恢复通知", "mains_restored"},
		{"battery_low", "低电量通知", "battery_low"},
		{"comm_lost", "通讯中断通知", "comm_lost"},
	} {
		items = append(items, Template{Key: v.key, SuggestedActionType: "notify", Rule: Record{Name: v.name, Enabled: false, DryRun: true, Scope: Scope{Type: "all"}, Trigger: Trigger{Type: "event", Events: []string{v.event}}, Actions: []ActionRef{}, RecoveryActions: []ActionRef{}}})
	}
	power := Condition{Field: "power_state", Op: "eq", Value: json.RawMessage(`"on_battery"`)}
	items = append(items, Template{Key: "shutdown", SuggestedActionType: "plan", Rule: Record{
		Name: "电池模式过久或电量低 → 关机计划", Enabled: false, DryRun: true, Scope: Scope{Type: "all"},
		Trigger: Trigger{Type: "condition", AnyOf: []Group{
			{AllOf: []Condition{power, {Field: "on_battery_elapsed", Op: "gte", Value: json.RawMessage(`600`)}}},
			{AllOf: []Condition{power, {Field: "battery_capacity_pct", Op: "lte", Value: json.RawMessage(`40`), ForS: 10}}},
			{AllOf: []Condition{{Field: "low_battery", Op: "eq", Value: json.RawMessage(`true`)}}},
		}}, Actions: []ActionRef{}, RecoveryActions: []ActionRef{},
	}})
	return items
}

// Warnings 对引用估算续航的规则返回提醒；不阻止保存，也不把估算值当安全保证。
func Warnings(r Record) []string {
	for _, group := range r.Trigger.AnyOf {
		for _, c := range group.AllOf {
			if c.Field == "runtime_estimate" {
				return []string{"runtime_estimate 可能来自估算，不应作为关机动作的唯一依据；建议同时配置电池供电时长或容量条件"}
			}
		}
	}
	return []string{}
}
