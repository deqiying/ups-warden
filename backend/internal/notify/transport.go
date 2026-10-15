package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	maxTransportBody = 64 << 10
	requestTimeout   = 10 * time.Second
	timestampHeader  = "X-UPS-Warden-Timestamp"
	signatureHeader  = "X-UPS-Warden-Signature"
)

// Config 是可明文保存的渠道配置，不包含 URL、Header 或其他凭据。
type Config struct {
	Method       string   `json:"method,omitempty"`
	BodyTemplate string   `json:"body_template,omitempty"`
	APIBase      string   `json:"api_base,omitempty"`
	ChatIDs      []string `json:"chat_ids,omitempty"`
	Format       string   `json:"format,omitempty"`
}

// Credentials 必须整体加密保存；API 只能公开各字段的 *_set 状态。
type Credentials struct {
	URL        string            `json:"url,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	SigningKey string            `json:"signing_key,omitempty"`
	Token      string            `json:"token,omitempty"`
	ProxyURL   string            `json:"proxy_url,omitempty"`
}

// Message 的 Body 已由上层按渠道格式渲染，传输层不再套用模板。
type Message struct {
	Severity string
	Title    string
	Body     string
	Vars     map[string]any
}

// Channel 仅执行单目标单次发送；重试、排队与日志由服务层负责。
type Channel interface {
	Send(context.Context, Message) error
	Close()
}

// Delivery 将单个可独立记录、重试的目标与发送渠道关联。
type Delivery struct {
	Target  string
	Channel Channel
}

// NormalizeConfig 仅补充当前渠道的默认值，不清除需要校验的跨类型字段。
func NormalizeConfig(kind string, cfg *Config) {
	if cfg == nil {
		return
	}
	switch kind {
	case "webhook":
		if cfg.Method == "" {
			cfg.Method = http.MethodPost
		}
		if cfg.Format == "" {
			cfg.Format = "json"
		}
	case "telegram":
		if cfg.APIBase == "" {
			cfg.APIBase = "https://api.telegram.org"
		}
		if cfg.Format == "" {
			cfg.Format = "text"
		}
	}
}

// ValidateConfig 不访问网络，且所有错误均为固定文本，不引用配置内容。
func ValidateConfig(kind string, cfg Config, cred Credentials) error {
	NormalizeConfig(kind, &cfg)
	if len(cfg.BodyTemplate) > maxTransportBody {
		return errors.New("notification template too large")
	}
	switch kind {
	case "webhook":
		if cfg.APIBase != "" || len(cfg.ChatIDs) != 0 || cred.Token != "" || cred.ProxyURL != "" {
			return errors.New("unexpected webhook fields")
		}
		if cfg.Method != http.MethodPost && cfg.Method != http.MethodPut {
			return errors.New("invalid webhook method")
		}
		if cfg.Format != "json" {
			return errors.New("invalid webhook format")
		}
		if !validTransportURL(cred.URL, "webhook") {
			return errors.New("invalid webhook URL")
		}
		if len(cred.SigningKey) > 4096 {
			return errors.New("signing key too large")
		}
		if !validHeaders(cred.Headers) {
			return errors.New("invalid webhook headers")
		}
	case "telegram":
		if cfg.Method != "" || cfg.BodyTemplate != "" || cred.URL != "" || len(cred.Headers) != 0 || cred.SigningKey != "" {
			return errors.New("unexpected telegram fields")
		}
		if cfg.Format != "text" && cfg.Format != "html" {
			return errors.New("invalid telegram format")
		}
		if !validTransportURL(cfg.APIBase, "base") {
			return errors.New("invalid telegram API base")
		}
		if cred.ProxyURL != "" && !validTransportURL(cred.ProxyURL, "proxy") {
			return errors.New("invalid telegram proxy")
		}
		if len(cred.Token) == 0 || len(cred.Token) > 1024 || !allTokenChars(cred.Token) {
			return errors.New("invalid telegram token")
		}
		if len(cfg.ChatIDs) == 0 || len(cfg.ChatIDs) > 32 {
			return errors.New("invalid telegram chat IDs")
		}
		seen := make(map[string]bool, len(cfg.ChatIDs))
		for _, chat := range cfg.ChatIDs {
			// Telegram 用户名不区分大小写，重复目标必须在入队前拒绝。
			key := strings.ToLower(chat)
			if !validChatID(chat) || seen[key] {
				return errors.New("invalid telegram chat IDs")
			}
			seen[key] = true
		}
	default:
		return errors.New("unsupported notification kind")
	}
	return nil
}

func validTransportURL(raw, role string) bool {
	if raw == "" || len(raw) > 8192 || strings.TrimSpace(raw) != raw {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" || u.Opaque != "" || u.Fragment != "" || strings.Contains(raw, "#") {
		return false
	}
	if role != "proxy" && u.User != nil {
		return false
	}
	if role != "webhook" && (u.RawQuery != "" || u.ForceQuery) {
		return false
	}
	if role == "proxy" && u.Path != "" && u.Path != "/" {
		return false
	}
	return true
}

func validHeaders(headers map[string]string) bool {
	if len(headers) > 64 {
		return false
	}
	total := 0
	seen := make(map[string]bool, len(headers))
	for name, value := range headers {
		total += len(name) + len(value)
		if len(name) == 0 || len(name) > 256 || len(value) > 8192 || total > maxTransportBody {
			return false
		}
		for i := range len(name) {
			c := name[i]
			if !asciiAlphaNum(c) && !strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)) {
				return false
			}
		}
		for i := range len(value) {
			if value[i] < 32 && value[i] != '\t' || value[i] == 127 {
				return false
			}
		}
		key := strings.ToLower(name)
		if seen[key] {
			return false
		}
		seen[key] = true
		switch key {
		case "host", "content-length", "transfer-encoding", "x-ups-warden-timestamp", "x-ups-warden-signature":
			return false
		}
	}
	return true
}

func asciiAlphaNum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func allTokenChars(token string) bool {
	for i := range len(token) {
		if !asciiAlphaNum(token[i]) && token[i] != ':' && token[i] != '_' && token[i] != '-' {
			return false
		}
	}
	return true
}

func validChatID(chat string) bool {
	if strings.HasPrefix(chat, "@") {
		if len(chat) < 6 || len(chat) > 33 || !asciiAlphaNum(chat[1]) || chat[1] >= '0' && chat[1] <= '9' {
			return false
		}
		for i := 2; i < len(chat); i++ {
			if !asciiAlphaNum(chat[i]) && chat[i] != '_' {
				return false
			}
		}
		return true
	}
	if chat == "" || len(chat) > 20 {
		return false
	}
	digits := strings.TrimPrefix(chat, "-")
	if digits == "" || len(digits) > 1 && digits[0] == '0' {
		return false
	}
	for i := range len(digits) {
		if digits[i] < '0' || digits[i] > '9' {
			return false
		}
	}
	id, err := strconv.ParseInt(chat, 10, 64)
	return err == nil && id != 0
}

// 内置注册表隔离发送服务与渠道构造；新增渠道不改变队列或模板调用。
var channelFactories = map[string]func(Config, Credentials) []Delivery{
	"webhook": func(cfg Config, cred Credentials) []Delivery {
		return newHTTPDeliveries("webhook", []string{"webhook"}, cfg, cred)
	},
	"telegram": func(cfg Config, cred Credentials) []Delivery {
		return newHTTPDeliveries("telegram", cfg.ChatIDs, cfg, cred)
	},
}

// NewDeliveries 校验后创建独立 HTTP 客户端；Telegram 每个 chat 只对应一次发送。
func NewDeliveries(kind string, cfg Config, cred Credentials) ([]Delivery, error) {
	if err := ValidateConfig(kind, cfg, cred); err != nil {
		return nil, err
	}
	NormalizeConfig(kind, &cfg)
	return channelFactories[kind](cfg, cred), nil
}

func newHTTPDeliveries(kind string, targets []string, cfg Config, cred Credentials) []Delivery {
	deliveries := make([]Delivery, 0, len(targets))
	for _, target := range targets {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		// 禁止隐式使用 HTTP_PROXY 等进程环境中的凭据或外部代理。
		transport.Proxy = nil
		if cred.ProxyURL != "" {
			proxy, _ := url.Parse(cred.ProxyURL) // 已由 ValidateConfig 校验。
			transport.Proxy = http.ProxyURL(proxy)
		}
		headers := make(http.Header, len(cred.Headers))
		for name, value := range cred.Headers {
			headers.Set(name, value)
		}
		channel := &httpChannel{
			kind: kind, cfg: cfg, endpoint: cred.URL, signingKey: cred.SigningKey,
			headers: headers, target: target,
			client: &http.Client{
				Transport: transport, Timeout: requestTimeout,
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			},
		}
		if kind == "telegram" {
			channel.endpoint = strings.TrimRight(cfg.APIBase, "/") + "/bot" + cred.Token + "/sendMessage"
		}
		deliveries = append(deliveries, Delivery{Target: target, Channel: channel})
	}
	return deliveries
}

type httpChannel struct {
	kind       string
	cfg        Config
	endpoint   string
	signingKey string
	headers    http.Header
	target     string
	client     *http.Client
}

func (c *httpChannel) Close() { c.client.CloseIdleConnections() }

func (c *httpChannel) Send(ctx context.Context, msg Message) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	body, method := msg.Body, c.cfg.Method
	if c.kind == "telegram" {
		payload := struct {
			ChatID    string `json:"chat_id"`
			Text      string `json:"text"`
			ParseMode string `json:"parse_mode,omitempty"`
		}{ChatID: c.target, Text: msg.Body}
		if c.cfg.Format == "html" {
			payload.ParseMode = "HTML"
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return &deliveryError{category: "request encoding failed"}
		}
		body, method = string(encoded), http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint, strings.NewReader(body))
	if err != nil {
		return &deliveryError{category: "invalid request"}
	}
	req.Header.Set("Content-Type", "application/json")
	if c.kind == "webhook" {
		for name, values := range c.headers {
			req.Header[name] = append([]string(nil), values...)
		}
		if c.signingKey != "" {
			timestamp := strconv.FormatInt(time.Now().Unix(), 10)
			mac := hmac.New(sha256.New, []byte(c.signingKey))
			_, _ = mac.Write([]byte(timestamp + "." + body))
			req.Header.Set(timestampHeader, timestamp)
			req.Header.Set(signatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
		}
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return safeTransportError(ctx, err)
	}
	defer resp.Body.Close()
	// Webhook 的成功契约只看状态码；不因无关响应体失败而重复外发。
	if c.kind == "webhook" {
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		return &deliveryError{category: "HTTP rejected", status: resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTransportBody+1))
	if err != nil {
		return safeTransportError(ctx, err)
	}
	if len(data) > maxTransportBody {
		return &deliveryError{category: "response too large"}
	}
	var result struct {
		OK         *bool `json:"ok"`
		Parameters struct {
			RetryAfter int64 `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.OK == nil {
		return &deliveryError{category: "invalid telegram response", status: resp.StatusCode}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && *result.OK {
		return nil
	}
	failure := &deliveryError{category: "telegram rejected", status: resp.StatusCode}
	if resp.StatusCode == http.StatusTooManyRequests && result.Parameters.RetryAfter > 0 {
		seconds := result.Parameters.RetryAfter
		const maxSeconds = int64((1<<63 - 1) / time.Second)
		if seconds > maxSeconds {
			seconds = maxSeconds
		}
		failure.retryAfter = time.Duration(seconds) * time.Second
	}
	return failure
}

// deliveryError 故意不保留原始错误或 Unwrap 链，避免 URL、Token 和代理密码泄漏。
type deliveryError struct {
	category   string
	status     int
	retryAfter time.Duration
}

func (e *deliveryError) Error() string {
	if e.status != 0 {
		return fmt.Sprintf("notification %s (HTTP %d)", e.category, e.status)
	}
	return "notification " + e.category
}

func safeTransportError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return &deliveryError{category: "request canceled"}
	}
	var timeout net.Error
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		return &deliveryError{category: "request timed out"}
	}
	return &deliveryError{category: "transport failed"}
}

// RetryDelay 仅返回 HTTP 429 响应中的 Telegram retry_after，其余错误返回零。
func RetryDelay(err error) time.Duration {
	var failure *deliveryError
	if errors.As(err, &failure) && failure.status == http.StatusTooManyRequests {
		return failure.retryAfter
	}
	return 0
}
