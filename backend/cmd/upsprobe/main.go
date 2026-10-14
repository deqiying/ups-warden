// upsprobe 是需显式授权的单数据源只读冒烟工具；不由服务或自动化任务隐式调用。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/driver/ayi9"
	"ups-warden/internal/driver/guard"
	"ups-warden/internal/driver/modbus"
	"ups-warden/internal/driver/snmp"
	"ups-warden/internal/model"
	"ups-warden/internal/normalize"
	"ups-warden/internal/profile"
	"ups-warden/internal/secret"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "upsprobe:", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, getenv func(string) string, out io.Writer) (err error) {
	flags := flag.NewFlagSet("upsprobe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	key := flags.String("profile", "", "必须指定内置 Profile")
	confirm := flags.Bool("confirm-readonly", false, "确认已获得目标设备只读访问授权")
	timeout := flags.Duration("timeout", 5*time.Second, "单次 Probe 总超时")
	if flags.Parse(args) != nil || flags.NArg() != 0 || !*confirm || *key == "" {
		return errors.New("必须指定 --profile KEY --confirm-readonly；地址与凭据仅从环境变量读取")
	}
	if *timeout <= 0 || *timeout > time.Minute {
		return errors.New("Probe 总超时须为 0～60 秒（不含 0）")
	}
	p, err := profile.Builtin(*key)
	if err != nil {
		return errors.New("未知内置 Profile")
	}
	plan, err := p.ReadPlan()
	if err != nil {
		return err
	}
	cfg := guard.Config{Address: strings.TrimSpace(getenv("UPS_WARDEN_PROBE_ADDRESS"))}
	if cfg.Address == "" {
		return errors.New("缺少 UPS_WARDEN_PROBE_ADDRESS（host:port）")
	}
	var client driver.Driver
	switch p.Protocol {
	case driver.ProtocolSNMP:
		client, err = snmp.New(snmp.Config{Connection: cfg, Version: getenv("UPS_WARDEN_PROBE_SNMP_VERSION"), Community: secret.NewString(getenv("UPS_WARDEN_PROBE_COMMUNITY"))}, *plan.SNMP)
	case driver.ProtocolModbus:
		client, err = modbus.New(modbus.Config{Connection: cfg}, *plan.Modbus)
	case driver.ProtocolAYI9:
		client, err = ayi9.New(ayi9.Config{Connection: cfg, Password: secret.NewString(getenv("UPS_WARDEN_PROBE_PASSWORD"))}, *plan.AYI9)
	default:
		return errors.New("不支持的协议")
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, client.Close()) }()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	_, raw, err := client.Probe(ctx)
	if err != nil {
		return err
	}
	snapshot, _, err := normalize.Normalize(1, raw, p)
	if err != nil {
		return err
	}
	// 只输出固定电气字段与目录枚举，不输出自由文本身份或原始报文。
	result := struct {
		Protocol    string                 `json:"protocol"`
		Profile     string                 `json:"profile"`
		At          time.Time              `json:"at"`
		Good        int                    `json:"good"`
		Invalid     int                    `json:"invalid"`
		Unsupported int                    `json:"unsupported"`
		Diagnostics int                    `json:"diagnostics"`
		Fields      map[string]model.Field `json:"fields"`
	}{Protocol: p.Protocol, Profile: p.Key, At: snapshot.At, Diagnostics: len(raw.Errors)}
	for _, field := range snapshot.Fields {
		switch field.Quality {
		case model.Good:
			result.Good++
		case model.Invalid:
			result.Invalid++
		case model.Unsupported:
			result.Unsupported++
		}
	}
	result.Fields = make(map[string]model.Field)
	observed := 0
	for _, key := range []string{"input_voltage[1]", "output_voltage[1]", "output_freq", "output_load_pct[1]", "battery_capacity_pct", "battery_voltage", "temperature", "native_seconds_on_battery", "output_source", "ups_mode", "mains_ok"} {
		field := snapshot.Fields[key]
		result.Fields[key] = field
		if field.Quality == model.Good {
			observed++
		}
	}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		return errors.New("输出 Probe 统计失败")
	}
	if observed == 0 {
		return errors.New("只读请求已结束，但没有有效观测字段")
	}
	return nil
}
