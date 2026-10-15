// Package action 执行持久化动作；只消费统一设备模型，不控制 UPS 数据源。
package action

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"ups-warden/internal/notify"
	"ups-warden/internal/secret"
	"ups-warden/internal/store"
)

// Config 保存非敏感动作参数；HTTP URL、Header 和 Body 全部放入 Credentials。
type Config struct {
	ChannelIDs     []int64  `json:"channel_ids,omitempty"`
	HostID         int64    `json:"host_id,omitempty"`
	Method         string   `json:"method,omitempty"`
	ExpectedStatus []int    `json:"expected_status,omitempty"`
	Script         string   `json:"script,omitempty"`
	Args           []string `json:"args,omitempty"`
	Command        string   `json:"command,omitempty"`
	Shutdown       bool     `json:"shutdown,omitempty"`
}

// Credentials 是加密存储的 HTTP 配置，不得直接写入响应、预览或错误。
type Credentials struct {
	URL          secret.String            `json:"url"`
	Headers      map[string]secret.String `json:"headers"`
	BodyTemplate secret.String            `json:"body_template"`
}

type plainCredentials struct {
	URL          string            `json:"url,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	BodyTemplate string            `json:"body_template,omitempty"`
}

// Record 是解密后的内部配置。
type Record struct {
	ID          int64       `json:"id"`
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	TimeoutS    int         `json:"timeout_s"`
	Config      Config      `json:"config"`
	Credentials Credentials `json:"-"`
}

// View 只暴露敏感字段是否已设置。
func (r Record) View() map[string]any {
	return map[string]any{"id": r.ID, "name": r.Name, "type": r.Type, "timeout_s": r.TimeoutS, "config": r.Config, "url_set": r.Credentials.URL.Reveal() != "", "headers_set": len(r.Credentials.Headers) > 0, "body_template_set": r.Credentials.BodyTemplate.Reveal() != ""}
}

// Columns 固定数据库字段顺序。
const Columns = "id,name,type,timeout_s,config,secret"

// Normalize 为省略的超时和 HTTP 方法赋默认值。
func Normalize(r *Record) {
	if r.TimeoutS == 0 {
		r.TimeoutS = 30
	}
	if r.Type == "http" && r.Config.Method == "" {
		r.Config.Method = http.MethodPost
	}
}

// Validate 在入口和持久化加载边界检查配置，不解析 DNS 或执行动作。
func Validate(r Record) error {
	if strings.TrimSpace(r.Name) == "" || len(r.Name) > 128 || strings.ContainsRune(r.Name, 0) || r.TimeoutS < 1 || r.TimeoutS > 3600 {
		return errors.New("action: 名称或超时无效")
	}
	c := r.Config
	hasHTTP := r.Credentials.URL.Reveal() != "" || len(r.Credentials.Headers) > 0 || r.Credentials.BodyTemplate.Reveal() != ""
	switch r.Type {
	case "notify":
		if len(c.ChannelIDs) == 0 || len(c.ChannelIDs) > 64 {
			return errors.New("action: 请选择通知渠道")
		}
		seen := map[int64]bool{}
		for _, id := range c.ChannelIDs {
			if id <= 0 || seen[id] {
				return errors.New("action: 渠道 ID 无效或重复")
			}
			seen[id] = true
		}
	case "http":
		u, err := url.Parse(r.Credentials.URL.Reveal())
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || strings.Contains(r.Credentials.URL.Reveal(), "{{") {
			return errors.New("action: HTTP URL 无效")
		}
		switch c.Method {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		default:
			return errors.New("action: HTTP 方法无效")
		}
		for _, v := range c.ExpectedStatus {
			if v < 200 || v > 599 {
				return errors.New("action: 期望状态码无效")
			}
		}
		for k, v := range r.Credentials.Headers {
			if !headerName(k) || strings.ContainsAny(v.Reveal(), "\r\n\x00") || strings.EqualFold(k, "Host") || strings.EqualFold(k, "Proxy-Authorization") || strings.EqualFold(k, "Connection") || strings.EqualFold(k, "Transfer-Encoding") || strings.EqualFold(k, "Content-Length") {
				return errors.New("action: HTTP Header 无效")
			}
		}
		if r.Credentials.BodyTemplate.Reveal() != "" {
			if err := notify.ValidateTemplate("test", "json", r.Credentials.BodyTemplate.Reveal()); err != nil {
				return errors.New("action: HTTP JSON 模板无效")
			}
		}
	case "script":
		if c.Script == "" || filepath.IsAbs(c.Script) || filepath.VolumeName(c.Script) != "" || strings.ContainsAny(c.Script, "\x00\\:") {
			return errors.New("action: 脚本路径无效")
		}
		for _, part := range strings.Split(c.Script, "/") {
			if part == ".." {
				return errors.New("action: 脚本路径无效")
			}
		}
		for _, arg := range c.Args {
			if strings.ContainsRune(arg, 0) {
				return errors.New("action: 脚本参数无效")
			}
		}
	case "ssh":
		if c.HostID <= 0 || strings.TrimSpace(c.Command) == "" || len(c.Command) > 65536 || strings.ContainsRune(c.Command, 0) {
			return errors.New("action: SSH 配置无效")
		}
	default:
		return errors.New("action: 不支持的动作类型（plan 留待 M7）")
	}
	if r.Type != "notify" && len(c.ChannelIDs) > 0 || r.Type != "http" && (hasHTTP || c.Method != "" || len(c.ExpectedStatus) > 0) || r.Type != "script" && (c.Script != "" || len(c.Args) > 0) || r.Type != "ssh" && (c.HostID != 0 || c.Command != "" || c.Shutdown) {
		return errors.New("action: 配置与动作类型不匹配")
	}
	return nil
}
func headerName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	return true
}

// Scan 在存储信任边界解密并校验，不执行网络请求。
func Scan(row interface{ Scan(...any) error }, cipher *secret.Cipher) (Record, error) {
	var r Record
	var cfg string
	var encrypted []byte
	if err := row.Scan(&r.ID, &r.Name, &r.Type, &r.TimeoutS, &cfg, &encrypted); err != nil {
		return r, err
	}
	if err := storedJSON([]byte(cfg), &r.Config); err != nil {
		return r, errors.New("action: 配置解码失败")
	}
	if len(encrypted) > 0 {
		plain, err := cipher.Decrypt(encrypted, "actions.secret")
		if err != nil {
			return r, errors.New("action: 凭据解密失败")
		}
		defer clear(plain)
		var creds plainCredentials
		if err = storedJSON(plain, &creds); err != nil {
			return r, errors.New("action: 凭据解码失败")
		}
		r.Credentials = Credentials{URL: secret.NewString(creds.URL), BodyTemplate: secret.NewString(creds.BodyTemplate)}
		if len(creds.Headers) > 0 {
			r.Credentials.Headers = make(map[string]secret.String, len(creds.Headers))
			for k, v := range creds.Headers {
				r.Credentials.Headers[k] = secret.NewString(v)
			}
		}
	}
	return r, Validate(r)
}
func storedJSON(data []byte, out any) error {
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

// Read 读取解密配置；不存在时返回 sql.ErrNoRows。
func Read(ctx context.Context, db *store.Store, cipher *secret.Cipher, id int64) (Record, error) {
	return Scan(db.Read().QueryRowContext(ctx, "SELECT "+Columns+" FROM actions WHERE id=?", id), cipher)
}

// Encode 将全部敏感 HTTP 配置使用 actions.secret AAD 加密。
func Encode(r Record, cipher *secret.Cipher) (string, []byte, error) {
	cfg, err := json.Marshal(r.Config)
	if err != nil {
		return "", nil, err
	}
	headers := make(map[string]string, len(r.Credentials.Headers))
	for k, v := range r.Credentials.Headers {
		headers[k] = v.Reveal()
	}
	plain, err := json.Marshal(plainCredentials{URL: r.Credentials.URL.Reveal(), Headers: headers, BodyTemplate: r.Credentials.BodyTemplate.Reveal()})
	if err != nil {
		return "", nil, err
	}
	defer clear(plain)
	encrypted, err := cipher.Encrypt(plain, "actions.secret")
	return string(cfg), encrypted, err
}
