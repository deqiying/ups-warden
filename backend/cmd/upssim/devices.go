package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"text/tabwriter"

	"ups-warden/internal/sim"
)

func jsonMarshal(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// parseNamed 允许 NAME 出现在 flag 之前或之后（flag 包遇到第一个位置参数即停止解析）。
func parseNamed(fs *flag.FlagSet, args []string) (string, error) {
	name := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	if err := parseFlags(fs, args); err != nil {
		return "", err
	}
	rest := fs.Args()
	if name == "" && len(rest) > 0 {
		name, rest = rest[0], rest[1:]
	}
	if len(rest) != 0 {
		return "", usagef("多余的位置参数: %v（flag 须写在 NAME 之后或之前，不能夹在中间）", rest)
	}
	if name == "" {
		return "", usagef("缺少设备名 NAME")
	}
	return name, nil
}

// visited 返回命令行中显式出现过的 flag，用于区分“未设置”与“设为默认值”。
func visited(fs *flag.FlagSet) map[string]bool {
	seen := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	return seen
}

// physicalFlags 注册 add/set 共用的物理参数。
type physicalFlags struct {
	load, capacity, temperature, inputVoltage, discharge float64
}

func (p *physicalFlags) register(fs *flag.FlagSet) {
	fs.Float64Var(&p.load, "load", 0, "负载百分比 0..100（不能为 0）")
	fs.Float64Var(&p.capacity, "capacity", 0, "当前电池电量百分比 0..100；low 模式不超过 15")
	fs.Float64Var(&p.temperature, "temperature", 0, "温度 °C，-40..100（不能为 0）")
	fs.Float64Var(&p.inputVoltage, "input-voltage", 0, "市电输入电压 V，1..500")
	fs.Float64Var(&p.discharge, "discharge-minutes", 0, "满电放空所需分钟数，1..1440")
}

func (p *physicalFlags) collect(seen map[string]bool) (physical, *float64) {
	pick := func(name string, v float64) *float64 {
		if !seen[name] {
			return nil
		}
		return &v
	}
	return physical{LoadPct: pick("load", p.load), Temperature: pick("temperature", p.temperature),
		InputVoltage: pick("input-voltage", p.inputVoltage), DischargeMinutes: pick("discharge-minutes", p.discharge)}, pick("capacity", p.capacity)
}

func readScenarioFlag(path string) (*string, error) {
	if path == "" {
		return nil, nil
	}
	data, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	// 客户端先严格校验，守护进程再校验一次并保存原文，不依赖客户端工作目录。
	if _, err := sim.ParseScenario(data); err != nil {
		return nil, usagef("场景文件无效: %v", err)
	}
	text := string(data)
	return &text, nil
}

func modeFlag(v string) (*sim.Mode, error) {
	switch m := sim.Mode(v); m {
	case sim.ModeMains, sim.ModeBattery, sim.ModeLow:
		return &m, nil
	}
	return nil, usagef("--mode 必须为 mains、battery 或 low")
}

func linkFlag(v string) (*string, error) {
	if v != "up" && v != "down" {
		return nil, usagef("--link 必须为 up 或 down")
	}
	return &v, nil
}

func addCommand(args []string, out, errout io.Writer) error {
	fs, stateFlag := newFlagSet("add NAME", errout)
	var req createRequest
	var mode, link, ip, scenario, community, password string
	var phys physicalFlags
	var asJSON bool
	fs.StringVar(&mode, "mode", "mains", "初始供电模式：mains、battery、low")
	fs.StringVar(&link, "link", "up", "初始通信状态：up、down")
	fs.StringVar(&ip, "ip", "127.0.0.1", "三协议共用的 loopback 监听 IP；多台设备建议各用不同 IP")
	fs.IntVar(&req.SNMPPort, "snmp-port", 0, "SNMP UDP 端口，0 自动分配")
	fs.IntVar(&req.ModbusPort, "modbus-port", 0, "Modbus TCP 端口，0 自动分配")
	fs.IntVar(&req.AYI9Port, "ayi9-port", 0, "AYI9 TCP 端口，0 自动分配")
	fs.BoolVar(&req.ET1K, "et1k", false, "启用 ET1K 怪癖（仅单相）")
	fs.IntVar(&req.Phases, "phases", 1, "相数 1 或 3；ET1K 只允许 1")
	fs.StringVar(&scenario, "scenario", "", "schema=1 场景 YAML，按时间轴运行；与 --mode/--link/物理参数互斥")
	fs.StringVar(&community, "community", "", "虚构 SNMP community（默认 sim-public，不输出）")
	fs.StringVar(&password, "password", "", "虚构 AYI9 密码，最长 15 字节（不输出）")
	fs.IntVar(&req.AYI9ErrorCode, "ayi9-error-code", 0, "固定 AYI9 错误码，如 103；0 正常")
	fs.BoolVar(&asJSON, "json", false, "以 JSON 输出")
	phys.register(fs)
	name, err := parseNamed(fs, args)
	if err != nil {
		return err
	}
	seen := visited(fs)
	req.Name, req.IP = name, ip
	if !seen["phases"] {
		req.Phases = 0
	}
	req.Params, req.Capacity = phys.collect(seen)
	if req.ScenarioYAML, err = readScenarioFlag(scenario); err != nil {
		return err
	}
	if req.ScenarioYAML == nil || seen["mode"] {
		if req.Mode, err = modeFlag(mode); err != nil {
			return err
		}
	}
	if req.ScenarioYAML == nil || seen["link"] {
		if req.Link, err = linkFlag(link); err != nil {
			return err
		}
	}
	if seen["community"] {
		req.Community = &community
	}
	if seen["password"] {
		req.Password = &password
	}
	c, err := clientFor(*stateFlag)
	if err != nil {
		return err
	}
	var view deviceView
	if err := c.do(http.MethodPost, "/v1/devices", req, &view); err != nil {
		return err
	}
	if asJSON {
		return writeJSONOut(out, view)
	}
	fmt.Fprintf(out, "已添加 %s\n", view.Name)
	printDevice(out, view)
	return nil
}

func setCommand(args []string, out, errout io.Writer) error {
	fs, stateFlag := newFlagSet("set NAME", errout)
	var req patchRequest
	var mode, link, scenario string
	var phys physicalFlags
	var errorCode int
	var asJSON bool
	fs.StringVar(&mode, "mode", "", "切换供电模式：mains、battery、low")
	fs.StringVar(&link, "link", "", "切换通信状态：up、down（down 时三协议都不回包）")
	fs.BoolVar(&req.Flash, "flash", false, "立即触发 5 秒闪断（仅市电模式）")
	fs.IntVar(&errorCode, "ayi9-error-code", 0, "切换 AYI9 固定错误码；0 恢复正常")
	fs.StringVar(&scenario, "scenario", "", "以当前时刻为起点运行新的场景 YAML；不能与其他修改同时使用")
	fs.BoolVar(&asJSON, "json", false, "以 JSON 输出")
	phys.register(fs)
	name, err := parseNamed(fs, args)
	if err != nil {
		var usage usageError
		if errors.As(err, &usage) && strings.Contains(usage.msg, "not defined") {
			return usagef("%s；监听地址、端口、ET1K、相数与凭据不能通过 set 修改，需 remove 后重新 add", usage.msg)
		}
		return err
	}
	seen := visited(fs)
	changes := len(seen)
	for _, meta := range []string{"json", "state-dir"} {
		if seen[meta] {
			changes--
		}
	}
	if changes == 0 {
		return usagef("至少需要修改一项（--mode、--link、--flash、物理参数、--ayi9-error-code 或 --scenario）；监听地址、端口、ET1K、相数与凭据不可修改，需 remove 后重新 add")
	}
	if seen["mode"] {
		if req.Mode, err = modeFlag(mode); err != nil {
			return err
		}
	}
	if seen["link"] {
		if req.Link, err = linkFlag(link); err != nil {
			return err
		}
	}
	if seen["ayi9-error-code"] {
		req.AYI9ErrorCode = &errorCode
	}
	req.Params, req.Capacity = phys.collect(seen)
	if req.ScenarioYAML, err = readScenarioFlag(scenario); err != nil {
		return err
	}
	c, err := clientFor(*stateFlag)
	if err != nil {
		return err
	}
	var view deviceView
	if err := c.do(http.MethodPatch, "/v1/devices/"+url.PathEscape(name), req, &view); err != nil {
		return err
	}
	if asJSON {
		return writeJSONOut(out, view)
	}
	fmt.Fprintf(out, "已修改 %s\n", view.Name)
	printDevice(out, view)
	return nil
}

func listCommand(args []string, out, errout io.Writer) error {
	fs, stateFlag := newFlagSet("list", errout)
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usagef("不接受位置参数: %v", fs.Args())
	}
	c, err := clientFor(*stateFlag)
	if err != nil {
		return err
	}
	var resp struct {
		Devices []deviceView `json:"devices"`
	}
	if err := c.do(http.MethodGet, "/v1/devices", nil, &resp); err != nil {
		return err
	}
	if *asJSON {
		return writeJSONOut(out, resp)
	}
	if len(resp.Devices) == 0 {
		fmt.Fprintln(out, "没有模拟设备。使用 upssim add NAME 添加。")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATUS\tMODE\tLINK\tCAP%\tON_BATT\tSNMP(udp)\tMODBUS(tcp)\tAYI9(tcp)\tPROFILES")
	for _, v := range resp.Devices {
		capacity, onBattery := "-", "-"
		if v.Snapshot != nil {
			capacity = fmt.Sprintf("%.1f", v.Snapshot.BatteryCapacity)
			if !v.Snapshot.Mains {
				onBattery = fmt.Sprintf("%ds", v.Snapshot.SecondsOnBattery)
			}
		}
		mode := string(v.Mode)
		if v.Flashing {
			mode += "(flash)"
		}
		if v.Control == sim.ControlScenario {
			mode += "*"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", v.Name, v.Status, mode, v.Link, capacity, onBattery,
			v.Endpoints.SNMP, v.Endpoints.Modbus, v.Endpoints.AYI9, profileText(v.Profiles))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(out, "MODE 带 * 表示按场景时间轴运行；PROFILES 依次为 SNMP / Modbus / AYI9 的内置 Profile 键。")
	return nil
}

func showCommand(args []string, out, errout io.Writer) error {
	fs, stateFlag := newFlagSet("show NAME", errout)
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	name, err := parseNamed(fs, args)
	if err != nil {
		return err
	}
	c, err := clientFor(*stateFlag)
	if err != nil {
		return err
	}
	var view deviceView
	if err := c.do(http.MethodGet, "/v1/devices/"+url.PathEscape(name), nil, &view); err != nil {
		return err
	}
	if *asJSON {
		return writeJSONOut(out, view)
	}
	fmt.Fprintf(out, "%s\n", view.Name)
	printDevice(out, view)
	return nil
}

func removeCommand(args []string, out, errout io.Writer) error {
	fs, stateFlag := newFlagSet("remove NAME", errout)
	name, err := parseNamed(fs, args)
	if err != nil {
		return err
	}
	c, err := clientFor(*stateFlag)
	if err != nil {
		return err
	}
	if err := c.do(http.MethodDelete, "/v1/devices/"+url.PathEscape(name), nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(out, "已删除 %s\n", name)
	return nil
}

func clientFor(stateFlag string) (*client, error) {
	dir, err := resolveStateDir(stateFlag)
	if err != nil {
		return nil, err
	}
	return dialControl(dir)
}

func writeJSONOut(out io.Writer, v any) error {
	data, err := jsonMarshal(v)
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	return err
}

func profileText(p profilesView) string {
	parts := make([]string, 0, 3)
	for _, v := range []*string{p.SNMP, p.Modbus, p.AYI9} {
		if v == nil {
			parts = append(parts, "-")
		} else {
			parts = append(parts, *v)
		}
	}
	return strings.Join(parts, " / ")
}

func printDevice(out io.Writer, v deviceView) {
	if v.Status != "running" {
		fmt.Fprintf(out, "  状态：%s（%s）\n", v.Status, v.Failure)
	}
	kind := "标准"
	if v.ET1K {
		kind = "ET1K"
	}
	mode := string(v.Mode)
	if v.Flashing {
		mode += "（闪断中）"
	}
	fmt.Fprintf(out, "  控制：%s  模式：%s  通信：%s  型号：%s  相数：%d\n", v.Control, mode, v.Link, kind, v.Phases)
	profile := func(p *string) string {
		if p == nil {
			return "无通用内置 Profile，需自定义"
		}
		return *p
	}
	fmt.Fprintf(out, "  SNMP   udp %-22s Profile: %s\n", v.Endpoints.SNMP, profile(v.Profiles.SNMP))
	fmt.Fprintf(out, "  Modbus tcp %-22s Profile: %s\n", v.Endpoints.Modbus, profile(v.Profiles.Modbus))
	fmt.Fprintf(out, "  AYI9   tcp %-22s Profile: %s\n", v.Endpoints.AYI9, profile(v.Profiles.AYI9))
	fmt.Fprintf(out, "  参数：负载 %.0f%%  温度 %.1f°C  输入 %.0fV  放电 %.0f 分钟  AYI9 错误码 %d  密码已设置 %t\n",
		v.Params.LoadPct, v.Params.Temperature, v.Params.InputVoltage, v.Params.DischargeMinutes, v.AYI9ErrorCode, v.PasswordSet)
	if s := v.Snapshot; s != nil {
		fmt.Fprintf(out, "  快照：市电 %t  低电 %t  电量 %.1f%%  电池供电 %ds  剩余 %ds\n",
			s.Mains, s.LowBattery, s.BatteryCapacity, s.SecondsOnBattery, s.RuntimeSeconds)
	}
	if len(v.SharesIPWith) > 0 {
		fmt.Fprintf(out, "  提示：与 %s 共用 %s，ups-warden 对同一 IP 串行采集，建议改用不同的 loopback IP（如 127.0.0.2）。\n",
			strings.Join(v.SharesIPWith, "、"), v.IP)
	}
}
