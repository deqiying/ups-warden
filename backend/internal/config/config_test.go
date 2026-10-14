package config

import (
	"errors"
	"flag"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	c, err := Parse(nil, func(string) string { return "" }, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:8080" || c.DataDir != "./data" || c.LogFormat != "text" || c.LogLevel != slog.LevelInfo || c.LogFile != "" || c.ScriptDir != filepath.Join("data", "scripts") {
		t.Fatalf("默认配置不符: %+v", c)
	}
}

func TestPrecedence(t *testing.T) {
	env := map[string]string{"UPS_WARDEN_LISTEN": "127.0.0.1:9000", "UPS_WARDEN_DATA_DIR": "environment", "UPS_WARDEN_LOG_LEVEL": "debug", "UPS_WARDEN_LOG_FILE": "environment.log", "UPS_WARDEN_LOG_FORMAT": "json", "UPS_WARDEN_SCRIPT_DIR": "scripts-env"}
	getenv := func(k string) string { return env[k] }
	c, err := Parse(nil, getenv, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != env["UPS_WARDEN_LISTEN"] || c.DataDir != "environment" || c.LogLevel != slog.LevelDebug || c.LogFile != "environment.log" || c.LogFormat != "json" || c.ScriptDir != "scripts-env" {
		t.Fatalf("环境变量未生效: %+v", c)
	}
	c, err = Parse([]string{"--listen=:8000", "--data-dir=cli", "--log-level=warn", "--log-file=cli.log", "--log-format=text", "--script-dir=scripts-cli"}, getenv, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8000" || c.DataDir != "cli" || c.LogLevel != slog.LevelWarn || c.LogFile != "cli.log" || c.LogFormat != "text" || c.ScriptDir != "scripts-cli" {
		t.Fatalf("CLI 优先级错误: %+v", c)
	}
	c, err = Parse([]string{"--data-dir=elsewhere"}, func(string) string { return "" }, io.Discard)
	if err != nil || c.ScriptDir != filepath.Join("elsewhere", "scripts") {
		t.Fatalf("脚本默认路径错误: %+v %v", c, err)
	}
}

func TestInvalidArguments(t *testing.T) {
	for _, arg := range []string{"--unknown", "positional", "--log-level=bad", "--log-format=xml", "--data-dir=", "--listen=bad", "--listen=:65536", "--listen=:-1", "--listen=:http"} {
		t.Run(arg, func(t *testing.T) {
			if _, err := Parse([]string{arg}, func(string) string { return "" }, io.Discard); err == nil {
				t.Fatal("应拒绝无效参数")
			}
		})
	}
	if _, err := Parse([]string{"--help"}, func(string) string { return "" }, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("帮助错误: %v", err)
	}
}
