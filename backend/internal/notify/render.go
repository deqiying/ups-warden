package notify

import (
	"encoding/json"
	"errors"
	"html"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"ups-warden/internal/model"
)

const maxTemplateBytes = 64 * 1024

var (
	errTemplateEvent  = errors.New("通知模板事件类型无效")
	errTemplateFormat = errors.New("通知模板格式无效")
	errTemplateSize   = errors.New("通知模板或输出超过大小限制")
	errTemplateSyntax = errors.New("通知模板语法无效")
	errTemplateValue  = errors.New("通知模板变量无效")
	errTemplateJSON   = errors.New("通知模板 JSON 输出无效")
)

// Template 是按事件和输出格式保存的无逻辑通知模板。
type Template struct {
	EventType string `json:"event_type"`
	Format    string `json:"format"`
	Body      string `json:"body"`
}

// planEvents 是关机计划通知事件；正文通过 {{detail}} 携带计划名称、运行 ID 与原因。
var planEvents = []string{"plan_started", "plan_step_failed", "plan_cancelled", "plan_completed", "plan_resumed", "plan_aborted"}

var templateEvents = append([]string{
	model.EventMainsLost, model.EventMainsRestored,
	model.EventBatteryLow, model.EventBatteryLowCleared, model.EventBatteryDepleted,
	model.EventCommLost, model.EventCommRestored,
	model.EventOverload, model.EventOverloadCleared,
	model.EventOnBypass, model.EventBypassCleared,
	model.EventUPSFault, model.EventUPSShuttingDown,
	model.EventTemperatureHigh, model.EventSelfTestResult, "test",
	"rule_triggered", "rule_recovered",
}, planEvents...)

// DefaultTemplates 返回全部已知事件和测试事件的 JSON、HTML、纯文本模板的独立副本。
func DefaultTemplates() []Template {
	result := make([]Template, 0, len(templateEvents)*3)
	for _, eventType := range templateEvents {
		for _, format := range []string{"json", "html", "text"} {
			body, _ := DefaultTemplate(eventType, format) // 固定目录中的事件和格式均合法。
			result = append(result, Template{EventType: eventType, Format: format, Body: body})
		}
	}
	return result
}

// DefaultTemplate 返回事件对应的默认模板；不接受未知事件或格式。
func DefaultTemplate(eventType, format string) (string, error) {
	if !knownTemplateEvent(eventType) {
		return "", errTemplateEvent
	}
	if !knownTemplateFormat(format) {
		return "", errTemplateFormat
	}
	label := map[string]string{
		model.EventMainsLost: "市电中断", model.EventMainsRestored: "市电恢复",
		model.EventBatteryLow: "电池电量低", model.EventBatteryLowCleared: "电池低电量解除",
		model.EventBatteryDepleted: "电池耗尽", model.EventCommLost: "通讯中断",
		model.EventCommRestored: "通讯恢复", model.EventOverload: "UPS 过载",
		model.EventOverloadCleared: "UPS 过载解除", model.EventOnBypass: "UPS 旁路供电",
		model.EventBypassCleared: "UPS 旁路解除", model.EventUPSFault: "UPS 故障",
		model.EventUPSShuttingDown: "UPS 正在关机", model.EventTemperatureHigh: "UPS 温度过高",
		model.EventSelfTestResult: "UPS 自检结果", "test": "通知测试",
		"rule_triggered": "规则条件命中", "rule_recovered": "规则条件恢复",
		"plan_started": "关机计划开始", "plan_step_failed": "关机计划步骤失败",
		"plan_cancelled": "关机计划已取消", "plan_completed": "关机计划已结束",
		"plan_resumed": "关机计划重启后继续", "plan_aborted": "关机计划重启后终止",
	}[eventType]
	if slices.Contains(planEvents, eventType) {
		switch format {
		case "json":
			return `{"title":"` + label + `","detail":"{{detail}}","device":{"id":"{{device.id}}","name":"{{device.name}}"},"event":{"type":"{{event.type}}","severity":"{{event.severity}}"},"time":"{{time}}"}`, nil
		case "html":
			return "<b>" + label + "</b>\n{{detail}}\n设备：{{device.name}}\n时间：{{time}}", nil
		default:
			return label + "\n{{detail}}\n设备：{{device.name}}\n时间：{{time}}", nil
		}
	}
	switch format {
	case "json":
		return `{"title":"` + label + `","device":{"id":"{{device.id}}","name":"{{device.name}}"},"event":{"type":"{{event.type}}","severity":"{{event.severity}}"},"battery_capacity_pct":"{{battery_capacity_pct}}","on_battery_elapsed":"{{on_battery_elapsed_text}}","runtime_estimate":"{{runtime_estimate_text}}","time":"{{time}}"}`, nil
	case "html":
		return "<b>" + label + "</b>\n设备：{{device.name}}\n事件：{{event.type}}（{{event.severity}}）\n电量：{{battery_capacity_pct}}%\n电池供电：{{on_battery_elapsed_text}}\n预计续航：{{runtime_estimate_text}}\n时间：{{time}}", nil
	default:
		return label + "\n设备：{{device.name}}\n事件：{{event.type}}（{{event.severity}}）\n电量：{{battery_capacity_pct}}%\n电池供电：{{on_battery_elapsed_text}}\n预计续航：{{runtime_estimate_text}}\n时间：{{time}}", nil
	}
}

func knownTemplateEvent(eventType string) bool {
	_, ok := model.EventSeverity(eventType)
	return ok || eventType == "test" || eventType == "rule_triggered" || eventType == "rule_recovered" || slices.Contains(planEvents, eventType)
}

func knownTemplateFormat(format string) bool {
	return format == "json" || format == "html" || format == "text"
}

// ValidateTemplate 在保存边界检查事件、格式、变量、语法、大小和 JSON 有效性。
// 使用包含引号、反斜线、控制字符的占位值，避免仅用简单值验证而漏掉转义问题。
func ValidateTemplate(eventType, format, body string) error {
	if !knownTemplateEvent(eventType) {
		return errTemplateEvent
	}
	vars := Variables("", model.Event{}, model.DeviceState{}, time.Time{})
	for key := range vars {
		vars[key] = "占位\"\\\n\r\t<>&'"
	}
	_, err := Render(body, format, vars)
	return err
}

// Render 仅替换严格的 {{path}}，不执行函数或方法；错误不含模板和变量内容。
// JSON 变量只转义字符串内容，模板必须自行提供双引号；输出按字节限制为 64 KiB。
func Render(body, format string, vars map[string]any) (string, error) {
	if !knownTemplateFormat(format) {
		return "", errTemplateFormat
	}
	if len(body) > maxTemplateBytes {
		return "", errTemplateSize
	}
	var out strings.Builder
	appendPart := func(part string) bool {
		if len(part) > maxTemplateBytes-out.Len() {
			return false
		}
		out.WriteString(part)
		return true
	}
	for len(body) > 0 {
		start := strings.Index(body, "{{")
		end := strings.Index(body, "}}")
		// JSON 的嵌套对象可合法以 }} 结尾，交由最终 JSON 校验区分。
		if format != "json" && end >= 0 && (start < 0 || end < start) {
			return "", errTemplateSyntax
		}
		if start < 0 {
			if !appendPart(body) {
				return "", errTemplateSize
			}
			break
		}
		if !appendPart(body[:start]) {
			return "", errTemplateSize
		}
		tail := body[start+2:]
		close := strings.Index(tail, "}}")
		if close < 0 || !validTemplatePath(tail[:close]) {
			return "", errTemplateSyntax
		}
		value, ok := vars[tail[:close]]
		if !ok {
			return "", errTemplateValue
		}
		text, err := templateScalar(value)
		if err != nil {
			return "", err
		}
		if len(text) > maxTemplateBytes {
			return "", errTemplateSize
		}
		switch format {
		case "json":
			encoded, _ := json.Marshal(text) // string 的 JSON 编码不会失败。
			text = string(encoded[1 : len(encoded)-1])
		case "html":
			text = html.EscapeString(text)
		}
		if !appendPart(text) {
			return "", errTemplateSize
		}
		body = tail[close+2:]
	}
	result := out.String()
	if format == "json" && !json.Valid([]byte(result)) {
		return "", errTemplateJSON
	}
	return result, nil
}

func validTemplatePath(path string) bool {
	if path == "" {
		return false
	}
	for _, segment := range strings.Split(path, ".") {
		if len(segment) >= 3 && segment[len(segment)-3] == '[' {
			if segment[len(segment)-2] < '1' || segment[len(segment)-2] > '3' || segment[len(segment)-1] != ']' {
				return false
			}
			segment = segment[:len(segment)-3]
		}
		if segment == "" {
			return false
		}
		for i, char := range segment {
			if char != '_' && !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') && !(i > 0 && char >= '0' && char <= '9') {
				return false
			}
		}
	}
	return true
}

func templateScalar(value any) (string, error) {
	// 不用 fmt.Sprint：调用方传入的 Stringer 或函数不能成为模板执行入口。
	switch v := value.(type) {
	case string:
		return v, nil
	case bool:
		return strconv.FormatBool(v), nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case int32:
		return strconv.FormatInt(int64(v), 10), nil
	case int16:
		return strconv.FormatInt(int64(v), 10), nil
	case int8:
		return strconv.FormatInt(int64(v), 10), nil
	case uint:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint64:
		return strconv.FormatUint(v, 10), nil
	case uint32:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint16:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint8:
		return strconv.FormatUint(uint64(v), 10), nil
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			return strconv.FormatFloat(v, 'f', -1, 64), nil
		}
	case float32:
		if !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) {
			return strconv.FormatFloat(float64(v), 'f', -1, 32), nil
		}
	}
	return "", errTemplateValue
}

// Variables 从统一模型生成扁平路径变量，不读取协议数据；非 Good 字段显示为 —。
// 时间保持 at 的时区，计时在消费时刷新；每个 Duration 字段均提供中文 _text 变体。
// detail 默认为空，仅计划通知由 Request.Detail 填充。
func Variables(deviceName string, event model.Event, state model.DeviceState, at time.Time) map[string]any {
	deviceID := state.DeviceID
	if deviceID == 0 {
		deviceID = event.DeviceID
	}
	vars := map[string]any{
		"device.name": deviceName, "device.id": deviceID,
		"event.type": event.Type, "event.severity": string(event.Severity),
		"time": at.Format(time.RFC3339), "detail": "",
	}
	for _, key := range model.Keys() {
		spec, _ := model.Lookup(key)
		field := model.Resolve(state.Fields, key)
		// 状态引擎不按秒发布，失联 hold 期间仍按调用时刻延续放电计时。
		if key == "on_battery_elapsed" && state.OnBatterySince != nil && state.PowerState != "unknown" {
			field = model.Field{Num: math.Max(0, math.Floor(at.Sub(*state.OnBatterySince).Seconds())), Quality: model.Good}
		}
		var value any = "—"
		if field.Quality == model.Good {
			switch spec.Kind {
			case model.Number, model.Duration:
				if !math.IsNaN(field.Num) && !math.IsInf(field.Num, 0) && (spec.Kind != model.Duration || field.Num >= 0) {
					value = field.Num
				}
			case model.Bool:
				value = field.Bool
			case model.Enum, model.Text:
				value = field.Str
			}
		}
		vars[key] = value
		if spec.Kind == model.Duration {
			vars[key+"_text"] = "—"
			if seconds, ok := value.(float64); ok {
				vars[key+"_text"] = durationText(seconds)
			}
		}
	}
	return vars
}

func durationText(seconds float64) string {
	// 使用浮点整除，避免异常大的统一值转换 int64 溢出。
	seconds = math.Floor(seconds)
	parts := make([]string, 0, 4)
	for _, unit := range []struct {
		seconds float64
		label   string
	}{{86400, "天"}, {3600, "小时"}, {60, "分"}} {
		count := math.Floor(seconds / unit.seconds)
		if count > 0 {
			parts = append(parts, strconv.FormatFloat(count, 'f', -1, 64)+" "+unit.label)
			seconds = math.Mod(seconds, unit.seconds)
		}
	}
	parts = append(parts, strconv.FormatFloat(seconds, 'f', -1, 64)+" 秒")
	return strings.Join(parts, " ")
}
