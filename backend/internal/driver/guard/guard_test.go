package guard

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/driver/snmp/ber"
)

type recording struct {
	bytes.Buffer
	closed bool
	short  bool
}

func (c *recording) Read(p []byte) (int, error) { return 0, io.EOF }
func (c *recording) Write(p []byte) (int, error) {
	if c.short {
		return c.Buffer.Write(p[:1])
	}
	return c.Buffer.Write(p)
}
func (c *recording) Close() error                     { c.closed = true; return nil }
func (c *recording) LocalAddr() net.Addr              { return nil }
func (c *recording) RemoteAddr() net.Addr             { return nil }
func (c *recording) SetDeadline(time.Time) error      { return nil }
func (c *recording) SetReadDeadline(time.Time) error  { return nil }
func (c *recording) SetWriteDeadline(time.Time) error { return nil }

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func readSNMP(t testing.TB) []byte {
	t.Helper()
	p, err := ber.Encode(ber.Message{Version: 1, Community: "test-community", PDU: ber.GetRequest, RequestID: 0x00800000, VarBinds: []ber.VarBind{{OID: "1.3.6.1.2.1.33.1.2.1.0", Value: driver.SNMPValue{Tag: 5}}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestWholePacketWhitelist(t *testing.T) {
	mb := []byte{0, 1, 0, 0, 0, 6, 0, 3, 0, 0, 0, 1}
	snmp := readSNMP(t)
	for protocol, p := range map[string][]byte{driver.ProtocolModbus: mb, driver.ProtocolSNMP: snmp, driver.ProtocolAYI9: []byte(`{"cmd":"QAD","session":"1","pass":"fake"}`)} {
		t.Run(protocol, func(t *testing.T) {
			if err := Validate(protocol, p); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < len(p); i++ {
				if Validate(protocol, p[:i]) == nil {
					t.Fatalf("接受截断 %d", i)
				}
			}
			if Validate(protocol, append(bytes.Clone(p), p...)) == nil {
				t.Fatal("接受拼接请求")
			}
		})
	}
	set := bytes.Clone(snmp)
	i := bytes.Index(set, []byte{0xa0})
	if i < 0 {
		t.Fatal("PDU missing")
	}
	set[i] = 0xa3
	cases := []struct {
		protocol string
		p        []byte
	}{
		{driver.ProtocolSNMP, set}, {driver.ProtocolModbus, []byte{0, 1, 0, 0, 0, 6, 0, 6, 0, 0, 0, 1}},
	}
	for _, p := range []string{`{"cmd":"SOFF","session":"1"}`, `{"cmd":"QAD","session":"1","host":"x"}`, `{"cmd":"QAD","cmd":"RTD","session":"1"}`, `{"cmd":"QAD","session":1}`, `{"cmd":"QAD","session":"1","pass":null}`, `{"cmd":"QAD","session":"1","\u0063md":"SOFF"}`, `{"cmd":"QAD","session":"1"} {"cmd":"SOFF"}`, `{"cmd":"QAD","session":"-1"}`} {
		cases = append(cases, struct {
			protocol string
			p        []byte
		}{driver.ProtocolAYI9, []byte(p)})
	}
	for _, tc := range cases {
		conn := &recording{}
		wrapped := Wrap(conn, tc.protocol, quiet)
		n, err := wrapped.Write(tc.p)
		if n != 0 || !errors.Is(err, ErrWriteBlocked) || conn.Len() != 0 {
			t.Fatal("拒绝前发送了字节")
		}
	}
}
func TestShortWriteCloses(t *testing.T) {
	c := &recording{short: true}
	g := Wrap(c, driver.ProtocolAYI9, quiet)
	p := []byte(`{"cmd":"QAD","session":"1"}`)
	if _, err := g.Write(p); !errors.Is(err, io.ErrShortWrite) || !c.closed {
		t.Fatal(err)
	}
	if _, err := g.Write(p); !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
}
func TestCancelInterruptsRead(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done, err := Begin(ctx, a, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	result := make(chan error, 1)
	go func() { _, err := a.Read(make([]byte, 1)); result <- err }()
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("未中断")
		}
	case <-time.After(time.Second):
		t.Fatal("取消未打断 I/O")
	}
}
func TestDialValidation(t *testing.T) {
	for _, addr := range []string{"", "127.0.0.1", "127.0.0.1:0", "127.0.0.1:65536", "http://localhost"} {
		if _, err := (Config{Address: addr}).Validate(); err == nil {
			t.Fatal(addr)
		}
	}
	if _, err := Dial(context.Background(), Config{Address: "localhost:1"}, "http"); !errors.Is(err, ErrWriteBlocked) {
		t.Fatal(err)
	}
}
func FuzzGuardNeverLeaksRejectedBytes(f *testing.F) {
	f.Add(byte(0), []byte(`{"cmd":"QAD","session":"1"}`))
	f.Add(byte(1), []byte{0, 1, 0, 0, 0, 6, 0, 3, 0, 0, 0, 1})
	f.Add(byte(2), readSNMP(f))
	f.Fuzz(func(t *testing.T, kind byte, p []byte) {
		if len(p) > 65536 {
			return
		}
		protocol := []string{driver.ProtocolAYI9, driver.ProtocolModbus, driver.ProtocolSNMP}[int(kind)%3]
		c := &recording{}
		g := Wrap(c, protocol, quiet)
		_, err := g.Write(p)
		if err != nil && c.Len() != 0 {
			t.Fatal("非法帧已泄漏")
		}
		if err == nil && !bytes.Equal(c.Bytes(), p) {
			t.Fatal("帧被改写")
		}
	})
}
