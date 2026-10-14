// Package config 解析启动参数；命令行优先于非空环境变量。
package config

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strconv"
)

// Config 是进程级配置，不包含设备凭据。
type Config struct {
	Listen    string
	DataDir   string
	LogLevel  slog.Level
	LogFile   string
	LogFormat string
	ScriptDir string
}

// Parse 使用独立 FlagSet，便于测试且不修改进程全局参数。
// getenv 应返回缺失变量的空字符串；output 接收帮助和参数诊断。
func Parse(args []string, getenv func(string) string, output io.Writer) (Config, error) {
	var c Config
	env := func(key, fallback string) string {
		if v := getenv("UPS_WARDEN_" + key); v != "" {
			return v
		}
		return fallback
	}
	fs := flag.NewFlagSet("ups-warden", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.StringVar(&c.Listen, "listen", env("LISTEN", "127.0.0.1:8080"), "HTTP 监听地址（UPS_WARDEN_LISTEN）")
	fs.StringVar(&c.DataDir, "data-dir", env("DATA_DIR", "./data"), "数据目录（UPS_WARDEN_DATA_DIR）")
	level := fs.String("log-level", env("LOG_LEVEL", "info"), "日志级别 debug|info|warn|error（UPS_WARDEN_LOG_LEVEL）")
	fs.StringVar(&c.LogFile, "log-file", env("LOG_FILE", ""), "滚动日志文件；空值使用 stderr（UPS_WARDEN_LOG_FILE）")
	fs.StringVar(&c.LogFormat, "log-format", env("LOG_FORMAT", "text"), "日志格式 text|json（UPS_WARDEN_LOG_FORMAT）")
	fs.StringVar(&c.ScriptDir, "script-dir", env("SCRIPT_DIR", ""), "脚本目录；默认 <data>/scripts（UPS_WARDEN_SCRIPT_DIR）")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if fs.NArg() != 0 {
		return c, fmt.Errorf("不接受位置参数")
	}
	if err := c.LogLevel.UnmarshalText([]byte(*level)); err != nil {
		return c, fmt.Errorf("无效的日志级别: %w", err)
	}
	if c.LogFormat != "text" && c.LogFormat != "json" {
		return c, fmt.Errorf("日志格式必须是 text 或 json")
	}
	if c.DataDir == "" {
		return c, fmt.Errorf("数据目录不能为空")
	}
	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return c, fmt.Errorf("无效的监听地址: %w", err)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return c, fmt.Errorf("监听端口必须在 0..65535 之间")
	}
	if c.ScriptDir == "" {
		c.ScriptDir = filepath.Join(c.DataDir, "scripts")
	}
	return c, nil
}
