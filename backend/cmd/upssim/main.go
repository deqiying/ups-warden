// upssim 提供只监听 loopback 的三协议模拟 UPS，供本机开发和联调。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"ups-warden/internal/sim"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "upssim: %v\n", err)
		os.Exit(1)
	}
}

func readConfig(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置: %w", err)
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() > 65536 {
		return nil, fmt.Errorf("配置必须为不超过 64 KiB 的普通文件")
	}
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil {
		return nil, err
	}
	if len(data) > 65536 {
		return nil, fmt.Errorf("配置超过 64 KiB")
	}
	return data, nil
}

func run(ctx context.Context, args []string, out, errout io.Writer) (result error) {
	flags := flag.NewFlagSet("upssim", flag.ContinueOnError)
	flags.SetOutput(errout)
	scenarioFile := flags.String("scenario", "", "schema=1 场景 YAML 文件（默认正常单相）")
	registerFile := flags.String("registers", "", "schema=1 自定义 Modbus 寄存器表 YAML 文件")
	snmpPort := flags.Int("snmp-port", 0, "SNMP UDP 本机端口，0 自动分配")
	modbusPort := flags.Int("modbus-port", 0, "Modbus TCP 本机端口，0 自动分配")
	ayi9Port := flags.Int("ayi9-port", 0, "AYI9 TCP 本机端口，0 自动分配")
	cfg := sim.Config{}
	flags.StringVar(&cfg.SNMPAddress, "snmp-address", "", "覆盖 SNMP loopback IP:port")
	flags.StringVar(&cfg.ModbusAddress, "modbus-address", "", "覆盖 Modbus loopback IP:port")
	flags.StringVar(&cfg.AYI9Address, "ayi9-address", "", "覆盖 AYI9 loopback IP:port")
	flags.StringVar(&cfg.Community, "community", "sim-public", "虚构 SNMP 测试 community（不输出）")
	flags.StringVar(&cfg.Password, "password", "", "虚构 AYI9 测试密码，最长 15 字节（不输出）")
	flags.BoolVar(&cfg.ET1K, "et1k", false, "启用 ET1K SNMP/Modbus 怪癖")
	flags.IntVar(&cfg.AYI9ErrorCode, "ayi9-error-code", 0, "固定 AYI9 错误码，如 103；0 正常")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("不接受位置参数")
	}
	for _, pair := range []struct {
		port    int
		address *string
	}{{*snmpPort, &cfg.SNMPAddress}, {*modbusPort, &cfg.ModbusAddress}, {*ayi9Port, &cfg.AYI9Address}} {
		if pair.port < 0 || pair.port > 65535 {
			return fmt.Errorf("端口必须在 0..65535 内")
		}
		if *pair.address == "" {
			*pair.address = net.JoinHostPort("127.0.0.1", strconv.Itoa(pair.port))
		} else if pair.port != 0 {
			return fmt.Errorf("不能同时指定 address 和非零 port")
		}
	}
	if *scenarioFile != "" {
		data, err := readConfig(*scenarioFile)
		if err != nil {
			return err
		}
		cfg.Scenario, err = sim.ParseScenario(data)
		if err != nil {
			return fmt.Errorf("场景: %w", err)
		}
	}
	if *registerFile != "" {
		data, err := readConfig(*registerFile)
		if err != nil {
			return err
		}
		cfg.Registers, err = sim.ParseRegisters(data)
		if err != nil {
			return fmt.Errorf("寄存器表: %w", err)
		}
	}
	server, err := sim.Start(cfg)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, server.Close()) }()
	snmp, modbus, ayi9 := server.Addresses()
	if _, err := fmt.Fprintf(out, "SNMP=%s Modbus=%s AYI9=%s ET1K=%t\n", snmp, modbus, ayi9, cfg.ET1K); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}
