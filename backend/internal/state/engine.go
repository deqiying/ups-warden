package state

import (
	"math"
	"slices"
	"time"

	"ups-warden/internal/model"
)

type capacityPoint struct {
	At    time.Time
	Value float64
}

// Engine 是单设备纯内存状态机；调用者须串行调用，以传入时间驱动，不执行 I/O。
// 持久化与 actor 使用候选副本，事务失败不污染已提交状态。
type Engine struct {
	config        Config
	view          model.DeviceState
	flags         map[string]bool
	causes        map[string]bool
	failures      int
	lostSince     *time.Time
	heldPower     string
	pendingTarget string
	samples       []capacityPoint
	lastResult    time.Time
	lastAdvance   time.Time
}

// NewEngine 创建 unknown 状态；首次有效 online 只建立基线，不报恢复事件。
func NewEngine(deviceID int64, cfg Config) (*Engine, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Engine{config: cfg, view: model.DeviceState{DeviceID: deviceID, SchemaVersion: model.SchemaVersion, Fields: map[string]model.Field{}, PowerState: "unknown", PowerStateRaw: "unknown", CommState: "unknown", RuntimeSource: "none"}, flags: map[string]bool{}, causes: map[string]bool{}}, nil
}

// Snapshot 返回独立视图。
func (e *Engine) Snapshot() model.DeviceState { return model.CloneState(e.view) }
func (e *Engine) clone() *Engine {
	n := *e
	n.view = model.CloneState(e.view)
	n.flags = map[string]bool{}
	for k, v := range e.flags {
		n.flags[k] = v
	}
	n.causes = map[string]bool{}
	for k, v := range e.causes {
		n.causes[k] = v
	}
	n.samples = slices.Clone(e.samples)
	if e.lostSince != nil {
		v := *e.lostSince
		n.lostSince = &v
	}
	return &n
}

// Apply 消费一次有效时序的采集结果；乱序结果和错误设备结果不改变状态。
// 失败立即停止未确认的去抖计时，达到阈值后标记 lost 和 stale。
func (e *Engine) Apply(r model.SourceResult) model.DeviceUpdate {
	if r.DeviceID != e.view.DeviceID || r.At.IsZero() || !e.lastResult.IsZero() && !r.At.After(e.lastResult) {
		return model.DeviceUpdate{State: e.Snapshot()}
	}
	e.lastResult = r.At
	now := r.At
	if now.Before(e.lastAdvance) {
		now = e.lastAdvance
	}
	e.lastAdvance = now
	var events []model.Event
	if r.Failed {
		e.failures++
		e.cancelPending()
		e.view.PowerStateRaw = "unknown"
		if e.failures >= e.config.Failures && e.view.CommState != "lost" {
			e.view.CommState = "lost"
			e.lostSince = timePtr(now)
			e.heldPower = e.view.PowerState
			e.alarm(model.EventCommLost, true, false, model.EventCommRestored, now, &events)
		}
	} else {
		e.failures = 0
		if e.view.CommState == "lost" && e.config.LostPolicy == "ignore" {
			e.view.PowerState = e.heldPower
		}
		e.view.CommState = "ok"
		e.lostSince = nil
		e.view.LastOKAt = now
		e.alarm(model.EventCommLost, false, true, model.EventCommRestored, now, &events)
		e.view.Fields = observationFields(r.Snapshot.Fields)
		e.view.PowerStateRaw = DeterminePower(r.Snapshot, e.config.InputThreshold)
		e.power(now, &events)
		if e.view.OnBatterySince != nil && e.view.PowerStateRaw == "on_battery" {
			if v, ok := number(e.view.Fields, "battery_capacity_pct"); ok && v >= 0 && v <= 100 {
				e.samples = append(e.samples, capacityPoint{now, v})
			}
		}
		e.fieldEvents(now, &events)
	}
	e.tick(now, &events)
	e.derived(now)
	e.summarize(events)
	return model.DeviceUpdate{State: e.Snapshot(), Events: events, Sample: !r.Failed, AlertsChanged: len(events) > 0}
}

// Tick 推进确认与放电计时，不生成新的历史测量点。
func (e *Engine) Tick(now time.Time) model.DeviceUpdate {
	if now.Before(e.lastAdvance) {
		now = e.lastAdvance
	}
	e.lastAdvance = now
	var events []model.Event
	e.tick(now, &events)
	e.derived(now)
	e.summarize(events)
	return model.DeviceUpdate{State: e.Snapshot(), Events: events, AlertsChanged: len(events) > 0}
}
func observationFields(fields map[string]model.Field) map[string]model.Field {
	out := map[string]model.Field{}
	for _, key := range model.Keys() {
		spec, _ := model.Lookup(key)
		if spec.Derived {
			continue
		}
		v, ok := fields[key]
		if !ok {
			v = model.Field{Quality: model.Unsupported, Reason: model.ReasonNotMapped}
		}
		if math.IsNaN(v.Num) || math.IsInf(v.Num, 0) {
			v = model.Field{Quality: model.Invalid, Reason: model.ReasonOutOfRange}
		}
		out[key] = v
	}
	return out
}

// DeterminePower 按 Profile 提示或默认顺序选择首个可解释的 good 依据。
func DeterminePower(s model.SourceSnapshot, threshold float64) string {
	indicators := s.Hints.Indicators
	if len(indicators) == 0 {
		indicators = []string{"ups_mode", "mains_ok", "output_source", "native_seconds_on_battery", "input_voltage"}
	}
	for _, key := range indicators {
		f := s.Fields[key]
		if key == "input_voltage" {
			if max, ok := maxPhase(s.Fields, key); ok {
				if max < threshold {
					return "on_battery"
				}
				return "online"
			}
			continue
		}
		if f.Quality != model.Good {
			continue
		}
		switch key {
		case "ups_mode":
			switch f.Str {
			case "battery":
				return "on_battery"
			case "line", "eco", "converter":
				return "online"
			case "bypass", "fault", "shutdown":
				return f.Str
			}
		case "mains_ok":
			if f.Bool {
				return "online"
			}
			return "on_battery"
		case "output_source":
			switch f.Str {
			case "battery":
				return "on_battery"
			case "normal":
				return "online"
			case "bypass":
				return "bypass"
			}
		case "native_seconds_on_battery":
			if !math.IsNaN(f.Num) && !math.IsInf(f.Num, 0) && f.Num >= 0 {
				if f.Num > 0 {
					return "on_battery"
				}
				return "online"
			}
		}
	}
	return "unknown"
}
func (e *Engine) cancelPending() {
	e.view.PendingSince = nil
	e.pendingTarget = ""
	if e.view.OutageCycleID == nil && !e.flags[model.EventMainsLost] {
		e.view.OnBatterySince = nil
		e.samples = nil
	}
}
func (e *Engine) power(now time.Time, events *[]model.Event) {
	raw, current := e.view.PowerStateRaw, e.view.PowerState
	if raw == "unknown" {
		e.cancelPending()
		return
	}
	if raw == "on_battery" {
		if current == "on_battery" {
			e.view.PendingSince = nil
			e.pendingTarget = ""
			return
		}
		if e.pendingTarget != "on_battery" {
			e.pendingTarget = "on_battery"
			e.view.PendingSince = timePtr(now)
			if e.view.OnBatterySince == nil {
				e.view.OnBatterySince = timePtr(now)
			}
		}
		if now.Sub(*e.view.PendingSince) >= seconds(e.config.OutageConfirmSeconds) {
			e.confirmBattery(now, events)
		}
		return
	}
	// 有未结束的周期时，即便显式 fault/bypass 曾改变显示，也必须经过恢复确认。
	if raw == "online" && (current == "on_battery" || e.flags[model.EventMainsLost] || e.view.OutageCycleID != nil) {
		if e.pendingTarget != "online" {
			e.pendingTarget = "online"
			e.view.PendingSince = timePtr(now)
		}
		if now.Sub(*e.view.PendingSince) >= seconds(e.config.RecoveryConfirmSeconds) {
			e.view.PowerState = "online"
			e.view.PendingSince = nil
			e.pendingTarget = ""
			e.view.OnBatterySince = nil
			e.samples = nil
			e.alarm(model.EventMainsLost, false, true, model.EventMainsRestored, now, events)
		}
		return
	}
	e.cancelPending()
	e.view.PowerState = raw
}
func (e *Engine) confirmBattery(now time.Time, events *[]model.Event) {
	e.view.PowerState = "on_battery"
	e.view.PendingSince = nil
	e.pendingTarget = ""
	if e.view.OnBatterySince == nil {
		e.view.OnBatterySince = timePtr(now)
	}
	e.alarm(model.EventMainsLost, true, false, model.EventMainsRestored, now, events)
}
func (e *Engine) tick(now time.Time, events *[]model.Event) {
	if e.view.CommState == "lost" {
		for k, v := range e.view.Fields {
			spec, _ := model.Lookup(k)
			if !spec.Derived {
				v.Quality = model.Stale
				v.Reason = model.ReasonCommStale
				e.view.Fields[k] = v
			}
		}
		switch e.config.LostPolicy {
		case "ignore":
			e.view.PowerState = "unknown"
		case "assume_on_battery_after":
			if e.lostSince != nil && now.Sub(*e.lostSince) >= seconds(e.config.AssumeAfterSeconds) && e.view.PowerState != "on_battery" {
				if e.view.OnBatterySince == nil {
					start := e.view.LastOKAt
					if start.IsZero() {
						start = *e.lostSince
					}
					e.view.OnBatterySince = timePtr(start)
				}
				e.confirmBattery(now, events)
			}
		}
	} else if e.failures == 0 && e.view.CommState == "ok" {
		e.power(now, events)
	}
}
func (e *Engine) derived(now time.Time) {
	e.view.At = now.UTC()
	e.view.RuntimeSource = "none"
	if e.view.Fields == nil {
		e.view.Fields = map[string]model.Field{}
	}
	e.view.Fields["power_state"] = model.Field{Str: e.view.PowerState, Quality: model.Good}
	e.view.Fields["comm_state"] = model.Field{Str: e.view.CommState, Quality: model.Good}
	elapsed := model.Field{Quality: model.Unsupported, Reason: model.ReasonNotMapped}
	if e.view.OnBatterySince != nil && e.view.PowerState != "unknown" {
		elapsed = model.Field{Num: math.Max(0, math.Floor(now.Sub(*e.view.OnBatterySince).Seconds())), Quality: model.Good}
	}
	e.view.Fields["on_battery_elapsed"] = elapsed
	runtime := model.Field{Quality: model.Unsupported, Reason: model.ReasonNotMapped}
	// 原生值有效时也要裁剪，以免长时间放电让候选副本持续增长。
	start := 0
	for start < len(e.samples) && now.Sub(e.samples[start].At) > 10*time.Minute {
		start++
	}
	if start > 0 {
		e.samples = slices.Clone(e.samples[start:])
	}
	if n, ok := number(e.view.Fields, "native_runtime_remaining"); ok && n >= 0 {
		runtime = model.Field{Num: math.Floor(n), Quality: model.Good}
		e.view.RuntimeSource = "native"
	} else if e.view.PowerState == "on_battery" {
		runtime = model.Field{Quality: model.Invalid, Reason: "estimating"}
		if n, ok := e.estimate(); ok && e.view.CommState == "ok" && e.failures == 0 {
			runtime = model.Field{Num: n, Quality: model.Good}
			e.view.RuntimeSource = "estimated"
		}
	}
	e.view.Fields["runtime_estimate"] = runtime
	e.view.Fields["runtime_source"] = model.Field{Str: e.view.RuntimeSource, Quality: model.Good}
}
func (e *Engine) estimate() (float64, bool) {
	p := e.samples
	if len(p) < 5 || p[len(p)-1].At.Sub(p[0].At) < 2*time.Minute {
		return 0, false
	}
	var sx, sy float64
	for _, v := range p {
		sx += v.At.Sub(p[0].At).Minutes()
		sy += v.Value
	}
	mx, my := sx/float64(len(p)), sy/float64(len(p))
	var xy, xx float64
	for _, v := range p {
		x := v.At.Sub(p[0].At).Minutes() - mx
		xy += x * (v.Value - my)
		xx += x * x
	}
	if xx == 0 {
		return 0, false
	}
	rate := -xy / xx
	if rate <= 0.05 {
		return 0, false
	}
	value := p[len(p)-1].Value / rate * 60
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return math.Max(0, math.Floor(value)), true
}
func number(fields map[string]model.Field, key string) (float64, bool) {
	v := fields[key]
	return v.Num, v.Quality == model.Good && !math.IsNaN(v.Num) && !math.IsInf(v.Num, 0)
}
func maxPhase(fields map[string]model.Field, key string) (float64, bool) {
	max, found := 0.0, false
	for _, suffix := range []string{"[1]", "[2]", "[3]"} {
		if v, ok := number(fields, key+suffix); ok {
			if !found || v > max {
				max = v
			}
			found = true
		}
	}
	return max, found
}
func timePtr(t time.Time) *time.Time { return &t }
func (e *Engine) alarm(kind string, trigger, clear bool, recovery string, at time.Time, events *[]model.Event) {
	active := e.flags[kind]
	if trigger && !active {
		e.flags[kind] = true
		severity, _ := model.EventSeverity(kind)
		*events = append(*events, model.Event{DeviceID: e.view.DeviceID, Type: kind, Severity: severity, At: at.UTC(), Active: true})
	} else if !trigger && clear && active {
		delete(e.flags, kind)
		if recovery != "" {
			severity, _ := model.EventSeverity(recovery)
			*events = append(*events, model.Event{DeviceID: e.view.DeviceID, Type: recovery, Severity: severity, At: at.UTC(), Clears: kind})
		} else {
			*events = append(*events, model.Event{DeviceID: e.view.DeviceID, At: at.UTC(), Clears: kind})
		}
	}
}
func (e *Engine) fieldEvents(now time.Time, events *[]model.Event) {
	f := e.view.Fields
	low := f["low_battery"]
	status := f["battery_status"]
	capacity, capacityOK := number(f, "battery_capacity_pct")
	e.compoundAlarm(model.EventBatteryLow, model.EventBatteryLowCleared, now, events, map[string]alarmEvidence{
		"low_battery":          {low.Quality == model.Good, low.Bool, !low.Bool},
		"battery_status":       {status.Quality == model.Good && status.Str != "unknown", status.Str == "low" || status.Str == "depleted", status.Str == "normal"},
		"battery_capacity_pct": {capacityOK, capacity <= e.config.BatteryLow, capacity >= e.config.BatteryLow+e.config.BatteryHysteresis},
	})
	overload := f["overload"]
	load := map[string]alarmEvidence{"overload": {overload.Quality == model.Good, overload.Bool, !overload.Bool}}
	for _, key := range []string{"output_load_pct[1]", "output_load_pct[2]", "output_load_pct[3]"} {
		v, ok := number(f, key)
		load[key] = alarmEvidence{ok, v >= e.config.Overload, v <= e.config.Overload-e.config.LoadHysteresis}
	}
	e.compoundAlarm(model.EventOverload, model.EventOverloadCleared, now, events, load)
	if v := f["battery_status"]; v.Quality == model.Good && v.Str != "unknown" {
		e.alarm(model.EventBatteryDepleted, v.Str == "depleted", v.Str != "depleted", "", now, events)
	}
	if v := f["ups_mode"]; v.Quality == model.Good && v.Str != "unknown" {
		e.alarm(model.EventUPSFault, v.Str == "fault", v.Str != "fault", "", now, events)
	}
	if e.view.PowerStateRaw != "unknown" {
		e.alarm(model.EventOnBypass, e.view.PowerStateRaw == "bypass", e.view.PowerStateRaw != "bypass", model.EventBypassCleared, now, events)
	}
	if v := f["shutting_down"]; v.Quality == model.Good {
		e.alarm(model.EventUPSShuttingDown, v.Bool, !v.Bool, "", now, events)
	} else if v := f["ups_mode"]; v.Quality == model.Good && v.Str != "unknown" {
		e.alarm(model.EventUPSShuttingDown, v.Str == "shutdown", v.Str != "shutdown", "", now, events)
	}
}

type alarmEvidence struct{ known, trigger, clear bool }

// 触发过的每个依据都须恢复且越过回差；无效字段不能被另一正常依据替代。
func (e *Engine) compoundAlarm(kind, recovery string, now time.Time, events *[]model.Event, checks map[string]alarmEvidence) {
	trigger, known, canClear := false, false, true
	for key, check := range checks {
		if check.known {
			known = true
			canClear = canClear && check.clear
			if check.trigger {
				trigger = true
				e.causes[key] = true
			}
		}
		if e.causes[key] && (!check.known || !check.clear) {
			canClear = false
		}
	}
	e.alarm(kind, trigger, known && canClear, recovery, now, events)
	if !e.flags[kind] {
		for key := range checks {
			delete(e.causes, key)
		}
	}
}
func (e *Engine) summarize(events []model.Event) {
	for i := range events {
		summary := map[string]model.Field{}
		for _, key := range []string{"power_state", "comm_state", "battery_capacity_pct", "output_load_pct[1]", "input_voltage[1]", "on_battery_elapsed", "runtime_estimate"} {
			if v, ok := e.view.Fields[key]; ok {
				summary[key] = v
			}
		}
		events[i].Summary = summary
	}
}
