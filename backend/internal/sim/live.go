package sim

import (
	"fmt"
	"math"
	"time"
)

// Mode 是手动控制下的供电模式；与通信状态（Online）相互独立。
type Mode string

const (
	// ModeMains 市电供电，电量按 discharge_minutes 速率回充到 100%。
	ModeMains Mode = "mains"
	// ModeBattery 电池供电，电量线性下降；电量 ≤ 20% 时自动置低电标志。
	ModeBattery Mode = "battery"
	// ModeLow 电池供电且低电，电量钳制为 ≤ 15%。
	ModeLow Mode = "low"
)

// Control 表示设备当前由场景时间轴驱动还是由手动命令驱动。
const (
	ControlScenario = "scenario"
	ControlManual   = "manual"
)

// lowCapacity 与场景 step "low" 的电量钳制保持一致。
const lowCapacity = 15

// Change 描述一次原子修改；nil 字段表示不修改。
// Scenario 非 nil 时以当前时刻为起点从头运行该场景，不能与其他状态或物理参数字段同时出现。
// 其他状态或物理参数字段会让设备从当前时刻起转为手动控制，电量与电池供电计时保持连续。
// LoadPct、Temperature、InputVoltage、DischargeMinutes 中 0 是场景的默认值标记，不能作为设置值。
type Change struct {
	Mode             *Mode
	Online           *bool
	Flash            bool
	LoadPct          *float64
	BatteryCapacity  *float64
	Temperature      *float64
	InputVoltage     *float64
	DischargeMinutes *float64
	AYI9ErrorCode    *int
	Scenario         *Scenario
}

// Params 是当前时间轴使用的物理参数。
type Params struct {
	LoadPct          float64 `json:"load_pct"`
	Temperature      float64 `json:"temperature"`
	InputVoltage     float64 `json:"input_voltage"`
	DischargeMinutes float64 `json:"discharge_minutes"`
}

// Status 是某一时刻的运行期视图。Control 为 scenario 时，Mode 与 Online 由场景当前状态推导。
type Status struct {
	Control       string
	Mode          Mode
	Online        bool
	Flashing      bool
	AYI9ErrorCode int
	Params        Params
	State         State
}

// segment 是一段以 origin 为起点的时间轴；运行期每次修改都会以当前状态为起始条件开启新段。
type segment struct {
	origin   time.Time
	scenario Scenario
	initial  start
}

func (s *Server) state() State {
	s.live.RLock()
	seg := s.seg
	s.live.RUnlock()
	return seg.scenario.stateFrom(seg.initial, s.cfg.Now().Sub(seg.origin))
}

func (s *Server) ayi9ErrorCode() int {
	s.live.RLock()
	defer s.live.RUnlock()
	return s.ayi9Code
}

// Apply 先完整校验再原子替换当前时间轴；校验失败时状态不变。
// 并发安全：每个协议响应只读取一次状态快照，不会混入切换前后的两份状态。
func (s *Server) Apply(c Change) error {
	s.live.Lock()
	defer s.live.Unlock()
	return s.applyLocked(c, s.cfg.Now())
}

func (s *Server) applyLocked(c Change, now time.Time) error {
	if c.AYI9ErrorCode != nil && (*c.AYI9ErrorCode < 0 || *c.AYI9ErrorCode > 999) {
		return fmt.Errorf("ayi9 错误码必须在 0..999 内")
	}
	manual := c.Mode != nil || c.Online != nil || c.Flash || c.LoadPct != nil || c.BatteryCapacity != nil ||
		c.Temperature != nil || c.InputVoltage != nil || c.DischargeMinutes != nil
	if c.Scenario != nil {
		if manual {
			return fmt.Errorf("scenario 不能与模式、通信状态、闪断或物理参数同时修改")
		}
		scenario, err := c.Scenario.normalized()
		if err != nil {
			return err
		}
		if s.cfg.ET1K && scenario.Phases != 1 {
			return fmt.Errorf("ET1K 模式只支持单相；三相使用标准模式")
		}
		s.seg = segment{origin: now, scenario: scenario, initial: start{Capacity: scenario.BatteryCapacity, Mains: true, Online: true}}
		s.control = ControlScenario
		s.mode = ""
	} else if manual {
		next, mode, err := s.manualSegment(c, now)
		if err != nil {
			return err
		}
		s.seg, s.mode, s.control = next, mode, ControlManual
	} else if c.AYI9ErrorCode == nil {
		return fmt.Errorf("至少需要修改一项")
	}
	if c.AYI9ErrorCode != nil {
		s.ayi9Code = *c.AYI9ErrorCode
	}
	return nil
}

// manualSegment 以 now 时刻的状态为锚点构造新的手动时间轴。
func (s *Server) manualSegment(c Change, now time.Time) (segment, Mode, error) {
	current := s.seg.scenario.stateFrom(s.seg.initial, now.Sub(s.seg.origin))
	mode := s.mode
	if s.control != ControlManual {
		mode = modeOf(current)
	}
	if c.Mode != nil {
		mode = *c.Mode
	}
	if mode != ModeMains && mode != ModeBattery && mode != ModeLow {
		return segment{}, "", fmt.Errorf("mode 必须为 mains、battery 或 low")
	}
	scenario := s.seg.scenario
	scenario.Steps = nil
	// 手动控制的回充上限固定为 100%；场景里的 battery_capacity_pct 只是场景的起始电量。
	scenario.BatteryCapacity = 100
	for _, p := range []struct {
		name  string
		value *float64
		field *float64
	}{
		{"load", c.LoadPct, &scenario.LoadPct}, {"temperature", c.Temperature, &scenario.Temperature},
		{"input-voltage", c.InputVoltage, &scenario.InputVoltage}, {"discharge-minutes", c.DischargeMinutes, &scenario.DischargeMinutes},
	} {
		if p.value == nil {
			continue
		}
		if *p.value == 0 || math.IsNaN(*p.value) || math.IsInf(*p.value, 0) {
			return segment{}, "", fmt.Errorf("%s 必须为非零有限值（0 保留为默认值标记）", p.name)
		}
		*p.field = *p.value
	}
	scenario, err := scenario.normalized()
	if err != nil {
		return segment{}, "", err
	}
	capacity := current.BatteryCapacity
	if c.BatteryCapacity != nil {
		capacity = *c.BatteryCapacity
		if math.IsNaN(capacity) || capacity < 0 || capacity > 100 {
			return segment{}, "", fmt.Errorf("capacity 必须在 0..100 内")
		}
		if mode == ModeLow && capacity > lowCapacity {
			return segment{}, "", fmt.Errorf("low 模式的电量不能超过 %d%%", lowCapacity)
		}
	}
	online := current.Online
	if s.control == ControlManual {
		online = s.seg.initial.Online
	}
	if c.Online != nil {
		online = *c.Online
	}
	initial := start{Capacity: capacity, Mains: mode == ModeMains, Low: mode == ModeLow, Online: online}
	if mode == ModeLow {
		initial.Capacity = math.Min(initial.Capacity, lowCapacity)
	}
	// 已在电池供电（含闪断期间）时延续计时；从市电切入则从此刻开始计时。
	if !initial.Mains && !current.Mains {
		initial.BatteryStart = -time.Duration(current.SecondsOnBattery) * time.Second
	}
	if c.Flash {
		if mode != ModeMains {
			return segment{}, "", fmt.Errorf("闪断只能在市电模式下触发")
		}
		scenario.Steps = []Step{{At: "0s", State: "flash"}}
	}
	return segment{origin: now, scenario: scenario, initial: initial}, mode, nil
}

// Snapshot 返回当前时刻的物理快照与运行期视图，用于展示与持久化。
func (s *Server) Snapshot() Status {
	s.live.RLock()
	seg, control, mode, code := s.seg, s.control, s.mode, s.ayi9Code
	s.live.RUnlock()
	elapsed := s.cfg.Now().Sub(seg.origin)
	state := seg.scenario.stateFrom(seg.initial, elapsed)
	status := Status{Control: control, Mode: mode, Online: state.Online, AYI9ErrorCode: code, State: state,
		Params: Params{LoadPct: seg.scenario.LoadPct, Temperature: seg.scenario.Temperature,
			InputVoltage: seg.scenario.InputVoltage, DischargeMinutes: seg.scenario.DischargeMinutes}}
	if control == ControlManual {
		status.Flashing = len(seg.scenario.Steps) > 0 && elapsed < 5*time.Second
	} else {
		status.Mode = modeOf(state)
	}
	return status
}

// modeOf 由物理状态推导模式；电池供电且低电标志置位（含电量 ≤ 20% 自动置位）视为 low。
func modeOf(state State) Mode {
	switch {
	case state.Mains:
		return ModeMains
	case state.LowBattery:
		return ModeLow
	default:
		return ModeBattery
	}
}
