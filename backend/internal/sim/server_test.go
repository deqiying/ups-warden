package sim

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/driver/guard"
	"ups-warden/internal/driver/snmp/ber"
)

func startTest(t *testing.T, cfg Config) *Server {
	t.Helper()
	s, err := Start(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func dialTest(t *testing.T, network, address string) net.Conn {
	t.Helper()
	c, err := net.DialTimeout(network, address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	return c
}
func mbPacket(fc byte, start, count uint16) []byte {
	p := []byte{0, 1, 0, 0, 0, 6, 0, fc}
	p = binary.BigEndian.AppendUint16(p, start)
	return binary.BigEndian.AppendUint16(p, count)
}
func mbRead(t *testing.T, c net.Conn, packet []byte) []byte {
	t.Helper()
	if _, err := c.Write(packet); err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 7)
	if _, err := io.ReadFull(c, header); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, int(binary.BigEndian.Uint16(header[4:6]))-1)
	if _, err := io.ReadFull(c, body); err != nil {
		t.Fatal(err)
	}
	return body
}
func snmpMessage(pdu byte, oids ...string) ber.Message {
	m := ber.Message{Version: 1, Community: "sim-public", RequestID: 0x12345678, PDU: pdu}
	if pdu == ber.GetBulkRequest {
		m.ErrorIndex = 60
	}
	for _, oid := range oids {
		m.VarBinds = append(m.VarBinds, ber.VarBind{OID: oid, Value: driver.SNMPValue{Tag: 5}})
	}
	return m
}
func snmpRead(t *testing.T, c net.Conn, m ber.Message) ber.Message {
	t.Helper()
	p, err := ber.Encode(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(p); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, maxPacket)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	response, err := ber.Decode(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if response.RequestID != m.RequestID || response.PDU != ber.Response {
		t.Fatal("SNMP 响应关联失败")
	}
	return response
}
func ayi9Read(t *testing.T, c net.Conn, decoder *json.Decoder, command, pass string) map[string]any {
	t.Helper()
	p, _ := json.Marshal(ayi9Request{Cmd: command, Session: "123", Pass: pass})
	if _, err := c.Write(p); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response["session"] != "123" || response["cmd"] != command {
		t.Fatal("AYI9 响应关联失败")
	}
	return response
}

func TestSharedProtocolStateAndSafeRequests(t *testing.T) {
	var seconds atomic.Int64
	origin := time.Unix(100000, 0)
	s := startTest(t, Config{Community: "fake-community", Password: "fake-password", ET1K: true,
		Scenario: Scenario{Steps: []Step{{At: "10s", State: "outage"}, {At: "60s", State: "recovery"}}}, Now: func() time.Time { return origin.Add(time.Duration(seconds.Load()) * time.Second) }})
	snmp, modbus, ayi9 := s.Addresses()
	sc := dialTest(t, "udp", snmp)
	mc := dialTest(t, "tcp", modbus)
	ac := dialTest(t, "tcp", ayi9)
	decoder := json.NewDecoder(ac)
	for _, second := range []int64{0, 25, 60} {
		seconds.Store(second)
		req := snmpMessage(ber.GetBulkRequest, "1.3.6.1.2.1.33")
		req.Community = "fake-community"
		response := snmpRead(t, sc, req)
		values := map[string]driver.SNMPValue{}
		for _, v := range response.VarBinds {
			if v.Value.Tag != 0x82 {
				values[v.OID] = v.Value
			}
		}
		mb := mbRead(t, mc, mbPacket(3, 0, 80))
		if len(mb) != 162 || mb[0] != 3 || mb[1] != 160 {
			t.Fatal("Modbus 帧长度错误")
		}
		qad := ayi9Read(t, ac, decoder, "QAD", "fake-password")
		parts := strings.Fields(strings.TrimPrefix(qad["value"].(string), "("))
		if len(parts) != 23 {
			t.Fatal("QAD 段数错误")
		}
		state := s.cfg.Scenario.StateAt(time.Duration(second) * time.Second)
		if values["1.3.6.1.2.1.33.1.4.4.1.2.1"].Int != int64(state.OutputVoltage[0]) || binary.BigEndian.Uint16(mb[2+28*2:]) != scaled(state.OutputVoltage[0], 10) || parts[8] != "220.0" {
			t.Fatal("三协议输出电压不一致")
		}
		expectedSource := int64(3)
		if !state.Mains {
			expectedSource = 5
		}
		if values["1.3.6.1.2.1.33.1.4.1.0"].Int != expectedSource || binary.BigEndian.Uint16(mb[2+17*2:]) != scaled(state.InputVoltage[0], 10) {
			t.Fatal("市电状态不一致")
		}
		if values["1.3.6.1.2.1.33.1.2.2.0"].Int != state.SecondsOnBattery {
			t.Fatal("放电计时错误")
		}
	}
	rated := ayi9Read(t, ac, decoder, "RTD", "fake-password")
	if len(strings.Fields(strings.TrimPrefix(rated["value"].(string), "("))) != 10 {
		t.Fatal("RTD 段数错误")
	}
	requests := s.Requests()
	if len(requests) != 10 {
		t.Fatalf("请求数=%d", len(requests))
	}
	for _, request := range requests {
		if !request.Allowed || guard.Validate(request.Protocol, request.Data) != nil {
			t.Fatalf("脱敏后 guard 失效: %s", request.Protocol)
		}
		if bytes.Contains(request.Data, []byte("fake-community")) || bytes.Contains(request.Data, []byte("fake-password")) {
			t.Fatal("记录泄露凭据")
		}
	}
	requests[0].Data[0] = 0
	if s.Requests()[0].Data[0] == 0 {
		t.Fatal("Requests 未深拷贝")
	}
	if s.DroppedRequests() != 0 {
		t.Fatal("意外丢失请求")
	}
}

func TestSNMPQuirksAndStandard(t *testing.T) {
	root := "1.3.6.1.2.1.33"
	for _, et1k := range []bool{false, true} {
		t.Run(fmt.Sprint(et1k), func(t *testing.T) {
			s := startTest(t, Config{ET1K: et1k})
			address, _, _ := s.Addresses()
			c := dialTest(t, "udp", address)
			get := snmpRead(t, c, snmpMessage(ber.GetRequest, root+".1.4.1.0"))
			if et1k && get.ErrorStatus != 2 || !et1k && get.VarBinds[0].Value.Int != 3 {
				t.Fatal("GET 怪癖错误")
			}
			next := snmpRead(t, c, snmpMessage(ber.GetNextRequest, root, root+".1.2"))
			expected := 2
			if et1k {
				expected = 1
			}
			if len(next.VarBinds) != expected {
				t.Fatal("GETNEXT 绑定数错误")
			}
			bulk := snmpRead(t, c, snmpMessage(ber.GetBulkRequest, root))
			if len(bulk.VarBinds) != 60 || bulk.VarBinds[59].Value.Exception != "endOfMibView" {
				t.Fatal("GETBULK 结束标记错误")
			}
			if et1k {
				short := snmpMessage(ber.GetRequest, root)
				short.RequestID = 1
				p, _ := ber.Encode(short)
				if _, err := c.Write(p); err != nil {
					t.Fatal(err)
				}
				_ = c.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
				if _, err := c.Read(make([]byte, 1024)); err == nil {
					t.Fatal("ET1K 未丢弃短 request-id")
				}
			} else {
				missing := snmpMessage(ber.GetRequest, root+".9.9.9.0")
				got := snmpRead(t, c, missing)
				if got.VarBinds[0].Value.Exception != "noSuchObject" {
					t.Fatal("v2 缺失值错误")
				}
				missing.Version = 0
				got = snmpRead(t, c, missing)
				if got.ErrorStatus != 2 || got.ErrorIndex != 1 {
					t.Fatal("v1 缺失值错误")
				}
			}
		})
	}
	s := startTest(t, Config{Scenario: Scenario{Phases: 3}})
	address, _, ayi9 := s.Addresses()
	c := dialTest(t, "udp", address)
	got := snmpRead(t, c, snmpMessage(ber.GetRequest, root+".1.4.4.1.4.3", root+".1.3.2.0", root+".1.5.3.1.3.3"))
	if got.VarBinds[0].Value.Int != 80 || got.VarBinds[1].Value.Int != 3 || got.VarBinds[2].Value.Int != 220 {
		t.Fatal("RFC 三相 W/旁路列错误")
	}
	ac := dialTest(t, "tcp", ayi9)
	decoder := json.NewDecoder(ac)
	rtd := ayi9Read(t, ac, decoder, "RTD", "")
	parts := strings.Fields(strings.TrimPrefix(rtd["value"].(string), "("))
	if parts[8] != "3/3" {
		t.Fatal("AYI9 三相编码错误")
	}
}

func TestModbusCustomAndExceptions(t *testing.T) {
	table := map[driver.RegKey]uint16{{FC: driver.FC03, Addr: 100}: 1234, {FC: driver.FC04, Addr: 100}: 5678}
	s := startTest(t, Config{Registers: table})
	table[driver.RegKey{FC: driver.FC03, Addr: 100}] = 9
	_, addr, _ := s.Addresses()
	c := dialTest(t, "tcp", addr)
	for _, fc := range []byte{3, 4} {
		body := mbRead(t, c, mbPacket(fc, 100, 1))
		want := uint16(1234)
		if fc == 4 {
			want = 5678
		}
		if binary.BigEndian.Uint16(body[2:]) != want {
			t.Fatal("自定义寄存器错误或未拷贝")
		}
	}
	for _, test := range []struct {
		fc           byte
		start, count uint16
		code         byte
	}{{3, 99, 1, 2}, {3, 100, 0, 3}, {3, 100, 126, 3}, {6, 100, 1, 1}} {
		body := mbRead(t, c, mbPacket(test.fc, test.start, test.count))
		if len(body) != 2 || body[0] != test.fc|0x80 || body[1] != test.code {
			t.Fatal("Modbus 异常错误")
		}
	}
	et := startTest(t, Config{ET1K: true})
	_, addr, _ = et.Addresses()
	c = dialTest(t, "tcp", addr)
	for _, test := range []struct {
		fc           byte
		start, count uint16
		code         byte
	}{{4, 0, 1, 1}, {3, 79, 2, 2}} {
		body := mbRead(t, c, mbPacket(test.fc, test.start, test.count))
		if body[1] != test.code {
			t.Fatal("ET1K Modbus 边界错误")
		}
	}
}

func TestAYI9PasswordErrorAndPipeline(t *testing.T) {
	s := startTest(t, Config{Password: "fake-password"})
	_, _, addr := s.Addresses()
	c := dialTest(t, "tcp", addr)
	decoder := json.NewDecoder(c)
	response := ayi9Read(t, c, decoder, "QAD", "wrong")
	if response["code"] != float64(103) {
		t.Fatal("密码错误没有返回103")
	}
	if response = ayi9Read(t, c, decoder, "RTD", "fake-password"); response["code"] != nil {
		t.Fatal("串行复用连接失败")
	}
	c2 := dialTest(t, "tcp", addr)
	_, err := c2.Write([]byte(`{"cmd":"QAD","session":"1","pass":"fake-password"}{"cmd":"RTD","session":"2","pass":"fake-password"}`))
	if err != nil {
		t.Fatal(err)
	}
	decoder = json.NewDecoder(c2)
	var first, second map[string]any
	if err := decoder.Decode(&first); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&second); err == nil {
		t.Fatal("流水线返回第二响应")
	}
	fixed := startTest(t, Config{AYI9ErrorCode: 103})
	_, _, addr = fixed.Addresses()
	c = dialTest(t, "tcp", addr)
	if ayi9Read(t, c, json.NewDecoder(c), "QAD", "")["code"] != float64(103) {
		t.Fatal("固定错误码失败")
	}
}

func TestOfflineAndClose(t *testing.T) {
	s := startTest(t, Config{Scenario: Scenario{Steps: []Step{{At: "0s", State: "offline"}}}})
	snmp, modbus, ayi9 := s.Addresses()
	for _, test := range []struct {
		network, address string
		packet           []byte
	}{{"udp", snmp, nil}, {"tcp", modbus, mbPacket(3, 0, 1)}, {"tcp", ayi9, []byte(`{"cmd":"QAD","session":"1"}`)}} {
		c := dialTest(t, test.network, test.address)
		packet := test.packet
		if packet == nil {
			packet, _ = ber.Encode(snmpMessage(ber.GetRequest, "1.3.6.1.2.1.33.1.4.1.0"))
		}
		if _, err := c.Write(packet); err != nil {
			t.Fatal(err)
		}
		_ = c.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		if _, err := c.Read(make([]byte, 512)); err == nil {
			t.Fatal("失联场景仍响应")
		}
	}
	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close 未停止连接 goroutine")
	}
	if err := s.Close(); err != nil {
		t.Fatal("Close 不幂等")
	}
	if len(s.Requests()) != 3 {
		t.Fatal("失联时未记录请求")
	}
}

func TestUnsafeAndBoundedInput(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", ":0", "192.0.2.1:0", "localhost:0", "127.0.0.1:65536"} {
		if s, err := Start(Config{SNMPAddress: addr}); err == nil {
			_ = s.Close()
			t.Fatal("接受非本机监听地址")
		}
	}
	if s, err := Start(Config{ET1K: true, Scenario: Scenario{Phases: 3}}); err == nil {
		_ = s.Close()
		t.Fatal("ET1K 接受三相")
	}
	s := startTest(t, Config{})
	snmp, modbus, ayi9 := s.Addresses()
	ac := dialTest(t, "tcp", ayi9)
	_, _ = ac.Write([]byte(`{"cmd":"SOFF","session":"1","pass":"secret"}`))
	_, _ = io.ReadAll(ac)
	mc := dialTest(t, "tcp", modbus)
	_ = mbRead(t, mc, mbPacket(6, 0, 1))
	sc := dialTest(t, "udp", snmp)
	packet, _ := ber.Encode(snmpMessage(ber.GetRequest, "1.3.6.1.2.1.33"))
	for i, b := range packet {
		if b == ber.GetRequest {
			packet[i] = 0xA3
			break
		}
	}
	_, _ = sc.Write(packet)
	_ = sc.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	_, _ = sc.Read(make([]byte, 512))
	for _, req := range s.Requests() {
		if req.Allowed || len(req.Data) != 0 {
			t.Fatal("不安全请求未清除载荷")
		}
	}
	if len(s.Requests()) != 3 {
		t.Fatal("不安全请求记录缺失")
	}
	if _, err := readObject(bufio.NewReader(strings.NewReader(`{"cmd":"` + strings.Repeat("x", maxPacket) + `"}`))); err == nil {
		t.Fatal("接受超大 JSON")
	}
	for _, p := range []string{`{"cmd":"QAD","cmd":"SOFF"}`, `{"cmd":"QAD","host":"x"}`, `{"cmd":"QAD","pass":null}`, `{"cmd":"QAD"}[]`} {
		if _, err := parseAYI9([]byte(p)); err == nil {
			t.Fatal("接受不安全 JSON")
		}
	}
	for i := 0; i < maxRequests+1; i++ {
		s.record(driver.ProtocolModbus, 12, nil, false)
	}
	if len(s.Requests()) != maxRequests || s.DroppedRequests() != 4 {
		t.Fatal("记录数量没有上限")
	}
}

func TestFrameLimitsAndFragmentation(t *testing.T) {
	for _, size := range []int{maxPacket, maxPacket + 1} {
		packet := `{"x":"` + strings.Repeat("x", size-8) + `"}`
		got, err := readObject(bufio.NewReader(strings.NewReader(packet)))
		if size == maxPacket && (err != nil || len(got) != size) || size > maxPacket && err == nil {
			t.Fatal("JSON 帧大小边界错误")
		}
	}
	s := startTest(t, Config{})
	_, modbus, ayi9 := s.Addresses()
	mc := dialTest(t, "tcp", modbus)
	packet := mbPacket(3, 28, 1)
	if _, err := mc.Write(packet[:4]); err != nil {
		t.Fatal(err)
	}
	body := mbRead(t, mc, packet[4:])
	if len(body) != 4 || binary.BigEndian.Uint16(body[2:]) != 2200 {
		t.Fatal("分段 Modbus 帧失败")
	}
	ac := dialTest(t, "tcp", ayi9)
	if _, err := ac.Write([]byte(`{"cmd":"QA`)); err != nil {
		t.Fatal(err)
	}
	if _, err := ac.Write([]byte(`D","session":"1"}`)); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.NewDecoder(ac).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response["cmd"] != "QAD" {
		t.Fatal("分段 AYI9 帧失败")
	}
	bad := dialTest(t, "tcp", modbus)
	if _, err := bad.Write([]byte{0, 1, 0, 0, 255, 255, 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Read(make([]byte, 1)); err == nil {
		t.Fatal("接受超大 Modbus 长度")
	}
}

func TestStartFailureReleasesEarlierListeners(t *testing.T) {
	reserved, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	snmpAddress := reserved.LocalAddr().String()
	if err := reserved.Close(); err != nil {
		t.Fatal(err)
	}
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if s, err := Start(Config{SNMPAddress: snmpAddress, AYI9Address: occupied.Addr().String()}); err == nil {
		_ = s.Close()
		t.Fatal("重复端口未启动失败")
	}
	reopened, err := net.ListenPacket("udp", snmpAddress)
	if err != nil {
		t.Fatalf("部分启动失败泄露 SNMP 监听器: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}
