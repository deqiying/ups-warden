package model

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// FieldSpec 是统一字段目录中的类型、单位、相序和存储契约。
type FieldSpec struct {
	Key     string
	Kind    Kind
	Unit    string
	Phased  bool
	Enum    []string
	History bool
	Derived bool
	Group   string
}

var catalog = buildCatalog()

func buildCatalog() []FieldSpec {
	var fields []FieldSpec
	add := func(group string, kind Kind, unit string, phased, history, derived bool, keys ...string) {
		for _, key := range keys {
			fields = append(fields, FieldSpec{Key: key, Kind: kind, Unit: unit, Phased: phased, History: history, Derived: derived, Group: group})
		}
	}
	add("ident", Text, "", false, false, false, "manufacturer", "model", "firmware", "agent_version", "serial", "name")
	add("nominal", Number, "V", false, false, false, "nominal_input_voltage", "nominal_output_voltage", "nominal_battery_voltage")
	add("nominal", Number, "Hz", false, false, false, "nominal_input_freq", "nominal_output_freq")
	add("nominal", Number, "VA", false, false, false, "rated_va")
	add("nominal", Number, "W", false, false, false, "rated_w")
	add("nominal", Number, "", false, false, false, "input_phases", "output_phases")
	for _, group := range []string{"input", "output", "bypass"} {
		add(group, Number, "V", true, true, false, group+"_voltage")
		add(group, Number, "A", true, true, false, group+"_current")
		unit := "VA"
		if group == "input" {
			unit = "W"
		}
		add(group, Number, unit, true, true, false, group+"_power")
		add(group, Number, "Hz", false, true, false, group+"_freq")
	}
	add("output", Number, "%", true, true, false, "output_load_pct")
	add("output", Enum, "", false, false, false, "output_source")
	add("battery", Enum, "", false, false, false, "battery_status")
	add("battery", Number, "%", false, true, false, "battery_capacity_pct")
	add("battery", Number, "V", false, true, false, "battery_voltage")
	add("battery", Number, "A", false, true, false, "battery_current")
	add("battery", Number, "°C", false, true, false, "temperature")
	add("battery", Duration, "s", false, true, false, "native_seconds_on_battery", "native_runtime_remaining")
	add("flags", Bool, "", false, false, false, "mains_ok", "low_battery", "overload", "shutting_down", "buzzer_muted")
	add("flags", Enum, "", false, false, false, "ups_mode", "ups_type")
	// 需求尚未确定自检位的枚举含义；使用文本保留统一字段，内置 Profile 暂不映射。
	add("flags", Text, "", false, false, false, "self_test_result")
	add("derived", Enum, "", false, false, true, "power_state", "runtime_source", "comm_state")
	add("derived", Duration, "s", false, true, true, "on_battery_elapsed", "runtime_estimate")
	enums := map[string][]string{
		"output_source":  {"other", "none", "normal", "bypass", "battery", "booster", "reducer", "unknown"},
		"battery_status": {"normal", "low", "depleted", "unknown"},
		"ups_mode":       {"power_on", "standby", "bypass", "line", "battery", "test", "fault", "converter", "eco", "shutdown", "unknown"},
		"ups_type":       {"online", "backup", "unknown"},
		"power_state":    {"online", "on_battery", "bypass", "fault", "shutdown", "unknown"},
		"runtime_source": {"native", "estimated", "none", "unknown"},
		"comm_state":     {"ok", "lost", "unknown"},
	}
	for i := range fields {
		fields[i].Enum = enums[fields[i].Key]
	}
	slices.SortFunc(fields, func(a, b FieldSpec) int { return strings.Compare(a.Key, b.Key) })
	return fields
}

// Catalog 返回完整静态目录的独立副本，调用方不能修改全局契约。
func Catalog() []FieldSpec {
	result := slices.Clone(catalog)
	for i := range result {
		result[i].Enum = slices.Clone(result[i].Enum)
	}
	return result
}

// Lookup 接受基础字段名或合法的按相实例名；相号仅允许规范的 [1]..[3]。
// Profile 映射必须另外要求按相字段含相号；不带相号只用于规则聚合与 PowerHints。
func Lookup(key string) (FieldSpec, bool) {
	base, phase, valid := splitField(key)
	if !valid {
		return FieldSpec{}, false
	}
	index, found := slices.BinarySearchFunc(catalog, base, func(s FieldSpec, value string) int { return strings.Compare(s.Key, value) })
	if !found || (phase != 0 && !catalog[index].Phased) {
		return FieldSpec{}, false
	}
	spec := catalog[index]
	spec.Enum = slices.Clone(spec.Enum)
	return spec, true
}

// Phase 返回实例相号；非按相实例或非法字段返回 0。
func Phase(key string) int {
	_, phase, valid := splitField(key)
	if !valid {
		return 0
	}
	if spec, ok := Lookup(key); !ok || !spec.Phased {
		return 0
	}
	return phase
}

func splitField(key string) (string, int, bool) {
	index := strings.IndexByte(key, '[')
	if index < 0 {
		return key, 0, !strings.ContainsRune(key, ']')
	}
	tail := key[index:]
	if len(tail) != 3 || tail[0] != '[' || tail[2] != ']' || tail[1] < '1' || tail[1] > '3' {
		return "", 0, false
	}
	return key[:index], int(tail[1] - '0'), true
}

// Keys 展开所有按相字段为 [1]..[3]，返回稳定顺序的字段实例键。
func Keys() []string {
	var keys []string
	for _, spec := range catalog {
		if spec.Phased {
			for phase := 1; phase <= 3; phase++ {
				keys = append(keys, spec.Key+"["+strconv.Itoa(phase)+"]")
			}
		} else {
			keys = append(keys, spec.Key)
		}
	}
	return keys
}

// IsPowerIndicator 判断统一字段是否可作为供电判定依据，不允许派生状态反向参与判断。
func IsPowerIndicator(key string) bool {
	switch key {
	case "ups_mode", "mains_ok", "output_source", "native_seconds_on_battery", "input_voltage":
		return true
	}
	return false
}

// Resolve 返回显式字段，或按相字段所有 Good 相的最大值；无 Good 相时返回非 Good。
func Resolve(fields map[string]Field, key string) Field {
	spec, ok := Lookup(key)
	if !ok {
		return Field{Quality: Unsupported, Reason: ReasonNotMapped}
	}
	if !spec.Phased || Phase(key) > 0 {
		if field, ok := fields[key]; ok {
			return field
		}
		return Field{Quality: Unsupported, Reason: ReasonNotMapped}
	}
	result := Field{Quality: Unsupported, Reason: ReasonNotMapped}
	for phase := 1; phase <= 3; phase++ {
		field, ok := fields[fmt.Sprintf("%s[%d]", key, phase)]
		if ok && field.Quality == Good && (result.Quality != Good || field.Num > result.Num) {
			result = field
		}
	}
	return result
}
