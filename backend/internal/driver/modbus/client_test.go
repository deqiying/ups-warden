package modbus

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/driver/guard"
)

type fakeConn struct {
	response *bytes.Reader
	packets  [][]byte
	closed   bool
	modify   func([]byte) []byte
}

func (c *fakeConn) Write(p []byte) (int, error) {
	c.packets = append(c.packets, bytes.Clone(p))
	count := int(binary.BigEndian.Uint16(p[10:]))
	reply := make([]byte, 9+count*2)
	copy(reply, p[:7])
	binary.BigEndian.PutUint16(reply[4:], uint16(3+2*count))
	reply[7], reply[8] = p[7], byte(count*2)
	for i := 0; i < count; i++ {
		binary.BigEndian.PutUint16(reply[9+i*2:], uint16(100+i))
	}
	if c.modify != nil {
		reply = c.modify(reply)
	}
	c.response = bytes.NewReader(reply)
	return len(p), nil
}
func (c *fakeConn) Read(p []byte) (int, error) {
	if c.response == nil {
		return 0, io.EOF
	}
	if len(p) > 1 {
		p = p[:1]
	}
	return c.response.Read(p)
}
func (c *fakeConn) Close() error                     { c.closed = true; return nil }
func (c *fakeConn) LocalAddr() net.Addr              { return nil }
func (c *fakeConn) RemoteAddr() net.Addr             { return nil }
func (c *fakeConn) SetDeadline(time.Time) error      { return nil }
func (c *fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fakeConn) SetWriteDeadline(time.Time) error { return nil }
func config(c net.Conn) Config {
	return Config{Connection: guard.Config{Address: "127.0.0.1:502", DialContext: func(context.Context, string, string) (net.Conn, error) { return c, nil }}}
}
func TestPollPartialAndPlanCopy(t *testing.T) {
	c := &fakeConn{}
	c.modify = func(p []byte) []byte {
		if p[7] == 4 {
			p = p[:9]
			binary.BigEndian.PutUint16(p[4:], 3)
			p[7] = 0x84
			p[8] = 2
		}
		return p
	}
	plan := driver.ModbusPlan{UnitID: 7, Blocks: []driver.ReadBlock{{FC: 3, Start: 0, Count: 2}, {FC: 4, Start: 5, Count: 1}, {FC: 3, Start: 9, Count: 1}}}
	client, err := New(config(c), plan)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	plan.Blocks[0].FC = 6
	raw, err := client.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if raw.At.IsZero() || len(raw.Modbus) != 3 || raw.Modbus[driver.RegKey{FC: 3, Addr: 1}] != 101 || len(raw.Errors) != 1 || raw.Errors[0].Kind != "unsupported" {
		t.Fatalf("部分结果错误: %+v", raw)
	}
	for _, p := range c.packets {
		if err := guard.Validate(driver.ProtocolModbus, p); err != nil {
			t.Fatal(err)
		}
	}
}
func TestMalformedResponsesReconnect(t *testing.T) {
	for name, mutate := range map[string]func([]byte) []byte{
		"transaction": func(p []byte) []byte { p[1]++; return p }, "protocol": func(p []byte) []byte { p[3] = 1; return p }, "unit": func(p []byte) []byte { p[6]++; return p }, "length": func(p []byte) []byte { p[4] = 0xff; return p }, "function": func(p []byte) []byte { p[7] = 4; return p }, "count": func(p []byte) []byte { p[8] = 0; return p }, "truncated": func(p []byte) []byte { return p[:8] },
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			first := &fakeConn{modify: mutate}
			second := &fakeConn{}
			cfg := config(first)
			cfg.Connection.DialContext = func(context.Context, string, string) (net.Conn, error) {
				calls++
				if calls == 1 {
					return first, nil
				}
				return second, nil
			}
			client, err := New(cfg, driver.ModbusPlan{Blocks: []driver.ReadBlock{{FC: 3, Count: 1}}})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			raw, err := client.Poll(context.Background())
			if err == nil || !first.closed || len(raw.Errors) != 1 {
				t.Fatalf("畸形响应未失败: %v", err)
			}
			if _, err = client.Poll(context.Background()); err != nil || calls != 2 {
				t.Fatalf("未重连: %v", err)
			}
		})
	}
}
func TestCloseInterruptsPoll(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	client, err := New(config(a), driver.ModbusPlan{Blocks: []driver.ReadBlock{{FC: 3, Count: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	request := make(chan struct{})
	go func() { var buf [12]byte; _, _ = io.ReadFull(b, buf[:]); close(request) }()
	result := make(chan error, 1)
	go func() { _, err := client.Poll(context.Background()); result <- err }()
	<-request
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close 未中断")
	}
	if _, err = client.Poll(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
}
func FuzzReadPlanSafety(f *testing.F) {
	f.Add(byte(3), uint16(0), uint16(1), byte(0))
	f.Add(byte(4), uint16(65535), uint16(1), byte(7))
	f.Add(byte(6), uint16(0), uint16(1), byte(0))
	f.Fuzz(func(t *testing.T, fc byte, start, count uint16, unit byte) {
		c := &fakeConn{}
		client, err := New(config(c), driver.ModbusPlan{UnitID: unit, Blocks: []driver.ReadBlock{{FC: driver.ReadFunc(fc), Start: start, Count: count}}})
		if err != nil {
			if len(c.packets) != 0 {
				t.Fatal("非法计划发出请求")
			}
			return
		}
		defer client.Close()
		if _, err := client.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, p := range c.packets {
			if err := guard.Validate(driver.ProtocolModbus, p); err != nil {
				t.Fatal(err)
			}
		}
	})
}
