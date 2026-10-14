package sim

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func TestScenarioTimeline(t *testing.T) {
	scenario, err := ParseScenario([]byte(`schema: 1
phases: 1
steps:
  - {at: 10s, state: outage}
  - {at: 20s, state: discharge}
  - {at: 30s, state: low}
  - {at: 40s, state: offline}
  - {at: 50s, state: online}
  - {at: 60s, state: recovery}
`))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		second             int
		mains, online, low bool
		onBattery          int64
	}{
		{0, true, true, false, 0}, {10, false, true, false, 0}, {25, false, true, false, 15},
		{30, false, true, true, 20}, {45, false, false, true, 35}, {55, false, true, true, 45}, {60, true, true, true, 0}, {181, true, true, false, 0},
	}
	for _, test := range tests {
		t.Run(fmt.Sprint(test.second), func(t *testing.T) {
			got := scenario.StateAt(time.Duration(test.second) * time.Second)
			if got.Mains != test.mains || got.Online != test.online || got.LowBattery != test.low || got.SecondsOnBattery != test.onBattery {
				t.Fatalf("状态不符: %+v", got)
			}
			if got.OutputVoltage[0] != 220 || got.OutputVoltage[1] != 0 {
				t.Fatal("单相电压不符")
			}
			if test.mains && got.InputVoltage[0] != 220 || !test.mains && got.InputVoltage[0] != 0 {
				t.Fatal("市电电压不符")
			}
			if got.BatteryCapacity < 0 || got.BatteryCapacity > 100 {
				t.Fatal("容量越界")
			}
			if again := scenario.StateAt(time.Duration(test.second) * time.Second); again != got {
				t.Fatal("StateAt 不纯")
			}
		})
	}
	if scenario.StateAt(-time.Second) != scenario.StateAt(0) {
		t.Fatal("负时间未钳制")
	}
}

func TestFlashAndThreePhase(t *testing.T) {
	s := Scenario{Phases: 3, Steps: []Step{{At: "10s", State: "flash"}}}
	if !s.StateAt(9*time.Second).Mains || !s.StateAt(15*time.Second).Mains {
		t.Fatal("闪断边界错误")
	}
	for _, second := range []int{10, 12, 14} {
		state := s.StateAt(time.Duration(second) * time.Second)
		if state.Mains || state.SecondsOnBattery != int64(second-10) {
			t.Fatalf("闪断状态不符: %+v", state)
		}
	}
	restored := s.StateAt(15 * time.Second)
	if restored.SecondsOnBattery != 0 {
		t.Fatal("恢复后计时未归零")
	}
	normal := s.StateAt(0)
	for i := 0; i < 3; i++ {
		if normal.OutputVoltage[i] != 220 || math.Abs(normal.OutputVA[i]-100) > 1e-9 || math.Abs(normal.OutputW[i]-80) > 1e-9 {
			t.Fatal("三相功率或电压不符")
		}
	}
	long := Scenario{Steps: []Step{{At: "0s", State: "outage"}}}.StateAt(24 * time.Hour)
	if long.BatteryCapacity != 0 || long.RuntimeSeconds != 0 || !long.LowBattery {
		t.Fatal("长期放电未钳制为零")
	}
}

func TestStrictScenario(t *testing.T) {
	cases := []string{
		"", "schema: 2", "schema: 1\nunknown: true", "schema: 1\nschema: 1", "schema: 1\n---\nschema: 1",
		"schema: 1\nphases: 2", "schema: 1\nload_pct: 101", "schema: 1\nload_pct: .nan", "schema: 1\nrated_w: 2000",
		"schema: 1\nsteps: [{at: 1s, state: nope}]", "schema: 1\nsteps: [{at: -1s, state: outage}]",
		"schema: 1\nsteps: [{at: 25h, state: outage}]", "schema: 1\nsteps: [{at: 1s, state: normal}, {at: 1s, state: outage}]",
		"schema: 1\nsteps: [{at: 1s, state: flash}, {at: 2s, state: normal}]", "schema: 1\nsteps: &steps []",
		"schema: 1\nsteps: *steps", strings.Repeat(" ", 65537),
	}
	for i, input := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if _, err := ParseScenario([]byte(input)); err == nil {
				t.Fatal("接受了无效场景")
			}
		})
	}
	many := "schema: 1\nsteps:\n"
	for i := 0; i < 257; i++ {
		many += fmt.Sprintf("  - {at: %ds, state: normal}\n", i)
	}
	if _, err := ParseScenario([]byte(many)); err == nil {
		t.Fatal("接受超过256事件")
	}
	if _, err := ParseScenario([]byte("schema: 1\n")); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterParser(t *testing.T) {
	regs, err := ParseRegisters([]byte("schema: 1\nregisters: [{fc: 3, addr: 100, value: 1234}, {fc: 4, addr: 100, value: 5678}]"))
	if err != nil || len(regs) != 2 {
		t.Fatalf("解析寄存器失败: %v", err)
	}
	for _, input := range []string{
		"schema: 2", "schema: 1\nregisters: [{fc: 6, addr: 0, value: 1}]",
		"schema: 1\nregisters: [{fc: 3, addr: 1, value: 1}, {fc: 3, addr: 1, value: 2}]",
		"schema: 1\nregisters: [{fc: 3, addr: 65536, value: 1}]", "schema: 1\nregisters: [{fc: 3, addr: 1, value: -1}]",
		"schema: 1\nregisters: [{fc: 3, addr: 1, value: 1, other: 2}]",
	} {
		if _, err := ParseRegisters([]byte(input)); err == nil {
			t.Fatal("接受非法寄存器表")
		}
	}
}

func FuzzParseScenario(f *testing.F) {
	f.Add([]byte("schema: 1\nsteps: [{at: 5s, state: flash}]"))
	f.Fuzz(func(t *testing.T, data []byte) {
		s, err := ParseScenario(data)
		if err != nil {
			return
		}
		state := s.StateAt(time.Hour)
		if state.BatteryCapacity < 0 || state.BatteryCapacity > 100 {
			t.Fatal("容量越界")
		}
	})
}
