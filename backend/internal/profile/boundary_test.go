package profile

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"ups-warden/internal/driver"
)

func TestAdditionalStrictBoundaries(t *testing.T) {
	cases := []struct{ data, path, line string }{
		{simpleSNMP + "  - {field: battery_voltage, source: {oid: 1.3.6}, type: int}\n", "fields[1].field", "line 10"},
		{simpleSNMP + "power_hints: {indicators: [input_voltage, input_voltage]}\n", "power_hints.indicators[1]", "line 10"},
		{strings.Replace(simpleSNMP, "oid: 1.3.6.1.2.1.33.1.2.5.0", "oid: 1.3.6, cmd: ''", 1), "fields[0].source.cmd", "line 8"},
		{strings.Replace(simpleAYI, "cmd: QAD, index: 18", "cmd: QAD, index: 18, oid: ''", 1), "fields[0].source.oid", "line 8"},
		{simpleAYI + "phases: {input: 0, from: {cmd: RTD, index: 8}}\n", "phases", "line 10"},
		{simpleSNMP + "    invert: false\n", "fields[0].invert", "line 10"},
		{simpleSNMP + "    scale: !custom 1\n", "fields[0].scale", "line 10"},
		{strings.Replace(simpleSNMP, "name: Test", "name: true", 1), "name", "line 3"},
		{strings.Replace(simpleSNMP, "schema: 1\n", "", 1), "schema", "line 1"},
		{simpleSNMP + "    invalid: {values: [null]}\n", "fields[0].invalid.values[0]", "line 10"},
		{simpleSNMP + "    invalid: {placeholders: [null]}\n", "fields[0].invalid.placeholders[0]", "line 10"},
		{strings.Replace(simpleMB, "addr: 0", "addr: null", 1), "fields[0].source.addr", "line 10"},
		{strings.Replace(strings.Replace(simpleMB, "modbus:\n", "modbus:\n  max_registers_per_read: 1\n", 1), "type: uint16", "type: uint32", 1), "fields[0].source.addr", "line 11"},
		{simpleAYI + "ayi9: {cmd: SON}\n", "ayi9.cmd", "line 10"},
		{strings.Replace(simpleAYI, "index: 18", "index: -1", 1), "fields[0].source.index", "line 8"},
		{strings.Replace(simpleSNMP, "1.3.6.1.2.1.33.1.2.5.0", "1.3.4294967296", 1), "fields[0].source", "line 8"},
		{simpleSNMP + "    bits: '0-16'\n", "fields[0].bits", "line 10"},
		{simpleSNMP + "    bits: '3'\n", "fields[0].bits", "line 10"},
		{strings.Replace(simpleSNMP, "field: battery_voltage", "field: mains_ok", 1), "fields[0].bit", "line 7"},
		{strings.Replace(strings.Replace(simpleSNMP, "field: battery_voltage", "field: model", 1), "type: int", "type: string", 1) + "    scale: 2\n", "fields[0].scale", "line 10"},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			_, err := Parse([]byte(tc.data))
			if err == nil || !strings.Contains(err.Error(), tc.path) || !strings.Contains(err.Error(), tc.line) {
				t.Fatalf("expected %s %s: %v", tc.line, tc.path, err)
			}
		})
	}
	// 类型化 JSON 同样拒绝未知字段、重复键、null 和尾随文档。
	for _, data := range []string{`{"schema":1,"schema":1}`, `{"schema":null}`, `{"unknown":1}`, `{"schema":1} {"schema":1}`} {
		if _, err := Parse([]byte(data)); err == nil {
			t.Fatal("invalid JSON subset accepted", data)
		}
	}
	for _, strategy := range []string{"get", "getbulk", "walk"} {
		p := mustParse(t, simpleSNMP+"snmp: {strategy: "+strategy+"}\n")
		plan, err := p.ReadPlan()
		if err != nil || plan.SNMP.Strategy != strategy {
			t.Fatal(plan, err)
		}
	}
	for _, base := range []string{simpleSNMP, simpleAYI} {
		p := mustParse(t, base)
		p.SNMP = nil
		p.AYI9 = nil
		if _, err := p.ReadPlan(); err != nil {
			t.Fatal("nil protocol config", err)
		}
	}
	p := mustParse(t, simpleSNMP)
	p.Fields[0].Offset = math.Inf(-1)
	if p.Validate() == nil {
		t.Fatal("program infinite offset")
	}
	p.Fields[0].Offset = 0
	p.Fields[0].Invalid.Range = []float64{0, math.NaN()}
	if p.Validate() == nil {
		t.Fatal("program NaN range")
	}
}

func TestModbusLongAndOverlappingBlocks(t *testing.T) {
	p := mustParse(t, simpleMB)
	p.Modbus.MaxRegistersPerRead = 3
	// 每一个两字字段互相重叠；边界必须以完整字段切分，允许跨块重复读取。
	p.Fields = nil
	fields := []string{"battery_voltage", "temperature", "battery_current", "battery_capacity_pct", "input_freq", "output_freq"}
	for i, key := range fields {
		fc, addr := 3, i
		p.Fields = append(p.Fields, Mapping{Field: key, Source: Source{FC: &fc, Addr: &addr}, Type: "uint32"})
	}
	plan, err := p.ReadPlan()
	if err != nil {
		t.Fatal(err)
	}
	assertReadOnly(t, p, plan)
	if !slices.Equal(plan.Modbus.Blocks, []driver.ReadBlock{{FC: 3, Start: 0, Count: 3}, {FC: 3, Start: 2, Count: 3}, {FC: 3, Start: 4, Count: 3}}) {
		t.Fatal(plan.Modbus.Blocks)
	}
	p.Modbus.MaxRegistersPerRead = 125
	p.Fields = nil
	// 稀疏字段跨越最大块长度，不能读到可读范围之外，也不能产生长于 125 的块。
	for i, key := range fields {
		fc, addr := 3, i*9
		p.Fields = append(p.Fields, Mapping{Field: key, Source: Source{FC: &fc, Addr: &addr}, Type: "uint16"})
	}
	p.Modbus.MaxRegistersPerRead = 20
	plan, err = p.ReadPlan()
	if err != nil {
		t.Fatal(err)
	}
	assertReadOnly(t, p, plan)
	if len(plan.Modbus.Blocks) != 2 || plan.Modbus.Blocks[0].Count != 19 {
		t.Fatal(plan.Modbus.Blocks)
	}
}
