package normalize

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/model"
	"ups-warden/internal/profile"
)

func ptr[T any](value T) *T { return &value }

const testOID = "1.3.6.1.2.1.33.1.2.5.0"

func snmpProfile(m profile.Mapping) *profile.Profile {
	m.Source = profile.Source{OID: testOID}
	return &profile.Profile{Schema: 1, Key: "test", Name: "测试", Version: 1, Protocol: driver.ProtocolSNMP, SNMP: &profile.SNMPConfig{Strategy: "getbulk", BulkRoot: "1.3.6.1.2.1.33", MaxRepetitions: 60}, Fields: []profile.Mapping{m}}
}

func TestNumericPipeline(t *testing.T) {
	for _, tc := range []struct {
		name, field, kind, unit string
		raw                     driver.SNMPValue
		scale                   *float64
		offset                  float64
		invalid                 profile.InvalidRule
		want                    float64
		quality                 model.Quality
		reason                  string
	}{
		{name: "scale-offset", field: "battery_voltage", kind: "int", raw: driver.SNMPValue{Tag: 2, Int: 294}, scale: ptr(0.1), offset: 1, want: 30.4, quality: model.Good},
		{name: "zero-scale", field: "battery_voltage", kind: "int", raw: driver.SNMPValue{Tag: 2, Int: 294}, scale: ptr(0.0), want: 0, quality: model.Good},
		{name: "minutes", field: "native_runtime_remaining", kind: "int", unit: "min", raw: driver.SNMPValue{Tag: 2, Int: 4}, want: 240, quality: model.Good},
		{name: "duration-integer", field: "native_runtime_remaining", kind: "int", raw: driver.SNMPValue{Tag: 2, Int: 19}, scale: ptr(0.1), want: 1, quality: model.Good},
		{name: "negative-duration", field: "native_runtime_remaining", kind: "int", raw: driver.SNMPValue{Tag: 2, Int: -1}, quality: model.Invalid, reason: model.ReasonOutOfRange},
		{name: "raw-sentinel", field: "battery_voltage", kind: "int", raw: driver.SNMPValue{Tag: 2, Int: 65535}, scale: ptr(0.1), invalid: profile.InvalidRule{Values: []string{"65535"}}, quality: model.Invalid, reason: model.ReasonInvalidValue},
		{name: "range", field: "battery_voltage", kind: "int", raw: driver.SNMPValue{Tag: 2, Int: 294}, scale: ptr(0.1), invalid: profile.InvalidRule{Range: []float64{0, 25}}, quality: model.Invalid, reason: model.ReasonOutOfRange},
		{name: "percent", field: "battery_capacity_pct", kind: "int", raw: driver.SNMPValue{Tag: 2, Int: 101}, quality: model.Invalid, reason: model.ReasonOutOfRange},
		{name: "zero-percent", field: "battery_capacity_pct", kind: "int", raw: driver.SNMPValue{Tag: 2, Int: 0}, quality: model.Good},
		{name: "exception", field: "battery_voltage", kind: "int", raw: driver.SNMPValue{Tag: 0x82, Exception: "endOfMibView"}, quality: model.Unsupported, reason: model.ReasonDeviceUnsupported},
		{name: "placeholder", field: "battery_voltage", kind: "int", raw: driver.SNMPValue{Tag: 4, Text: " --- "}, quality: model.Invalid, reason: model.ReasonPlaceholder},
		{name: "nan", field: "battery_voltage", kind: "int", raw: driver.SNMPValue{Tag: 4, Text: "NaN"}, quality: model.Invalid, reason: model.ReasonDecodeError},
		{name: "overflow", field: "battery_voltage", kind: "int", raw: driver.SNMPValue{Tag: 2, Int: 10}, scale: ptr(math.MaxFloat64), quality: model.Invalid, reason: model.ReasonOutOfRange},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := snmpProfile(profile.Mapping{Field: tc.field, Type: tc.kind, Scale: tc.scale, Offset: tc.offset, Unit: tc.unit, Invalid: tc.invalid})
			raw := driver.RawData{Protocol: driver.ProtocolSNMP, At: time.Date(2026, 10, 14, 10, 0, 0, 0, time.FixedZone("local", 8*3600)), SNMP: map[string]driver.SNMPValue{testOID: tc.raw}}
			snapshot, traces, err := Normalize(7, raw, p)
			if err != nil {
				t.Fatal(err)
			}
			got := snapshot.Fields[tc.field]
			if got.Quality != tc.quality || got.Reason != tc.reason || math.Abs(got.Num-tc.want) > 1e-8 {
				t.Fatalf("值或质量错误: %+v", got)
			}
			if snapshot.SourceID != 7 || snapshot.At.Location() != time.UTC || len(traces) != len(snapshot.Fields) {
				t.Fatal("快照元数据或Trace错误")
			}
			if _, exists := snapshot.Fields["power_state"]; exists {
				t.Fatal("采集层不得生成派生状态")
			}
			if _, err := json.Marshal(snapshot); err != nil {
				t.Fatalf("快照无法序列化: %v", err)
			}
		})
	}
}

func TestEnumBitsAndText(t *testing.T) {
	p := snmpProfile(profile.Mapping{Field: "output_source", Type: "int", Enum: map[string]string{"3": "normal", "5": "battery"}})
	raw := driver.RawData{Protocol: driver.ProtocolSNMP, SNMP: map[string]driver.SNMPValue{testOID: {Tag: 2, Int: 99}}}
	got, traces, err := Normalize(1, raw, p)
	if err != nil {
		t.Fatal(err)
	}
	if field := got.Fields["output_source"]; field.Str != "unknown" || field.Quality != model.Invalid || field.Reason != model.ReasonEnumUnmapped {
		t.Fatalf("枚举未知处理: %+v", field)
	}
	found := false
	for _, trace := range traces {
		if trace.Field == "output_source" {
			found = trace.Raw == int64(99) && trace.Decoded == float64(99)
		}
	}
	if !found {
		t.Fatal("Trace丢失原始枚举")
	}
	for _, text := range []string{"  ET1K  ", "---", "", "N/A"} {
		p := snmpProfile(profile.Mapping{Field: "model", Type: "string", Invalid: profile.InvalidRule{Placeholders: []string{"N/A"}}})
		raw.SNMP[testOID] = driver.SNMPValue{Tag: 4, Text: text}
		s, _, err := Normalize(1, raw, p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, "ET1K") {
			if s.Fields["model"].Str != "ET1K" {
				t.Fatal("文本未trim")
			}
		} else if s.Fields["model"].Reason != model.ReasonPlaceholder {
			t.Fatal("占位符未处理")
		}
	}
}

func TestModbusDecodeOrders(t *testing.T) {
	for _, tc := range []struct {
		name, kind, byteOrder, wordOrder string
		words                            []uint16
		want                             float64
	}{
		{"u16", "uint16", "big", "big", []uint16{65535}, 65535},
		{"s16", "int16", "big", "big", []uint16{65535}, -1},
		{"u32", "uint32", "big", "big", []uint16{1, 2}, 65538},
		{"s32", "int32", "big", "big", []uint16{65535, 65534}, -2},
		{"byte-little", "uint32", "little", "big", []uint16{0x0100, 0x0200}, 65538},
		{"word-little", "uint32", "big", "little", []uint16{2, 1}, 65538},
		{"both-little", "uint32", "little", "little", []uint16{0x0200, 0x0100}, 65538},
		{"float", "float32", "big", "big", []uint16{0x3fc0, 0}, 1.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &profile.Profile{Schema: 1, Key: "test", Name: "测试", Version: 1, Protocol: driver.ProtocolModbus, Modbus: &profile.ModbusConfig{Readable: []profile.ReadableRange{{FC: 3, Start: 0, End: 79}}, MaxRegistersPerRead: 100, ByteOrder: tc.byteOrder, WordOrder: tc.wordOrder}, Fields: []profile.Mapping{{Field: "battery_voltage", Type: tc.kind, Source: profile.Source{FC: ptr(3), Addr: ptr(0)}}}}
			raw := driver.RawData{Protocol: driver.ProtocolModbus, Modbus: map[driver.RegKey]uint16{}}
			for i, word := range tc.words {
				raw.Modbus[driver.RegKey{FC: 3, Addr: uint16(i)}] = word
			}
			s, traces, err := Normalize(1, raw, p)
			if err != nil {
				t.Fatal(err)
			}
			if got := s.Fields["battery_voltage"]; got.Quality != model.Good || got.Num != tc.want {
				t.Fatalf("解码错误: %+v", got)
			}
			for _, trace := range traces {
				if words, ok := trace.Raw.([]uint16); ok {
					words[0] = 0
				}
			}
			if raw.Modbus[driver.RegKey{FC: 3, Addr: 0}] != tc.words[0] {
				t.Fatal("Trace修改原始寄存器")
			}
			delete(raw.Modbus, driver.RegKey{FC: 3, Addr: 0})
			s, _, err = Normalize(1, raw, p)
			if err != nil {
				t.Fatal(err)
			}
			if s.Fields["battery_voltage"].Quality != model.Unsupported {
				t.Fatal("缺失寄存器被补零")
			}
		})
	}
}

func ayi9Raw() driver.RawData {
	return driver.RawData{Protocol: driver.ProtocolAYI9, AYI9: &driver.AYI9Raw{
		QAD: strings.Fields("226.6 0 0 49.9 0 0 0 0 220 0 0 50 1 0 0 27 0 0 29.4 0 100 53 0000000000010011"),
		RTD: strings.Fields("--- s9 ET1K 220 220 0 50 0 1/1 1K"),
	}}
}

func TestAYI9PhasesAndState(t *testing.T) {
	p, err := profile.Builtin("et1k-ayi9-json")
	if err != nil {
		t.Fatal(err)
	}
	raw := ayi9Raw()
	before, _ := json.Marshal(raw.AYI9)
	s, _, err := Normalize(1, raw, p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Fields["ups_mode"].Str != "line" || !s.Fields["mains_ok"].Bool || s.Fields["rated_va"].Num != 1000 {
		t.Fatalf("状态或SI解码错误: %+v", s.Fields)
	}
	if s.Fields["native_runtime_remaining"].Reason != model.ReasonInvalidValue {
		t.Fatal("ET1K原生0剩余时间应无效")
	}
	for _, key := range []string{"input_voltage[2]", "output_current[2]", "output_load_pct[3]"} {
		if s.Fields[key].Reason != model.ReasonPhaseInactive {
			t.Errorf("无效相未屏蔽: %s %+v", key, s.Fields[key])
		}
	}
	raw.AYI9.QAD[22] = "0000000010010100"
	s, _, err = Normalize(1, raw, p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Fields["ups_mode"].Str != "battery" || s.Fields["mains_ok"].Bool {
		t.Fatal("电池状态字解释错误")
	}
	raw.AYI9.QAD[22] = "0000000000001111"
	s, _, err = Normalize(1, raw, p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Fields["ups_mode"].Reason != model.ReasonEnumUnmapped {
		t.Fatal("未知模式未降级")
	}
	raw.AYI9.QAD[22] = "0000000000010011"
	after, _ := json.Marshal(raw.AYI9)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("归一化修改输入")
	}
	generic, err := profile.Builtin("ayi9-json-generic")
	if err != nil {
		t.Fatal(err)
	}
	raw.AYI9.RTD[8] = "3/3"
	s, _, err = Normalize(1, raw, generic)
	if err != nil {
		t.Fatal(err)
	}
	if s.Fields["output_current[2]"].Quality != model.Good || s.Fields["input_phases"].Num != 3 {
		t.Fatal("三相数据错误")
	}
	raw.AYI9.RTD[8] = "broken"
	s, _, err = Normalize(1, raw, generic)
	if err != nil {
		t.Fatal(err)
	}
	if s.Fields["output_current[2]"].Quality == model.Good || s.Fields["output_phases"].Reason != model.ReasonDecodeError {
		t.Fatal("坏相数字段不应产生有效零")
	}
	raw.AYI9.RTD = nil
	raw.AYI9.QAD = []string{"1", "2"}
	s, _, err = Normalize(1, raw, generic)
	if err != nil {
		t.Fatal(err)
	}
	if s.Fields["battery_voltage"].Reason != model.ReasonDecodeError || s.Fields["model"].Quality != model.Unsupported {
		t.Fatal("字段数或缺失处理错误")
	}
}

func TestPartialErrorsAndInvalidProfile(t *testing.T) {
	p := snmpProfile(profile.Mapping{Field: "battery_voltage", Type: "int"})
	raw := driver.RawData{Protocol: driver.ProtocolSNMP, SNMP: map[string]driver.SNMPValue{testOID: {Tag: 2, Int: 294}}, Errors: []driver.BlockError{{OID: testOID, Kind: "unsupported"}}}
	s, _, err := Normalize(1, raw, p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Fields["battery_voltage"].Quality != model.Unsupported {
		t.Fatal("错误块被忽略")
	}
	raw.Errors[0].Kind = "decode_error"
	s, _, err = Normalize(1, raw, p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Fields["battery_voltage"].Reason != model.ReasonDecodeError {
		t.Fatal("解码错误未传播")
	}
	raw.Protocol = driver.ProtocolAYI9
	if _, _, err := Normalize(1, raw, p); err == nil {
		t.Fatal("应拒绝协议不匹配")
	}
	if _, _, err := Normalize(1, raw, nil); err == nil {
		t.Fatal("应拒绝nil Profile")
	}
	p.Fields[0].Field = "not_a_field"
	if _, _, err := Normalize(1, driver.RawData{Protocol: driver.ProtocolSNMP}, p); err == nil {
		t.Fatal("应拒绝非法Profile")
	}
}

func FuzzNormalizeAYI9(f *testing.F) {
	for _, seed := range []string{"100", "NaN", "---", "-1", "101", "1e999"} {
		f.Add(seed, "1/1", uint8(20))
	}
	p, err := profile.Builtin("ayi9-json-generic")
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, value, phases string, index uint8) {
		if len(value) > 4096 || len(phases) > 128 {
			t.Skip()
		}
		raw := ayi9Raw()
		raw.AYI9.QAD[int(index)%23] = value
		raw.AYI9.RTD[8] = phases
		result, _, err := Normalize(1, raw, p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := json.Marshal(result); err != nil {
			t.Fatalf("字段不能JSON序列化: %v", err)
		}
		for key, field := range result.Fields {
			if field.Quality == model.Good {
				spec, _ := model.Lookup(key)
				if spec.Unit == "%" && (field.Num < 0 || field.Num > 100) {
					t.Fatalf("百分比越界: %s", strconv.FormatFloat(field.Num, 'g', -1, 64))
				}
			}
		}
	})
}

func TestYAMLNumericSentinels(t *testing.T) {
	for _, sentinel := range []string{"65535", "0xFFFF", "0b1111111111111111", "0o177777", "0177777", "65_535", "6.5535e4"} {
		t.Run(sentinel, func(t *testing.T) {
			p, err := profile.Parse([]byte(`schema: 1
key: sentinel-test
name: 哨兵测试
version: 1
protocol: modbus
modbus:
  readable: [{fc: 3, start: 0, end: 0}]
fields:
  - field: battery_voltage
    source: {fc: 3, addr: 0}
    type: uint16
    scale: 0.1
    invalid: {values: [` + sentinel + `]}
`))
			if err != nil {
				t.Fatal(err)
			}
			raw := driver.RawData{Protocol: driver.ProtocolModbus, Modbus: map[driver.RegKey]uint16{{FC: 3, Addr: 0}: 65535}}
			snapshot, _, err := Normalize(1, raw, p)
			if err != nil {
				t.Fatal(err)
			}
			field := snapshot.Fields["battery_voltage"]
			if field.Quality != model.Invalid || field.Reason != model.ReasonInvalidValue {
				t.Fatalf("数值哨兵未生效: %+v", field)
			}
		})
	}
}
