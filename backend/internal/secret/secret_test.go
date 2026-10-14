package secret

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCipher(t *testing.T) {
	c, err := New(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("test-credential")
	encrypted, err := c.Encrypt(plain, "sources.secret")
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.Decrypt(encrypted, "sources.secret")
	if err != nil || !bytes.Equal(result, plain) {
		t.Fatalf("往返失败: %v", err)
	}
	other, err := c.Encrypt(plain, "sources.secret")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(other, encrypted) {
		t.Fatal("nonce 不应重复")
	}
	wrong, err := New(bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.Decrypt(encrypted, "sources.secret"); err == nil {
		t.Fatal("错误密钥被接受")
	}
	if _, err := c.Decrypt(encrypted, "channels.secret"); err == nil {
		t.Fatal("错误 AAD 被接受")
	}
	if _, err := c.Encrypt(plain, ""); err == nil {
		t.Fatal("空 AAD 被接受")
	}
	for i := range encrypted {
		altered := bytes.Clone(encrypted)
		altered[i] ^= 1
		if _, err := c.Decrypt(altered, "sources.secret"); err == nil {
			t.Fatalf("篡改字节 %d 未被拒绝", i)
		}
	}
	for i := 0; i < len(encrypted); i++ {
		if _, err := c.Decrypt(encrypted[:i], "sources.secret"); err == nil {
			t.Fatalf("截断 %d 未被拒绝", i)
		}
	}
	for _, size := range []int{0, 16, 31, 33} {
		if _, err := New(make([]byte, size)); err == nil {
			t.Fatalf("接受非法密钥长度 %d", size)
		}
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	first, err := Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := first.Encrypt([]byte("credential"), "sources.secret")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Decrypt(data, "sources.secret"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "master.key")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(original) != 32 {
		t.Fatal("密钥文件长度错误")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("密钥权限 %v", info.Mode())
	}
	envKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	fromEnv, err := Load(dir, envKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fromEnv.Decrypt(data, "sources.secret"); err == nil {
		t.Fatal("环境变量未优先")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, after) {
		t.Fatal("环境密钥不应改写文件")
	}
	for _, bad := range []string{"secret-not-base64", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := Load(dir, bad); err == nil || strings.Contains(err.Error(), bad) {
			t.Fatal("非法环境密钥应失败且不泄漏")
		}
	}
	if err := os.WriteFile(path, []byte("truncated"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, ""); err == nil {
		t.Fatal("不应覆盖损坏密钥")
	}
	envDir := t.TempDir()
	if _, err := Load(envDir, envKey); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(envDir, "master.key")); !os.IsNotExist(err) {
		t.Fatal("环境密钥不应落盘")
	}
}

func TestMaskedString(t *testing.T) {
	value := NewString("do-not-leak")
	for _, format := range []string{"%s", "%v", "%+v", "%#v", "%q", "%x"} {
		if got := fmt.Sprintf(format, value); strings.Contains(got, value.Reveal()) || got != mask {
			t.Fatalf("格式 %s 泄漏或非掩码: %s", format, got)
		}
	}
	b, err := json.Marshal(struct{ Value String }{value})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), value.Reveal()) || !bytes.Contains(b, []byte(mask)) {
		t.Fatal("JSON 未脱敏")
	}
	var log bytes.Buffer
	slog.New(slog.NewJSONHandler(&log, nil)).Info("test", "value", value)
	if strings.Contains(log.String(), value.Reveal()) || !strings.Contains(log.String(), mask) {
		t.Fatal("日志未脱敏")
	}
	if value.Reveal() != "do-not-leak" {
		t.Fatal("Reveal 错误")
	}
}

func TestMaskedStringInUnexportedField(t *testing.T) {
	value := NewString("nested-sensitive-value")
	wrapped := struct{ credential String }{value}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if got := fmt.Sprintf(format, wrapped); strings.Contains(got, value.Reveal()) {
			t.Errorf("非导出字段在 %s 下泄漏明文", format)
		}
	}
	var zero String
	if zero.Reveal() != "" {
		t.Fatal("零值必须可用")
	}
}
