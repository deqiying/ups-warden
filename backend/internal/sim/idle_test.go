package sim

import (
	"context"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/driver/ayi9"
	"ups-warden/internal/driver/guard"
	"ups-warden/internal/driver/modbus"
)

func TestIdleConnectionsSurvivePollingInterval(t *testing.T) {
	for _, protocol := range []string{driver.ProtocolModbus, driver.ProtocolAYI9} {
		t.Run(protocol, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				a, b := net.Pipe()
				defer b.Close()
				server := idleServer()
				done := make(chan struct{})
				go func() {
					defer close(done)
					defer b.Close()
					if protocol == driver.ProtocolModbus {
						server.serveModbus(b)
					} else {
						server.serveAYI9(b)
					}
				}()
				cfg := guard.Config{Address: "127.0.0.1:1", DialContext: func(context.Context, string, string) (net.Conn, error) { return a, nil }}
				var client driver.Driver
				var err error
				if protocol == driver.ProtocolModbus {
					client, err = modbus.New(modbus.Config{Connection: cfg}, driver.ModbusPlan{Blocks: []driver.ReadBlock{{FC: 3, Count: 1}}})
				} else {
					client, err = ayi9.New(ayi9.Config{Connection: cfg}, driver.AYI9Plan{})
				}
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				first, err := client.Poll(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				// synctest 内推进虚拟时间，不实际等待三秒。
				time.Sleep(3 * time.Second)
				second, err := client.Poll(context.Background())
				if err != nil {
					t.Fatalf("正常空闲连接被关闭: %v", err)
				}
				if protocol == driver.ProtocolAYI9 && (!first.AYI9.RTDAt.Equal(second.AYI9.RTDAt) || len(server.Requests()) != 3) {
					t.Fatal("空闲导致RTD重新读取")
				}
				if err := client.Close(); err != nil {
					t.Error(err)
				}
				<-done
			})
		})
	}
}

func TestPartialFrameStillTimesOut(t *testing.T) {
	for _, protocol := range []string{driver.ProtocolModbus, driver.ProtocolAYI9} {
		t.Run(protocol, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				a, b := net.Pipe()
				defer a.Close()
				server := idleServer()
				done := make(chan struct{})
				go func() {
					defer close(done)
					defer b.Close()
					if protocol == driver.ProtocolModbus {
						server.serveModbus(b)
					} else {
						server.serveAYI9(b)
					}
				}()
				first := byte(0)
				if protocol == driver.ProtocolAYI9 {
					first = '{'
				}
				if _, err := a.Write([]byte{first}); err != nil {
					t.Fatal(err)
				}
				<-done
				requests := server.Requests()
				if len(requests) != 1 || requests[0].Allowed {
					t.Fatal("半帧未被超时拒绝并记录")
				}
			})
		})
	}
}

// idleServer 构造只用于直接调用服务函数的最小 Server，时间轴与 Start 的默认值一致。
func idleServer() *Server {
	scenario, err := Scenario{}.normalized()
	if err != nil {
		panic(err)
	}
	return &Server{cfg: Config{Now: time.Now}, control: ControlScenario,
		seg: segment{origin: time.Now(), scenario: scenario, initial: start{Capacity: scenario.BatteryCapacity, Mains: true, Online: true}}}
}
