package notify

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"html"
	"io"
	"strings"

	"ups-warden/internal/secret"
	"ups-warden/internal/store"
)

// ChannelRecord 是已解密的内部配置；Credentials 不得直接写入响应或日志。
type ChannelRecord struct {
	ID          int64       `json:"id"`
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	Config      Config      `json:"config"`
	Enabled     bool        `json:"enabled"`
	Credentials Credentials `json:"-"`
}

// View 只暴露凭据是否设置；Webhook URL 和代理地址可能包含凭据，整体视为秘密。
func (c ChannelRecord) View() map[string]any {
	return map[string]any{"id": c.ID, "name": c.Name, "type": c.Type, "config": c.Config, "enabled": c.Enabled,
		"url_set": c.Credentials.URL != "", "headers_set": len(c.Credentials.Headers) > 0,
		"signing_key_set": c.Credentials.SigningKey != "", "token_set": c.Credentials.Token != "", "proxy_url_set": c.Credentials.ProxyURL != ""}
}

// ChannelColumns 固定查询字段顺序，不接受用户指定 SQL 标识符。
const ChannelColumns = "id,name,type,config,enabled,secret"

// ScanChannel 在持久化信任边界解密、严格解析并校验配置，不发起网络请求。
func ScanChannel(row interface{ Scan(...any) error }, cipher *secret.Cipher) (ChannelRecord, error) {
	var c ChannelRecord
	var cfg string
	var encrypted []byte
	if err := row.Scan(&c.ID, &c.Name, &c.Type, &cfg, &c.Enabled, &encrypted); err != nil {
		return c, err
	}
	if err := strictStoredJSON([]byte(cfg), &c.Config); err != nil {
		return c, errors.New("notify: 渠道配置解码失败")
	}
	if len(encrypted) > 0 {
		plain, err := cipher.Decrypt(encrypted, "channels.secret")
		if err != nil {
			return c, errors.New("notify: 渠道凭据解密失败")
		}
		defer clear(plain)
		if err := strictStoredJSON(plain, &c.Credentials); err != nil {
			return c, errors.New("notify: 渠道凭据解码失败")
		}
	}
	NormalizeConfig(c.Type, &c.Config)
	if err := ValidateConfig(c.Type, c.Config, c.Credentials); err != nil {
		return c, err
	}
	return c, nil
}
func strictStoredJSON(data []byte, out any) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("null")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

// ReadChannel 读取单个渠道；不存在时返回 sql.ErrNoRows。
func ReadChannel(ctx context.Context, db *store.Store, cipher *secret.Cipher, id int64) (ChannelRecord, error) {
	return ScanChannel(db.Read().QueryRowContext(ctx, "SELECT "+ChannelColumns+" FROM channels WHERE id=?", id), cipher)
}

// EncodeChannel 在配置事务前编码并加密；明文凭据不进入 channels.config。
func EncodeChannel(c ChannelRecord, cipher *secret.Cipher) (string, []byte, error) {
	cfg, err := json.Marshal(c.Config)
	if err != nil {
		return "", nil, err
	}
	plain, err := json.Marshal(c.Credentials)
	if err != nil {
		return "", nil, err
	}
	defer clear(plain)
	encrypted, err := cipher.Encrypt(plain, "channels.secret")
	return string(cfg), encrypted, err
}

// LoadTemplate 未覆盖时返回内置模板；存储错误不伪装成缺省。
func LoadTemplate(ctx context.Context, db *store.Store, eventType, format string) (string, error) {
	var body string
	err := db.Read().QueryRowContext(ctx, "SELECT body FROM notify_templates WHERE event_type=? AND format=?", eventType, format).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultTemplate(eventType, format)
	}
	return body, err
}

// SafeSummary 在截断前移除配置中的秘密及其常见编码，避免消息正文意外带入凭据。
func SafeSummary(body string, cred Credentials) string {
	values := []string{cred.URL, cred.SigningKey, cred.Token, cred.ProxyURL}
	for _, v := range cred.Headers {
		values = append(values, v)
	}
	for _, v := range values {
		if v == "" {
			continue
		}
		body = strings.ReplaceAll(body, v, "[REDACTED]")
		encoded, _ := json.Marshal(v)
		body = strings.ReplaceAll(body, string(encoded[1:len(encoded)-1]), "[REDACTED]")
		body = strings.ReplaceAll(body, html.EscapeString(v), "[REDACTED]")
	}
	runes := []rune(body)
	if len(runes) > 200 {
		runes = runes[:200]
	}
	return string(runes)
}
