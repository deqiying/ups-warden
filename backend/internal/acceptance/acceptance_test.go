package acceptance

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/driver/guard"
	"ups-warden/internal/model"
	"ups-warden/internal/sim"
)

// waitSnapshot 轮询设备快照，直到通讯正常且满足 ok。
func (s *server) waitSnapshot(id int64, ok func(model.DeviceState) bool) model.DeviceState {
	s.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var state model.DeviceState
		var list struct {
			Items []model.DeviceState `json:"items"`
		}
		s.call("GET", "/snapshots", nil, 200, &list)
		for _, item := range list.Items {
			if item.DeviceID == id {
				state = item
			}
		}
		if state.CommState == "ok" && ok(state) {
			return state
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("设备 %d 快照未就绪: %+v", id, state)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestAcceptanceProtocolsAndReadOnly 覆盖 AC-02（RFC 1628 通用 Profile）、AC-03（自定义 Profile 接入
// 新寄存器表，不改代码）、AC-10（ayi9-json 密码错误 103）与 AC-11 的模拟器部分（服务全程只发只读请求）。
func TestAcceptanceProtocolsAndReadOnly(t *testing.T) {
	simulator, err := sim.Start(sim.Config{Password: "sim-pass", Registers: map[driver.RegKey]uint16{{FC: driver.FC04, Addr: 123}: 271}})
	if err != nil {
		t.Fatal(err)
	}
	defer simulator.Close()
	snmpAddr, modbusAddr, ayi9Addr := simulator.Addresses()
	s := startServer(t)

	// AC-02：标准字段与模拟器物理状态一致，未实现的字段为 unsupported。
	host, port := hostPort(t, snmpAddr)
	id := s.create("/devices", map[string]any{"name": "rfc1628"})
	s.create("/devices/1/sources", map[string]any{"protocol": "snmp", "host": host, "port": port, "profile_key": "rfc1628-generic", "poll_ms": 500, "community": "sim-public"})
	scenario, err := sim.ParseScenario([]byte("schema: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := scenario.StateAt(0)
	state := s.waitSnapshot(id, func(model.DeviceState) bool { return true })
	for key, value := range map[string]float64{"input_voltage[1]": want.InputVoltage[0], "output_voltage[1]": want.OutputVoltage[0], "output_load_pct[1]": want.LoadPct, "battery_capacity_pct": want.BatteryCapacity, "temperature": want.Temperature} {
		if f := state.Fields[key]; f.Quality != model.Good || math.Abs(f.Num-value) > 1.001 {
			t.Fatalf("AC-02 %s=%+v want %g", key, f, value)
		}
	}
	unsupported := 0
	for _, f := range state.Fields {
		if f.Quality == model.Unsupported {
			unsupported++
		}
	}
	if unsupported == 0 {
		t.Fatal("AC-02 未实现字段没有显示为 unsupported")
	}

	// AC-03：通过 API 导入自定义 Profile，接入 FC04 自定义寄存器表。
	host, port = hostPort(t, modbusAddr)
	profile := "schema: 1\nkey: custom-fc04\nname: 自定义寄存器表\nversion: 1\nprotocol: modbus\nmodbus:\n  readable: [{fc: 4, start: 123, end: 123}]\nfields:\n  - field: battery_voltage\n    source: {fc: 4, addr: 123}\n    type: uint16\n    scale: 0.1\n"
	s.call("POST", "/profiles", map[string]any{"content": profile}, 201, nil)
	id = s.create("/devices", map[string]any{"name": "custom"})
	s.create("/devices/2/sources", map[string]any{"protocol": "modbus", "host": host, "port": port, "profile_key": "custom-fc04", "poll_ms": 500})
	s.waitSnapshot(id, func(st model.DeviceState) bool {
		f := st.Fields["battery_voltage"]
		return f.Quality == model.Good && math.Abs(f.Num-27.1) < 1e-9
	})

	// AC-10：连接测试显示密码错误（103），不回显密码。
	host, port = hostPort(t, ayi9Addr)
	var failure struct{ Code, Message string }
	s.call("POST", "/sources/test", map[string]any{"protocol": "ayi9-json", "host": host, "port": port, "profile_key": "ayi9-json-generic", "password": "wrong-pass"}, 502, &failure)
	if failure.Code != "source_password_wrong" || !strings.Contains(failure.Message, "103") || strings.Contains(failure.Message, "wrong-pass") {
		t.Fatalf("AC-10 %+v", failure)
	}

	// AC-11（模拟器部分）：服务发出的全部请求均为白名单只读请求，三种协议都有覆盖。
	seen := map[string]int{}
	for _, r := range simulator.Requests() {
		if !r.Allowed || guard.Validate(r.Protocol, r.Data) != nil {
			t.Fatalf("AC-11 收到非只读请求: %s", r.Protocol)
		}
		seen[r.Protocol]++
	}
	if simulator.DroppedRequests() != 0 || seen[driver.ProtocolSNMP] == 0 || seen[driver.ProtocolModbus] == 0 || seen[driver.ProtocolAYI9] == 0 {
		t.Fatalf("AC-11 请求审计不完整: %v dropped=%d", seen, simulator.DroppedRequests())
	}
}

// TestAcceptanceOutageNotificationAndCommLoss 经 HTTP API 配置规则与 Webhook，覆盖：
// AC-04（短于确认时长的闪断不产生 mains_lost、不触发规则）；AC-05 通知部分（确认后 2 s 内送达）；
// AC-08（停电期间失联 hold：产生 comm_lost，放电计时继续，时长条件照常触发）。
// 模拟器使用可跳变的场景时钟，服务本身使用真实时间。
func TestAcceptanceOutageNotificationAndCommLoss(t *testing.T) {
	var offset atomic.Int64
	base := time.Now()
	scenario := sim.Scenario{Steps: []sim.Step{{At: "100s", State: "flash"}, {At: "200s", State: "outage"}, {At: "300s", State: "offline"}}}
	simulator, err := sim.Start(sim.Config{ET1K: true, Scenario: scenario, Now: func() time.Time { return base.Add(time.Duration(offset.Load())) }})
	if err != nil {
		t.Fatal(err)
	}
	defer simulator.Close()
	jump := func(d time.Duration) { offset.Store(int64(d)) }
	s := startServer(t)
	url, deliveries := startWebhook(t, 16)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := s.stream(ctx, 4096)

	_, modbusAddr, _ := simulator.Addresses()
	host, port := hostPort(t, modbusAddr)
	device := s.create("/devices", map[string]any{"name": "sim", "config": map[string]any{"outage_confirm_s": 2, "recovery_confirm_s": 2, "comm_failures": 3}})
	s.create("/devices/1/sources", map[string]any{"protocol": "modbus", "host": host, "port": port, "profile_key": "et1k-modbus", "poll_ms": 200, "timeout_ms": 100})
	channel := s.create("/channels", map[string]any{"name": "webhook", "type": "webhook", "enabled": true, "url": url, "config": map[string]any{"body_template": `{"event":"{{event.type}}"}`}})
	notify := s.create("/actions", map[string]any{"name": "notify", "type": "notify", "config": map[string]any{"channel_ids": []int64{channel}}})
	s.create("/rules", map[string]any{"name": "停电通知", "enabled": true, "dry_run": false, "scope": map[string]any{"type": "all"}, "trigger": map[string]any{"type": "event", "events": []string{"mains_lost"}}, "actions": []map[string]any{{"action_id": notify}}})
	s.create("/rules", map[string]any{"name": "放电时长", "enabled": true, "dry_run": false, "scope": map[string]any{"type": "all"}, "trigger": map[string]any{"type": "condition", "any_of": []map[string]any{{"all_of": []map[string]any{{"field": "on_battery_elapsed", "op": "gte", "value": 6}}}}}, "actions": []map[string]any{{"action_id": notify}}})
	s.waitSnapshot(device, func(st model.DeviceState) bool { return st.PowerState == "online" })

	// awaitEvent 等待指定类型的业务事件帧并返回其 at。
	awaitEvent := func(kind string, within time.Duration) time.Time {
		t.Helper()
		timer := time.NewTimer(within)
		defer timer.Stop()
		for {
			select {
			case e := <-events:
				if e.name != "event" {
					continue
				}
				var v model.Event
				if err := json.Unmarshal(e.data, &v); err != nil {
					t.Fatal(err)
				}
				if v.Type == model.EventMainsLost && kind != model.EventMainsLost {
					t.Fatalf("意外的 %s", v.Type)
				}
				if v.Type == kind {
					return v.At
				}
			case <-timer.C:
				t.Fatalf("%s 内未收到 %s", within, kind)
			}
		}
	}

	// AC-04：服务观测到原始停电后、2 秒确认时长到达前恢复。
	jump(100 * time.Second)
	s.waitSnapshot(device, func(st model.DeviceState) bool { return st.PowerStateRaw == "on_battery" })
	jump(106 * time.Second)
	time.Sleep(3 * time.Second)
	select {
	case d := <-deliveries:
		t.Fatal("AC-04 闪断触发了规则:", d.body)
	default:
	}
	state := s.waitSnapshot(device, func(model.DeviceState) bool { return true })
	if state.PowerState != "online" || state.OutageCycleID != nil {
		t.Fatal("AC-04 闪断改变了供电状态", state.PowerState)
	}

	// AC-05 通知部分：持续停电，确认后 2 秒内送达。
	jump(200 * time.Second)
	confirmed := awaitEvent(model.EventMainsLost, 10*time.Second)
	select {
	case d := <-deliveries:
		if !strings.Contains(d.body, "mains_lost") || d.received.Sub(confirmed) > 2*time.Second {
			t.Fatalf("AC-05 通知内容或时效不满足: %s %s", d.body, d.received.Sub(confirmed))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("AC-05 确认后未送达通知")
	}

	// AC-08：停电期间失联，hold 策略保持 on_battery，计时继续，时长条件照常触发。
	jump(300 * time.Second)
	awaitEvent(model.EventCommLost, 10*time.Second)
	select {
	case d := <-deliveries:
		if !strings.Contains(d.body, "rule_triggered") {
			t.Fatal("AC-08 时长条件通知内容错误:", d.body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("AC-08 失联期间时长条件未触发")
	}
	var lost model.DeviceState
	s.call("GET", "/devices/1/snapshot", nil, 200, &lost)
	if lost.CommState != "lost" || lost.PowerState != "on_battery" || lost.Fields["on_battery_elapsed"].Num < 6 {
		t.Fatalf("AC-08 失联状态错误: comm=%s power=%s elapsed=%+v", lost.CommState, lost.PowerState, lost.Fields["on_battery_elapsed"])
	}
}
