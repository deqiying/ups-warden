package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/natefinch/lumberjack.v2"
	"ups-warden/internal/config"
)

func TestRedactionAndLevel(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			var out bytes.Buffer
			logger, closer, err := New(config.Config{LogFormat: format, LogLevel: slog.LevelInfo}, &out)
			if err != nil {
				t.Fatal(err)
			}
			defer closer.Close()
			logger.Debug("hidden-debug")
			for _, key := range []string{"password", "pass", "community", "token", "secret", "private_key", "Authorization", "master_key"} {
				logger.Info("test", key, "sensitive-value")
			}
			logger.WithGroup("secret").Info("test", "value", "sensitive-value")
			logger.Info("test", slog.Group("nested", "token", "sensitive-value"), "payload", map[string]any{"array": []any{map[string]string{"pass": "sensitive-value"}}, "normal": "visible"})
			if strings.Contains(out.String(), "sensitive-value") || strings.Contains(out.String(), "hidden-debug") {
				t.Fatalf("日志脱敏或级别失败: %s", out.String())
			}
			if !strings.Contains(out.String(), "visible") || !strings.Contains(out.String(), "******") {
				t.Fatal("非敏感属性或掩码丢失")
			}
			if format == "json" {
				for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
					if !json.Valid([]byte(line)) {
						t.Fatalf("无效JSON: %s", line)
					}
				}
			}
		})
	}
}

func TestFileAndRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "service.log")
	var stderr bytes.Buffer
	logger, closer, err := New(config.Config{LogFormat: "text", LogFile: path}, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	roller, ok := closer.(*lumberjack.Logger)
	if !ok {
		t.Fatal("未使用滚动日志")
	}
	if roller.MaxSize != 20 || roller.MaxBackups != 5 {
		t.Fatal("滚动配置错误")
	}
	logger.Info("before-rotation")
	if err := roller.Rotate(); err != nil {
		t.Fatal(err)
	}
	logger.Info("after-rotation")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("after-rotation")) || bytes.Contains(data, []byte("before-rotation")) || stderr.Len() != 0 {
		t.Fatal("文件输出错误")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), "service-*.log"))
	if err != nil || len(files) != 1 {
		t.Fatalf("轮转备份错误: %v %v", files, err)
	}
	if _, _, err := New(config.Config{LogFormat: "text", LogFile: filepath.Dir(path)}, &stderr); err == nil {
		t.Fatal("应拒绝把目录作为日志文件")
	}
	if _, _, err := New(config.Config{LogFormat: "xml"}, &stderr); err == nil {
		t.Fatal("应拒绝未知格式")
	}
}
