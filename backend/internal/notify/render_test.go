package notify

import (
	"encoding/json"
	"html"
	"math"
	"strings"
	"testing"
	"time"

	"ups-warden/internal/model"
)

func TestRenderEscaping(t *testing.T) {
	injection := "\"},\"injected\":true,\"x\":\"\\\n\r\t<script>&'\u2028\u2029{{event.type}}"
	vars := map[string]any{"device.name": injection, "event.type": "must-not-recurse"}
	got, err := Render(`{"device":{"name":"{{device.name}}"}}`, "json", vars)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Device map[string]string `json:"device"`
	}
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Device) != 1 || decoded.Device["name"] != injection {
		t.Fatalf("JSON 未安全保留变量: %q", got)
	}
	for _, format := range []string{"html", "text"} {
		t.Run(format, func(t *testing.T) {
			got, err := Render("<b>{{device.name}}</b>", format, vars)
			if err != nil {
				t.Fatal(err)
			}
			want := "<b>" + injection + "</b>"
			if format == "html" {
				want = "<b>" + html.EscapeString(injection) + "</b>"
			}
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
	for _, body := range []string{`{"nested":{"ok":true}}`, `{"a":{},"b":{"name":"{{device.name}}"}}`} {
		if _, err := Render(body, "json", vars); err != nil {
			t.Fatalf("合法嵌套 JSON 被拒绝: %v", err)
		}
	}
}

func TestRenderRejectsInvalidTokens(t *testing.T) {
	for _, body := range []string{
		"{{unknown_secret}}", "{{device.name", "{{device.name}", "{{}}", "{{ device.name}}",
		"{{device.name }}", "{{.device.name}}", "{{device..name}}", "{{device.name()}}",
		"{{device.name | secret}}", "{{if device.name}}", "{{{device.name}}}", "secret }}",
		"{{device.name}}{{broken", "{{input_voltage[0]}}", "{{input_voltage[01]}}",
		"{{input_voltage[4]}}", "{{input_voltage[-1]}}", "{{input_voltage[1}}", "{{1bad}}",
	} {
		t.Run(body, func(t *testing.T) {
			got, err := Render(body, "text", map[string]any{"device.name": "secret"})
			if got != "" || err == nil {
				t.Fatalf("破损或未知 token 被接受: %q, %v", got, err)
			}
			assertSafeTemplateError(t, err)
		})
	}
	for _, body := range []string{`{"value":{{device.name}}}`, `{"value":"{{device.name}}"`, `not JSON`} {
		if _, err := Render(body, "json", map[string]any{"device.name": "secret"}); err == nil {
			t.Fatalf("无效 JSON 被接受: %s", body)
		}
	}
}

func TestRenderScalarsAndNoExecution(t *testing.T) {
	values := []struct {
		value any
		want  string
	}{
		{true, "true"}, {false, "false"}, {float64(12.5), "12.5"}, {float32(1.5), "1.5"},
		{int(1), "1"}, {int64(2), "2"}, {int32(3), "3"}, {int16(4), "4"}, {int8(5), "5"},
		{uint(6), "6"}, {uint64(7), "7"}, {uint32(8), "8"}, {uint16(9), "9"}, {uint8(10), "10"},
		{"battery", "battery"},
	}
	for _, tc := range values {
		got, err := Render("{{value}}", "text", map[string]any{"value": tc.value})
		if err != nil || got != tc.want {
			t.Fatalf("scalar %T: got %q, %v, want %q", tc.value, got, err, tc.want)
		}
	}
	for _, value := range []any{nil, map[string]any{}, []string{}, func() string { panic("不得执行") }, panicTemplateStringer{}, math.NaN(), math.Inf(1), float32(math.Inf(-1))} {
		got, err := Render("{{value}}", "text", map[string]any{"value": value})
		if err == nil || got != "" {
			t.Fatalf("不支持的值 %T 未被拒绝", value)
		}
		assertSafeTemplateError(t, err)
	}
}

type panicTemplateStringer struct{}

func (panicTemplateStringer) String() string { panic("不得调用 Stringer") }

func TestVariablesUnifiedModel(t *testing.T) {
	at := time.Date(2026, 10, 15, 12, 30, 0, 0, time.FixedZone("local", 8*3600))
	since := at.Add(-750 * time.Second)
	state := model.DeviceState{
		DeviceID: 42, PowerState: "on_battery", CommState: "lost", OnBatterySince: &since,
		Fields: map[string]model.Field{
			"input_voltage[1]":          {Num: 220.5, Str: "错误类型", Quality: model.Good},
			"input_voltage[2]":          {Num: 230, Quality: model.Stale},
			"battery_capacity_pct":      {Num: 50, Quality: model.Good},
			"mains_ok":                  {Bool: false, Num: 999, Quality: model.Good},
			"low_battery":               {Bool: true, Quality: model.Good},
			"ups_mode":                  {Str: "battery", Quality: model.Good},
			"manufacturer":              {Str: "制造商", Quality: model.Good},
			"power_state":               {Str: "on_battery", Quality: model.Good},
			"comm_state":                {Str: "lost", Quality: model.Good},
			"runtime_source":            {Str: "native", Quality: model.Good},
			"on_battery_elapsed":        {Num: 1, Quality: model.Good},
			"runtime_estimate":          {Num: 750, Quality: model.Good},
			"native_runtime_remaining":  {Num: 3661.9, Quality: model.Good},
			"native_seconds_on_battery": {Num: 0, Quality: model.Good},
			"temperature":               {Num: 99, Quality: model.Invalid},
			"rated_va":                  {Num: 1000, Quality: model.Unsupported},
			"protocol_secret":           {Str: "不得泄露", Quality: model.Good},
		},
	}
	event := model.Event{DeviceID: 99, Type: model.EventMainsLost, Severity: model.SeverityCritical}
	vars := Variables("机房 UPS", event, state, at)
	want := map[string]any{
		"device.name": "机房 UPS", "device.id": int64(42), "event.type": model.EventMainsLost,
		"event.severity": "critical", "time": "2026-10-15T12:30:00+08:00",
		"input_voltage[1]": float64(220.5), "input_voltage[2]": "—", "input_voltage[3]": "—",
		"battery_capacity_pct": float64(50), "mains_ok": false, "low_battery": true,
		"ups_mode": "battery", "manufacturer": "制造商", "power_state": "on_battery", "comm_state": "lost",
		"runtime_source": "native", "on_battery_elapsed": float64(750), "on_battery_elapsed_text": "12 分 30 秒",
		"runtime_estimate": float64(750), "runtime_estimate_text": "12 分 30 秒",
		"native_runtime_remaining_text": "1 小时 1 分 1 秒", "native_seconds_on_battery_text": "0 秒",
		"temperature": "—", "rated_va": "—",
	}
	for key, value := range want {
		if vars[key] != value {
			t.Errorf("%s = %#v, want %#v", key, vars[key], value)
		}
	}
	if _, exists := vars["protocol_secret"]; exists {
		t.Fatal("目录外字段泄露")
	}
	for _, key := range model.Keys() {
		if _, exists := vars[key]; !exists {
			t.Errorf("缺少统一字段 %s", key)
		}
	}
	got, err := Render("{{input_voltage[1]}} {{mains_ok}} {{ups_mode}}", "text", vars)
	if err != nil || got != "220.5 false battery" {
		t.Fatalf("按相和类型渲染: %q, %v", got, err)
	}
	if state.Fields["on_battery_elapsed"].Num != 1 {
		t.Fatal("Variables 修改了输入状态")
	}
}

func TestVariablesQualityAndElapsed(t *testing.T) {
	at := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	for _, quality := range []model.Quality{model.Stale, model.Invalid, model.Unsupported, ""} {
		state := model.DeviceState{Fields: map[string]model.Field{
			"runtime_estimate": {Num: 750, Quality: quality},
			"mains_ok":         {Bool: true, Quality: quality},
			"ups_mode":         {Str: "battery", Quality: quality},
		}}
		vars := Variables("", model.Event{}, state, at)
		for _, key := range []string{"runtime_estimate", "runtime_estimate_text", "mains_ok", "ups_mode"} {
			if vars[key] != "—" {
				t.Fatalf("quality %q 字段 %s = %#v", quality, key, vars[key])
			}
		}
	}
	future := at.Add(time.Minute)
	vars := Variables("", model.Event{DeviceID: 9}, model.DeviceState{PowerState: "on_battery", OnBatterySince: &future}, at)
	if vars["on_battery_elapsed"] != float64(0) || vars["on_battery_elapsed_text"] != "0 秒" || vars["device.id"] != int64(9) {
		t.Fatalf("未来计时或设备 ID 回退错误: %#v", vars)
	}
	vars = Variables("", model.Event{}, model.DeviceState{PowerState: "unknown", OnBatterySince: &future}, at)
	if vars["on_battery_elapsed"] != "—" {
		t.Fatal("unknown 状态不得伪造计时")
	}
	for _, number := range []float64{math.NaN(), math.Inf(1), -1} {
		vars = Variables("", model.Event{}, model.DeviceState{Fields: map[string]model.Field{"runtime_estimate": {Num: number, Quality: model.Good}}}, at)
		if vars["runtime_estimate"] != "—" || vars["runtime_estimate_text"] != "—" {
			t.Fatalf("无效时长未被过滤: %v", number)
		}
	}
	for _, tc := range []struct {
		seconds float64
		want    string
	}{{0, "0 秒"}, {59.9, "59 秒"}, {60, "1 分 0 秒"}, {750, "12 分 30 秒"}, {90061, "1 天 1 小时 1 分 1 秒"}} {
		if got := durationText(tc.seconds); got != tc.want {
			t.Errorf("durationText(%v) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}

func TestAllDefaultTemplates(t *testing.T) {
	templates := DefaultTemplates()
	if len(templates) != 16*3 {
		t.Fatalf("默认模板数量 = %d", len(templates))
	}
	seen := make(map[string]bool)
	for _, template := range templates {
		key := template.EventType + "/" + template.Format
		if seen[key] {
			t.Fatalf("重复模板 %s", key)
		}
		seen[key] = true
		if err := ValidateTemplate(template.EventType, template.Format, template.Body); err != nil {
			t.Fatalf("默认模板 %s 保存校验失败: %v", key, err)
		}
		severity, known := model.EventSeverity(template.EventType)
		if !known && template.EventType != "test" {
			t.Fatalf("未知默认事件 %s", template.EventType)
		}
		vars := Variables("\"<b>UPS</b>\\\n", model.Event{Type: template.EventType, Severity: severity}, model.DeviceState{}, time.Now())
		body, err := Render(template.Body, template.Format, vars)
		if err != nil || body == "" {
			t.Fatalf("默认模板 %s 渲染失败: %v", key, err)
		}
		got, err := DefaultTemplate(template.EventType, template.Format)
		if err != nil || got != template.Body {
			t.Fatalf("默认模板查询 %s 不一致", key)
		}
	}
	templates[0].Body = "不得改变后续副本"
	if DefaultTemplates()[0].Body == templates[0].Body {
		t.Fatal("默认模板副本共享")
	}
}

func TestValidateTemplate(t *testing.T) {
	for _, tc := range []struct {
		event, format, body string
	}{
		{"secret_event", "text", "secret_body"},
		{"test", "secret_format", "secret_body"},
		{"test", "text", "{{secret_variable}}"},
		{"test", "text", "{{device.name"},
		{"test", "json", `{"n":{{battery_capacity_pct}}}`},
		{"test", "json", `{"n":"{{battery_capacity_pct}}",}`},
		{"test", "text", "{{battery_capacity_pct[1]}}"},
		{"test", "text", "{{input_voltage}}"},
		{"test", "text", strings.Repeat("x", maxTemplateBytes+1)},
	} {
		if err := ValidateTemplate(tc.event, tc.format, tc.body); err == nil {
			t.Fatalf("非法模板被接受: %+v", tc)
		} else {
			assertSafeTemplateError(t, err)
		}
	}
	for _, format := range []string{"json", "html", "text"} {
		body := "{{device.name}} {{input_voltage[1]}} {{runtime_estimate_text}}"
		if format == "json" {
			body = `{"n":"{{device.name}}","phase":"{{input_voltage[1]}}","runtime":"{{runtime_estimate_text}}"}`
		}
		if err := ValidateTemplate("test", format, body); err != nil {
			t.Fatalf("有效 %s 模板被拒绝: %v", format, err)
		}
	}
	for _, pair := range [][2]string{{"secret", "text"}, {"test", "secret"}} {
		if _, err := DefaultTemplate(pair[0], pair[1]); err == nil {
			t.Fatal("非法默认模板查询被接受")
		} else {
			assertSafeTemplateError(t, err)
		}
	}
}

func TestRenderSizeLimits(t *testing.T) {
	for _, tc := range []struct {
		body, format string
		vars         map[string]any
		valid        bool
	}{
		{strings.Repeat("x", maxTemplateBytes), "text", nil, true},
		{strings.Repeat("x", maxTemplateBytes+1), "text", nil, false},
		{"{{value}}", "text", map[string]any{"value": strings.Repeat("x", maxTemplateBytes)}, true},
		{"{{value}}", "text", map[string]any{"value": strings.Repeat("x", maxTemplateBytes+1)}, false},
		{"{{value}}{{value}}", "text", map[string]any{"value": strings.Repeat("x", maxTemplateBytes/2+1)}, false},
		{"{{value}}", "html", map[string]any{"value": strings.Repeat("<", maxTemplateBytes/2)}, false},
		{`"{{value}}"`, "json", map[string]any{"value": strings.Repeat("\"", maxTemplateBytes/2)}, false},
		{"{{value}}", "text", map[string]any{"value": strings.Repeat("中", maxTemplateBytes/3+1)}, false},
	} {
		got, err := Render(tc.body, tc.format, tc.vars)
		if (err == nil) != tc.valid || len(got) > maxTemplateBytes {
			t.Fatalf("size body=%d format=%s: output=%d err=%v valid=%v", len(tc.body), tc.format, len(got), err, tc.valid)
		}
		if err != nil {
			assertSafeTemplateError(t, err)
		}
	}
}

func assertSafeTemplateError(t *testing.T, err error) {
	t.Helper()
	switch err.Error() {
	case "通知模板事件类型无效", "通知模板格式无效", "通知模板或输出超过大小限制", "通知模板语法无效", "通知模板变量无效", "通知模板 JSON 输出无效":
	default:
		t.Fatalf("错误不是固定安全文本: %q", err.Error())
	}
}

func FuzzRender(f *testing.F) {
	for _, seed := range []string{"{{device.name}}", `{"name":"{{device.name}}"}`, "{{input_voltage[1]}}", "{{runtime_estimate_text}}", "{{broken", "}}", "{{device.name()}}", `{"a":{}}`} {
		f.Add(seed)
	}
	// 固定变量，只对模板输入做 fuzz；不使用设备、数据库或网络。
	vars := Variables("\"<UPS>&\\\n{{other}}", model.Event{Type: "test", Severity: model.SeverityInfo}, model.DeviceState{}, time.Unix(0, 0).UTC())
	f.Fuzz(func(t *testing.T, body string) {
		for _, format := range []string{"json", "html", "text"} {
			got, err := Render(body, format, vars)
			if err != nil {
				assertSafeTemplateError(t, err)
				if got != "" {
					t.Fatal("错误时不得返回部分输出")
				}
				continue
			}
			if len(got) > maxTemplateBytes || (format == "json" && !json.Valid([]byte(got))) {
				t.Fatal("成功结果违反大小或 JSON 契约")
			}
		}
	})
}
