package driver_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/driver/ayi9"
	"ups-warden/internal/driver/guard"
	"ups-warden/internal/driver/modbus"
	"ups-warden/internal/driver/snmp"
	"ups-warden/internal/model"
	"ups-warden/internal/normalize"
	"ups-warden/internal/profile"
	"ups-warden/internal/secret"
	"ups-warden/internal/sim"
)

func TestThreeProtocolScenarios(t *testing.T) {
	for _, et1k := range []bool{false, true} {
		name := "standard"
		if et1k {
			name = "et1k"
		}
		t.Run(name, func(t *testing.T) {
			scenario, err := sim.ParseScenario([]byte("schema: 1\nsteps:\n  - {at: 10s, state: outage}\n  - {at: 100s, state: low}\n  - {at: 200s, state: recovery}\n  - {at: 300s, state: offline}\n  - {at: 400s, state: online}\n"))
			if err != nil {
				t.Fatal(err)
			}
			var elapsed atomic.Int64
			base := time.Now()
			server, err := sim.Start(sim.Config{ET1K: et1k, Scenario: scenario, Now: func() time.Time { return base.Add(time.Duration(elapsed.Load())) }})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := server.Close(); err != nil {
					t.Error(err)
				}
			})
			sa, ma, aa := server.Addresses()
			skey, akey := "rfc1628-generic", "ayi9-json-generic"
			if et1k {
				skey, akey = "et1k-snmp", "et1k-ayi9-json"
			}
			keys := []string{skey, "et1k-modbus", akey}
			profiles := make([]*profile.Profile, 3)
			clients := make([]driver.Driver, 3)
			for i, key := range keys {
				p, err := profile.Builtin(key)
				if err != nil {
					t.Fatal(err)
				}
				profiles[i] = p
				plan, err := p.ReadPlan()
				if err != nil {
					t.Fatal(err)
				}
				cfg := guard.Config{Address: []string{sa, ma, aa}[i], Timeout: 50 * time.Millisecond}
				switch i {
				case 0:
					clients[i], err = snmp.New(snmp.Config{Connection: cfg, Community: secret.NewString("sim-public")}, *plan.SNMP)
				case 1:
					clients[i], err = modbus.New(modbus.Config{Connection: cfg}, *plan.Modbus)
				case 2:
					clients[i], err = ayi9.New(ayi9.Config{Connection: cfg}, *plan.AYI9)
				}
				if err != nil {
					t.Fatal(err)
				}
				client := clients[i]
				t.Cleanup(func() {
					if err := client.Close(); err != nil {
						t.Error(err)
					}
				})
			}
			for _, at := range []time.Duration{0, 50 * time.Second, 110 * time.Second, 220 * time.Second, 310 * time.Second, 410 * time.Second} {
				elapsed.Store(int64(at))
				want := scenario.StateAt(at)
				for i, client := range clients {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					raw, err := client.Poll(ctx)
					cancel()
					if !want.Online {
						if err == nil {
							t.Fatalf("%s 失联未报错", keys[i])
						}
						continue
					}
					if err != nil {
						t.Fatalf("%s at %s: %v", keys[i], at, err)
					}
					snapshot, _, err := normalize.Normalize(int64(i+1), raw, profiles[i])
					if err != nil {
						t.Fatal(err)
					}
					expected := map[string]float64{"input_voltage[1]": want.InputVoltage[0], "output_voltage[1]": want.OutputVoltage[0], "output_freq": want.OutputFrequency, "output_load_pct[1]": want.LoadPct, "battery_capacity_pct": want.BatteryCapacity, "battery_voltage": want.BatteryVoltage, "temperature": want.Temperature}
					if i == 0 {
						expected["rated_w"] = want.RatedW
					}
					for key, value := range expected {
						field := snapshot.Fields[key]
						tolerance := 0.101
						if i == 0 {
							tolerance = 1.001
						}
						if field.Quality != model.Good || math.Abs(field.Num-value) > tolerance {
							t.Fatalf("%s/%s at %s: %+v want %g", keys[i], key, at, field, value)
						}
					}
					if i == 0 {
						source := "normal"
						if !want.Mains {
							source = "battery"
						}
						if snapshot.Fields["output_source"].Str != source {
							t.Fatal("SNMP供电状态不符")
						}
					}
					if i == 2 {
						if snapshot.Fields["mains_ok"].Quality != model.Good || snapshot.Fields["low_battery"].Quality != model.Good || snapshot.Fields["mains_ok"].Bool != want.Mains || snapshot.Fields["low_battery"].Bool != want.LowBattery {
							t.Fatal("AYI9供电/低电状态不符")
						}
					}
				}
			}
			if server.DroppedRequests() != 0 {
				t.Fatal("请求审计不完整")
			}
			for _, request := range server.Requests() {
				if !request.Allowed || guard.Validate(request.Protocol, request.Data) != nil {
					t.Fatal("模拟器收到非白名单请求")
				}
				if strings.Contains(string(request.Data), "sim-public") {
					t.Fatal("审计泄漏community")
				}
			}
		})
	}
}
func TestCustomRegistersAndPassword(t *testing.T) {
	server, err := sim.Start(sim.Config{Password: "synthetic", Registers: map[driver.RegKey]uint16{{FC: 4, Addr: 123}: 271}})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	_, ma, aa := server.Addresses()
	p, err := profile.Parse([]byte("schema: 1\nkey: custom\nname: 自定义\nversion: 1\nprotocol: modbus\nmodbus:\n  readable: [{fc: 4, start: 123, end: 123}]\nfields:\n  - field: battery_voltage\n    source: {fc: 4, addr: 123}\n    type: uint16\n    scale: 0.1\n"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.ReadPlan()
	if err != nil {
		t.Fatal(err)
	}
	client, err := modbus.New(modbus.Config{Connection: guard.Config{Address: ma}}, *plan.Modbus)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, raw, err := client.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := normalize.Normalize(1, raw, p)
	if err != nil || snapshot.Fields["battery_voltage"].Num != 27.1 {
		t.Fatalf("自定义映射失败: %v %+v", err, snapshot)
	}
	wrong, err := ayi9.New(ayi9.Config{Connection: guard.Config{Address: aa}, Password: secret.NewString("wrong")}, driver.AYI9Plan{})
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.Close()
	_, _, err = wrong.Probe(context.Background())
	if !errors.Is(err, ayi9.ErrPasswordWrong) {
		t.Fatal(err)
	}
	for _, request := range server.Requests() {
		if strings.Contains(string(request.Data), "wrong") || strings.Contains(string(request.Data), "synthetic") {
			t.Fatal("审计泄漏密码")
		}
	}
}
