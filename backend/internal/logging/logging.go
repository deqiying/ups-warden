// Package logging 提供结构化日志、敏感属性脱敏及文件滚动。
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/natefinch/lumberjack.v2"
	"ups-warden/internal/config"
)

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// New 默认写 stderr；指定文件时使用 20 MB、5 份备份的滚动文件。
// 调用方负责在所有模块关闭后关闭返回的 writer。
func New(c config.Config, stderr io.Writer) (*slog.Logger, io.Closer, error) {
	if c.LogFormat != "text" && c.LogFormat != "json" {
		return nil, nil, fmt.Errorf("不支持的日志格式")
	}
	output := stderr
	var closer io.Closer = nopCloser{}
	if c.LogFile != "" {
		if err := os.MkdirAll(filepath.Dir(c.LogFile), 0700); err != nil {
			return nil, nil, fmt.Errorf("创建日志目录: %w", err)
		}
		// lumberjack 延迟打开文件；提前检查使无权限等错误在启动阶段显式失败。
		f, err := os.OpenFile(c.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return nil, nil, fmt.Errorf("打开日志文件: %w", err)
		}
		if err := f.Close(); err != nil {
			return nil, nil, fmt.Errorf("关闭日志检查句柄: %w", err)
		}
		roller := &lumberjack.Logger{Filename: c.LogFile, MaxSize: 20, MaxBackups: 5}
		output, closer = roller, roller
	}
	options := &slog.HandlerOptions{Level: c.LogLevel, ReplaceAttr: redact}
	var handler slog.Handler = slog.NewTextHandler(output, options)
	if c.LogFormat == "json" {
		handler = slog.NewJSONHandler(output, options)
	}
	return slog.New(handler), closer, nil
}

func sensitive(key string) bool {
	switch strings.ToLower(key) {
	case "password", "pass", "community", "token", "secret", "private_key", "authorization", "master_key", "ups_warden_master_key":
		return true
	default:
		return false
	}
}

func redact(groups []string, a slog.Attr) slog.Attr {
	hidden := sensitive(a.Key)
	for _, group := range groups {
		hidden = hidden || sensitive(group)
	}
	if hidden {
		a.Value = slog.StringValue("******")
	} else if a.Value.Kind() == slog.KindAny {
		a.Value = slog.AnyValue(redactAny(a.Value.Any()))
	}
	return a
}

// 常见的 JSON 对象也可能放在 Any 属性中，不能只检查最外层键。
func redactAny(value any) any {
	switch v := value.(type) {
	case map[string]any:
		clean := make(map[string]any, len(v))
		for k, item := range v {
			if sensitive(k) {
				clean[k] = "******"
			} else {
				clean[k] = redactAny(item)
			}
		}
		return clean
	case map[string]string:
		clean := make(map[string]string, len(v))
		for k, item := range v {
			if sensitive(k) {
				clean[k] = "******"
			} else {
				clean[k] = item
			}
		}
		return clean
	case []any:
		clean := make([]any, len(v))
		for i, item := range v {
			clean[i] = redactAny(item)
		}
		return clean
	default:
		return value
	}
}
