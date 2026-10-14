package profile

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"ups-warden/internal/driver"
)

const simpleSNMP = "schema: 1\nkey: test\nname: Test\nversion: 1\nprotocol: snmp\nfields:\n  - field: battery_voltage\n    source: {oid: 1.3.6.1.2.1.33.1.2.5.0}\n    type: int\n"
const simpleMB = "schema: 1\nkey: test\nname: Test\nversion: 1\nprotocol: modbus\nmodbus:\n  readable: [{fc: 3, start: 0, end: 65535}]\nfields:\n  - field: battery_voltage\n    source: {fc: 3, addr: 0}\n    type: uint16\n"
const simpleAYI = "schema: 1\nkey: test\nname: Test\nversion: 1\nprotocol: ayi9-json\nfields:\n  - field: battery_voltage\n    source: {cmd: QAD, index: 18}\n    type: number_string\n"

func mustParse(t *testing.T, data string) *Profile {
	t.Helper()
	p, err := Parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func mapping(t *testing.T, p *Profile, key string) Mapping {
	t.Helper()
	for _, m := range p.Fields {
		if m.Field == key {
			return m
		}
	}
	t.Fatalf("%s missing %s", p.Key, key)
	return Mapping{}
}

func TestDefaultsAndJSON(t *testing.T) {
	p := mustParse(t, simpleSNMP)
	if !reflect.DeepEqual(p.SNMP, defaultSNMP()) || *p.Fields[0].Scale != 1 {
		t.Fatalf("bad defaults: %+v", p)
	}
	p = mustParse(t, simpleMB)
	if p.Modbus.MaxRegistersPerRead != 100 || p.Modbus.ByteOrder != "big" || p.Modbus.WordOrder != "big" || p.Modbus.UnitID != 0 {
		t.Fatal(p.Modbus)
	}
	p = mustParse(t, simpleAYI)
	if p.AYI9.RTDIntervalSeconds != 600 {
		t.Fatal(p.AYI9)
	}
	p = mustParse(t, strings.Replace(simpleSNMP, "type: int", "type: int\n    scale: 0", 1))
	if *p.Fields[0].Scale != 0 {
		t.Fatal("explicit zero scale overwritten")
	}
	p = mustParse(t, `{"schema":1,"key":"json","name":"JSON","version":1,"protocol":"snmp","fields":[{"field":"battery_voltage","source":{"oid":"1.3.6.1.2.1.33.1.2.5.0"},"type":"int","invalid":{"values":[0,999],"range":[0,300]}}]}`)
	if !reflect.DeepEqual(p.Fields[0].Invalid.Values, []string{"0", "999"}) {
		t.Fatal(p.Fields[0].Invalid)
	}
	for _, protocol := range []string{"snmp", "ayi9-json"} {
		p := &Profile{Schema: 1, Key: "empty", Name: "Empty", Version: 1, Protocol: protocol}
		if protocol == "snmp" {
			p.SNMP = &SNMPConfig{}
			p.Fields = []Mapping{{Field: "battery_voltage", Source: Source{OID: "1.3.6"}, Type: "int"}}
		} else {
			i := 18
			p.AYI9 = &AYI9Config{}
			p.Fields = []Mapping{{Field: "battery_voltage", Source: Source{Cmd: "QAD", Index: &i}, Type: "number_string"}}
		}
		plan, err := p.ReadPlan()
		if err != nil {
			t.Fatal(err)
		}
		if protocol == "snmp" && plan.SNMP.MaxRepetitions != 60 {
			t.Fatal(plan)
		}
		if protocol == "ayi9-json" && plan.AYI9.RTDInterval != 10*time.Minute {
			t.Fatal(plan)
		}
	}
	var nilProfile *Profile
	if err := nilProfile.Validate(); err == nil {
		t.Fatal("nil accepted")
	}
	if _, err := nilProfile.ReadPlan(); err == nil {
		t.Fatal("nil plan accepted")
	}
}

func TestStrictErrors(t *testing.T) {
	cases := []struct{ name, data, path, line string }{
		{"unknown", simpleSNMP + "typo: true\n", "typo", "line 10"},
		{"nested unknown", strings.Replace(simpleSNMP, "oid: 1.3.6.1.2.1.33.1.2.5.0", "oid: 1.3.6, write: true", 1), "fields[0].source.write", "line 8"},
		{"duplicate", simpleSNMP + "schema: 1\n", "schema", "line 10"},
		{"nested duplicate", strings.Replace(simpleSNMP, "oid: 1.3.6.1.2.1.33.1.2.5.0", "oid: 1.3.6, oid: 1.3.7", 1), "fields[0].source.oid", "line 8"},
		{"trailing", simpleSNMP + "---\n{}\n", "$", "line"},
		{"null config", simpleSNMP + "snmp: null\n", "snmp", "line 10"},
		{"null scale", simpleSNMP + "    scale: null\n", "fields[0].scale", "line 10"},
		{"null enum", simpleSNMP + "    enum: {1: null}\n", "fields[0].enum.1", "line 10"},
		{"null source", strings.Replace(simpleSNMP, "{oid: 1.3.6.1.2.1.33.1.2.5.0}", "null", 1), "fields[0].source", "line 8"},
		{"alias", simpleSNMP + "snmp: &c {}\nayi9: *c\n", "ayi9", "line 11"},
		{"merge", simpleSNMP + "snmp: {<<: {strategy: getbulk}}\n", "snmp.<<", "line 10"},
		{"typed integer", strings.Replace(simpleSNMP, "version: 1", "version: 1.5", 1), "version", "line 4"},
		{"schema", strings.Replace(simpleSNMP, "schema: 1", "schema: 2", 1), "schema", "line 1"},
		{"key", strings.Replace(simpleSNMP, "key: test", "key: Bad Key", 1), "key", "line 2"},
		{"name", strings.Replace(simpleSNMP, "name: Test", "name: ''", 1), "name", "line 3"},
		{"version", strings.Replace(simpleSNMP, "version: 1", "version: 0", 1), "version", "line 4"},
		{"protocol", strings.Replace(simpleSNMP, "protocol: snmp", "protocol: http", 1), "protocol", "line 5"},
		{"configuration", simpleSNMP + "ayi9: {}\n", "protocol", "line 5"},
		{"write strategy", simpleSNMP + "snmp: {strategy: set}\n", "snmp.strategy", "line 10"},
		{"bad root", simpleSNMP + "snmp: {bulk_root: 1.40.3}\n", "snmp.bulk_root", "line 10"},
		{"zero bulk", simpleSNMP + "snmp: {max_repetitions: 0}\n", "snmp.max_repetitions", "line 10"},
		{"unknown field", strings.Replace(simpleSNMP, "field: battery_voltage", "field: nope", 1), "fields[0].field", "line 7"},
		{"derived", strings.Replace(simpleSNMP, "field: battery_voltage", "field: runtime_estimate", 1), "fields[0].field", "line 7"},
		{"base phase", strings.Replace(simpleSNMP, "field: battery_voltage", "field: input_voltage", 1), "fields[0].field", "line 7"},
		{"phase 4", strings.Replace(simpleSNMP, "field: battery_voltage", "field: input_voltage[4]", 1), "fields[0].field", "line 7"},
		{"OID", strings.Replace(simpleSNMP, "1.3.6.1.2.1.33.1.2.5.0", "3.4", 1), "fields[0].source", "line 8"},
		{"cross source", strings.Replace(simpleSNMP, "oid: 1.3.6.1.2.1.33.1.2.5.0", "oid: 1.3.6, cmd: QAD, index: 0", 1), "fields[0].source", "line 8"},
		{"type", strings.Replace(simpleSNMP, "type: int", "type: bool", 1), "fields[0].type", "line 9"},
		{"protocol type", strings.Replace(simpleSNMP, "type: int", "type: uint16", 1), "fields[0].type", "line 9"},
		{"text type", strings.Replace(simpleSNMP, "field: battery_voltage", "field: model", 1), "fields[0].type", "line 9"},
		{"numeric type", strings.Replace(simpleSNMP, "type: int", "type: string", 1), "fields[0].type", "line 9"},
		{"infinite scale", simpleSNMP + "    scale: .inf\n", "fields[0].scale", "line 10"},
		{"nan offset", simpleSNMP + "    offset: .nan\n", "fields[0].offset", "line 10"},
		{"unit", simpleSNMP + "    unit: W\n", "fields[0].unit", "line 10"},
		{"bit conflict", simpleSNMP + "    bit: 1\n    bits: '0-3'\n", "fields[0].bit", "line 10"},
		{"bit range", simpleSNMP + "    bit: 16\n", "fields[0].bit", "line 10"},
		{"bits range", simpleSNMP + "    bits: '3-2'\n", "fields[0].bits", "line 10"},
		{"invert", simpleSNMP + "    invert: true\n", "fields[0].invert", "line 10"},
		{"range length", simpleSNMP + "    invalid: {range: [0]}\n", "fields[0].invalid.range", "line 10"},
		{"range ordered", simpleSNMP + "    invalid: {range: [2, 1]}\n", "fields[0].invalid.range", "line 10"},
		{"range finite", simpleSNMP + "    invalid: {range: [0, .inf]}\n", "fields[0].invalid.range", "line 10"},
		{"enum missing", strings.Replace(simpleSNMP, "field: battery_voltage", "field: battery_status", 1), "fields[0].enum", "line 7"},
		{"enum target", strings.Replace(simpleSNMP, "field: battery_voltage", "field: battery_status", 1) + "    enum: {1: bogus}\n", "fields[0].enum", "line 10"},
		{"enum numeric", simpleSNMP + "    enum: {1: normal}\n", "fields[0].type", "line 9"},
		{"hints", simpleSNMP + "power_hints: {indicators: [battery_voltage]}\n", "power_hints.indicators[0]", "line 10"},
		{"static phases", simpleSNMP + "phases: {input: 4, output: 1}\n", "phases", "line 10"},
		{"dynamic protocol", simpleSNMP + "phases: {from: {cmd: RTD, index: 8}}\n", "phases.from", "line 10"},
		{"mix phases", simpleAYI + "phases: {input: 1, from: {cmd: RTD, index: 8}}\n", "phases", "line 10"},
		{"wrong phase index", simpleAYI + "phases: {from: {cmd: RTD, index: 7}}\n", "phases.from", "line 10"},
		{"command", strings.Replace(simpleAYI, "cmd: QAD", "cmd: SOFF", 1), "fields[0].source", "line 8"},
		{"QAD index", strings.Replace(simpleAYI, "index: 18", "index: 23", 1), "fields[0].source.index", "line 8"},
		{"RTD index", strings.Replace(simpleAYI, "cmd: QAD, index: 18", "cmd: RTD, index: 10", 1), "fields[0].source.index", "line 8"},
		{"RTD interval", simpleAYI + "ayi9: {rtd_interval_s: 0}\n", "ayi9.rtd_interval_s", "line 10"},
		{"write FC", strings.Replace(simpleMB, "source: {fc: 3", "source: {fc: 6", 1), "fields[0].source.fc", "line 10"},
		{"address overflow", strings.Replace(simpleMB, "addr: 0", "addr: 65536", 1), "fields[0].source.addr", "line 10"},
		{"negative address", strings.Replace(simpleMB, "addr: 0", "addr: -1", 1), "fields[0].source.addr", "line 10"},
		{"multireg overflow", strings.Replace(strings.Replace(simpleMB, "addr: 0", "addr: 65535", 1), "type: uint16", "type: uint32", 1), "fields[0].source.addr", "line 10"},
		{"read FC", strings.Replace(simpleMB, "readable: [{fc: 3", "readable: [{fc: 6", 1), "modbus.readable[0].fc", "line 7"},
		{"read range", strings.Replace(simpleMB, "start: 0, end: 65535", "start: 2, end: 1", 1), "modbus.readable[0]", "line 7"},
		{"unreadable", strings.Replace(simpleMB, "start: 0, end: 65535", "start: 1, end: 79", 1), "fields[0].source.addr", "line 10"},
		{"block limit", strings.Replace(simpleMB, "modbus:\n", "modbus:\n  max_registers_per_read: 126\n", 1), "modbus.max_registers_per_read", "line 7"},
		{"byte order", strings.Replace(simpleMB, "modbus:\n", "modbus:\n  byte_order: middle\n", 1), "modbus.byte_order", "line 7"},
		{"word order", strings.Replace(simpleMB, "modbus:\n", "modbus:\n  word_order: middle\n", 1), "modbus.word_order", "line 7"},
		{"unit ID", strings.Replace(simpleMB, "modbus:\n", "modbus:\n  unit_id: 256\n", 1), "modbus.unit_id", "line 7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.data))
			if err == nil || !strings.Contains(err.Error(), tc.path) || !strings.Contains(err.Error(), tc.line) {
				t.Fatalf("expected %s %s, got %v", tc.line, tc.path, err)
			}
		})
	}
	_, err := Parse(make([]byte, maxDocumentBytes+1))
	if err == nil {
		t.Fatal("oversize accepted")
	}
	p := mustParse(t, simpleSNMP)
	p.Fields = append(p.Fields, p.Fields[0])
	if err := p.Validate(); err == nil {
		t.Fatal("duplicate field accepted")
	}
	p = mustParse(t, simpleSNMP)
	v := math.NaN()
	p.Fields[0].Scale = &v
	if err := p.Validate(); err == nil {
		t.Fatal("program NaN accepted")
	}
}

func TestBuiltins(t *testing.T) {
	ps, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 5 {
		t.Fatal(len(ps))
	}
	for _, p := range ps {
		if !IsBuiltinKey(p.Key) {
			t.Fatal(p.Key)
		}
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
		plan, err := p.ReadPlan()
		if err != nil {
			t.Fatal(err)
		}
		assertReadOnly(t, p, plan)
		for _, m := range p.Fields {
			if m.Field == "self_test_result" {
				t.Fatal("unverified self-test mapped")
			}
		}
	}
	a, _ := Builtin("et1k-snmp")
	b, _ := Builtin("et1k-snmp")
	a.Fields[0].Source.OID = "9"
	a.Fields[4].Enum["2"] = "unknown"
	a.Match.Models[0] = "Changed"
	a.PowerHints.Indicators[0] = "Changed"
	if b.Fields[0].Source.OID == "9" || b.Fields[4].Enum["2"] != "normal" || b.Match.Models[0] != "ET1K" || b.PowerHints.Indicators[0] != "output_source" {
		t.Fatal("shared instances")
	}
	if IsBuiltinKey("custom") {
		t.Fatal("custom reserved")
	}
	if _, err := Builtin("../custom"); err == nil {
		t.Fatal("unknown builtin accepted")
	}
	rfc, _ := Builtin("rfc1628-generic")
	et, _ := Builtin("et1k-snmp")
	for _, p := range []*Profile{rfc, et} {
		if *mapping(t, p, "battery_voltage").Scale != 0.1 || *mapping(t, p, "temperature").Scale != 1 {
			t.Fatal("battery scaling")
		}
		for phase := 1; phase <= 3; phase++ {
			column := 3
			if p.Key == "et1k-snmp" {
				column = 2
			}
			if got := mapping(t, p, fmt.Sprintf("bypass_voltage[%d]", phase)).Source.OID; got != fmt.Sprintf("1.3.6.1.2.1.33.1.5.3.1.%d.%d", column, phase) {
				t.Fatal(got)
			}
		}
	}
	for _, m := range rfc.Fields {
		if strings.HasPrefix(m.Field, "output_power[") || strings.HasPrefix(m.Field, "bypass_power[") {
			t.Fatal("W mapped as VA")
		}
	}
	if len(mapping(t, rfc, "native_runtime_remaining").Invalid.Values) != 0 || !slices.Contains(mapping(t, et, "native_runtime_remaining").Invalid.Values, "0") {
		t.Fatal("runtime invalid policy")
	}
	mb, _ := Builtin("et1k-modbus")
	if *mapping(t, mb, "output_voltage[1]").Source.Addr != 28 || mb.Phases.Input != 1 || mb.Phases.Output != 1 {
		t.Fatal("Modbus ET1K mapping")
	}
	ag, _ := Builtin("ayi9-json-generic")
	ae, _ := Builtin("et1k-ayi9-json")
	if len(mapping(t, ag, "native_runtime_remaining").Invalid.Values) != 0 || !slices.Contains(mapping(t, ae, "native_runtime_remaining").Invalid.Values, "0") {
		t.Fatal("AYI runtime invalid policy")
	}
}

func assertReadOnly(t testing.TB, p *Profile, plan driver.ReadPlan) {
	t.Helper()
	if plan.Modbus != nil {
		for _, b := range plan.Modbus.Blocks {
			if b.FC != 3 && b.FC != 4 || b.Count < 1 || b.Count > 125 || int(b.Start)+int(b.Count) > 65536 {
				t.Fatal("unsafe read block", b)
			}
			covered := false
			for _, r := range readableUnion(p.Modbus.Readable) {
				if int(b.FC) == r.FC && int(b.Start) >= r.Start && int(b.Start)+int(b.Count)-1 <= r.End {
					covered = true
				}
			}
			if !covered {
				t.Fatal("unreadable block", b)
			}
		}
		for _, m := range p.Fields {
			covered := false
			for _, b := range plan.Modbus.Blocks {
				if int(b.FC) == *m.Source.FC && int(b.Start) <= *m.Source.Addr && int(b.Start)+int(b.Count) >= *m.Source.Addr+m.RegisterCount() {
					covered = true
				}
			}
			if !covered {
				t.Fatal("split or missing mapping", m)
			}
		}
	}
	if plan.SNMP != nil && plan.SNMP.Strategy != "get" && plan.SNMP.Strategy != "getbulk" && plan.SNMP.Strategy != "walk" {
		t.Fatal("unsafe SNMP strategy")
	}
	if plan.AYI9 != nil && plan.AYI9.RTDInterval <= 0 {
		t.Fatal("unsafe RTD interval")
	}
}

func TestModbusPlanning(t *testing.T) {
	p := mustParse(t, simpleMB)
	c := p.Modbus
	c.MaxRegistersPerRead = 4
	c.Readable = []ReadableRange{{FC: 4, Start: 0, End: 10}, {FC: 3, Start: 65534, End: 65535}, {FC: 3, Start: 0, End: 3}, {FC: 3, Start: 5, End: 20}}
	mk := func(field string, fc, addr int, typ string) Mapping {
		return Mapping{Field: field, Source: Source{FC: &fc, Addr: &addr}, Type: typ}
	}
	p.Fields = []Mapping{mk("battery_voltage", 3, 0, "uint32"), mk("temperature", 3, 1, "uint32"), mk("battery_current", 3, 2, "uint32"), mk("battery_capacity_pct", 3, 5, "uint16"), mk("input_freq", 4, 0, "uint16"), mk("output_freq", 3, 65534, "uint32")}
	plan, err := p.ReadPlan()
	if err != nil {
		t.Fatal(err)
	}
	assertReadOnly(t, p, plan)
	want := []driver.ReadBlock{{FC: 3, Start: 0, Count: 4}, {FC: 3, Start: 5, Count: 1}, {FC: 3, Start: 65534, Count: 2}, {FC: 4, Start: 0, Count: 1}}
	if !reflect.DeepEqual(plan.Modbus.Blocks, want) {
		t.Fatalf("%+v != %+v", plan.Modbus.Blocks, want)
	}
	c.MaxRegistersPerRead = 2
	plan, err = p.ReadPlan()
	if err != nil {
		t.Fatal(err)
	}
	assertReadOnly(t, p, plan)
	if len(plan.Modbus.Blocks) != 6 {
		t.Fatal(plan.Modbus.Blocks)
	}
	c.Readable = []ReadableRange{{FC: 3, Start: 0, End: 0}, {FC: 3, Start: 1, End: 100}}
	c.MaxRegistersPerRead = 100
	p.Fields = []Mapping{mk("battery_voltage", 3, 0, "uint32"), mk("temperature", 3, 10, "uint16"), mk("battery_current", 3, 20, "uint16")}
	plan, err = p.ReadPlan()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Modbus.Blocks) != 2 || plan.Modbus.Blocks[0].Count != 11 {
		t.Fatal("gap 8 merge / gap 9 separate", plan.Modbus.Blocks)
	}
	c.Readable = []ReadableRange{{FC: 3, Start: 0, End: 0}, {FC: 3, Start: 2, End: 100}}
	if _, err := p.ReadPlan(); err == nil {
		t.Fatal("multiword crossed unreadable gap")
	}
	p = mustParse(t, strings.Replace(simpleMB, "addr: 0", "addr: 65535", 1))
	plan, err = p.ReadPlan()
	if err != nil {
		t.Fatal(err)
	}
	if plan.Modbus.Blocks[0].Start != 65535 || plan.Modbus.Blocks[0].Count != 1 {
		t.Fatal(plan)
	}
}

func TestSNMPDedupAndRecommendation(t *testing.T) {
	p := mustParse(t, simpleSNMP)
	m := p.Fields[0]
	m.Field = "battery_current"
	p.Fields = append(p.Fields, m)
	plan, err := p.ReadPlan()
	if err != nil || len(plan.SNMP.OIDs) != 1 {
		t.Fatal(plan, err)
	}
	ps, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	r := Recommend("snmp", driver.DeviceInfo{Model: "ET1K", AgentVersion: "ayi9 v1"}, ps)
	if len(r) != 2 || r[0].Key != "et1k-snmp" || r[0].Score != 100 || r[1].Score != 10 {
		t.Fatal(r)
	}
	r = Recommend("snmp", driver.DeviceInfo{AgentVersion: "ayi9 v1"}, ps)
	if len(r) != 2 || r[0].Score != 50 {
		t.Fatal(r)
	}
	r = Recommend("snmp", driver.DeviceInfo{Model: "unknown"}, ps)
	if len(r) != 1 || r[0].Key != "rfc1628-generic" {
		t.Fatal(r)
	}
	custom := &Profile{Key: "custom-mb", Protocol: "modbus", Match: Match{Models: []string{"Different"}}}
	ps = append(ps, nil, custom)
	r = Recommend("modbus", driver.DeviceInfo{Model: "ET1K"}, ps)
	if len(r) != 2 || r[0].Score != 0 || r[1].Score != 0 {
		t.Fatal(r)
	}
}

func TestUnitFactor(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		factor   float64
	}{{"", "V", 1}, {"V", "V", 1}, {"min", "s", 60}, {"ms", "s", .001}, {"kV", "V", 1000}, {"mV", "V", .001}, {"mA", "A", .001}, {"kW", "W", 1000}, {"kVA", "VA", 1000}} {
		v, ok := UnitFactor(tc.from, tc.to)
		if !ok || v != tc.factor {
			t.Fatal(tc, v, ok)
		}
	}
	for _, pair := range [][2]string{{"W", "VA"}, {"VA", "W"}, {"K", "°C"}, {"A", "V"}} {
		if _, ok := UnitFactor(pair[0], pair[1]); ok {
			t.Fatal(pair)
		}
	}
}

func FuzzParseReadPlan(f *testing.F) {
	for _, key := range builtinKeys {
		data, _ := builtinFiles.ReadFile("builtin/" + key + ".yaml")
		f.Add(data)
	}
	f.Add([]byte(simpleSNMP))
	f.Add([]byte(simpleMB))
	f.Add([]byte("null"))
	f.Add([]byte(`{"schema":1,"fields":null}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := Parse(data)
		if err != nil {
			return
		}
		plan, err := p.ReadPlan()
		if err != nil {
			t.Fatal("validated profile cannot compile", err)
		}
		assertReadOnly(t, p, plan)
	})
}
