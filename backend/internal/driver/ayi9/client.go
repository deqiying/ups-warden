// Package ayi9 仅实现 AYI9 网卡的 QAD / RTD 查询，不提供控制命令。
package ayi9

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"ups-warden/internal/driver"
	"ups-warden/internal/driver/guard"
	"ups-warden/internal/secret"
)

// Command 是只读指令；编码时仍检查白名单，不能依靠 Go 类型限制强制转换。
type Command string

const (
	QAD Command = "QAD"
	RTD Command = "RTD"
)

// ErrPasswordWrong 可通过 errors.Is 判断，不包含密码或设备响应。
var ErrPasswordWrong = errors.New("密码错误（103）")

// DeviceError 表示设备拒绝查询；Code=0 表示无具体码的 FAILED。
type DeviceError struct{ Code int }

func (e *DeviceError) Error() string {
	if e.Code == 103 {
		return ErrPasswordWrong.Error()
	}
	return fmt.Sprintf("AYI9 查询被拒绝（错误码 %d）", e.Code)
}
func (e *DeviceError) Is(target error) bool { return e.Code == 103 && target == ErrPasswordWrong }

// Config 使用掩码类型保存凭据，Password 最多 15 字节。
type Config struct {
	Connection guard.Config
	Password   secret.String
}

// Client 以单连接串行查询并缓存 RTD；关闭后不能重新使用。
type Client struct {
	cfg     Config
	plan    driver.AYI9Plan
	mu      sync.Mutex
	serial  chan struct{}
	conn    net.Conn
	reader  *io.LimitedReader
	decoder *json.Decoder
	session uint64
	rated   driver.AYI9Raw
	life    context.Context
	cancel  context.CancelFunc
}

// New 校验只读周期与凭据，不联网；周期零值使用 10 分钟。
func New(cfg Config, plan driver.AYI9Plan) (*Client, error) {
	var err error
	cfg.Connection, err = cfg.Connection.Validate()
	if err != nil {
		return nil, err
	}
	if len(cfg.Password.Reveal()) > 15 || !utf8.ValidString(cfg.Password.Reveal()) {
		return nil, errors.New("AYI9 密码须为不超过 15 字节的 UTF-8 文本")
	}
	if plan.RTDInterval == 0 {
		plan.RTDInterval = 10 * time.Minute
	}
	if plan.RTDInterval < time.Second || plan.RTDInterval > 365*24*time.Hour {
		return nil, errors.New("RTD 周期须为 1 秒至 365 天")
	}
	life, cancel := context.WithCancel(context.Background())
	return &Client{cfg: cfg, plan: plan, life: life, cancel: cancel, serial: make(chan struct{}, 1)}, nil
}

// EncodeRequest 只构造完整的白名单 JSON；错误不包含凭据。
func EncodeRequest(cmd Command, session uint64, password secret.String) ([]byte, error) {
	if cmd != QAD && cmd != RTD || len(password.Reveal()) > 15 || !utf8.ValidString(password.Reveal()) {
		return nil, guard.ErrWriteBlocked
	}
	request := struct {
		Cmd     Command `json:"cmd"`
		Session string  `json:"session"`
		Pass    string  `json:"pass,omitempty"`
	}{cmd, strconv.FormatUint(session, 10), password.Reveal()}
	return json.Marshal(request)
}

// Probe 读取一次 QAD/RTD 并从 RTD 获取身份；不发送额外发现命令。
func (c *Client) Probe(ctx context.Context) (driver.DeviceInfo, driver.RawData, error) {
	raw, err := c.Poll(ctx)
	info := driver.DeviceInfo{}
	if raw.AYI9 != nil {
		info.Identity = raw.AYI9.UID
		if len(raw.AYI9.RTD) == 10 {
			info.Manufacturer = raw.AYI9.RTD[0]
			info.Firmware = raw.AYI9.RTD[1]
			info.Model = raw.AYI9.RTD[2]
		}
	}
	return info, raw, err
}

// Poll 返回独立原始快照；RTD 刷新失败不会把旧额定信息标成新观测。
func (c *Client) Poll(ctx context.Context) (raw driver.RawData, err error) {
	raw = driver.RawData{Protocol: driver.ProtocolAYI9, AYI9: &driver.AYI9Raw{}}
	defer func() { raw.At = time.Now().UTC() }()
	// 令牌等待可取消，状态锁只在取得串行资格后持有。
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
		c.conn, err = guard.Dial(ctx, c.cfg.Connection, driver.ProtocolAYI9)
		if err != nil {
			return raw, err
		}
		c.reader = &io.LimitedReader{R: c.conn}
		c.decoder = json.NewDecoder(c.reader)
		c.rated = driver.AYI9Raw{}
	}
	qad, err := c.exchange(ctx, QAD)
	if err != nil {
		raw.Errors = append(raw.Errors, driver.BlockError{Command: string(QAD), Kind: "query", Message: "QAD 查询失败"})
		return raw, errors.Join(err, c.disconnect())
	}
	raw.AYI9.UID, raw.AYI9.QADText = qad.uid, qad.value
	raw.AYI9.QAD, err = split(qad.value, 23)
	if err != nil {
		raw.Errors = append(raw.Errors, driver.BlockError{Command: string(QAD), Kind: "decode", Message: "QAD 段数错误"})
		return raw, err
	}
	if c.rated.RTDAt.IsZero() || time.Since(c.rated.RTDAt) >= c.plan.RTDInterval {
		rtd, readErr := c.exchange(ctx, RTD)
		if readErr != nil {
			raw.Errors = append(raw.Errors, driver.BlockError{Command: string(RTD), Kind: "query", Message: "RTD 查询失败"})
			return raw, errors.Join(readErr, c.disconnect())
		}
		raw.AYI9.RTDText = rtd.value
		raw.AYI9.RTD, err = split(rtd.value, 10)
		if err != nil {
			raw.Errors = append(raw.Errors, driver.BlockError{Command: string(RTD), Kind: "decode", Message: "RTD 段数错误"})
			return raw, err
		}
		if rtd.uid != "" && qad.uid != "" && rtd.uid != qad.uid {
			markIdentityFailure(&raw)
			return raw, errors.Join(errors.New("AYI9 查询身份不一致"), c.disconnect())
		}
		raw.AYI9.RTDAt = time.Now().UTC()
		c.rated = driver.AYI9Raw{UID: rtd.uid, RTDText: rtd.value, RTD: slices.Clone(raw.AYI9.RTD), RTDAt: raw.AYI9.RTDAt}
	} else {
		if c.rated.UID != "" && qad.uid != "" && c.rated.UID != qad.uid {
			markIdentityFailure(&raw)
			return raw, errors.Join(errors.New("AYI9 设备身份发生变化，需重连"), c.disconnect())
		}
		raw.AYI9.RTDText = c.rated.RTDText
		raw.AYI9.RTD = slices.Clone(c.rated.RTD)
		raw.AYI9.RTDAt = c.rated.RTDAt
	}
	return raw, nil
}
func split(value string, count int) ([]string, error) {
	text := strings.TrimSpace(value)
	if !strings.HasPrefix(text, "(") {
		return nil, errors.New("AYI9 数据缺少起始标记")
	}
	parts := strings.Fields(strings.TrimPrefix(text, "("))
	if len(parts) != count {
		return parts, errors.New("AYI9 数据段数不符")
	}
	return parts, nil
}
func (c *Client) exchange(ctx context.Context, cmd Command) (response, error) {
	done, err := guard.Begin(ctx, c.conn, c.cfg.Connection.Timeout)
	if err != nil {
		return response{}, err
	}
	defer done()
	c.session++
	request, err := EncodeRequest(cmd, c.session, c.cfg.Password)
	if err != nil {
		return response{}, err
	}
	if _, err = c.conn.Write(request); err != nil {
		return response{}, transportError(ctx, err)
	}
	// 每次交换共享同一解码器，保留其预读字节；限制所有不匹配响应的累计大小与数量。
	c.reader.N = 64 * 1024
	start := c.decoder.InputOffset()
	for i := 0; i < 16; i++ {
		var packet json.RawMessage
		if err := c.decoder.Decode(&packet); err != nil {
			return response{}, transportError(ctx, err)
		}
		if c.decoder.InputOffset()-start > 64*1024 {
			return response{}, errors.New("AYI9 响应超出长度限制")
		}
		r, err := parseResponse(packet)
		if err != nil {
			return response{}, err
		}
		if r.session != strconv.FormatUint(c.session, 10) {
			continue
		}
		if r.cmd != "" && r.cmd != string(cmd) {
			return response{}, errors.New("AYI9 响应命令不匹配")
		}
		if r.code != 0 || r.value == "FAILED" {
			return response{}, &DeviceError{Code: r.code}
		}
		return r, nil
	}
	return response{}, errors.New("AYI9 响应 session 持续不匹配")
}
func transportError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, guard.ErrWriteBlocked) {
		return guard.ErrWriteBlocked
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return fmt.Errorf("AYI9 请求超时: %w", context.DeadlineExceeded)
	}
	return errors.New("AYI9 响应读取失败或格式非法")
}
func (c *Client) disconnect() error {
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	c.decoder = nil
	c.reader = nil
	c.rated = driver.AYI9Raw{}
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return errors.New("关闭 AYI9 连接失败")
	}
	return nil
}

// Close 幂等取消请求并释放连接。
func (c *Client) Close() error { c.cancel(); c.mu.Lock(); defer c.mu.Unlock(); return c.disconnect() }

func markIdentityFailure(raw *driver.RawData) {
	// 即使上层选择保留部分采集，也不能消费跨设备拼接的 QAD / RTD。
	for _, cmd := range []Command{QAD, RTD} {
		raw.Errors = append(raw.Errors, driver.BlockError{Command: string(cmd), Kind: "identity", Message: "设备身份不一致"})
	}
}
