package profile

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"ups-warden/internal/model"
)

var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// UnitFactor 只支持语义等价且明确列出的单位换算；空原单位表示目录单位。
func UnitFactor(from, to string) (float64, bool) {
	if from == "" || from == to {
		return 1, true
	}
	v, ok := map[string]float64{"min>s": 60, "ms>s": 0.001, "kV>V": 1000, "mV>V": 0.001, "mA>A": 0.001, "kW>W": 1000, "kVA>VA": 1000}[from+">"+to]
	return v, ok
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func validOID(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return false
	}
	nums := make([]uint64, len(parts))
	for i, p := range parts {
		if p == "" {
			return false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
		v, e := strconv.ParseUint(p, 10, 32)
		if e != nil {
			return false
		}
		nums[i] = v
	}
	return nums[0] <= 2 && (nums[0] == 2 || nums[1] <= 39)
}

// Validate 对程序构造对象同样执行完整校验，不修改配置。
func (p *Profile) Validate() error {
	if p == nil {
		return p.fail("$", "nil profile")
	}
	if p.Schema != 1 {
		return p.fail("schema", "unsupported schema")
	}
	if !keyPattern.MatchString(p.Key) {
		return p.fail("key", "invalid key")
	}
	if strings.TrimSpace(p.Name) == "" {
		return p.fail("name", "required")
	}
	if p.Version < 1 {
		return p.fail("version", "must be positive")
	}
	switch p.Protocol {
	case "snmp":
		if p.Modbus != nil || p.AYI9 != nil {
			return p.fail("protocol", "cross-protocol configuration")
		}
		c := p.snmpConfig()
		if c.Strategy != "get" && c.Strategy != "getbulk" && c.Strategy != "walk" {
			return p.fail("snmp.strategy", "must be get, getbulk or walk")
		}
		if !validOID(c.BulkRoot) {
			return p.fail("snmp.bulk_root", "invalid OID")
		}
		if c.MaxRepetitions < 1 || c.MaxRepetitions > 65535 {
			return p.fail("snmp.max_repetitions", "must be 1..65535")
		}
	case "modbus":
		if p.SNMP != nil || p.AYI9 != nil {
			return p.fail("protocol", "cross-protocol configuration")
		}
		if p.Modbus == nil {
			return p.fail("modbus", "required readable ranges")
		}
		c := p.Modbus
		if c.MaxRegistersPerRead < 1 || c.MaxRegistersPerRead > 125 {
			return p.fail("modbus.max_registers_per_read", "must be 1..125")
		}
		if c.UnitID < 0 || c.UnitID > 255 {
			return p.fail("modbus.unit_id", "must be 0..255")
		}
		if c.ByteOrder != "big" && c.ByteOrder != "little" {
			return p.fail("modbus.byte_order", "must be big or little")
		}
		if c.WordOrder != "big" && c.WordOrder != "little" {
			return p.fail("modbus.word_order", "must be big or little")
		}
		if len(c.Readable) == 0 {
			return p.fail("modbus.readable", "required")
		}
		for i, r := range c.Readable {
			path := fmt.Sprintf("modbus.readable[%d]", i)
			if r.FC != 3 && r.FC != 4 {
				return p.fail(path+".fc", "only FC03/FC04 allowed")
			}
			if r.Start < 0 || r.End > 65535 || r.Start > r.End {
				return p.fail(path, "invalid address range")
			}
		}
	case "ayi9-json":
		if p.SNMP != nil || p.Modbus != nil {
			return p.fail("protocol", "cross-protocol configuration")
		}
		c := p.ayi9Config()
		if c.RTDIntervalSeconds < 1 || c.RTDIntervalSeconds > 86400*365 {
			return p.fail("ayi9.rtd_interval_s", "must be 1..31536000")
		}
	default:
		return p.fail("protocol", "unsupported protocol")
	}
	if p.Phases != nil {
		ph := p.Phases
		if ph.From != nil {
			if ph.Input != 0 || ph.Output != 0 || p.lines["phases.input"] != 0 || p.lines["phases.output"] != 0 {
				return p.fail("phases", "cannot mix static and dynamic phases")
			}
			if p.Protocol != "ayi9-json" || ph.From.Cmd != "RTD" || ph.From.Index == nil || *ph.From.Index != 8 {
				return p.fail("phases.from", "only RTD index 8 allowed")
			}
			if err := p.validateSource(*ph.From, 1, "phases.from"); err != nil {
				return err
			}
		} else if ph.Input < 1 || ph.Input > 3 || ph.Output < 1 || ph.Output > 3 {
			return p.fail("phases", "static input/output must be 1..3")
		}
	}
	hints := map[string]bool{}
	for i, k := range p.PowerHints.Indicators {
		path := fmt.Sprintf("power_hints.indicators[%d]", i)
		if !model.IsPowerIndicator(k) || hints[k] {
			return p.fail(path, "invalid or duplicate power indicator")
		}
		hints[k] = true
	}
	if len(p.Fields) == 0 {
		return p.fail("fields", "at least one mapping required")
	}
	seen := map[string]bool{}
	for i, m := range p.Fields {
		path := fmt.Sprintf("fields[%d]", i)
		spec, ok := model.Lookup(m.Field)
		if !ok || spec.Derived || (spec.Phased && model.Phase(m.Field) == 0) || seen[m.Field] {
			return p.fail(path+".field", "unknown, derived, non-instance or duplicate field")
		}
		seen[m.Field] = true
		if err := p.validateSource(m.Source, m.RegisterCount(), path+".source"); err != nil {
			return err
		}
		allowed := []string{"int", "uint16", "int16", "uint32", "int32", "float32", "string", "number_string", "si_number", "bitstring16"}
		if !slices.Contains(allowed, m.Type) {
			return p.fail(path+".type", "unsupported type")
		}
		if p.Protocol == "modbus" && !slices.Contains([]string{"uint16", "int16", "uint32", "int32", "float32"}, m.Type) {
			return p.fail(path+".type", "incompatible Modbus type")
		}
		if p.Protocol == "snmp" && !slices.Contains([]string{"int", "string"}, m.Type) {
			return p.fail(path+".type", "incompatible SNMP type")
		}
		if p.Protocol == "ayi9-json" && !slices.Contains([]string{"string", "number_string", "si_number", "bitstring16"}, m.Type) {
			return p.fail(path+".type", "incompatible AYI9 type")
		}
		if m.Bit != nil && m.Bits != "" {
			return p.fail(path+".bit", "bit and bits are mutually exclusive")
		}
		if m.Bit != nil && (*m.Bit < 0 || *m.Bit > 15) {
			return p.fail(path+".bit", "must be 0..15")
		}
		if m.Bits != "" {
			parts := strings.Split(m.Bits, "-")
			if len(parts) != 2 {
				return p.fail(path+".bits", "expected low-high")
			}
			lo, e1 := strconv.Atoi(parts[0])
			hi, e2 := strconv.Atoi(parts[1])
			if e1 != nil || e2 != nil || lo < 0 || hi > 15 || lo > hi {
				return p.fail(path+".bits", "invalid bit range")
			}
		}
		numeric := m.Type != "string"
		if (m.Bit != nil || m.Bits != "") && (!numeric || m.Type == "float32" || m.Type == "si_number" || m.Type == "number_string") {
			return p.fail(path+".bit", "bit extraction requires integer type")
		}
		if (m.Invert || p.lines[path+".invert"] != 0) && spec.Kind != model.Bool {
			return p.fail(path+".invert", "only bool supports invert")
		}
		if spec.Kind == model.Bool && (m.Bit == nil || len(m.Enum) > 0) {
			return p.fail(path+".bit", "bool requires single bit without enum")
		}
		if m.Bit != nil && spec.Kind != model.Bool && spec.Kind != model.Enum {
			return p.fail(path+".bit", "single bit requires bool or enum")
		}
		if spec.Kind == model.Text && (m.Type != "string" || m.Bit != nil || m.Bits != "" || len(m.Enum) > 0) {
			return p.fail(path+".type", "text requires string")
		}
		if (spec.Kind == model.Number || spec.Kind == model.Duration) && (!numeric || len(m.Enum) > 0) {
			return p.fail(path+".type", "number requires numeric decode without enum")
		}
		if spec.Kind == model.Enum {
			if len(m.Enum) == 0 {
				return p.fail(path+".enum", "enum mapping required")
			}
			for k, v := range m.Enum {
				if !slices.Contains(spec.Enum, v) {
					return p.fail(path+".enum."+k, "invalid target enum")
				}
			}
		} else if len(m.Enum) > 0 {
			return p.fail(path+".enum", "only enum field supports enum mapping")
		}
		if m.Scale != nil && !finite(*m.Scale) {
			return p.fail(path+".scale", "must be finite")
		}
		if !finite(m.Offset) {
			return p.fail(path+".offset", "must be finite")
		}
		if _, ok := UnitFactor(m.Unit, spec.Unit); !ok {
			return p.fail(path+".unit", "unsupported unit conversion")
		}
		if spec.Kind != model.Number && spec.Kind != model.Duration && ((m.Scale != nil && *m.Scale != 1) || m.Offset != 0 || m.Unit != "") {
			return p.fail(path+".scale", "transforms only allowed on numeric fields")
		}
		if len(m.Invalid.Range) > 0 {
			if (spec.Kind != model.Number && spec.Kind != model.Duration) || len(m.Invalid.Range) != 2 || !finite(m.Invalid.Range[0]) || !finite(m.Invalid.Range[1]) || m.Invalid.Range[0] > m.Invalid.Range[1] {
				return p.fail(path+".invalid.range", "expected finite ordered unified numeric range")
			}
		}
	}
	return nil
}
func (p *Profile) validateSource(s Source, count int, path string) error {
	// 字符串零值无法区分未声明和显式空值；解析文档另用位置表拒绝跨协议键。
	allowed := map[string][]string{"snmp": {"oid"}, "modbus": {"fc", "addr"}, "ayi9-json": {"cmd", "index"}}[p.Protocol]
	for _, key := range []string{"oid", "fc", "addr", "cmd", "index"} {
		if p.lines[path+"."+key] != 0 && !slices.Contains(allowed, key) {
			return p.fail(path+"."+key, "cross-protocol source key")
		}
	}
	switch p.Protocol {
	case "snmp":
		if !validOID(s.OID) || s.FC != nil || s.Addr != nil || s.Cmd != "" || s.Index != nil {
			return p.fail(path, "expected SNMP OID only")
		}
	case "modbus":
		if s.OID != "" || s.Cmd != "" || s.Index != nil || s.FC == nil || s.Addr == nil {
			return p.fail(path, "expected FC/address only")
		}
		if *s.FC != 3 && *s.FC != 4 {
			return p.fail(path+".fc", "only FC03/FC04 allowed")
		}
		if *s.Addr < 0 || *s.Addr > 65535 || count > p.Modbus.MaxRegistersPerRead || *s.Addr+count-1 > 65535 {
			return p.fail(path+".addr", "invalid complete register span")
		}
		covered := false
		for _, r := range readableUnion(p.Modbus.Readable) {
			if r.FC == *s.FC && r.Start <= *s.Addr && r.End >= *s.Addr+count-1 {
				covered = true
			}
		}
		if !covered {
			return p.fail(path+".addr", "span outside readable ranges")
		}
	case "ayi9-json":
		if s.OID != "" || s.FC != nil || s.Addr != nil || s.Index == nil || (s.Cmd != "QAD" && s.Cmd != "RTD") {
			return p.fail(path, "expected QAD/RTD index only")
		}
		limit := 22
		if s.Cmd == "RTD" {
			limit = 9
		}
		if *s.Index < 0 || *s.Index > limit {
			return p.fail(path+".index", "index outside command range")
		}
	}
	return nil
}
