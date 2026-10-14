package driver_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/driver/ayi9"
	"ups-warden/internal/driver/guard"
	"ups-warden/internal/driver/modbus"
)

func TestQueuedPollRespectsContext(t *testing.T) {
	for _, protocol := range []string{driver.ProtocolModbus, driver.ProtocolAYI9} {
		for _, expired := range []bool{false, true} {
			name := protocol + "/deadline"
			if expired {
				name = protocol + "/cancelled"
			}
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					a, b := net.Pipe()
					defer b.Close()
					cfg := guard.Config{Address: "127.0.0.1:1", Timeout: time.Second, DialContext: func(context.Context, string, string) (net.Conn, error) { return a, nil }}
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
					first := make(chan error, 1)
					go func() { _, err := client.Poll(context.Background()); first <- err }()
					if _, err := b.Read(make([]byte, 4096)); err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
					defer cancel()
					if expired {
						cancel()
					}
					started := time.Now()
					_, err = client.Poll(ctx)
					if !(errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) || time.Since(started) > 20*time.Millisecond {
						t.Errorf("排队请求未及时响应取消: %s %v", time.Since(started), err)
					}
					if err := client.Close(); err != nil {
						t.Error(err)
					}
					<-first
				})
			})
		}
	}
}
