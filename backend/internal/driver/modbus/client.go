// Package modbus 实现 FC03 / FC04，只按读计划采集，不包含型号知识。
package modbus

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"sync"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/driver/guard"
)

// Config 声明目标和超时；Unit ID 和块列表来自 ReadPlan。
type Config struct{ Connection guard.Config }

// Client 串行复用 TCP 连接。Close 为终态且会取消正在进行的请求。
type Client struct {
	cfg    guard.Config
	plan   driver.ModbusPlan
	mu     sync.Mutex
	serial chan struct{}
	conn   net.Conn
	tx     uint16
	life   context.Context
	cancel context.CancelFunc
}

// New 校验并复制读计划，不发出网络请求。
func New(cfg Config, plan driver.ModbusPlan) (*Client, error) {
	connection, err := cfg.Connection.Validate()
	if err != nil {
		return nil, err
	}
	if len(plan.Blocks) == 0 || len(plan.Blocks) > 256 {
		return nil, errors.New("Modbus 读计划须包含 1～256 个块")
	}
	for _, b := range plan.Blocks {
		if _, err := EncodeRead(1, plan.UnitID, b); err != nil {
			return nil, err
		}
	}
	plan.Blocks = slices.Clone(plan.Blocks)
	life, cancel := context.WithCancel(context.Background())
	return &Client{cfg: connection, plan: plan, life: life, cancel: cancel, serial: make(chan struct{}, 1)}, nil
}

// EncodeRead 生成单个完整 MBAP + 只读 PDU；拒绝类型强制转换出的写功能码。
func EncodeRead(tx uint16, unit uint8, b driver.ReadBlock) ([]byte, error) {
	if (b.FC != driver.FC03 && b.FC != driver.FC04) || b.Count == 0 || b.Count > 125 || int(b.Start)+int(b.Count) > 65536 {
		return nil, errors.New("Modbus 非法只读块")
	}
	p := make([]byte, 12)
	binary.BigEndian.PutUint16(p, tx)
	binary.BigEndian.PutUint16(p[4:], 6)
	p[6], p[7] = unit, byte(b.FC)
	binary.BigEndian.PutUint16(p[8:], b.Start)
	binary.BigEndian.PutUint16(p[10:], b.Count)
	return p, nil
}

// Probe 读取一次计划；Modbus 不猜测设备身份，Profile 由用户选择。
func (c *Client) Probe(ctx context.Context) (driver.DeviceInfo, driver.RawData, error) {
	raw, err := c.Poll(ctx)
	return driver.DeviceInfo{}, raw, err
}

// Poll 返回已成功读取的块及失败块；协议异常不阻断其余块，传输错误留待下一轮重连。
func (c *Client) Poll(ctx context.Context) (raw driver.RawData, err error) {
	raw = driver.RawData{Protocol: driver.ProtocolModbus, Modbus: map[driver.RegKey]uint16{}}
	defer func() { raw.At = time.Now().UTC() }()
	// 排队也受本次 context 限制，不能阻塞在其他 Poll 持有的状态锁上。
	select {
	case c.serial <- struct{}{}:
		defer func() { <-c.serial }()
	case <-ctx.Done():
		return raw, ctx.Err()
	case <-c.life.Done():
		return raw, net.ErrClosed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.life.Err() != nil {
		return raw, net.ErrClosed
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(c.life, cancel)
	defer stop()
	if err = ctx.Err(); err != nil {
		return raw, err
	}
	if c.conn == nil {
		c.conn, err = guard.Dial(ctx, c.cfg, driver.ProtocolModbus)
		if err != nil {
			return raw, err
		}
	}
	for i, b := range c.plan.Blocks {
		values, exception, readErr := c.read(ctx, b)
		if readErr != nil {
			for _, pending := range c.plan.Blocks[i:] {
				raw.Errors = append(raw.Errors, driver.BlockError{FC: pending.FC, Start: pending.Start, Count: pending.Count, Kind: "transport", Message: "Modbus 请求失败或本轮未读取"})
			}
			closeErr := c.disconnect()
			return raw, errors.Join(readErr, closeErr)
		}
		if exception != 0 {
			raw.Errors = append(raw.Errors, driver.BlockError{FC: b.FC, Start: b.Start, Count: b.Count, Kind: "unsupported", Message: fmt.Sprintf("Modbus 异常码 0x%02x", exception)})
			continue
		}
		for j, v := range values {
			raw.Modbus[driver.RegKey{FC: b.FC, Addr: b.Start + uint16(j)}] = v
		}
	}
	return raw, nil
}
func (c *Client) read(ctx context.Context, b driver.ReadBlock) ([]uint16, byte, error) {
	done, err := guard.Begin(ctx, c.conn, c.cfg.Timeout)
	if err != nil {
		return nil, 0, err
	}
	defer done()
	c.tx++
	packet, err := EncodeRead(c.tx, c.plan.UnitID, b)
	if err != nil {
		return nil, 0, err
	}
	if _, err = c.conn.Write(packet); err != nil {
		return nil, 0, requestError(ctx, err)
	}
	var header [7]byte
	if _, err = io.ReadFull(c.conn, header[:]); err != nil {
		return nil, 0, requestError(ctx, err)
	}
	length := int(binary.BigEndian.Uint16(header[4:6]))
	if binary.BigEndian.Uint16(header[:2]) != c.tx || binary.BigEndian.Uint16(header[2:4]) != 0 || header[6] != c.plan.UnitID || length < 3 || length > 254 {
		return nil, 0, errors.New("Modbus 响应头不匹配或长度非法")
	}
	body := make([]byte, length-1)
	if _, err = io.ReadFull(c.conn, body); err != nil {
		return nil, 0, requestError(ctx, err)
	}
	if body[0] == byte(b.FC)|0x80 {
		if len(body) != 2 || body[1] == 0 {
			return nil, 0, errors.New("Modbus 异常响应格式非法")
		}
		return nil, body[1], nil
	}
	if body[0] != byte(b.FC) || int(body[1]) != int(b.Count)*2 || len(body) != 2+int(b.Count)*2 {
		return nil, 0, errors.New("Modbus 响应功能码或数据长度不匹配")
	}
	values := make([]uint16, b.Count)
	for i := range values {
		values[i] = binary.BigEndian.Uint16(body[2+i*2:])
	}
	return values, 0, nil
}
func requestError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, guard.ErrWriteBlocked) {
		return guard.ErrWriteBlocked
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return fmt.Errorf("Modbus 请求超时: %w", context.DeadlineExceeded)
	}
	return errors.New("Modbus 请求传输失败")
}
func (c *Client) disconnect() error {
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return errors.New("关闭 Modbus 连接失败")
	}
	return nil
}

// Close 幂等关闭连接，不再允许 Poll 或 Probe。
func (c *Client) Close() error { c.cancel(); c.mu.Lock(); defer c.mu.Unlock(); return c.disconnect() }
