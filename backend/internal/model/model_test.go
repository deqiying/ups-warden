package model

import (
	"slices"
	"strings"
	"testing"
)

func TestCatalogComplete(t *testing.T) {
	expected := strings.Fields(`manufacturer model firmware agent_version serial name
 nominal_input_voltage nominal_input_freq nominal_output_voltage nominal_output_freq rated_va rated_w nominal_battery_voltage input_phases output_phases
 input_voltage input_current input_power input_freq output_voltage output_current output_power output_load_pct output_freq output_source
 bypass_voltage bypass_current bypass_power bypass_freq battery_status battery_capacity_pct battery_voltage battery_current temperature native_seconds_on_battery native_runtime_remaining
 mains_ok low_battery overload shutting_down buzzer_muted ups_mode ups_type self_test_result power_state on_battery_elapsed runtime_estimate runtime_source comm_state`)
	got := Catalog()
	if len(got) != len(expected) || len(Keys()) != 69 {
		t.Fatalf("字段数量：目录%d，实例%d", len(got), len(Keys()))
	}
	for _, key := range expected {
		spec, ok := Lookup(key)
		if !ok || spec.Key != key {
			t.Errorf("缺少字段 %s", key)
		}
		if spec.Kind == Enum && !slices.Contains(spec.Enum, "unknown") {
			t.Errorf("枚举缺少unknown: %s", key)
		}
		if spec.History && spec.Kind != Number && spec.Kind != Duration {
			t.Errorf("非数值历史字段: %s", key)
		}
	}
	for _, key := range Keys() {
		if _, ok := Lookup(key); !ok {
			t.Errorf("非法实例 %s", key)
		}
	}
	for _, key := range []string{"input_voltage[0]", "input_voltage[4]", "input_voltage[01]", "input_voltage[1][2]", "battery_voltage[1]", "input_voltage]", "other"} {
		if _, ok := Lookup(key); ok {
			t.Errorf("接受非法字段 %s", key)
		}
	}
	for _, key := range []string{"power_state", "comm_state", "runtime_source", "on_battery_elapsed", "runtime_estimate"} {
		spec, _ := Lookup(key)
		if !spec.Derived {
			t.Errorf("未标派生 %s", key)
		}
	}
	var zero Field
	if zero.Quality == Good {
		t.Fatal("零值不得视为可信字段")
	}
}

func TestCatalogCopies(t *testing.T) {
	all := Catalog()
	for i := range all {
		if all[i].Key == "ups_mode" {
			all[i].Enum[0] = "changed"
		}
	}
	spec, _ := Lookup("ups_mode")
	spec.Enum[0] = "changed-again"
	fresh, _ := Lookup("ups_mode")
	if fresh.Enum[0] != "power_on" {
		t.Fatal("目录被外部修改")
	}
}

func TestResolve(t *testing.T) {
	fields := map[string]Field{
		"output_load_pct[1]": {Num: 30, Quality: Good},
		"output_load_pct[2]": {Num: 90, Quality: Stale},
		"output_load_pct[3]": {Num: 40, Quality: Good},
	}
	if got := Resolve(fields, "output_load_pct"); got.Num != 40 || got.Quality != Good {
		t.Fatalf("聚合错误: %+v", got)
	}
	if got := Resolve(fields, "output_load_pct[2]"); got.Quality != Stale {
		t.Fatal("显式字段质量丢失")
	}
	for _, key := range []string{"unknown", "battery_voltage", "input_voltage"} {
		if Resolve(fields, key).Quality == Good {
			t.Errorf("缺值误为Good: %s", key)
		}
	}
	fields["output_load_pct[1]"] = Field{Quality: Invalid}
	fields["output_load_pct[3]"] = Field{Quality: Unsupported}
	if Resolve(fields, "output_load_pct").Quality == Good {
		t.Fatal("没有Good相仍聚合成功")
	}
	if !IsPowerIndicator("input_voltage") || IsPowerIndicator("input_voltage[1]") || IsPowerIndicator("power_state") {
		t.Fatal("供电依据错误")
	}
}

func TestEventSeverities(t *testing.T) {
	for event, want := range map[string]Severity{EventMainsLost: SeverityCritical, EventMainsRestored: SeverityInfo, EventCommLost: SeverityWarning, EventBatteryDepleted: SeverityCritical, EventSelfTestResult: SeverityInfo} {
		got, ok := EventSeverity(event)
		if !ok || got != want {
			t.Errorf("%s: %s", event, got)
		}
	}
	if _, ok := EventSeverity("unknown"); ok {
		t.Fatal("未知事件不应有默认级别")
	}
}
