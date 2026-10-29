// Package sim 提供仅监听本机的 UPS 三协议模拟器，不用于连接真实设备。
package sim

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"time"

	"go.yaml.in/yaml/v3"
)

// Step 是相对于启动时间的事件；state 可为 normal/outage/discharge/low/recovery/offline/online/flash。
// flash 恰好持续 5 秒；offline/online 只改变通信，不改变供电状态。
type Step struct {
	At    string `yaml:"at"`
	State string `yaml:"state"`
}

// Scenario 是 schema=1 的有界场景。零值等价于正常单相；数值零值采用默认值。
// Steps 必须按时间严格递增，最大 256 条、最长 24 小时。
type Scenario struct {
	Schema           int     `yaml:"schema"`
	Phases           int     `yaml:"phases"`
	InputVoltage     float64 `yaml:"input_voltage"`
	OutputVoltage    float64 `yaml:"output_voltage"`
	LoadPct          float64 `yaml:"load_pct"`
	BatteryCapacity  float64 `yaml:"battery_capacity_pct"`
	BatteryVoltage   float64 `yaml:"battery_voltage"`
	Temperature      float64 `yaml:"temperature"`
	RatedVA          float64 `yaml:"rated_va"`
	RatedW           float64 `yaml:"rated_w"`
	DischargeMinutes float64 `yaml:"discharge_minutes"`
	Steps            []Step  `yaml:"steps"`
}

// State 是所有协议共用的物理快照。OutputVA 和 OutputW 含义独立，不能互换。
type State struct {
	Online, Mains, LowBattery                                        bool
	Phases                                                           int
	InputVoltage, OutputVoltage, BypassVoltage                       [3]float64
	InputCurrent, OutputCurrent                                      [3]float64
	OutputVA, OutputW                                                [3]float64
	InputFrequency, OutputFrequency, LoadPct                         float64
	BatteryCapacity, BatteryVoltage, Temperature                     float64
	SecondsOnBattery, RuntimeSeconds                                 int64
	RatedVA, RatedW                                                  float64
	NominalInputVoltage, NominalOutputVoltage, NominalBatteryVoltage float64
}

// ParseScenario 严格解析单文档 YAML；拒绝未知字段、别名、重复键和超过 64 KiB 的输入。
func ParseScenario(data []byte) (Scenario, error) {
	var s Scenario
	if err := strictYAML(data, &s); err != nil {
		return s, err
	}
	if s.Schema != 1 {
		return s, fmt.Errorf("scenario schema 必须为 1")
	}
	return s.normalized()
}

func strictYAML(data []byte, out any) error {
	if len(data) == 0 || len(data) > 64*1024 {
		return fmt.Errorf("YAML 长度必须为 1..65536 字节")
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return fmt.Errorf("无效 YAML: %w", err)
	}
	count := 0
	var check func(*yaml.Node, int) error
	check = func(n *yaml.Node, depth int) error {
		count++
		if depth > 12 || count > 32768 || n.Kind == yaml.AliasNode || n.Anchor != "" {
			return fmt.Errorf("YAML 嵌套、节点数量或别名超出限制")
		}
		for _, child := range n.Content {
			if err := check(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := check(&node, 0); err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("无效 YAML 字段: %w", err)
	}
	var extra yaml.Node // 不对被拒绝的后续文档展开别名。
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("只允许一个 YAML 文档")
	}
	return nil
}

func (s Scenario) normalized() (Scenario, error) {
	if s.Schema == 0 {
		s.Schema = 1
	}
	if s.Schema != 1 {
		return s, fmt.Errorf("scenario schema 必须为 1")
	}
	if s.Phases == 0 {
		s.Phases = 1
	}
	if s.Phases != 1 && s.Phases != 3 {
		return s, fmt.Errorf("phases 必须为 1 或 3")
	}
	defaults := []struct {
		p               *float64
		value, min, max float64
	}{
		{&s.InputVoltage, 220, 1, 500}, {&s.OutputVoltage, 220, 1, 500}, {&s.LoadPct, 30, 0, 100},
		{&s.BatteryCapacity, 100, 0, 100}, {&s.BatteryVoltage, 24, 1, 1000}, {&s.Temperature, 25, -40, 100},
		{&s.RatedVA, 1000, 1, 60000}, {&s.RatedW, 800, 1, 60000}, {&s.DischargeMinutes, 30, 1, 1440},
	}
	for _, v := range defaults {
		if *v.p == 0 {
			*v.p = v.value
		}
		if math.IsNaN(*v.p) || math.IsInf(*v.p, 0) || *v.p < v.min || *v.p > v.max {
			return s, fmt.Errorf("场景物理数值超出范围")
		}
	}
	if s.RatedW > s.RatedVA {
		return s, fmt.Errorf("rated_w 不能大于 rated_va")
	}
	if len(s.Steps) > 256 {
		return s, fmt.Errorf("steps 超过 256 条")
	}
	previous := time.Duration(-1)
	flashEnd := time.Duration(-1)
	for i, step := range s.Steps {
		at, err := time.ParseDuration(step.At)
		if err != nil || at < 0 || at > 24*time.Hour || at <= previous {
			return s, fmt.Errorf("steps[%d].at 必须递增且在 0..24h 内", i)
		}
		if at < flashEnd {
			return s, fmt.Errorf("steps[%d] 与 5s flash 重叠", i)
		}
		switch step.State {
		case "normal", "outage", "discharge", "low", "recovery", "offline", "online":
		case "flash":
			flashEnd = at + 5*time.Second
		default:
			return s, fmt.Errorf("steps[%d].state 未知", i)
		}
		previous = at
	}
	s.Steps = append([]Step(nil), s.Steps...)
	return s, nil
}

// StateAt 是无 I/O、无时钟副作用的纯函数；elapsed 为相对于场景起点的时间。
// 有效场景应由 ParseScenario 或 Start 校验。负时间钳制为零。
func (s Scenario) StateAt(elapsed time.Duration) State {
	s, err := s.normalized()
	if err != nil {
		return State{}
	}
	return s.stateFrom(start{Capacity: s.BatteryCapacity, Mains: true, Online: true}, elapsed)
}

// start 是一段时间轴的起始条件，供运行期切换状态时延续上一段的物理状态。
// BatteryStart 为相对本段起点的进入电池供电时刻，可为负，表示在本段之前已开始放电。
type start struct {
	Capacity           float64
	Mains, Low, Online bool
	BatteryStart       time.Duration
}

// stateFrom 从起始条件按场景步骤推进到 elapsed；s 必须已经过 normalized。
// 回充上限始终为 s.BatteryCapacity，而不是起始电量。
func (s Scenario) stateFrom(initial start, elapsed time.Duration) State {
	if elapsed < 0 {
		elapsed = 0
	}
	mains, online, low := initial.Mains, initial.Online, initial.Low
	capacity := initial.Capacity
	cursor := time.Duration(0)
	batteryStart := initial.BatteryStart
	advance := func(to time.Duration) {
		seconds := (to - cursor).Seconds()
		if mains {
			capacity = math.Min(s.BatteryCapacity, capacity+seconds*100/(s.DischargeMinutes*60))
		} else {
			capacity = math.Max(0, capacity-seconds*100/(s.DischargeMinutes*60))
		}
		cursor = to
	}
	flashEnd := time.Duration(-1)
	flashMains := true
	flashLow := false
	flashBatteryStart := time.Duration(0)
	restoreFlash := func() { mains = flashMains; low = flashLow; batteryStart = flashBatteryStart; flashEnd = -1 }
	for _, step := range s.Steps {
		at, _ := time.ParseDuration(step.At)
		if at > elapsed {
			break
		}
		if flashEnd >= 0 && flashEnd <= at {
			advance(flashEnd)
			restoreFlash()
		}
		advance(at)
		switch step.State {
		case "normal", "recovery":
			mains = true
			low = false
		case "outage", "discharge", "low":
			if mains {
				batteryStart = at
			}
			mains = false
			if step.State == "low" {
				capacity = math.Min(capacity, 15)
				low = true
			}
		case "offline":
			online = false
		case "online":
			online = true
		case "flash":
			flashMains = mains
			flashLow = low
			flashBatteryStart = batteryStart
			flashEnd = at + 5*time.Second
			if mains {
				batteryStart = at
			}
			mains = false
		}
	}
	if flashEnd >= 0 && flashEnd <= elapsed {
		advance(flashEnd)
		restoreFlash()
	}
	advance(elapsed)
	state := State{Online: online, Mains: mains, LowBattery: low || capacity <= 20, Phases: s.Phases,
		InputFrequency: 50, OutputFrequency: 50, LoadPct: s.LoadPct, BatteryCapacity: capacity,
		BatteryVoltage: s.BatteryVoltage * (0.9 + capacity/1000), Temperature: s.Temperature,
		RatedVA: s.RatedVA, RatedW: s.RatedW, RuntimeSeconds: int64(capacity * s.DischargeMinutes * 60 / 100),
		NominalInputVoltage: s.InputVoltage, NominalOutputVoltage: s.OutputVoltage, NominalBatteryVoltage: s.BatteryVoltage}
	if !mains {
		state.InputFrequency = 0
		state.SecondsOnBattery = int64((elapsed - batteryStart) / time.Second)
	}
	for i := 0; i < s.Phases; i++ {
		state.OutputVoltage[i] = s.OutputVoltage
		state.OutputVA[i] = s.RatedVA * s.LoadPct / 100 / float64(s.Phases)
		state.OutputW[i] = s.RatedW * s.LoadPct / 100 / float64(s.Phases)
		state.OutputCurrent[i] = state.OutputVA[i] / s.OutputVoltage
		if mains {
			state.InputVoltage[i] = s.InputVoltage
			state.BypassVoltage[i] = s.InputVoltage
			state.InputCurrent[i] = state.OutputCurrent[i]
		}
	}
	return state
}
