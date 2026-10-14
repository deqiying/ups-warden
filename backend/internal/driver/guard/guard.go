// Package guard 是驱动唯一网络出口；每次发送前验证完整的只读请求。
package guard

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"ups-warden/internal/driver"
)

// ErrWriteBlocked 表示请求未通过字节白名单，没有任何字节被发送。
var ErrWriteBlocked = errors.New("非完整只读请求，发送已阻止")

// DialFunc 用于注入本机模拟连接；生产默认使用 net.Dialer。
type DialFunc func(context.Context, string, string) (net.Conn, error)

// Config 是连接设置。Address 必须明确为 host:port，不进行扫描或协议发现。
type Config struct {
	Address     string
	Timeout     time.Duration
	DialContext DialFunc
	Logger      *slog.Logger
}

// Validate 校验目标与超时，返回独立配置；零超时使用 2 秒。
func (c Config) Validate() (Config, error) {
	host, port, err := net.SplitHostPort(c.Address)
	n, portErr := strconv.Atoi(port)
	if err != nil || host == "" || portErr != nil || n < 1 || n > 65535 {
		return c, errors.New("驱动地址必须为明确的 host:port")
	}
	if c.Timeout == 0 {
		c.Timeout = 2 * time.Second
	}
	if c.Timeout < 0 || c.Timeout > time.Minute {
		return c, errors.New("请求超时必须大于 0 且不超过 1 分钟")
	}
	return c, nil
}

// Dial 根据协议选择 UDP/TCP，返回无法绕过发送校验的连接。
func Dial(ctx context.Context, cfg Config, protocol string) (net.Conn, error) {
	cfg, err := cfg.Validate()
	if err != nil {
		return nil, err
	}
	network := "tcp"
	switch protocol {
	case driver.ProtocolSNMP:
		network = "udp"
	case driver.ProtocolModbus, driver.ProtocolAYI9:
	default:
		return nil, ErrWriteBlocked
	}
	dial := cfg.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: cfg.Timeout}).DialContext
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	conn, err := dial(ctx, network, cfg.Address)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("驱动连接失败")
	}
	if conn == nil {
		return nil, errors.New("驱动连接器返回空连接")
	}
	return Wrap(conn, protocol, cfg.Logger), nil
}

// Wrap 用于受控连接和测试；只读检查不可由调用方关闭。
// 每次 Write 必须是完整单帧，短写会关闭连接，不允许尾部重试成为另一条指令。
func Wrap(conn net.Conn, protocol string, logger *slog.Logger) net.Conn {
	if logger == nil {
		logger = slog.Default()
	}
	return &guarded{conn: conn, protocol: protocol, logger: logger}
}

type guarded struct {
	conn     net.Conn
	protocol string
	logger   *slog.Logger
	mu       sync.Mutex
	broken   bool
}

func (c *guarded) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.broken {
		return 0, net.ErrClosed
	}
	if err := Validate(c.protocol, p); err != nil {
		c.logger.Error("驱动只读护栏阻止发送", "protocol", c.protocol)
		return 0, ErrWriteBlocked
	}
	n, err := c.conn.Write(p)
	if err != nil || n != len(p) {
		c.broken = true
		closeErr := c.conn.Close()
		if err == nil {
			err = io.ErrShortWrite
		}
		return n, errors.Join(err, closeErr)
	}
	return n, nil
}
func (c *guarded) Read(p []byte) (int, error)         { return c.conn.Read(p) }
func (c *guarded) Close() error                       { return c.conn.Close() }
func (c *guarded) LocalAddr() net.Addr                { return c.conn.LocalAddr() }
func (c *guarded) RemoteAddr() net.Addr               { return c.conn.RemoteAddr() }
func (c *guarded) SetDeadline(t time.Time) error      { return c.conn.SetDeadline(t) }
func (c *guarded) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *guarded) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }

// Begin 设置单次请求截止时间；取消立即打断阻塞 I/O。调用者须串行使用连接。
// cleanup 等待取消回调完成，防止旧请求的截止时间污染下一请求。
func Begin(ctx context.Context, conn net.Conn, timeout time.Duration) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, errors.New("设置驱动请求截止时间失败")
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		if err := conn.SetDeadline(time.Now()); err != nil {
			// 回调无法返回错误；截止时间设置失败时关闭连接并记录脱敏诊断。
			slog.Error("取消驱动请求时设置截止时间失败")
			if closeErr := conn.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
				slog.Error("取消驱动请求时关闭连接失败")
			}
		}
	})
	return func() {
		if !stop() {
			<-done
		}
	}, nil
}
