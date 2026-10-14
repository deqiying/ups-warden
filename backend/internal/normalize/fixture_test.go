package normalize

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/model"
	"ups-warden/internal/profile"
)

type fixtureSample struct {
	Mode  string `json:"mode"`
	Index int    `json:"index"`
	SNMP  struct {
		At       time.Time                   `json:"at"`
		Finished time.Time                   `json:"finished_at"`
		Values   map[string]driver.SNMPValue `json:"values"`
	} `json:"snmp"`
	Modbus struct {
		At        time.Time `json:"at"`
		Finished  time.Time `json:"finished_at"`
		Registers []uint16  `json:"registers"`
	} `json:"modbus"`
	AYI9 struct {
		At          time.Time `json:"at"`
		Finished    time.Time `json:"finished_at"`
		QAD         string    `json:"qad"`
		RTD         string    `json:"rtd"`
		RTDAt       time.Time `json:"rtd_at"`
		RTDFinished time.Time `json:"rtd_finished_at"`
		UID         string    `json:"uid"`
	} `json:"ayi9"`
}

func TestET1KFixtureRegression(t *testing.T) {
	content, err := os.ReadFile("../../testdata/et1k/capture.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Version int             `json:"version"`
		Samples []fixtureSample `json:"samples"`
	}
	if err := json.Unmarshal(content, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Version != 1 || len(fixture.Samples) != 60 {
		t.Fatalf("fixture版本或样本数量错误: %d %d", fixture.Version, len(fixture.Samples))
	}
	profiles := map[string]*profile.Profile{}
	for _, key := range []string{"et1k-snmp", "et1k-modbus", "et1k-ayi9-json"} {
		p, err := profile.Builtin(key)
		if err != nil {
			t.Fatal(err)
		}
		profiles[key] = p
	}
	count := map[string]int{}
	firstBattery, lastBattery := 0.0, 0.0
	previousSeconds := -1.0
	previousAt := time.Time{}
	for _, sample := range fixture.Samples {
		count[sample.Mode]++
		t.Run(fmt.Sprintf("%s/%02d", sample.Mode, sample.Index), func(t *testing.T) {
			if sample.Mode != "mains" && sample.Mode != "battery" {
				t.Fatal("未知采样阶段")
			}
			if sample.SNMP.At.Year() != 2000 || sample.AYI9.At.Year() != 2000 || sample.Modbus.At.Year() != 2000 {
				t.Fatal("fixture时间未平移")
			}
			if len(sample.Modbus.Registers) != 80 || sample.AYI9.UID != "fixture-et1k@ayi9.com" {
				t.Fatal("寄存器数量或身份脱敏错误")
			}
			rawSNMP := driver.RawData{Protocol: driver.ProtocolSNMP, At: sample.SNMP.Finished, SNMP: sample.SNMP.Values}
			rawModbus := driver.RawData{Protocol: driver.ProtocolModbus, At: sample.Modbus.Finished, Modbus: map[driver.RegKey]uint16{}}
			for addr, value := range sample.Modbus.Registers {
				rawModbus.Modbus[driver.RegKey{FC: 3, Addr: uint16(addr)}] = value
			}
			rawAYI9 := driver.RawData{Protocol: driver.ProtocolAYI9, At: sample.AYI9.Finished, AYI9: &driver.AYI9Raw{UID: sample.AYI9.UID, QADText: sample.AYI9.QAD, RTDText: sample.AYI9.RTD, QAD: strings.Fields(strings.TrimPrefix(sample.AYI9.QAD, "(")), RTD: strings.Fields(strings.TrimPrefix(sample.AYI9.RTD, "(")), RTDAt: sample.AYI9.RTDFinished}}
			snmp, _, err := Normalize(1, rawSNMP, profiles["et1k-snmp"])
			if err != nil {
				t.Fatal(err)
			}
			modbus, _, err := Normalize(2, rawModbus, profiles["et1k-modbus"])
			if err != nil {
				t.Fatal(err)
			}
			ayi9, traces, err := Normalize(3, rawAYI9, profiles["et1k-ayi9-json"])
			if err != nil {
				t.Fatal(err)
			}
			if snmp.At.IsZero() || !snmp.At.Equal(sample.SNMP.Finished) || !modbus.At.Equal(sample.Modbus.Finished) || !ayi9.At.Equal(sample.AYI9.Finished) {
				t.Fatal("快照必须使用各协议采集完成时间")
			}
			// SNMP 电压/温度为截断整数；Modbus 与 QAD 核心物理量应在0.1精度内一致。
			for field, snmpTolerance := range map[string]float64{"input_voltage[1]": 1, "output_voltage[1]": 1, "output_freq": 0.1, "output_load_pct[1]": 1, "battery_capacity_pct": 1, "battery_voltage": 0.1, "temperature": 1} {
				a, m, s := ayi9.Fields[field], modbus.Fields[field], snmp.Fields[field]
				if a.Quality != model.Good || m.Quality != model.Good || s.Quality != model.Good {
					t.Fatalf("%s质量不符: ayi9=%+v modbus=%+v snmp=%+v", field, a, m, s)
				}
				if math.Abs(a.Num-m.Num) > 0.100001 || math.Abs(a.Num-s.Num) >= snmpTolerance+1e-8 {
					t.Errorf("%s协议不一致: ayi9=%g modbus=%g snmp=%g", field, a.Num, m.Num, s.Num)
				}
			}
			wantMode, wantSource, wantMains := "line", "normal", true
			if sample.Mode == "battery" {
				wantMode, wantSource, wantMains = "battery", "battery", false
			}
			if ayi9.Fields["ups_mode"].Str != wantMode || ayi9.Fields["mains_ok"].Bool != wantMains || snmp.Fields["output_source"].Str != wantSource {
				t.Fatal("归一化供电字段与采样阶段不符")
			}
			for _, snapshot := range []model.SourceSnapshot{snmp, modbus, ayi9} {
				if snapshot.Fields["input_voltage[2]"].Quality == model.Good || snapshot.Fields["output_voltage[3]"].Quality == model.Good {
					t.Fatal("单相设备额外相误为Good")
				}
			}
			for _, snapshot := range []model.SourceSnapshot{snmp, ayi9} {
				if snapshot.Fields["native_runtime_remaining"].Quality == model.Good {
					t.Fatal("ET1K剩余时间0必须无效")
				}
			}
			if ayi9.Fields["manufacturer"].Reason != model.ReasonPlaceholder {
				t.Fatal("厂商占位符未识别")
			}
			if len(traces) != 64 {
				t.Fatalf("观测字段Trace数量 %d", len(traces))
			}
			if sample.Mode == "battery" {
				seconds := snmp.Fields["native_seconds_on_battery"]
				if seconds.Quality != model.Good || seconds.Num < previousSeconds {
					t.Fatal("连续放电计时回退或质量错误")
				}
				if !previousAt.IsZero() && sample.SNMP.At.Sub(previousAt) < 2*time.Second {
					t.Fatal("采样相对时间丢失")
				}
				previousSeconds = seconds.Num
				previousAt = sample.SNMP.At
				if sample.Index == 0 {
					firstBattery = ayi9.Fields["battery_capacity_pct"].Num
				}
				lastBattery = ayi9.Fields["battery_capacity_pct"].Num
			}
			encoded, err := json.Marshal(ayi9)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "1.3.6.1.2.1") || strings.Contains(string(encoded), "QAD") || strings.Contains(string(encoded), "RTD") {
				t.Fatal("统一快照泄漏协议定位")
			}
		})
	}
	if count["mains"] != 10 || count["battery"] != 50 || firstBattery != 98 || lastBattery != 95 || previousSeconds != 222 {
		t.Fatalf("采样序列错误: %v 容量%g->%g 计时%g", count, firstBattery, lastBattery, previousSeconds)
	}
}
