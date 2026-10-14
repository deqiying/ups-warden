// Package secret 管理凭据掩码与带用途绑定的 AES-256-GCM 密文。
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
)

const mask = "******"

// String 默认只输出掩码，只有 Reveal 可以显式提取明文。
// 私有指针避免 fmt 在外层非导出字段中跳过 Formatter 后递归展开明文。
type String struct{ value *string }

// NewString 包装敏感字符串；调用方不得另行记录其原始值。
func NewString(value string) String { return String{value: &value} }

// Reveal 仅供凭据消费端获取明文。
func (s String) Reveal() string {
	if s.value == nil {
		return ""
	}
	return *s.value
}
func (s String) String() string               { return mask }
func (s String) GoString() string             { return mask }
func (s String) LogValue() slog.Value         { return slog.StringValue(mask) }
func (s String) MarshalJSON() ([]byte, error) { return []byte(`"******"`), nil }
func (s String) MarshalText() ([]byte, error) { return []byte(mask), nil }

// Format 覆盖包含 %#v 在内的 fmt 格式，避免结构体内部字段被展开。
func (s String) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, mask) }

// Cipher 不暴露主密钥，密文只能在相同的非空 AAD 下解密。
type Cipher struct{ aead cipher.AEAD }

// New 要求恰好 32 字节密钥。
func New(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, errors.New("主密钥必须是 32 字节")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化 GCM: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Load 优先使用非空 Base64 环境密钥，否则读取或独占创建 master.key。
// 文件保存原始 32 字节；Linux 强制私有权限，Windows 的访问控制由目录 ACL 提供。
func Load(dataDir, encodedKey string) (*Cipher, error) {
	if encodedKey != "" {
		key, err := base64.StdEncoding.DecodeString(encodedKey)
		if err != nil {
			return nil, errors.New("UPS_WARDEN_MASTER_KEY 必须是 Base64 编码的 32 字节密钥")
		}
		return New(key)
	}
	path := filepath.Join(dataDir, "master.key")
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("master.key 必须是普通文件，不允许符号链接")
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			return nil, errors.New("master.key 权限过宽，请设置为 0600")
		}
		key, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("读取主密钥: %w", err)
		}
		return New(key)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("检查主密钥文件: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("生成主密钥: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("独占创建主密钥: %w", err)
	}
	_, writeErr := f.Write(key)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		// 不覆盖或自动删除可能已部分落盘的密钥，保留现场供恢复。
		return nil, fmt.Errorf("持久化主密钥失败，请检查 master.key: %w", err)
	}
	return New(key)
}

// Encrypt 输出 版本(1B) | 随机 nonce(12B) | ciphertext+tag；aad 使用“表名.列名”。
func (c *Cipher) Encrypt(plain []byte, aad string) ([]byte, error) {
	if aad == "" {
		return nil, errors.New("加密用途 AAD 不能为空")
	}
	out := make([]byte, 1+c.aead.NonceSize())
	out[0] = 1
	if _, err := rand.Read(out[1:]); err != nil {
		return nil, fmt.Errorf("生成加密 nonce: %w", err)
	}
	return c.aead.Seal(out, out[1:], plain, []byte(aad)), nil
}

// Decrypt 对错误密钥、用途、版本或被篡改密文均返回不含凭据的错误。
func (c *Cipher) Decrypt(data []byte, aad string) ([]byte, error) {
	prefix := 1 + c.aead.NonceSize()
	if aad == "" || len(data) < prefix+c.aead.Overhead() || data[0] != 1 {
		return nil, errors.New("无效的密文格式或加密用途")
	}
	plain, err := c.aead.Open(nil, data[1:prefix], data[prefix:], []byte(aad))
	if err != nil {
		return nil, errors.New("凭据解密失败：请恢复正确的主密钥备份或重新填写凭据")
	}
	return plain, nil
}
