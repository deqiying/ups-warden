package sim

import (
	"encoding/json"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ups-warden/internal/driver/snmp/ber"
)

type virtualClock struct{ offset atomic.Int64 }

func (c *virtualClock) now() time.Time {
	return time.Unix(1_700_000_000, 0).Add(time.Duration(c.offset.Load()))
}
func (c *virtualClock) advance(d time.Duration) { c.offset.Add(int64(d)) }

func ptr[T any](v T) *T { return &v }

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestApplyKeepsPhysicalContinuity(t *testing.T) {
	var clock virtualClock
	s := startTest(t, Config{Now: clock.now})
	if st := s.Snapshot(); st.Control != ControlScenario || st.Mode != ModeMains || !near(st.State.BatteryCapacity, 100) {
		t.Fatalf("默认状态错误: %+v", st)
	}
	if err := s.Apply(Change{Mode: ptr(ModeBattery)}); err != nil {
		t.Fatal(err)
	}
	clock.advance(60 * time.Second)
	st := s.Snapshot()
	// 默认 30 分钟放完，60 秒下降 100*60/1800。
	if st.Control != ControlManual || st.State.Mains || st.State.SecondsOnBattery != 60 || !near(st.State.BatteryCapacity, 100-100.0*60/1800) {
		t.Fatalf("电池模式错误: %+v", st.State)
	}
	if err := s.Apply(Change{Mode: ptr(ModeLow)}); err != nil {
		t.Fatal(err)
	}
	clock.advance(10 * time.Second)
	st = s.Snapshot()
	if st.Mode != ModeLow || !st.State.LowBattery || st.State.SecondsOnBattery != 70 || st.State.BatteryCapacity > lowCapacity {
		t.Fatalf("低电模式未延续电池计时: %+v", st.State)
	}
	low := st.State.BatteryCapacity
	if err := s.Apply(Change{Mode: ptr(ModeMains)}); err != nil {
		t.Fatal(err)
	}
	st = s.Snapshot()
	if !st.State.Mains || st.State.SecondsOnBattery != 0 || !near(st.State.BatteryCapacity, low) {
		t.Fatalf("恢复市电时电量不连续: %+v", st.State)
	}
	clock.advance(2 * time.Hour)
	if c := s.Snapshot().State.BatteryCapacity; !near(c, 100) {
		t.Fatalf("回充上限应为 100，实际 %v", c)
	}
}

func TestApplyFlashLinkAndParams(t *testing.T) {
	var clock virtualClock
	s := startTest(t, Config{Now: clock.now, Initial: &Change{Mode: ptr(ModeMains), BatteryCapacity: ptr(80.0)}})
	if st := s.Snapshot(); st.Control != ControlManual || !near(st.State.BatteryCapacity, 80) {
		t.Fatalf("Initial 未生效: %+v", st)
	}
	if err := s.Apply(Change{Flash: true}); err != nil {
		t.Fatal(err)
	}
	clock.advance(2 * time.Second)
	if st := s.Snapshot(); st.State.Mains || !st.Flashing || st.Mode != ModeMains {
		t.Fatalf("闪断期间应断电且保持配置模式: %+v", st)
	}
	clock.advance(4 * time.Second)
	if st := s.Snapshot(); !st.State.Mains || st.Flashing {
		t.Fatalf("闪断 5 秒后未恢复: %+v", st)
	}
	if err := s.Apply(Change{Online: ptr(false), LoadPct: ptr(55.0)}); err != nil {
		t.Fatal(err)
	}
	if st := s.Snapshot(); st.Online || st.Params.LoadPct != 55 || !st.State.Mains {
		t.Fatalf("失联或参数修改错误: %+v", st)
	}
	// 失联只改通信；后续切换模式不应恢复通信。
	if err := s.Apply(Change{Mode: ptr(ModeBattery)}); err != nil {
		t.Fatal(err)
	}
	if st := s.Snapshot(); st.Online || st.State.Mains || st.Params.LoadPct != 55 {
		t.Fatalf("模式切换影响了通信状态或参数: %+v", st)
	}
}

func TestApplyValidationIsAtomic(t *testing.T) {
	var clock virtualClock
	s := startTest(t, Config{Now: clock.now, ET1K: true})
	before := s.Snapshot()
	for name, change := range map[string]Change{
		"空修改":       {},
		"低电大电量":     {Mode: ptr(ModeLow), BatteryCapacity: ptr(50.0)},
		"零负载":       {LoadPct: ptr(0.0)},
		"负载越界":      {LoadPct: ptr(150.0)},
		"电量越界":      {BatteryCapacity: ptr(101.0)},
		"未知模式":      {Mode: ptr(Mode("bypass"))},
		"场景互斥":      {Scenario: &Scenario{}, Mode: ptr(ModeBattery)},
		"ET1K 三相场景": {Scenario: &Scenario{Phases: 3}},
		"错误码越界":     {AYI9ErrorCode: ptr(1000), Mode: ptr(ModeBattery)},
		"电池模式闪断":    {Mode: ptr(ModeBattery), Flash: true},
	} {
		if err := s.Apply(change); err == nil {
			t.Fatalf("%s: 接受了非法修改", name)
		}
	}
	if after := s.Snapshot(); after.Control != before.Control || after.State.Mains != before.State.Mains || after.AYI9ErrorCode != 0 {
		t.Fatalf("校验失败后状态被修改: %+v", after)
	}
	// 场景控制下的参数修改转为手动接管，并从当前时刻延续。
	if err := s.Apply(Change{Scenario: &Scenario{Steps: []Step{{At: "0s", State: "outage"}}}}); err != nil {
		t.Fatal(err)
	}
	clock.advance(30 * time.Second)
	if err := s.Apply(Change{Temperature: ptr(40.0)}); err != nil {
		t.Fatal(err)
	}
	if st := s.Snapshot(); st.Control != ControlManual || st.Mode != ModeBattery || st.State.SecondsOnBattery != 30 || st.Params.Temperature != 40 {
		t.Fatalf("场景接管错误: %+v", st)
	}
}

func TestApplyVisibleOverProtocols(t *testing.T) {
	s := startTest(t, Config{})
	snmp, _, ayi9 := s.Addresses()
	source := func() int64 {
		c := dialTest(t, "udp", snmp)
		resp := snmpRead(t, c, snmpMessage(ber.GetRequest, "1.3.6.1.2.1.33.1.4.1.0"))
		return resp.VarBinds[0].Value.Int
	}
	if source() != 3 {
		t.Fatal("初始应为市电输出")
	}
	if err := s.Apply(Change{Mode: ptr(ModeBattery), AYI9ErrorCode: ptr(103)}); err != nil {
		t.Fatal(err)
	}
	if source() != 5 {
		t.Fatal("切换后 SNMP 未输出电池供电")
	}
	c := dialTest(t, "tcp", ayi9)
	if ayi9Read(t, c, json.NewDecoder(c), "QAD", "")["code"] != float64(103) {
		t.Fatal("运行期错误码未生效")
	}
}

// 并发修改与读取不应出现数据竞争或半更新状态；-race 在 Linux CI 中执行。
func TestApplyConcurrentWithRequests(t *testing.T) {
	s := startTest(t, Config{})
	_, _, ayi9 := s.Addresses()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		modes := []Mode{ModeMains, ModeBattery, ModeLow}
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := s.Apply(Change{Mode: ptr(modes[i%3])}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	c := dialTest(t, "tcp", ayi9)
	decoder := json.NewDecoder(c)
	for range 50 {
		value, _ := ayi9Read(t, c, decoder, "QAD", "")["value"].(string)
		fields := strings.Fields(strings.TrimPrefix(value, "("))
		if len(fields) != 23 {
			t.Fatalf("响应字段数错误: %q", value)
		}
		// 市电时输入电压非零且状态字 bit7 为 0；电池时相反，二者不能混合。
		mains := fields[0] != "0.0"
		if bit7 := fields[22][len(fields[22])-8] == '1'; mains == bit7 {
			t.Fatalf("单个响应混入了两种状态: %q", value)
		}
	}
	close(stop)
	wg.Wait()
}
