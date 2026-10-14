// Package snmp 实现仅经 guard 发送的 SNMP v1/v2c 只读客户端。
package snmp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/driver/guard"
	"ups-warden/internal/driver/snmp/ber"
	"ups-warden/internal/secret"
)

// Config 包含连接配置及脱敏 community；Version 支持 1/2c，默认 2c。
type Config struct {
	Connection guard.Config
	Version    string
	Community  secret.String
}

// Client 串行执行请求；Close 是终态并中断正在进行的网络操作。
type Client struct {
	cfg         Config
	plan        driver.SNMPPlan
	version     int
	serial      chan struct{}
	lifetime    context.Context
	stop        context.CancelFunc
	mu          sync.Mutex
	conn        net.Conn
	closed      bool
	unsupported map[string]bool
}

var _ driver.Driver = (*Client)(nil)

// New 独立校验手工构造的计划，并复制 OID 列表以免调用方突变。
func New(cfg Config, plan driver.SNMPPlan) (*Client, error) {
	c, e := cfg.Connection.Validate()
	if e != nil {
		return nil, errors.New("SNMP 连接配置无效")
	}
	cfg.Connection = c
	version := 1
	switch cfg.Version {
	case "", "2c":
		cfg.Version = "2c"
	case "1":
		version = 0
	default:
		return nil, errors.New("SNMP 版本必须为 1 或 2c")
	}
	if cfg.Community.Reveal() == "" {
		cfg.Community = secret.NewString("public")
	}
	if len(cfg.Community.Reveal()) > 255 {
		return nil, errors.New("SNMP community 过长")
	}
	if plan.Strategy == "" {
		plan.Strategy = "getbulk"
	}
	switch plan.Strategy {
	case "get", "getnext", "walk", "getbulk":
	default:
		return nil, errors.New("SNMP 读取策略无效")
	}
	if plan.BulkRoot == "" {
		plan.BulkRoot = "1.3.6.1.2.1.33"
	}
	if plan.MaxRepetitions == 0 {
		plan.MaxRepetitions = 60
	}
	if plan.MaxRepetitions < 1 || plan.MaxRepetitions > 65535 || len(plan.OIDs) > 4096 {
		return nil, errors.New("SNMP 计划超过安全界限")
	}
	check := func(o string) error {
		_, e := ber.Encode(ber.Message{Version: version, PDU: ber.GetRequest, VarBinds: []ber.VarBind{{OID: o, Value: driver.SNMPValue{Tag: 5}}}})
		return e
	}
	if check(plan.BulkRoot) != nil {
		return nil, errors.New("SNMP 起始 OID 无效")
	}
	seen := map[string]bool{}
	list := []string{}
	for _, o := range plan.OIDs {
		if check(o) != nil {
			return nil, errors.New("SNMP 计划 OID 无效")
		}
		if !seen[o] {
			seen[o] = true
			list = append(list, o)
		}
	}
	plan.OIDs = list
	if plan.Strategy == "get" && len(list) == 0 {
		return nil, errors.New("SNMP GET 计划必须包含 OID")
	}
	lifetime, stop := context.WithCancel(context.Background())
	return &Client{cfg: cfg, plan: plan, version: version, serial: make(chan struct{}, 1), lifetime: lifetime, stop: stop, unsupported: map[string]bool{}}, nil
}
func safeIO(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return fmt.Errorf("SNMP 请求超时: %w", context.DeadlineExceeded)
	}
	return errors.New("SNMP 网络操作失败")
}
func (c *Client) connection(ctx context.Context) (net.Conn, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("SNMP 客户端已关闭")
	}
	if c.conn != nil {
		conn := c.conn
		c.mu.Unlock()
		return conn, nil
	}
	c.mu.Unlock()
	// Poll 的串行令牌保证只有一个 dial；不持状态锁，Close 可立即取消连接建立。
	conn, e := guard.Dial(ctx, c.cfg.Connection, driver.ProtocolSNMP)
	if e != nil {
		return nil, safeIO(ctx, e)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			c.logCloseFailure()
		}
		return nil, errors.New("SNMP 客户端已关闭")
	}
	c.conn = conn
	c.unsupported = map[string]bool{}
	return conn, nil
}
func (c *Client) disconnect(conn net.Conn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == conn {
		if err := c.conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			c.logCloseFailure()
		}
		c.conn = nil
	}
}
func (c *Client) request(ctx context.Context, pdu byte, oids []string) (ber.Message, error) {
	conn, e := c.connection(ctx)
	if e != nil {
		return ber.Message{}, e
	}
	id, e := ber.NewRequestID()
	if e != nil {
		return ber.Message{}, e
	}
	m := ber.Message{Version: c.version, Community: c.cfg.Community.Reveal(), PDU: pdu, RequestID: id}
	if pdu == ber.GetBulkRequest {
		m.ErrorIndex = c.plan.MaxRepetitions
	}
	for _, o := range oids {
		m.VarBinds = append(m.VarBinds, ber.VarBind{OID: o, Value: driver.SNMPValue{Tag: 5}})
	}
	b, e := ber.Encode(m)
	if e != nil {
		return ber.Message{}, errors.New("SNMP 请求编码失败")
	}
	cleanup, e := guard.Begin(ctx, conn, c.cfg.Connection.Timeout)
	if e != nil {
		c.disconnect(conn)
		return ber.Message{}, safeIO(ctx, e)
	}
	defer cleanup()
	n, e := conn.Write(b)
	if e == nil && n != len(b) {
		e = io.ErrShortWrite
	}
	if e != nil {
		c.disconnect(conn)
		return ber.Message{}, safeIO(ctx, e)
	}
	buf := make([]byte, 65535)
	for {
		if ctx.Err() != nil {
			c.disconnect(conn)
			return ber.Message{}, ctx.Err()
		}
		n, e := conn.Read(buf)
		if e != nil {
			c.disconnect(conn)
			return ber.Message{}, safeIO(ctx, e)
		}
		r, e := ber.Decode(buf[:n])
		if e != nil {
			c.disconnect(conn)
			return ber.Message{}, errors.New("SNMP 响应 BER 无效")
		}
		if r.PDU != ber.Response || r.RequestID != id || r.Version != c.version || r.Community != m.Community {
			continue
		}
		return r, nil
	}
}
func addValue(raw *driver.RawData, v ber.VarBind) {
	if v.Value.Tag == 4 && strings.TrimSpace(v.Value.Text) == "---" {
		v.Value.Text = ""
		v.Value.Bytes = nil
	}
	raw.SNMP[v.OID] = v.Value
	if v.Value.Exception != "" {
		raw.Errors = append(raw.Errors, driver.BlockError{OID: v.OID, Kind: "unsupported", Message: v.Value.Exception})
	}
}
func markUnsupported(raw *driver.RawData, o string) {
	addValue(raw, ber.VarBind{OID: o, Value: driver.SNMPValue{Exception: "noSuchName"}})
}
func (c *Client) get(ctx context.Context, raw *driver.RawData) error {
	if len(c.plan.OIDs) == 0 {
		e := errors.New("SNMP GET 没有可读取的计划 OID")
		raw.Errors = append(raw.Errors, driver.BlockError{Kind: "read_error", Message: e.Error()})
		return e
	}
	// 建立连接后再读取缓存：断线重连必须恢复所有被剔除的 OID。
	if _, e := c.connection(ctx); e != nil {
		return e
	}
	oids := []string{}
	for _, o := range c.plan.OIDs {
		if c.unsupported[o] {
			markUnsupported(raw, o)
		} else {
			oids = append(oids, o)
		}
	}
	var errs []error
	for start := 0; start < len(oids); start += 20 {
		end := min(start+20, len(oids))
		batch := append([]string(nil), oids[start:end]...)
		for len(batch) > 0 {
			r, e := c.request(ctx, ber.GetRequest, batch)
			if e != nil {
				for _, o := range batch {
					raw.Errors = append(raw.Errors, driver.BlockError{OID: o, Kind: "read_error", Message: e.Error()})
				}
				errs = append(errs, e)
				if ctx.Err() != nil {
					return errors.Join(errs...)
				}
				break
			}
			if r.ErrorStatus == 2 {
				if r.ErrorIndex < 1 || r.ErrorIndex > len(batch) {
					e := errors.New("SNMP noSuchName 索引无效")
					raw.Errors = append(raw.Errors, driver.BlockError{Kind: "decode_error", Message: e.Error()})
					errs = append(errs, e)
					break
				}
				i := r.ErrorIndex - 1
				o := batch[i]
				// v2c 的兼容性 noSuchName 也在本轮剔除，但只有 v1 缓存到重连。
				if c.version == 0 {
					c.unsupported[o] = true
				}
				markUnsupported(raw, o)
				batch = append(batch[:i], batch[i+1:]...)
				continue
			}
			if r.ErrorStatus != 0 {
				e := fmt.Errorf("SNMP agent error-status=%d error-index=%d", r.ErrorStatus, r.ErrorIndex)
				for _, o := range batch {
					raw.Errors = append(raw.Errors, driver.BlockError{OID: o, Kind: "agent_error", Message: e.Error()})
				}
				errs = append(errs, e)
				break
			}
			expected := map[string]bool{}
			for _, o := range batch {
				expected[o] = true
			}
			for _, v := range r.VarBinds {
				if !expected[v.OID] {
					e := errors.New("SNMP GET 响应含非请求或重复 OID")
					raw.Errors = append(raw.Errors, driver.BlockError{OID: v.OID, Kind: "decode_error", Message: e.Error()})
					errs = append(errs, e)
					continue
				}
				addValue(raw, v)
				delete(expected, v.OID)
			}
			for o := range expected {
				e := errors.New("SNMP GET 响应缺少 OID")
				raw.Errors = append(raw.Errors, driver.BlockError{OID: o, Kind: "read_error", Message: e.Error()})
				errs = append(errs, e)
			}
			break
		}
	}
	return errors.Join(errs...)
}
func (c *Client) walk(ctx context.Context, raw *driver.RawData, pdu byte) error {
	cursor := c.plan.BulkRoot
	for round := 0; round < 20; round++ {
		r, e := c.request(ctx, pdu, []string{cursor})
		if e != nil {
			return e
		}
		if r.ErrorStatus != 0 {
			return fmt.Errorf("SNMP agent error-status=%d error-index=%d", r.ErrorStatus, r.ErrorIndex)
		}
		if len(r.VarBinds) == 0 {
			return errors.New("SNMP walk 返回空列表")
		}
		for _, v := range r.VarBinds {
			if v.Value.Exception == "endOfMibView" {
				// 结束标记可重复最后一个有效 OID，只终止遍历，不能覆盖其值。
				_, seen := raw.SNMP[v.OID]
				if !seen && (v.OID == c.plan.BulkRoot || strings.HasPrefix(v.OID, c.plan.BulkRoot+".")) {
					addValue(raw, v)
				}
				return nil
			}
			if ber.CompareOID(v.OID, cursor) <= 0 {
				return errors.New("SNMP walk OID 未单调递增")
			}
			if !strings.HasPrefix(v.OID, c.plan.BulkRoot+".") {
				return nil
			}
			addValue(raw, v)
			cursor = v.OID
		}
	}
	return errors.New("SNMP walk 达到 20 轮安全上限")
}

// Poll 保留部分采集值及块错误；GETBULK 失败在本轮回退 GET 并记录无 OID 的 fallback 诊断。
func (c *Client) Poll(ctx context.Context) (driver.RawData, error) {
	raw := driver.RawData{Protocol: driver.ProtocolSNMP, At: time.Now(), SNMP: map[string]driver.SNMPValue{}}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.lifetime, cancel)
	defer func() { stop(); cancel() }()
	select {
	case c.serial <- struct{}{}:
		defer func() { <-c.serial }()
	case <-ctx.Done():
		return raw, ctx.Err()
	}
	if ctx.Err() != nil {
		return raw, ctx.Err()
	}
	var e error
	switch c.plan.Strategy {
	case "get":
		e = c.get(ctx, &raw)
	default:
		pdu := ber.GetNextRequest
		if c.plan.Strategy == "getbulk" && c.version == 1 {
			pdu = ber.GetBulkRequest
		}
		e = c.walk(ctx, &raw, pdu)
		if e != nil {
			if pdu == ber.GetBulkRequest && ctx.Err() == nil {
				raw.Errors = append(raw.Errors, driver.BlockError{Kind: "fallback", Message: e.Error()})
				e = c.get(ctx, &raw)
			} else {
				raw.Errors = append(raw.Errors, driver.BlockError{Kind: "read_error", Message: e.Error()})
			}
		}
	}
	return raw, e
}

// Probe 仅使用本轮已读的 UPS-MIB 身份值，不扫描或追加任何读取。
func (c *Client) Probe(ctx context.Context) (driver.DeviceInfo, driver.RawData, error) {
	raw, e := c.Poll(ctx)
	text := func(o string) string {
		v := raw.SNMP["1.3.6.1.2.1.33.1.1."+o+".0"]
		if v.Exception != "" {
			return ""
		}
		return v.Text
	}
	return driver.DeviceInfo{Manufacturer: text("1"), Model: text("2"), Firmware: text("3"), AgentVersion: text("4"), Identity: text("5")}, raw, e
}

// Close 幂等、终态；直接关闭连接以中断阻塞中的请求。
func (c *Client) Close() error {
	c.stop()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.conn != nil {
		e := c.conn.Close()
		c.conn = nil
		if e != nil {
			return errors.New("关闭 SNMP 连接失败")
		}
	}
	return nil
}

func (c *Client) logCloseFailure() {
	logger := c.cfg.Connection.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Error("关闭 SNMP 连接失败")
}
