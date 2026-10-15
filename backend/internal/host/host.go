// Package host 提供严格绑定已确认指纹的受控主机 SSH 与在线探测。
package host

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"strings"

	"golang.org/x/crypto/ssh"
	"ups-warden/internal/secret"
	"ups-warden/internal/store"
)

// Connection 描述 SSH 连接；sudo 只使用目标主机的 NOPASSWD 配置。
type Connection struct {
	Port       int    `json:"port"`
	Username   string `json:"username"`
	AuthMethod string `json:"auth_method"`
	Sudo       bool   `json:"sudo"`
}

// Credentials 的默认序列化只含掩码；持久化必须通过 Encode 显式加密。
type Credentials struct {
	Password   secret.String `json:"password"`
	PrivateKey secret.String `json:"private_key"`
	Passphrase secret.String `json:"passphrase"`
}

// OnlineCheck 指定 TCP 端口或 ICMP 探测。
type OnlineCheck struct {
	Type string `json:"type"`
	Port int    `json:"port"`
}

// Record 是主机配置；凭据永不出现在 API JSON 中。
type Record struct {
	ID          int64       `json:"id"`
	Name        string      `json:"name"`
	Address     string      `json:"address"`
	Kind        string      `json:"kind"`
	Conn        Connection  `json:"conn"`
	Credentials Credentials `json:"-"`
	Fingerprint string      `json:"hostkey_fingerprint"`
	OnlineCheck OnlineCheck `json:"online_check"`
}

// Result 区分成功、失败和已发送但尚未确认完成的关机命令。
type Result struct {
	Status     string `json:"status"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMS int64  `json:"duration_ms"`
}

// Columns 是 Scan 需要的稳定列顺序。
const Columns = "id,name,address,kind,conn,secret,COALESCE(hostkey_fingerprint,''),online_check"

// ErrUnconfirmed 和 ErrFingerprint 表示不得绕过的 SSH 信任边界。
var (
	ErrUnconfirmed = errors.New("主机指纹尚未确认，请先获取并确认指纹")
	ErrFingerprint = errors.New("SSH 主机指纹不匹配，可能存在中间人攻击；请重新核实主机身份")
)

type plainCredentials struct {
	Password   string `json:"password"`
	PrivateKey string `json:"private_key"`
	Passphrase string `json:"passphrase"`
}

// Encode 将凭据绑定 hosts.secret AAD，其他配置保持可查询 JSON。
func Encode(h Record, cipher *secret.Cipher) (conn string, encrypted []byte, online string, err error) {
	c, err := json.Marshal(h.Conn)
	if err != nil {
		return "", nil, "", err
	}
	o, err := json.Marshal(h.OnlineCheck)
	if err != nil {
		return "", nil, "", err
	}
	p, err := json.Marshal(plainCredentials{h.Credentials.Password.Reveal(), h.Credentials.PrivateKey.Reveal(), h.Credentials.Passphrase.Reveal()})
	if err != nil {
		return "", nil, "", err
	}
	encrypted, err = cipher.Encrypt(p, "hosts.secret")
	return string(c), encrypted, string(o), err
}

// Scan 读取配置并解密凭据；错误不包含明文。
func Scan(row interface{ Scan(...any) error }, cipher *secret.Cipher) (Record, error) {
	var h Record
	var c, o string
	var encrypted []byte
	if err := row.Scan(&h.ID, &h.Name, &h.Address, &h.Kind, &c, &encrypted, &h.Fingerprint, &o); err != nil {
		return h, err
	}
	if json.Unmarshal([]byte(c), &h.Conn) != nil || json.Unmarshal([]byte(o), &h.OnlineCheck) != nil {
		return h, errors.New("主机配置 JSON 无效")
	}
	if len(encrypted) != 0 {
		p, err := cipher.Decrypt(encrypted, "hosts.secret")
		if err != nil {
			return h, err
		}
		var creds plainCredentials
		if json.Unmarshal(p, &creds) != nil {
			return h, errors.New("主机凭据 JSON 无效")
		}
		h.Credentials = Credentials{secret.NewString(creds.Password), secret.NewString(creds.PrivateKey), secret.NewString(creds.Passphrase)}
	}
	return h, nil
}

// Read 按 ID 读取主机；不存在时返回 sql.ErrNoRows。
func Read(ctx context.Context, db *store.Store, cipher *secret.Cipher, id int64) (Record, error) {
	return Scan(db.Read().QueryRowContext(ctx, "SELECT "+Columns+" FROM hosts WHERE id=?", id), cipher)
}

// View 仅暴露凭据设置状态，不返回掩码占位值或原文。
func (h Record) View() map[string]any {
	return map[string]any{"id": h.ID, "name": h.Name, "address": h.Address, "kind": h.Kind, "conn": h.Conn, "hostkey_fingerprint": h.Fingerprint, "online_check": h.OnlineCheck,
		"password_set": h.Credentials.Password.Reveal() != "", "private_key_set": h.Credentials.PrivateKey.Reveal() != "", "passphrase_set": h.Credentials.Passphrase.Reveal() != ""}
}

func validAddress(s string) bool {
	if s == "" || len(s) > 253 || strings.ContainsAny(s, " /\\\t\r\n\x00@[]") {
		return false
	}
	if net.ParseIP(s) != nil {
		return true
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-') {
				return false
			}
		}
	}
	return true
}

func validFingerprint(s string) bool {
	if !strings.HasPrefix(s, "SHA256:") {
		return false
	}
	b, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(s, "SHA256:"))
	return err == nil && len(b) == 32
}

// Validate 校验保存配置；未确认指纹允许保存，但不能连接认证或执行命令。
func Validate(h Record) error {
	if strings.TrimSpace(h.Name) == "" || len(h.Name) > 128 || strings.ContainsRune(h.Name, 0) || !validAddress(h.Address) {
		return errors.New("主机名称或地址无效")
	}
	switch h.Kind {
	case "linux", "windows", "nas", "esxi", "macos", "other":
	default:
		return errors.New("主机类型无效")
	}
	if h.Conn.Port < 1 || h.Conn.Port > 65535 || strings.TrimSpace(h.Conn.Username) == "" || len(h.Conn.Username) > 128 || strings.ContainsAny(h.Conn.Username, "\x00\r\n") {
		return errors.New("SSH 端口或用户名无效")
	}
	if h.Fingerprint != "" && !validFingerprint(h.Fingerprint) {
		return ErrFingerprint
	}
	if len(h.Credentials.Password.Reveal()) > 65536 || len(h.Credentials.PrivateKey.Reveal()) > 65536 || len(h.Credentials.Passphrase.Reveal()) > 65536 {
		return errors.New("主机凭据过长")
	}
	switch h.Conn.AuthMethod {
	case "password":
		if h.Credentials.Password.Reveal() == "" || h.Credentials.PrivateKey.Reveal() != "" || h.Credentials.Passphrase.Reveal() != "" {
			return errors.New("密码认证需要密码且不能包含私钥")
		}
	case "private_key":
		if h.Credentials.Password.Reveal() != "" {
			return errors.New("私钥认证不能包含密码")
		}
		if _, err := signer(h.Credentials); err != nil {
			return err
		}
	default:
		return errors.New("SSH 认证方式无效")
	}
	if h.OnlineCheck.Type != "tcp" && h.OnlineCheck.Type != "icmp" {
		return errors.New("在线检测类型无效")
	}
	if h.OnlineCheck.Type == "tcp" && (h.OnlineCheck.Port < 1 || h.OnlineCheck.Port > 65535) {
		return errors.New("在线检测 TCP 端口无效")
	}
	return nil
}

func signer(c Credentials) (ssh.Signer, error) {
	var s ssh.Signer
	var err error
	if c.Passphrase.Reveal() != "" {
		s, err = ssh.ParsePrivateKeyWithPassphrase([]byte(c.PrivateKey.Reveal()), []byte(c.Passphrase.Reveal()))
	} else {
		s, err = ssh.ParsePrivateKey([]byte(c.PrivateKey.Reveal()))
	}
	if err != nil {
		return nil, errors.New("SSH 私钥或口令无效")
	}
	return s, nil
}
