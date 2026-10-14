package snmp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/driver/guard"
	"ups-warden/internal/driver/snmp/ber"
	"ups-warden/internal/secret"
)

const root = "1.3.6.1.2.1.33"

func oid(i int) string { return fmt.Sprintf("%s.1.2.%d.0", root, i) }
func response(m ber.Message) ber.Message {
	m.PDU = ber.Response
	m.ErrorStatus = 0
	m.ErrorIndex = 0
	for i := range m.VarBinds {
		m.VarBinds[i].Value = driver.SNMPValue{Tag: 2, Int: int64(i + 1)}
	}
	return m
}
func pipeClient(t *testing.T, plan driver.SNMPPlan, version string, fn func(ber.Message) []ber.Message) *Client {
	t.Helper()
	cfg := Config{Version: version, Connection: guard.Config{Address: "test.invalid:161", Timeout: time.Second, DialContext: func(_ context.Context, network, _ string) (net.Conn, error) {
		if network != "udp" {
			return nil, errors.New("not UDP")
		}
		a, b := net.Pipe()
		t.Cleanup(func() { _ = b.Close() })
		go func() {
			defer b.Close()
			buf := make([]byte, 65535)
			for {
				n, e := b.Read(buf)
				if e != nil {
					return
				}
				m, e := ber.Decode(buf[:n])
				if e != nil {
					return
				}
				if m.RequestIDBytes != 4 {
					return
				}
				for _, r := range fn(m) {
					out, e := ber.Encode(r)
					if e != nil {
						return
					}
					if _, e = b.Write(out); e != nil {
						return
					}
				}
			}
		}()
		return a, nil
	}}}
	c, e := New(cfg, plan)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
func TestGETBatchCopyAndMatching(t *testing.T) {
	list := []string{}
	for i := 0; i < 45; i++ {
		list = append(list, oid(i))
	}
	var mu sync.Mutex
	counts := []int{}
	c := pipeClient(t, driver.SNMPPlan{Strategy: "get", OIDs: list}, "", func(m ber.Message) []ber.Message {
		mu.Lock()
		counts = append(counts, len(m.VarBinds))
		mu.Unlock()
		r := response(m)
		wrongID := r
		wrongID.RequestID++
		wrongVersion := r
		wrongVersion.Version = 0
		wrongCommunity := r
		wrongCommunity.Community = "wrong-test-only"
		return []ber.Message{wrongID, wrongVersion, wrongCommunity, r}
	})
	list[0] = "3.0"
	raw, e := c.Poll(context.Background())
	if e != nil || len(raw.SNMP) != 45 {
		t.Fatalf("%v %d", e, len(raw.SNMP))
	}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(counts) != "[20 20 5]" {
		t.Fatal(counts)
	}
}
func TestV1NoSuchNameCacheReconnect(t *testing.T) {
	var mu sync.Mutex
	requests := [][]string{}
	c := pipeClient(t, driver.SNMPPlan{Strategy: "get", OIDs: []string{oid(1), oid(2)}}, "1", func(m ber.Message) []ber.Message {
		names := []string{}
		bad := 0
		for i, v := range m.VarBinds {
			names = append(names, v.OID)
			if v.OID == oid(1) {
				bad = i + 1
			}
		}
		mu.Lock()
		requests = append(requests, names)
		mu.Unlock()
		r := response(m)
		if bad != 0 {
			r.ErrorStatus = 2
			r.ErrorIndex = bad
		}
		return []ber.Message{r}
	})
	for i := 0; i < 2; i++ {
		raw, e := c.Poll(context.Background())
		if e != nil || raw.SNMP[oid(1)].Exception != "noSuchName" || len(raw.SNMP) != 2 {
			t.Fatal(raw, e)
		}
	}
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	c.disconnect(conn)
	if _, e := c.Poll(context.Background()); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 5 || len(requests[2]) != 1 || len(requests[3]) != 2 {
		t.Fatal(requests)
	}
}
func TestBulkContinuationAndExceptions(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	c := pipeClient(t, driver.SNMPPlan{}, "", func(m ber.Message) []ber.Message {
		mu.Lock()
		defer mu.Unlock()
		calls++
		r := response(m)
		if m.PDU != ber.GetBulkRequest || m.ErrorStatus != 0 || m.ErrorIndex != 60 {
			r.ErrorStatus = 5
			return []ber.Message{r}
		}
		switch calls {
		case 1:
			if m.VarBinds[0].OID != root {
				r.ErrorStatus = 5
			}
			r.VarBinds = []ber.VarBind{{OID: oid(1), Value: driver.SNMPValue{Tag: 4, Text: "---"}}, {OID: oid(2), Value: driver.SNMPValue{Tag: 0x44, Bytes: []byte{1, 2}}}}
		case 2:
			if m.VarBinds[0].OID != oid(2) {
				r.ErrorStatus = 5
			}
			r.VarBinds = []ber.VarBind{{OID: oid(3), Value: driver.SNMPValue{Tag: 0x80}}, {OID: oid(4), Value: driver.SNMPValue{Tag: 0x82}}}
		}
		return []ber.Message{r}
	})
	raw, e := c.Poll(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if raw.SNMP[oid(1)].Text != "" || len(raw.SNMP[oid(2)].Bytes) != 2 || raw.SNMP[oid(3)].Exception != "noSuchObject" || len(raw.Errors) != 2 {
		t.Fatal(raw)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatal(calls)
	}
}
func TestBulkFallback(t *testing.T) {
	c := pipeClient(t, driver.SNMPPlan{OIDs: []string{oid(1)}}, "", func(m ber.Message) []ber.Message {
		r := response(m)
		if m.PDU == ber.GetBulkRequest {
			r.ErrorStatus = 5
		}
		return []ber.Message{r}
	})
	raw, e := c.Poll(context.Background())
	if e != nil || len(raw.SNMP) != 1 || len(raw.Errors) != 1 || raw.Errors[0].Kind != "fallback" || raw.Errors[0].OID != "" {
		t.Fatal(raw, e)
	}
}
func TestWalkLimits(t *testing.T) {
	for _, mode := range []string{"outside", "repeat", "limit"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			calls := 0
			c := pipeClient(t, driver.SNMPPlan{Strategy: "walk"}, "1", func(m ber.Message) []ber.Message {
				mu.Lock()
				defer mu.Unlock()
				calls++
				r := response(m)
				o := oid(calls)
				if mode == "outside" {
					o = "1.3.6.1.2.1.34.0"
				}
				if mode == "repeat" {
					o = m.VarBinds[0].OID + ".1"
					if calls > 1 {
						o = m.VarBinds[0].OID
					}
				}
				r.VarBinds = []ber.VarBind{{OID: o, Value: driver.SNMPValue{Tag: 2, Int: 1}}}
				return []ber.Message{r}
			})
			raw, e := c.Poll(context.Background())
			if mode == "outside" {
				if e != nil || len(raw.SNMP) != 0 {
					t.Fatal(raw, e)
				}
			} else if e == nil {
				t.Fatal("missing limit error")
			}
			mu.Lock()
			defer mu.Unlock()
			if mode == "limit" && calls != 20 {
				t.Fatal(calls)
			}
		})
	}
}
func TestPartialGETAndMissing(t *testing.T) {
	list := []string{}
	for i := 0; i < 21; i++ {
		list = append(list, oid(i))
	}
	c := pipeClient(t, driver.SNMPPlan{Strategy: "get", OIDs: list}, "", func(m ber.Message) []ber.Message {
		r := response(m)
		if len(r.VarBinds) == 1 {
			r.VarBinds = nil
		}
		return []ber.Message{r}
	})
	raw, e := c.Poll(context.Background())
	if e == nil || len(raw.SNMP) != 20 || len(raw.Errors) != 1 {
		t.Fatal(raw, e)
	}
}
func TestCancelCloseAndTimeout(t *testing.T) {
	for _, mode := range []string{"cancel", "close", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{}, 1)
			c := pipeClient(t, driver.SNMPPlan{Strategy: "get", OIDs: []string{oid(1)}}, "", func(ber.Message) []ber.Message { started <- struct{}{}; return nil })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "timeout" {
				c.cfg.Connection.Timeout = 20 * time.Millisecond
			}
			done := make(chan error, 1)
			go func() { _, e := c.Poll(ctx); done <- e }()
			<-started
			switch mode {
			case "cancel":
				cancel()
			case "close":
				if e := c.Close(); e != nil {
					t.Fatal(e)
				}
			}
			select {
			case e := <-done:
				if e == nil {
					t.Fatal("no failure")
				}
				if mode == "cancel" && !errors.Is(e, context.Canceled) {
					t.Fatal(e)
				}
				if mode == "timeout" && !errors.Is(e, context.DeadlineExceeded) {
					t.Fatal(e)
				}
			case <-time.After(time.Second):
				t.Fatal("I/O not interrupted")
			}
			if e := c.Close(); e != nil {
				t.Fatal(e)
			}
			if _, e := c.Poll(context.Background()); e == nil {
				t.Fatal("Close not terminal")
			}
		})
	}
}
func TestPlanValidationAndErrorRedaction(t *testing.T) {
	cfg := Config{Connection: guard.Config{Address: "test.invalid:161"}, Community: secret.NewString("private-test-value")}
	for _, p := range []driver.SNMPPlan{{Strategy: "set"}, {BulkRoot: "3.1"}, {MaxRepetitions: -1}, {MaxRepetitions: 65536}, {OIDs: []string{"1.3.bad"}}} {
		if _, e := New(cfg, p); e == nil {
			t.Fatal(p)
		}
	}
	cfg.Connection.DialContext = func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("private-test-value") }
	c, e := New(cfg, driver.SNMPPlan{Strategy: "get", OIDs: []string{oid(1)}})
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.Poll(context.Background())
	if e == nil || strings.Contains(e.Error(), "private-test-value") {
		t.Fatal(e)
	}
}
func TestLocalUDPProbe(t *testing.T) {
	server, e := net.ListenPacket("udp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 65535)
		n, addr, e := server.ReadFrom(buf)
		if e != nil {
			done <- e
			return
		}
		m, e := ber.Decode(buf[:n])
		if e != nil {
			done <- e
			return
		}
		r := response(m)
		for i := range r.VarBinds {
			r.VarBinds[i].Value = driver.SNMPValue{Tag: 4, Text: fmt.Sprintf("identity-%d", i)}
		}
		b, e := ber.Encode(r)
		if e == nil {
			_, e = server.WriteTo(b, addr)
		}
		done <- e
	}()
	oids := []string{}
	for i := 1; i <= 5; i++ {
		oids = append(oids, fmt.Sprintf("%s.1.1.%d.0", root, i))
	}
	c, e := New(Config{Connection: guard.Config{Address: server.LocalAddr().String(), Timeout: time.Second}}, driver.SNMPPlan{Strategy: "get", OIDs: oids})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	info, raw, e := c.Probe(context.Background())
	if e != nil || len(raw.SNMP) != 5 || info.Manufacturer != "identity-0" || info.Model != "identity-1" || info.Identity != "identity-4" {
		t.Fatal(info, raw, e)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}

// recordConn 不联网，在 guard 后记录并校验每一帧，随即生成有界响应。
type recordConn struct {
	packets  [][]byte
	response []byte
}

func (c *recordConn) Write(b []byte) (int, error) {
	if e := guard.Validate(driver.ProtocolSNMP, b); e != nil {
		return 0, e
	}
	m, e := ber.Decode(b)
	if e != nil {
		return 0, e
	}
	if m.RequestIDBytes != 4 {
		return 0, errors.New("short request-id")
	}
	c.packets = append(c.packets, append([]byte(nil), b...))
	r := response(m)
	if m.PDU != ber.GetRequest {
		r.VarBinds = []ber.VarBind{{OID: "2.0", Value: driver.SNMPValue{Tag: 0x82}}}
	}
	c.response, e = ber.Encode(r)
	return len(b), e
}
func (c *recordConn) Read(b []byte) (int, error) {
	n := copy(b, c.response)
	c.response = nil
	return n, nil
}
func (*recordConn) Close() error                     { return nil }
func (*recordConn) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (*recordConn) RemoteAddr() net.Addr             { return &net.UDPAddr{} }
func (*recordConn) SetDeadline(time.Time) error      { return nil }
func (*recordConn) SetReadDeadline(time.Time) error  { return nil }
func (*recordConn) SetWriteDeadline(time.Time) error { return nil }
func FuzzReadPlan(f *testing.F) {
	f.Add("get", "1.3.6.1.2.1.33", "1.3.6.1.2.1.33.0", uint16(60), true)
	f.Add("set", "bad", "bad", uint16(0), false)
	f.Add("getbulk", "1.3.6.1.2.1.33", "1.3.6.1.2.1.33.0", uint16(60), false)
	f.Add("walk", "1.3.6.1.2.1.33", "1.3.6.1.2.1.33.0", uint16(60), true)
	f.Fuzz(func(t *testing.T, strategy, root, oid string, max uint16, v1 bool) {
		rec := &recordConn{}
		version := "2c"
		if v1 {
			version = "1"
		}
		cfg := Config{Version: version, Connection: guard.Config{Address: "test.invalid:161", DialContext: func(context.Context, string, string) (net.Conn, error) { return rec, nil }}}
		c, e := New(cfg, driver.SNMPPlan{Strategy: strategy, BulkRoot: root, MaxRepetitions: int(max), OIDs: []string{oid}})
		if e != nil {
			if len(rec.packets) != 0 {
				t.Fatal("invalid plan sent")
			}
			return
		}
		defer c.Close()
		if _, e := c.Poll(context.Background()); e != nil {
			t.Fatal(e)
		}
		if len(rec.packets) == 0 {
			t.Fatal("valid plan did not pass guard")
		}
		for _, b := range rec.packets {
			if e := guard.Validate(driver.ProtocolSNMP, b); e != nil {
				t.Fatal(e)
			}
			m, e := ber.Decode(b)
			if e != nil || m.RequestIDBytes != 4 || m.PDU == 0xA3 {
				t.Fatal(m, e)
			}
		}
	})
}

func TestCloseDuringDialAndQueuedContext(t *testing.T) {
	started := make(chan struct{})
	c, e := New(Config{Connection: guard.Config{Address: "test.invalid:161", DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}}, driver.SNMPPlan{Strategy: "get", OIDs: []string{oid(1)}})
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := c.Poll(context.Background()); done <- e }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, e := c.Poll(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	if e := c.Close(); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("dial survived Close")
		}
	case <-time.After(time.Second):
		t.Fatal("dial not canceled")
	}
}

func TestBulkTimeoutReconnectFallback(t *testing.T) {
	var mu sync.Mutex
	bulk, get := 0, 0
	c := pipeClient(t, driver.SNMPPlan{OIDs: []string{oid(1)}}, "", func(m ber.Message) []ber.Message {
		mu.Lock()
		defer mu.Unlock()
		if m.PDU == ber.GetBulkRequest {
			bulk++
			return nil
		}
		get++
		return []ber.Message{response(m)}
	})
	c.cfg.Connection.Timeout = 20 * time.Millisecond
	raw, e := c.Poll(context.Background())
	if e != nil || len(raw.SNMP) != 1 || len(raw.Errors) != 1 || raw.Errors[0].Kind != "fallback" {
		t.Fatal(raw, e)
	}
	mu.Lock()
	defer mu.Unlock()
	if bulk != 1 || get != 1 {
		t.Fatal(bulk, get)
	}
}

func TestV2NoSuchNameAndBadIndex(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(fmt.Sprint(bad), func(t *testing.T) {
			c := pipeClient(t, driver.SNMPPlan{Strategy: "get", OIDs: []string{oid(1), oid(2)}}, "2c", func(m ber.Message) []ber.Message {
				r := response(m)
				r.ErrorStatus = 2
				r.ErrorIndex = 1
				if bad {
					r.ErrorIndex = 3
				}
				return []ber.Message{r}
			})
			raw, e := c.Poll(context.Background())
			if bad {
				if e == nil || len(raw.Errors) != 1 {
					t.Fatal(raw, e)
				}
			} else {
				if e != nil || len(raw.SNMP) != 2 || raw.SNMP[oid(2)].Exception != "noSuchName" {
					t.Fatal(raw, e)
				}
				if len(c.unsupported) != 0 {
					t.Fatal("v2c noSuchName cached")
				}
			}
		})
	}
}

func TestBulkWithoutFallbackOIDsDoesNotHideFailure(t *testing.T) {
	c := pipeClient(t, driver.SNMPPlan{}, "", func(m ber.Message) []ber.Message { r := response(m); r.ErrorStatus = 5; return []ber.Message{r} })
	raw, e := c.Poll(context.Background())
	if e == nil || len(raw.Errors) != 2 || raw.Errors[0].Kind != "fallback" {
		t.Fatal(raw, e)
	}
}

func TestSerialConcurrentPolls(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	var mu sync.Mutex
	calls := 0
	c := pipeClient(t, driver.SNMPPlan{Strategy: "get", OIDs: []string{oid(1)}}, "", func(m ber.Message) []ber.Message {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			started <- struct{}{}
			<-release
		}
		return []ber.Message{response(m)}
	})
	done := make(chan error, 2)
	go func() { _, e := c.Poll(context.Background()); done <- e }()
	<-started
	go func() { _, e := c.Poll(context.Background()); done <- e }()
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(time.Second):
			t.Fatal("serial poll blocked")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatal(calls)
	}
}

func TestEndOfMibDoesNotOverwriteValue(t *testing.T) {
	for _, split := range []bool{false, true} {
		t.Run(fmt.Sprint(split), func(t *testing.T) {
			rounds := 0
			c := pipeClient(t, driver.SNMPPlan{Strategy: "getbulk", BulkRoot: root}, "2c", func(m ber.Message) []ber.Message {
				rounds++
				r := response(m)
				value := ber.VarBind{OID: oid(1), Value: driver.SNMPValue{Tag: 2, Int: 800}}
				end := ber.VarBind{OID: oid(1), Value: driver.SNMPValue{Tag: 0x82, Exception: "endOfMibView"}}
				r.VarBinds = []ber.VarBind{value, end}
				if split {
					if rounds == 1 {
						r.VarBinds = []ber.VarBind{value}
					} else {
						r.VarBinds = []ber.VarBind{end}
					}
				}
				return []ber.Message{r}
			})
			raw, err := c.Poll(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if raw.SNMP[oid(1)].Int != 800 || raw.SNMP[oid(1)].Exception != "" || len(raw.Errors) != 0 {
				t.Fatalf("结束标记覆盖有效数据: %+v", raw)
			}
		})
	}
}
