// Package normalize 将协议原始数据按 Profile 转为统一模型；不执行 I/O。
package normalize

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"ups-warden/internal/driver"
	"ups-warden/internal/model"
	"ups-warden/internal/profile"
)

// Trace 仅用于诊断，含协议位置和原始值，不得写入历史或用于规则判断。
// 未映射、无效和不支持的字段同样有 Trace，方便解释缺值而非误报为 0。
type Trace struct {
	Field   string        `json:"field"`
	Source  string        `json:"source,omitempty"`
	Raw     any           `json:"raw,omitempty"`
	Decoded any           `json:"decoded,omitempty"`
	Final   model.Field   `json:"final"`
	Quality model.Quality `json:"quality"`
	Reason  string        `json:"reason,omitempty"`
}

// Normalize 返回独立的统一快照和按字段排序的诊断记录，不修改 raw 或 p。
// 非法 Profile 或协议不匹配返回错误；单个字段失败只降低该字段质量。
// 派生字段由状态引擎产生，不出现在单数据源快照中。
func Normalize(sourceID int64, raw driver.RawData, p *profile.Profile) (model.SourceSnapshot, []Trace, error) {
	if p == nil {
		return model.SourceSnapshot{}, nil, errors.New("归一化缺少 Profile")
	}
	if err := p.Validate(); err != nil {
		return model.SourceSnapshot{}, nil, fmt.Errorf("归一化 Profile 无效: %w", err)
	}
	if raw.Protocol != p.Protocol {
		return model.SourceSnapshot{}, nil, errors.New("原始协议与 Profile 不匹配")
	}
	snapshot := model.SourceSnapshot{SourceID: sourceID, At: raw.At.UTC(), Fields: make(map[string]model.Field), Hints: model.PowerHints{Indicators: slices.Clone(p.PowerHints.Indicators)}}
	traces := make(map[string]Trace)
	for _, key := range model.Keys() {
		spec, _ := model.Lookup(key)
		if spec.Derived {
			continue
		}
		field := bad(spec, model.Unsupported, model.ReasonNotMapped)
		snapshot.Fields[key] = field
		traces[key] = Trace{Field: key}
	}
	for _, mapping := range p.Fields {
		spec, _ := model.Lookup(mapping.Field)
		value, quality, reason := extract(raw, mapping)
		trace := Trace{Field: mapping.Field, Source: sourceName(mapping.Source), Raw: value}
		var field model.Field
		if quality != model.Good {
			field = bad(spec, quality, reason)
		} else {
			field, trace.Decoded = convert(value, mapping, spec, p)
		}
		snapshot.Fields[mapping.Field] = field
		traces[mapping.Field] = trace
	}
	applyPhases(&snapshot, traces, raw, p)
	result := make([]Trace, 0, len(traces))
	for _, key := range model.Keys() {
		trace, ok := traces[key]
		if !ok {
			continue
		}
		trace.Final = snapshot.Fields[key]
		trace.Quality, trace.Reason = trace.Final.Quality, trace.Final.Reason
		result = append(result, trace)
	}
	return snapshot, result, nil
}

func bad(spec model.FieldSpec, quality model.Quality, reason string) model.Field {
	field := model.Field{Quality: quality, Reason: reason}
	if spec.Kind == model.Enum {
		field.Str = "unknown"
	}
	return field
}

func sourceName(source profile.Source) string {
	switch {
	case source.OID != "":
		return "snmp:" + source.OID
	case source.FC != nil && source.Addr != nil:
		return fmt.Sprintf("modbus:FC%02d/%d", *source.FC, *source.Addr)
	case source.Index != nil:
		return fmt.Sprintf("ayi9:%s[%d]", source.Cmd, *source.Index)
	default:
		return ""
	}
}

func extract(raw driver.RawData, m profile.Mapping) (any, model.Quality, string) {
	for _, failure := range raw.Errors {
		matches := false
		switch raw.Protocol {
		case driver.ProtocolSNMP:
			matches = failure.OID == m.Source.OID
		case driver.ProtocolModbus:
			if m.Source.FC != nil && m.Source.Addr != nil {
				start, end := *m.Source.Addr, *m.Source.Addr+m.RegisterCount()
				matches = int(failure.FC) == *m.Source.FC && int(failure.Start) < end && int(failure.Start)+int(failure.Count) > start
			}
		case driver.ProtocolAYI9:
			matches = failure.Command == m.Source.Cmd
		}
		if matches {
			if failure.Kind == "unsupported" {
				return nil, model.Unsupported, model.ReasonDeviceUnsupported
			}
			return nil, model.Invalid, model.ReasonDecodeError
		}
	}
	switch raw.Protocol {
	case driver.ProtocolSNMP:
		value, ok := raw.SNMP[m.Source.OID]
		if !ok || value.Exception != "" {
			return nil, model.Unsupported, model.ReasonDeviceUnsupported
		}
		switch value.Tag {
		case 2:
			return value.Int, model.Good, ""
		case 0x41, 0x42, 0x43, 0x46:
			return value.Uint, model.Good, ""
		case 4, 6:
			return value.Text, model.Good, ""
		default:
			return slices.Clone(value.Bytes), model.Invalid, model.ReasonDecodeError
		}
	case driver.ProtocolModbus:
		if m.Source.FC == nil || m.Source.Addr == nil {
			return nil, model.Invalid, model.ReasonDecodeError
		}
		words := make([]uint16, m.RegisterCount())
		for i := range words {
			value, ok := raw.Modbus[driver.RegKey{FC: driver.ReadFunc(*m.Source.FC), Addr: uint16(*m.Source.Addr + i)}]
			if !ok {
				return nil, model.Unsupported, model.ReasonDeviceUnsupported
			}
			words[i] = value
		}
		return words, model.Good, ""
	case driver.ProtocolAYI9:
		if raw.AYI9 == nil || m.Source.Index == nil {
			return nil, model.Unsupported, model.ReasonDeviceUnsupported
		}
		values, size := raw.AYI9.QAD, 23
		if m.Source.Cmd == "RTD" {
			values, size = raw.AYI9.RTD, 10
		}
		if values == nil {
			return nil, model.Unsupported, model.ReasonDeviceUnsupported
		}
		if len(values) != size {
			return slices.Clone(values), model.Invalid, model.ReasonDecodeError
		}
		if *m.Source.Index < 0 || *m.Source.Index >= size {
			return nil, model.Invalid, model.ReasonDecodeError
		}
		return values[*m.Source.Index], model.Good, ""
	}
	return nil, model.Invalid, model.ReasonDecodeError
}

func convert(raw any, m profile.Mapping, spec model.FieldSpec, p *profile.Profile) (model.Field, any) {
	if text, ok := raw.(string); ok {
		text = strings.TrimSpace(text)
		if text == "" || text == "---" || slices.Contains(m.Invalid.Placeholders, text) {
			return bad(spec, model.Invalid, model.ReasonPlaceholder), nil
		}
	}
	decoded, err := decode(raw, m.Type, p.Modbus)
	if err != nil {
		return bad(spec, model.Invalid, model.ReasonDecodeError), nil
	}
	// 无效常量对解码后的原始域匹配，避免缩放后 65535 等哨兵值漏判。
	for _, invalid := range m.Invalid.Values {
		if equalInvalid(decoded, invalid) {
			return bad(spec, model.Invalid, model.ReasonInvalidValue), decoded
		}
	}
	value := decoded
	if m.Bit != nil || m.Bits != "" {
		number, ok := value.(float64)
		if !ok || number < 0 || number > 65535 || math.Trunc(number) != number {
			return bad(spec, model.Invalid, model.ReasonDecodeError), decoded
		}
		low, high := 0, 0
		if m.Bit != nil {
			low, high = *m.Bit, *m.Bit
		} else {
			pieces := strings.Split(m.Bits, "-")
			if len(pieces) != 2 {
				return bad(spec, model.Invalid, model.ReasonDecodeError), decoded
			}
			low, _ = strconv.Atoi(pieces[0])
			high, _ = strconv.Atoi(pieces[1])
		}
		if low < 0 || high < low || high > 15 {
			return bad(spec, model.Invalid, model.ReasonDecodeError), decoded
		}
		value = float64((uint16(number) >> low) & uint16((uint32(1)<<(high-low+1))-1))
	}
	if spec.Kind == model.Bool {
		number, ok := value.(float64)
		if !ok || (number != 0 && number != 1) {
			return bad(spec, model.Invalid, model.ReasonOutOfRange), decoded
		}
		flag := number == 1
		if m.Invert {
			flag = !flag
		}
		return model.Field{Bool: flag, Quality: model.Good}, decoded
	}
	if spec.Kind == model.Enum {
		key := ""
		switch typed := value.(type) {
		case string:
			key = typed
		case float64:
			key = strconv.FormatFloat(typed, 'f', -1, 64)
		}
		mapped, ok := m.Enum[key]
		if !ok || !slices.Contains(spec.Enum, mapped) {
			return bad(spec, model.Invalid, model.ReasonEnumUnmapped), decoded
		}
		return model.Field{Str: mapped, Quality: model.Good}, decoded
	}
	if spec.Kind == model.Text {
		text, ok := value.(string)
		if !ok {
			return bad(spec, model.Invalid, model.ReasonDecodeError), decoded
		}
		return model.Field{Str: strings.TrimSpace(text), Quality: model.Good}, decoded
	}
	number, ok := value.(float64)
	if !ok {
		return bad(spec, model.Invalid, model.ReasonDecodeError), decoded
	}
	scale := 1.0
	if m.Scale != nil {
		scale = *m.Scale
	}
	factor, ok := profile.UnitFactor(m.Unit, spec.Unit)
	if !ok {
		return bad(spec, model.Invalid, model.ReasonDecodeError), decoded
	}
	number = (number*scale + m.Offset) * factor
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return bad(spec, model.Invalid, model.ReasonOutOfRange), decoded
	}
	if len(m.Invalid.Range) == 2 && (number < m.Invalid.Range[0] || number > m.Invalid.Range[1]) {
		return bad(spec, model.Invalid, model.ReasonOutOfRange), decoded
	}
	if spec.Unit == "%" && (number < 0 || number > 100) {
		return bad(spec, model.Invalid, model.ReasonOutOfRange), decoded
	}
	if spec.Kind == model.Duration {
		if number < 0 {
			return bad(spec, model.Invalid, model.ReasonOutOfRange), decoded
		}
		number = math.Trunc(number)
	}
	if spec.Key == "input_phases" || spec.Key == "output_phases" {
		if number < 1 || number > 3 || math.Trunc(number) != number {
			return bad(spec, model.Invalid, model.ReasonOutOfRange), decoded
		}
	}
	return model.Field{Num: number, Quality: model.Good}, decoded
}

func equalInvalid(value any, invalid string) bool {
	switch typed := value.(type) {
	case string:
		return typed == strings.TrimSpace(invalid)
	case float64:
		number, err := strconv.ParseFloat(strings.TrimSpace(invalid), 64)
		return err == nil && typed == number
	default:
		return false
	}
}
