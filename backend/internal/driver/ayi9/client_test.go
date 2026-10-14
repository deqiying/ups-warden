package ayi9

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/driver/guard"
	"ups-warden/internal/secret"
)

const qadText = "(220 0 0 50 0 0 0 0 220 0 0 50 0 0 0 25 0 0 26 0 99 30 0000000000010011"
const rtdText = "(--- s9 ET1K 220 220 0 50 0 1/1 1K"

type fakeConn struct {
	reader  *bytes.Reader
	packets [][]byte
	closed  bool
	reply   func(map[string]string) []byte
}

func (c *fakeConn) Write(p []byte) (int, error) {
	c.packets = append(c.packets, bytes.Clone(p))
	var req map[string]string
	if err := json.Unmarshal(p, &req); err != nil {
		return 0, err
	}
	var packet []byte
	if c.reply != nil {
		packet = c.reply(req)
	} else {
		value := qadText
		if req["cmd"] == "RTD" {
			value = rtdText
		}
		var err error
		packet, err = json.Marshal(map[string]string{"cmd": req["cmd"], "session": req["session"], "uid": "fixture@ayi9.com", "value": value})
		if err != nil {
			return 0, err
		}
	}
	c.reader = bytes.NewReader(packet)
	return len(p), nil
}
func (c *fakeConn) Read(p []byte) (int, error) {
	if c.reader == nil {
		return 0, io.EOF
	}
	if len(p) > 3 {
		p = p[:3]
	}
	return c.reader.Read(p)
}
func (c *fakeConn) Close() error                     { c.closed = true; return nil }
func (c *fakeConn) LocalAddr() net.Addr              { return nil }
func (c *fakeConn) RemoteAddr() net.Addr             { return nil }
func (c *fakeConn) SetDeadline(time.Time) error      { return nil }
func (c *fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fakeConn) SetWriteDeadline(time.Time) error { return nil }
func config(c net.Conn) Config {
	return Config{Connection: guard.Config{Address: "127.0.0.1:2390", DialContext: func(context.Context, string, string) (net.Conn, error) { return c, nil }}}
}
func TestSerialCacheAndIndependentRaw(t *testing.T) {
	conn := &fakeConn{}
	client, err := New(config(conn), driver.AYI9Plan{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	info, first, err := client.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(conn.packets) != 2 || info.Model != "ET1K" || info.Identity != "fixture@ayi9.com" || first.AYI9.RTDAt.IsZero() || first.At.Before(first.AYI9.RTDAt) {
		t.Fatal("身份/时间/命令数错误")
	}
	first.AYI9.RTD[2] = "mutated"
	second, err := client.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(conn.packets) != 3 || second.AYI9.RTD[2] != "ET1K" || !second.AYI9.RTDAt.Equal(first.AYI9.RTDAt) {
		t.Fatal("RTD 缓存损坏")
	}
	client.rated.RTDAt = time.Now().Add(-11 * time.Minute)
	if _, err := client.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(conn.packets) != 5 {
		t.Fatal("未刷新RTD")
	}
	for _, p := range conn.packets {
		if err := guard.Validate(driver.ProtocolAYI9, p); err != nil {
			t.Fatal(err)
		}
	}
}
func TestResponseErrors(t *testing.T) {
	for _, packet := range []string{`{"session":"1","value":"103"}`, `{"session":"1","value":103}`, `{"session":"1","value":"FAILED","code":103}`, `{"session":"1","code":"103"}`} {
		conn := &fakeConn{reply: func(map[string]string) []byte { return []byte(packet) }}
		client, err := New(config(conn), driver.AYI9Plan{})
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = client.Probe(context.Background())
		if !errors.Is(err, ErrPasswordWrong) || !conn.closed {
			t.Fatalf("错误码未识别: %v", err)
		}
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, packet := range []string{`null`, `[]`, `{"session":"1","session":"2","value":"x"}`, `{"session":"1","value":null}`, `{"session":"1","code":103,"value":"104"}`, `{"session":null,"value":"FAILED"}`} {
		if _, err := parseResponse([]byte(packet)); err == nil {
			t.Fatal("接受非法响应")
		}
	}
}
func TestMismatchedSessionAndLimit(t *testing.T) {
	conn := &fakeConn{reply: func(req map[string]string) []byte {
		value := qadText
		if req["cmd"] == "RTD" {
			value = rtdText
		}
		correct, _ := json.Marshal(map[string]string{"session": req["session"], "cmd": req["cmd"], "value": value})
		return append([]byte(`{"session":"9999","value":"FAILED"}`), correct...)
	}}
	client, err := New(config(conn), driver.AYI9Plan{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err = client.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, packet := range []string{strings.Repeat(`{"session":"9999","value":"FAILED"}`, 20), `{"session":"1","value":"` + strings.Repeat("x", 70*1024) + `"}`} {
		c := &fakeConn{reply: func(map[string]string) []byte { return []byte(packet) }}
		client, err := New(config(c), driver.AYI9Plan{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = client.Poll(context.Background()); err == nil || !c.closed {
			t.Fatal("超限响应未关闭")
		}
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
func TestDecodeFailurePreservesRaw(t *testing.T) {
	conn := &fakeConn{reply: func(req map[string]string) []byte {
		p, _ := json.Marshal(map[string]string{"session": req["session"], "value": "(1 2"})
		return p
	}}
	client, err := New(config(conn), driver.AYI9Plan{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	raw, err := client.Poll(context.Background())
	if err == nil || raw.AYI9.QADText != "(1 2" || len(raw.Errors) != 1 {
		t.Fatal("解码错误丢失")
	}
}
func TestTimeoutAndReconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, b := net.Pipe()
		defer b.Close()
		calls := 0
		cfg := config(a)
		cfg.Connection.Timeout = 20 * time.Millisecond
		cfg.Connection.DialContext = func(context.Context, string, string) (net.Conn, error) {
			calls++
			if calls == 1 {
				return a, nil
			}
			return &fakeConn{}, nil
		}
		client, err := New(cfg, driver.AYI9Plan{})
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		if _, err := client.Poll(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		if _, err := client.Poll(context.Background()); err != nil || calls != 2 {
			t.Fatalf("未重连: %v", err)
		}
	})
}
func FuzzReadPlanSafety(f *testing.F) {
	f.Add(int64(600), "fake")
	f.Add(int64(-1), "")
	f.Fuzz(func(t *testing.T, seconds int64, password string) {
		if len(password) > 128 {
			return
		}
		conn := &fakeConn{}
		cfg := config(conn)
		cfg.Password = secret.NewString(password)
		client, err := New(cfg, driver.AYI9Plan{RTDInterval: time.Duration(seconds) * time.Second})
		if err != nil {
			if len(conn.packets) != 0 {
				t.Fatal("非法计划被发送")
			}
			return
		}
		defer client.Close()
		if _, err = client.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, p := range conn.packets {
			if err := guard.Validate(driver.ProtocolAYI9, p); err != nil {
				t.Fatal(err)
			}
		}
	})
}
func FuzzResponse(f *testing.F) {
	f.Add([]byte(`{"session":"1","code":103}`))
	f.Add([]byte(`{"session":"1","value":"(1 2"}`))
	f.Fuzz(func(t *testing.T, p []byte) {
		if len(p) > 70*1024 {
			return
		}
		_, _ = parseResponse(p)
	})
}

func TestIdentityMismatchInvalidatesBothCommands(t *testing.T) {
	conn := &fakeConn{reply: func(req map[string]string) []byte {
		value := qadText
		if req["cmd"] == "RTD" {
			value = rtdText
		}
		packet, _ := json.Marshal(map[string]string{"session": req["session"], "uid": req["cmd"] + "-identity", "value": value})
		return packet
	}}
	client, err := New(config(conn), driver.AYI9Plan{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	raw, err := client.Poll(context.Background())
	if err == nil || !conn.closed || len(raw.Errors) != 2 {
		t.Fatal("跨设备数据未失效")
	}
	for _, failure := range raw.Errors {
		if failure.Kind != "identity" {
			t.Fatal("身份失败语义丢失")
		}
	}
}
