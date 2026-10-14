package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// 进程级并发上限避免多个 Service 合计占用无限 Argon2 内存。
var hashSlots = make(chan struct{}, 2)

func validPassword(p string) bool {
	return len(p) <= 1024 && utf8.ValidString(p) && utf8.RuneCountInString(p) >= 8
}
func username(s string) (string, error) {
	if len(s) < 1 || len(s) > 64 {
		return "", ErrValidation
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return "", ErrValidation
		}
	}
	return strings.ToLower(s), nil
}
func derive(ctx context.Context, p string, salt []byte, m, t uint32, threads uint8) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case hashSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-hashSlots }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Argon2 不支持中途取消；最多两次计算，结束后仍检查取消，绝不提交取消请求。
	out := argon2.IDKey([]byte(p), salt, t, m, threads, 32)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func passwordHash(ctx context.Context, p string) (string, error) {
	if !validPassword(p) {
		return "", ErrValidation
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: 生成密码盐: %w", err)
	}
	h, err := derive(ctx, p, salt, 65536, 3, 2)
	if err != nil {
		return "", err
	}
	return "$argon2id$v=19$m=65536,t=3,p=2$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(h), nil
}
func verify(ctx context.Context, p, encoded string) (bool, error) {
	if len(encoded) > 200 {
		return false, errors.New("auth: 无效密码哈希")
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, errors.New("auth: 无效密码哈希")
	}
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return false, errors.New("auth: 无效密码参数")
	}
	vals := make([]uint64, 3)
	for i, prefix := range []string{"m=", "t=", "p="} {
		if !strings.HasPrefix(params[i], prefix) {
			return false, errors.New("auth: 无效密码参数")
		}
		n, err := strconv.ParseUint(strings.TrimPrefix(params[i], prefix), 10, 32)
		if err != nil {
			return false, errors.New("auth: 无效密码参数")
		}
		vals[i] = n
	}
	if vals[0] < 8192 || vals[0] > 65536 || vals[1] < 1 || vals[1] > 3 || vals[2] < 1 || vals[2] > 2 {
		return false, errors.New("auth: 密码参数超出安全范围")
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, e2 := base64.RawStdEncoding.DecodeString(parts[5])
	if e1 != nil || e2 != nil || len(salt) != 16 || len(want) != 32 {
		return false, errors.New("auth: 无效密码哈希")
	}
	got, err := derive(ctx, p, salt, uint32(vals[0]), uint32(vals[1]), uint8(vals[2]))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
func dummy(ctx context.Context, p string) error {
	_, err := derive(ctx, p, make([]byte, 16), 65536, 3, 2)
	return err
}
