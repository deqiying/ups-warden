// fixture-import 只从本地采样文件生成最小脱敏回归资料，不连接设备。
package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"ups-warden/internal/driver"
)

const upsRoot = "1.3.6.1.2.1.33."
const fixtureUID = "fixture-et1k@ayi9.com"

// 未解码 raw_hex、transport 或 uid，避免将敏感报文带入输出或错误信息。
type exchange struct {
	Protocol string          `json:"protocol"`
	Started  string          `json:"started_at"`
	Finished string          `json:"finished_at"`
	Duration *float64        `json:"duration_ms"`
	RawFile  string          `json:"raw_file"`
	Parsed   json.RawMessage `json:"parsed"`
}
type rawSample struct {
	Index     *int                `json:"sample"`
	Mode      string              `json:"requested_mode"`
	Started   string              `json:"window_started_at"`
	Finished  string              `json:"window_finished_at"`
	Results   map[string]exchange `json:"protocol_results"`
	RatedFile string              `json:"rated_file"`
}
type stamp struct {
	At       time.Time `json:"at"`
	Finished time.Time `json:"finished_at"`
	Duration float64   `json:"duration_ms"`
}
type duplicateValue struct {
	OID   string           `json:"oid"`
	Value driver.SNMPValue `json:"value"`
}
type sample struct {
	Mode           string    `json:"mode"`
	Index          int       `json:"index"`
	WindowStarted  time.Time `json:"window_started_at"`
	WindowFinished time.Time `json:"window_finished_at"`
	SNMP           struct {
		stamp
		Values     map[string]driver.SNMPValue `json:"values"`
		Duplicates []duplicateValue            `json:"duplicate_values,omitempty"`
	} `json:"snmp"`
	Modbus struct {
		stamp
		Registers []uint16 `json:"registers"`
	} `json:"modbus"`
	AYI9 struct {
		stamp
		QAD         string    `json:"qad"`
		RTD         string    `json:"rtd"`
		RTDAt       time.Time `json:"rtd_at"`
		RTDFinished time.Time `json:"rtd_finished_at"`
		RTDDuration float64   `json:"rtd_duration_ms"`
		UID         string    `json:"uid"`
	} `json:"ayi9"`
}
type capture struct {
	Version int      `json:"version"`
	Samples []sample `json:"samples"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "fixture-import:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("fixture-import", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	mains := fs.String("mains", "", "本地市电采样目录")
	battery := fs.String("battery", "", "本地电池采样目录")
	out := fs.String("out", "", "新的输出文件（拒绝覆盖）")
	if fs.Parse(args) != nil || fs.NArg() != 0 || *mains == "" || *battery == "" || *out == "" {
		return errors.New("必须指定 --mains PATH --battery PATH --out FILE")
	}
	for _, p := range []string{*mains, *battery, *out} {
		if strings.Contains(p, "://") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, `\\`) {
			return errors.New("仅允许本地文件路径")
		}
	}
	ms, err := loadPhase(*mains, "mains")
	if err != nil {
		return fmt.Errorf("mains: %w", err)
	}
	bs, err := loadPhase(*battery, "battery")
	if err != nil {
		return fmt.Errorf("battery: %w", err)
	}
	if !bs[0].WindowStarted.After(ms[len(ms)-1].WindowFinished) {
		return errors.New("电池阶段必须晚于市电阶段")
	}
	all := append(ms, bs...)
	base := ms[0].WindowStarted
	anchor := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	shift := func(t time.Time) time.Time { return anchor.Add(t.Sub(base)) }
	for i := range all {
		s := &all[i]
		s.WindowStarted, s.WindowFinished = shift(s.WindowStarted), shift(s.WindowFinished)
		for _, st := range []*stamp{&s.SNMP.stamp, &s.Modbus.stamp, &s.AYI9.stamp} {
			st.At, st.Finished = shift(st.At), shift(st.Finished)
		}
		s.AYI9.RTDAt, s.AYI9.RTDFinished = shift(s.AYI9.RTDAt), shift(s.AYI9.RTDFinished)
	}
	data, err := json.MarshalIndent(capture{Version: 1, Samples: all}, "", "  ")
	if err != nil {
		return errors.New("输出编码失败")
	}
	// O_EXCL 同时防止误覆盖和检查/创建之间的竞争。
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return errors.New("无法创建输出（文件已存在或路径不可写）")
	}
	_, writeErr := f.Write(append(data, '\n'))
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("写入输出失败；可能存在不完整输出，请人工检查")
	}
	return nil
}

func regular(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("本地文件缺失或不是普通文件")
	}
	return nil
}
func readJSON(path string, dst any) error {
	if err := regular(path); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errors.New("读取本地文件失败")
	}
	if json.Unmarshal(data, dst) != nil {
		return errors.New("JSON 记录损坏")
	}
	return nil
}
func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, errors.New("时间缺失或不合法")
	}
	return t.UTC(), nil
}
func timeStamp(e exchange, protocol string) (stamp, error) {
	a, err := parseTime(e.Started)
	if err != nil {
		return stamp{}, err
	}
	b, err := parseTime(e.Finished)
	if err != nil {
		return stamp{}, err
	}
	if e.Protocol != protocol || b.Before(a) || e.Duration == nil || *e.Duration < 0 || math.IsNaN(*e.Duration) || math.IsInf(*e.Duration, 0) {
		return stamp{}, errors.New("协议或采样时间不合法")
	}
	return stamp{At: a, Finished: b, Duration: *e.Duration}, nil
}
func checkRawFile(dir, name string) error {
	if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
		return errors.New("原始响应文件名缺失或不合法")
	}
	return regular(filepath.Join(dir, name))
}

func loadPhase(dir, mode string) ([]sample, error) {
	var rated exchange
	if err := readJSON(filepath.Join(dir, "0001-rated-rtd.json"), &rated); err != nil {
		return nil, fmt.Errorf("RTD: %w", err)
	}
	rtdStamp, err := timeStamp(rated, "ayi9")
	if err != nil {
		return nil, fmt.Errorf("RTD: %w", err)
	}
	if err := checkRawFile(dir, rated.RawFile); err != nil {
		return nil, fmt.Errorf("RTD: %w", err)
	}
	rtd, err := ayi9Value(rated.Parsed, "RTD", 10)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "samples.jsonl")
	if err := regular(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("无法读取样本文件")
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 4*1024*1024)
	var samples []sample
	line := 0
	for scanner.Scan() {
		line++
		var r rawSample
		if json.Unmarshal(scanner.Bytes(), &r) != nil {
			return nil, fmt.Errorf("第 %d 行 JSON 损坏", line)
		}
		s, err := convert(dir, r, mode, rated.RawFile)
		if err != nil {
			return nil, fmt.Errorf("第 %d 行: %w", line, err)
		}
		s.AYI9.RTD, s.AYI9.RTDAt = rtd, rtdStamp.At
		s.AYI9.RTDFinished, s.AYI9.RTDDuration = rtdStamp.Finished, rtdStamp.Duration
		if rtdStamp.Finished.After(s.WindowStarted) {
			return nil, errors.New("RTD 晚于首轮采样")
		}
		samples = append(samples, s)
	}
	if scanner.Err() != nil {
		return nil, errors.New("样本文件读取失败或行过长")
	}
	if len(samples) == 0 {
		return nil, errors.New("样本文件为空")
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].Index < samples[j].Index })
	for i, s := range samples {
		if s.Index != i {
			return nil, errors.New("样本索引重复或缺失（必须从 0 连续编号）")
		}
		if i > 0 && !s.WindowStarted.After(samples[i-1].WindowFinished) {
			return nil, errors.New("样本时间顺序不合法")
		}
	}
	return samples, nil
}

func convert(dir string, r rawSample, mode, ratedFile string) (sample, error) {
	var s sample
	if r.Index == nil || *r.Index < 0 || r.Mode != mode || len(r.Results) != 3 || r.RatedFile != ratedFile {
		return s, errors.New("索引、模式、协议集合或 RTD 引用不合法")
	}
	s.Mode, s.Index = mode, *r.Index
	var err error
	s.WindowStarted, err = parseTime(r.Started)
	if err != nil {
		return s, err
	}
	s.WindowFinished, err = parseTime(r.Finished)
	if err != nil {
		return s, err
	}
	for _, p := range []struct {
		key, protocol string
		st            *stamp
	}{
		{"qad", "ayi9", &s.AYI9.stamp}, {"modbus", "modbus", &s.Modbus.stamp}, {"snmp", "snmp", &s.SNMP.stamp},
	} {
		e, ok := r.Results[p.key]
		if !ok {
			return s, errors.New("缺失协议结果")
		}
		*p.st, err = timeStamp(e, p.protocol)
		if err != nil {
			return s, err
		}
		if err := checkRawFile(dir, e.RawFile); err != nil {
			return s, err
		}
	}
	if !s.WindowStarted.Equal(s.AYI9.At) || !s.WindowFinished.Equal(s.SNMP.Finished) || s.Modbus.At.Before(s.AYI9.Finished) || s.SNMP.At.Before(s.Modbus.Finished) {
		return s, errors.New("协议串行时间或采样窗口不合法")
	}
	s.AYI9.QAD, err = ayi9Value(r.Results["qad"].Parsed, "QAD", 23)
	if err != nil {
		return s, err
	}
	s.AYI9.UID = fixtureUID
	var mb struct {
		Unit      *int      `json:"unit_id"`
		Function  *int      `json:"function"`
		Start     *int      `json:"start"`
		Registers []*uint16 `json:"registers"`
	}
	if json.Unmarshal(r.Results["modbus"].Parsed, &mb) != nil || mb.Unit == nil || *mb.Unit != 0 || mb.Function == nil || *mb.Function != 3 || mb.Start == nil || *mb.Start != 0 || len(mb.Registers) != 80 {
		return s, errors.New("Modbus 数据不合法（要求 FC03/start=0/80 寄存器）")
	}
	s.Modbus.Registers = make([]uint16, len(mb.Registers))
	for i, reg := range mb.Registers {
		if reg == nil {
			return s, errors.New("Modbus 寄存器值缺失")
		}
		s.Modbus.Registers[i] = *reg
	}
	s.SNMP.Values, s.SNMP.Duplicates, err = snmpValues(r.Results["snmp"].Parsed)
	return s, err
}

var identityToken = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func ayi9Value(raw json.RawMessage, cmd string, count int) (string, error) {
	var v struct {
		Cmd   string `json:"cmd"`
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Cmd != cmd || !strings.HasPrefix(v.Value, "(") {
		return "", errors.New("ayi9 响应损坏")
	}
	fields := strings.Fields(v.Value[1:])
	if len(fields) != count {
		return "", errors.New("ayi9 字段数量不合法")
	}
	for i, token := range fields {
		if cmd == "RTD" && i < 3 {
			if (i == 0 && token != "---") || (i > 0 && !identityToken.MatchString(token)) {
				return "", errors.New("RTD 身份字段不合法")
			}
			continue
		}
		if cmd == "RTD" && i == 8 {
			if token != "1/1" && token != "3/1" && token != "3/3" {
				return "", errors.New("RTD 相数字段不合法")
			}
			continue
		}
		if cmd == "RTD" && i == 9 {
			token = strings.TrimSuffix(strings.TrimSuffix(token, "K"), "k")
		}
		if cmd == "QAD" && i == 22 {
			if len(token) != 16 || strings.Trim(token, "01") != "" {
				return "", errors.New("QAD 状态位不合法")
			}
			continue
		}
		n, err := strconv.ParseFloat(token, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return "", errors.New("ayi9 数字字段不合法")
		}
	}
	// 只校验、不重编码，保留小数位、空格及原始电气数值。
	return v.Value, nil
}

type varbind struct {
	OID       string          `json:"oid"`
	Tag       *uint8          `json:"tag"`
	Hex       *string         `json:"value_hex"`
	Value     json.RawMessage `json:"value"`
	Exception string          `json:"exception"`
}

var oidPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)+$`)

func snmpValues(raw json.RawMessage) (map[string]driver.SNMPValue, []duplicateValue, error) {
	var parsed struct {
		Varbinds []varbind `json:"varbinds"`
		Count    *int      `json:"varbind_count"`
	}
	if json.Unmarshal(raw, &parsed) != nil || len(parsed.Varbinds) == 0 || parsed.Count == nil || *parsed.Count != len(parsed.Varbinds) {
		return nil, nil, errors.New("SNMP varbind 集合不合法")
	}
	result := make(map[string]driver.SNMPValue)
	var duplicates []duplicateValue
	for _, v := range parsed.Varbinds {
		if !oidPattern.MatchString(v.OID) || v.Tag == nil || v.Hex == nil {
			return nil, nil, errors.New("SNMP OID/类型/原始值缺失")
		}
		value, err := decodeSNMP(v)
		if err != nil {
			return nil, nil, err
		}
		if !strings.HasPrefix(v.OID, upsRoot) {
			continue
		}
		if strings.HasPrefix(v.OID, upsRoot+"1.1.") {
			suffix := strings.TrimPrefix(v.OID, upsRoot+"1.1.")
			switch suffix {
			case "1.0", "2.0", "3.0", "4.0":
			case "5.0":
				// 设备名只允许 OCTET STRING 或无载荷的异常/NULL；不能由
				// 非预期 tag 绕过脱敏，将名称字节经其他类型写入派生资料。
				if value.Tag == 4 {
					value.Text = "ET1K-fixture"
					value.Bytes = []byte(value.Text)
				} else if value.Tag != 5 && value.Exception == "" {
					return nil, nil, errors.New("SNMP 设备名类型不合法")
				}
			default:
				continue
			}
		} else if value.Tag == 4 {
			// 非身份组文本不是电气标量；不导出未知自由文本。
			value.Text = "fixture-redacted"
			value.Bytes = []byte(value.Text)
		}
		if _, exists := result[v.OID]; exists {
			// GETBULK 结束时可重复最后一个 OID；保留所有后续 tag/exception，
			// 不用 endOfMibView 覆盖同 OID 首次返回的电气标量。
			duplicates = append(duplicates, duplicateValue{OID: v.OID, Value: value})
		} else {
			result[v.OID] = value
		}
	}
	if len(result) == 0 {
		return nil, nil, errors.New("SNMP 没有 UPS-MIB 数据")
	}
	return result, duplicates, nil
}

func decodeSNMP(v varbind) (driver.SNMPValue, error) {
	var out driver.SNMPValue
	if v.Tag == nil || v.Hex == nil {
		return out, errors.New("SNMP 类型或原始值缺失")
	}
	out.Tag = *v.Tag
	b, err := hex.DecodeString(*v.Hex)
	if err != nil {
		return out, errors.New("SNMP value_hex 损坏")
	}
	switch out.Tag {
	case 2, 0x41, 0x42, 0x43, 0x46:
		if len(b) == 0 || len(b) > 9 {
			return out, errors.New("SNMP 整数长度不合法")
		}
		n := new(big.Int).SetBytes(b)
		if out.Tag == 2 {
			if b[0]&0x80 != 0 {
				n.Sub(n, new(big.Int).Lsh(big.NewInt(1), uint(len(b)*8)))
			}
			if !n.IsInt64() {
				return out, errors.New("SNMP 有符号整数溢出")
			}
			out.Int = n.Int64()
		} else {
			if b[0]&0x80 != 0 || !n.IsUint64() {
				return out, errors.New("SNMP 无符号整数溢出或负数")
			}
			out.Uint = n.Uint64()
		}
	case 4:
		out.Bytes = bytes.Clone(b)
		if utf8.Valid(b) {
			out.Text = string(b)
		}
	case 6:
		// OBJECT IDENTIFIER 保留 BER 字节，不借助浮点中间值。
		if len(b) == 0 || b[len(b)-1]&0x80 != 0 {
			return out, errors.New("SNMP OID 值损坏")
		}
		out.Bytes = bytes.Clone(b)
	case 5:
		if len(b) != 0 {
			return out, errors.New("SNMP NULL 值不合法")
		}
	case 0x80, 0x81, 0x82:
		out.Exception = map[uint8]string{0x80: "noSuchObject", 0x81: "noSuchInstance", 0x82: "endOfMibView"}[out.Tag]
		if len(b) != 0 || v.Exception != out.Exception {
			return out, errors.New("SNMP exception 不合法")
		}
	default:
		return out, errors.New("SNMP tag 不支持")
	}
	if out.Tag < 0x80 && v.Exception != "" {
		return out, errors.New("SNMP exception 与 tag 不一致")
	}
	return out, nil
}
