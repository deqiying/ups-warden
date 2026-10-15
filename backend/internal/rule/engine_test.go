package rule

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"ups-warden/internal/model"
)

func conditionRule(c Condition) Record {
	return Record{ID: 1, Name: "fixture", Enabled: true, DryRun: true, Scope: Scope{Type: "all"}, Trigger: Trigger{Type: "condition", AnyOf: []Group{{AllOf: []Condition{c}}}}, Actions: []ActionRef{{1}}, RecoveryActions: []ActionRef{{2}}}
}
func update(at time.Time, field string, value model.Field) model.DeviceUpdate {
	return model.DeviceUpdate{State: model.DeviceState{DeviceID: 1, At: at, Fields: map[string]model.Field{field: value}}}
}
func kinds(ds []decision) []string {
	out := []string{}
	for _, d := range ds {
		out = append(out, d.Kind)
	}
	return out
}
func TestDurationEdgeCooldownAndRecovery(t *testing.T) {
	r := conditionRule(Condition{Field: "battery_capacity_pct", Op: "lte", Value: json.RawMessage(`40`), ForS: 10})
	r.CooldownS = 60
	at := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	st := instance{}
	check := func(seconds int, value float64, want ...string) {
		t.Helper()
		u := update(at.Add(time.Duration(seconds)*time.Second), "battery_capacity_pct", model.Field{Num: value, Quality: model.Good})
		if got := kinds(evaluate(r, &st, u)); !slices.Equal(got, want) {
			t.Fatalf("t=%d: %v want %v", seconds, got, want)
		}
	}
	check(0, 30)
	check(9, 30)
	check(10, 30, "fired")
	check(11, 30)
	check(12, 80, "recovered")
	check(13, 30)
	check(23, 30, "skipped_cooldown")
	check(90, 30) // 不能在冷却结束后补发被跳过的边沿。
	check(91, 80) // 未真正命中的边沿没有恢复动作。
	check(92, 30)
	check(102, 30, "fired")
	check(103, 80, "recovered")
}
func TestQualityChangeOnlyAndNoFalseRecovery(t *testing.T) {
	r := conditionRule(Condition{Field: "battery_capacity_pct", Op: "lt", Value: json.RawMessage(`40`), ForS: 5})
	at := time.Now()
	st := instance{}
	good := model.Field{Num: 30, Quality: model.Good}
	evaluate(r, &st, update(at, "battery_capacity_pct", good))
	if got := kinds(evaluate(r, &st, update(at.Add(5*time.Second), "battery_capacity_pct", good))); !slices.Equal(got, []string{"fired"}) {
		t.Fatal(got)
	}
	for i := range 4 {
		bad := model.Field{Num: 0, Quality: model.Stale, Reason: model.ReasonCommStale}
		got := kinds(evaluate(r, &st, update(at.Add(time.Duration(6+i)*time.Second), "battery_capacity_pct", bad)))
		if i == 0 && !slices.Equal(got, []string{"quality_blocked"}) || i > 0 && len(got) > 0 {
			t.Fatal(got)
		}
		if !st.Fired {
			t.Fatal("失联错误地解除了已触发边沿")
		}
	}
	if got := kinds(evaluate(r, &st, update(at.Add(20*time.Second), "battery_capacity_pct", good))); !slices.Equal(got, []string{"quality_restored"}) {
		t.Fatal(got)
	}
	if got := evaluate(r, &st, update(at.Add(30*time.Second), "battery_capacity_pct", good)); len(got) > 0 {
		t.Fatal("缺测后重复触发", got)
	}
	if got := kinds(evaluate(r, &st, update(at.Add(31*time.Second), "battery_capacity_pct", model.Field{Num: 80, Quality: model.Good}))); !slices.Equal(got, []string{"recovered"}) {
		t.Fatal(got)
	}
}
func TestOROfANDAndPhaseMaximum(t *testing.T) {
	r := conditionRule(Condition{Field: "power_state", Op: "eq", Value: json.RawMessage(`"on_battery"`)})
	r.Trigger.AnyOf[0].AllOf = append(r.Trigger.AnyOf[0].AllOf, Condition{Field: "output_load_pct", Op: "gte", Value: json.RawMessage(`90`)})
	r.Trigger.AnyOf = append(r.Trigger.AnyOf, Group{AllOf: []Condition{{Field: "low_battery", Op: "eq", Value: json.RawMessage(`true`)}}})
	u := model.DeviceUpdate{State: model.DeviceState{At: time.Now(), Fields: map[string]model.Field{
		"power_state": {Str: "on_battery", Quality: model.Good}, "output_load_pct[1]": {Num: 30, Quality: model.Good}, "output_load_pct[2]": {Num: 91, Quality: model.Good}, "low_battery": {Quality: model.Good},
	}}}
	st := instance{}
	if got := kinds(evaluate(r, &st, u)); !slices.Equal(got, []string{"fired"}) {
		t.Fatal(got)
	}
	u.State.Fields["power_state"] = model.Field{Str: "online", Quality: model.Good}
	u.State.Fields["low_battery"] = model.Field{Bool: true, Quality: model.Good}
	if got := evaluate(r, &st, u); len(got) > 0 {
		t.Fatal("OR分支切换不能重复触发", got)
	}
	u.State.Fields["low_battery"] = model.Field{Bool: false, Quality: model.Good}
	if got := kinds(evaluate(r, &st, u)); !slices.Equal(got, []string{"recovered"}) {
		t.Fatal(got)
	}
}
func TestEventPairCooldownAndReplay(t *testing.T) {
	r := conditionRule(Condition{})
	r.Trigger = Trigger{Type: "event", Events: []string{model.EventMainsLost, model.EventCommLost}}
	r.CooldownS = 60
	st := instance{}
	at := time.Now()
	lost := model.Event{ID: 1, Type: model.EventMainsLost, Active: true, At: at}
	u := model.DeviceUpdate{Events: []model.Event{lost}}
	if got := kinds(evaluate(r, &st, u)); !slices.Equal(got, []string{"fired"}) {
		t.Fatal(got)
	}
	if got := evaluate(r, &st, u); len(got) > 0 {
		t.Fatal("重放事件重复触发", got)
	}
	u.Events = []model.Event{{ID: 2, Type: model.EventCommLost, Active: true, At: at.Add(time.Second)}}
	if got := kinds(evaluate(r, &st, u)); !slices.Equal(got, []string{"skipped_cooldown"}) {
		t.Fatal(got)
	}
	u.Events = []model.Event{{ID: 3, Type: model.EventMainsRestored, Clears: model.EventMainsLost, At: at.Add(2 * time.Second)}}
	if got := kinds(evaluate(r, &st, u)); !slices.Equal(got, []string{"recovered"}) {
		t.Fatal(got)
	}
	if got := evaluate(r, &st, u); len(got) > 0 {
		t.Fatal(got)
	}
	u.Events = []model.Event{{Clears: model.EventCommLost, At: at.Add(3 * time.Second)}}
	if got := evaluate(r, &st, u); len(got) > 0 {
		t.Fatal("冷却跳过的事件错误地运行恢复动作", got)
	}
}
func TestValidationScopeAndTemplates(t *testing.T) {
	for _, tc := range []struct {
		scope Scope
		id    int64
		tags  []string
		want  bool
	}{
		{Scope{Type: "all"}, 1, nil, true}, {Scope{Type: "device", DeviceIDs: []int64{2}}, 1, nil, false},
		{Scope{Type: "tags", Tags: []string{"lab"}}, 1, []string{"nas", "lab"}, true},
	} {
		if tc.scope.matches(tc.id, tc.tags) != tc.want {
			t.Fatal(tc)
		}
	}
	for _, c := range []Condition{
		{Field: "battery_capacity_pct", Op: "lte", Value: json.RawMessage(`null`)},
		{Field: "battery_capacity_pct", Op: "lt", Value: json.RawMessage(`"40"`)},
		{Field: "power_state", Op: "eq", Value: json.RawMessage(`"made_up"`)},
		{Field: "low_battery", Op: "gt", Value: json.RawMessage(`true`)},
		{Field: "OID", Op: "eq", Value: json.RawMessage(`1`)},
		{Field: "output_load_pct[4]", Op: "gt", Value: json.RawMessage(`1`)},
		{Field: "temperature", Op: "eq", Value: json.RawMessage(`30`), ForS: -1},
	} {
		if Validate(conditionRule(c)) == nil {
			t.Fatal("接受非法条件", c)
		}
	}
	for _, template := range Templates() {
		if !template.Rule.DryRun || template.Rule.Enabled || len(template.Rule.Actions) != 0 {
			t.Fatal("模板不安全", template)
		}
		template.Rule.Actions = []ActionRef{{1}}
		if err := Validate(template.Rule); err != nil {
			t.Fatal(err)
		}
	}
	r := conditionRule(Condition{Field: "runtime_estimate", Op: "lte", Value: json.RawMessage(`300`)})
	if len(Warnings(r)) != 1 {
		t.Fatal("缺少续航警告")
	}
}
func TestAllComparisons(t *testing.T) {
	for _, v := range []struct {
		op    string
		value float64
		want  bool
	}{{"eq", 2, true}, {"ne", 2, false}, {"gt", 1, true}, {"gte", 2, true}, {"lt", 3, true}, {"lte", 2, true}} {
		b, _ := json.Marshal(v.value)
		if compare(Condition{Field: "temperature", Op: v.op, Value: b}, model.Field{Num: 2, Quality: model.Good}) != v.want {
			t.Fatal(v)
		}
	}
}
