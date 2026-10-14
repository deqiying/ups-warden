package state

import (
	"math"
	"testing"
	"testing/synctest"
	"time"

	"ups-warden/internal/model"
)

func reading(at time.Time, power string, capacity float64) model.SourceResult {
	mode := "line"
	if power == "on_battery" {
		mode = "battery"
	} else if power != "online" {
		mode = power
	}
	return model.SourceResult{DeviceID: 1, SourceID: 1, At: at, Snapshot: model.SourceSnapshot{SourceID: 1, At: at, Fields: map[string]model.Field{"ups_mode": {Str: mode, Quality: model.Good}, "battery_capacity_pct": {Num: capacity, Quality: model.Good}, "output_load_pct[1]": {Num: 25, Quality: model.Good}}}}
}
func mustEngine(t *testing.T, c Config) *Engine {
	t.Helper()
	e, err := NewEngine(1, c)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func hasEvent(u model.DeviceUpdate, kind string) bool {
	for _, e := range u.Events {
		if e.Type == kind {
			return true
		}
	}
	return false
}
func TestFlashDebounceAndRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := mustEngine(t, DefaultConfig())
		at := time.Now()
		if u := e.Apply(reading(at, "online", 100)); len(u.Events) != 0 {
			t.Fatal("启动误报")
		}
		time.Sleep(time.Second)
		u := e.Apply(reading(time.Now(), "on_battery", 99))
		if u.State.PowerState != "online" || u.State.PendingSince == nil || u.State.OnBatterySince == nil {
			t.Fatal("未进入疑似停电")
		}
		time.Sleep(5 * time.Second)
		u = e.Apply(reading(time.Now(), "online", 99))
		if len(u.Events) != 0 || u.State.PendingSince != nil || u.State.OnBatterySince != nil {
			t.Fatal("闪断产生事件")
		}
		time.Sleep(time.Second)
		start := time.Now()
		e.Apply(reading(start, "on_battery", 98))
		time.Sleep(9 * time.Second)
		if u = e.Tick(time.Now()); hasEvent(u, model.EventMainsLost) {
			t.Fatal("提前确认")
		}
		time.Sleep(time.Second)
		u = e.Tick(time.Now())
		if !hasEvent(u, model.EventMainsLost) || u.State.Fields["on_battery_elapsed"].Num != 10 {
			t.Fatal("确认或原始计时错误")
		}
		time.Sleep(time.Second)
		e.Apply(reading(time.Now(), "unknown", 97))
		time.Sleep(time.Minute)
		u = e.Tick(time.Now())
		if u.State.PowerState != "on_battery" || hasEvent(u, model.EventMainsRestored) {
			t.Fatal("unknown被当作恢复")
		}
		time.Sleep(time.Second)
		e.Apply(reading(time.Now(), "online", 97))
		time.Sleep(59 * time.Second)
		if u = e.Tick(time.Now()); u.State.PowerState != "on_battery" {
			t.Fatal("恢复提前")
		}
		time.Sleep(time.Second)
		u = e.Tick(time.Now())
		if !hasEvent(u, model.EventMainsRestored) || u.State.PowerState != "online" || u.State.OnBatterySince != nil {
			t.Fatal("恢复未清理")
		}
	})
}
func TestLostPoliciesAndQuality(t *testing.T) {
	for _, policy := range []string{"hold", "ignore", "assume_on_battery_after"} {
		t.Run(policy, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cfg := DefaultConfig()
				cfg.LostPolicy = policy
				cfg.AssumeAfterSeconds = 60
				e := mustEngine(t, cfg)
				initial := "on_battery"
				if policy == "assume_on_battery_after" {
					initial = "online"
				}
				last := time.Now()
				e.Apply(reading(last, initial, 80))
				time.Sleep(10 * time.Second)
				e.Tick(time.Now())
				for i := 0; i < 3; i++ {
					time.Sleep(time.Second)
					u := e.Apply(model.SourceResult{DeviceID: 1, At: time.Now(), Failed: true})
					if i < 2 && hasEvent(u, model.EventCommLost) {
						t.Fatal("通信提前失联")
					}
				}
				u := e.Snapshot()
				if u.CommState != "lost" || u.Fields["battery_capacity_pct"].Quality != model.Stale {
					t.Fatal("失联未降质量")
				}
				time.Sleep(time.Minute)
				update := e.Tick(time.Now())
				u = update.State
				switch policy {
				case "hold":
					if u.PowerState != "on_battery" || u.Fields["on_battery_elapsed"].Num != 73 {
						t.Fatal("hold计时中断")
					}
				case "ignore":
					if u.PowerState != "unknown" || u.Fields["on_battery_elapsed"].Quality == model.Good || hasEvent(update, model.EventMainsRestored) {
						t.Fatal("ignore错误恢复")
					}
				case "assume_on_battery_after":
					if u.PowerState != "on_battery" || !u.OnBatterySince.Equal(last) || !hasEvent(update, model.EventMainsLost) {
						t.Fatal("假定时长起点错误")
					}
				}
				time.Sleep(time.Second)
				update = e.Apply(reading(time.Now(), "online", 80))
				if !hasEvent(update, model.EventCommRestored) || update.State.PowerState == "online" {
					t.Fatal("重连绕过去抖")
				}
			})
		})
	}
}
func TestFailureCancelsPending(t *testing.T) {
	e := mustEngine(t, DefaultConfig())
	at := time.Now()
	e.Apply(reading(at, "online", 99))
	e.Apply(reading(at.Add(time.Second), "on_battery", 99))
	e.Apply(model.SourceResult{DeviceID: 1, At: at.Add(2 * time.Second), Failed: true})
	u := e.Tick(at.Add(time.Minute))
	if hasEvent(u, model.EventMainsLost) || u.State.PendingSince != nil {
		t.Fatal("失败期间继续确认")
	}
}
func TestHysteresisAndInvalidFields(t *testing.T) {
	e := mustEngine(t, DefaultConfig())
	at := time.Now()
	for i, n := range []float64{40, 30, 32, 34, 35, 29} {
		r := reading(at.Add(time.Duration(i)*time.Second), "online", n)
		u := e.Apply(r)
		wantRaise := i == 1 || i == 5
		wantClear := i == 4
		if hasEvent(u, model.EventBatteryLow) != wantRaise || hasEvent(u, model.EventBatteryLowCleared) != wantClear {
			t.Fatalf("容量 %g 回差错误: %+v", n, u.Events)
		}
	}
	r := reading(at.Add(10*time.Second), "online", 100)
	r.Snapshot.Fields["battery_capacity_pct"] = model.Field{Num: 100, Quality: model.Invalid}
	u := e.Apply(r)
	if hasEvent(u, model.EventBatteryLowCleared) {
		t.Fatal("invalid清除告警")
	}
	r = reading(at.Add(11*time.Second), "online", 29)
	r.Snapshot.Fields["output_load_pct[1]"] = model.Field{Num: 90, Quality: model.Good}
	if !hasEvent(e.Apply(r), model.EventOverload) {
		t.Fatal("未触发过载")
	}
	r.At = at.Add(12 * time.Second)
	r.Snapshot.Fields["output_load_pct[1]"] = model.Field{Num: 85, Quality: model.Good}
	if !hasEvent(e.Apply(r), model.EventOverloadCleared) {
		t.Fatal("未清除过载")
	}
}
func TestRuntimeEstimateAndNative(t *testing.T) {
	e := mustEngine(t, DefaultConfig())
	at := time.Now()
	for i := 0; i < 5; i++ {
		u := e.Apply(reading(at.Add(time.Duration(i)*30*time.Second), "on_battery", 100-float64(i)))
		if i < 4 && u.State.RuntimeSource == "estimated" {
			t.Fatal("估算过早")
		}
	}
	u := e.Snapshot()
	if u.RuntimeSource != "estimated" || math.Abs(u.Fields["runtime_estimate"].Num-2880) > 1 {
		t.Fatalf("估算错误 %+v", u.Fields["runtime_estimate"])
	}
	r := reading(at.Add(150*time.Second), "on_battery", 95)
	r.Snapshot.Fields["native_runtime_remaining"] = model.Field{Num: 300, Quality: model.Good}
	u = e.Apply(r).State
	if u.RuntimeSource != "native" || u.Fields["runtime_estimate"].Num != 300 {
		t.Fatal("原生值未优先")
	}
	r.At = at.Add(151 * time.Second)
	r.Snapshot.Fields["native_runtime_remaining"] = model.Field{Num: math.NaN(), Quality: model.Good}
	u = e.Apply(r).State
	if u.RuntimeSource == "native" {
		t.Fatal("NaN成为原生时间")
	}
}
func TestPowerHintsAndOrder(t *testing.T) {
	s := reading(time.Now(), "on_battery", 99).Snapshot
	s.Fields["mains_ok"] = model.Field{Bool: true, Quality: model.Good}
	if DeterminePower(s, 50) != "on_battery" {
		t.Fatal("默认顺序错误")
	}
	s.Hints.Indicators = []string{"mains_ok", "ups_mode"}
	if DeterminePower(s, 50) != "online" {
		t.Fatal("提示未优先")
	}
	s.Hints.Indicators = []string{"input_voltage"}
	s.Fields["input_voltage[1]"] = model.Field{Num: 0, Quality: model.Good}
	s.Fields["input_voltage[2]"] = model.Field{Num: 220, Quality: model.Good}
	if DeterminePower(s, 50) != "online" {
		t.Fatal("未取相最大值")
	}
}
func TestSnapshotIsolationAndOldResults(t *testing.T) {
	e := mustEngine(t, DefaultConfig())
	r := reading(time.Now(), "online", 90)
	u := e.Apply(r)
	r.Snapshot.Fields["battery_capacity_pct"] = model.Field{Num: 1}
	u.State.Fields["battery_capacity_pct"] = model.Field{Num: 2}
	if e.Snapshot().Fields["battery_capacity_pct"].Num != 90 {
		t.Fatal("快照共享map")
	}
	r.At = r.At.Add(-time.Second)
	r.Failed = true
	if e.Apply(r).State.CommState != "ok" {
		t.Fatal("乱序覆盖")
	}
}
