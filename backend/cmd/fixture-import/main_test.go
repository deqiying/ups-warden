package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const syntheticQAD = "(222.500 0.0 0.0 49.9 0.0 0.0 0.0 0 220.0 0.0 0.0 49.9 0.0 1.0 0.0 29 0 0 29.400 0 100 55.800 0000000000010011"
const syntheticRTD = "(--- s9 ET1K 220 220 0.0 50 0.0 1/1 1K"
const secretMarker = "synthetic-private-marker"

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func writeTest(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
func vb(oid string, tag int, hexValue string) map[string]any {
	return map[string]any{"oid": oid, "tag": tag, "value_hex": hexValue, "value": 1.2345}
}
func synthPhase(t *testing.T, root, mode string, base time.Time) string {
	t.Helper()
	dir := filepath.Join(root, mode)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	makeExchange := func(protocol, file string, at time.Time, parsed any) map[string]any {
		writeTest(t, filepath.Join(dir, file), []byte(secretMarker))
		return map[string]any{"protocol": protocol, "raw_file": file, "raw_hex": hex.EncodeToString([]byte(secretMarker)), "transport": secretMarker, "community": secretMarker,
			"started_at": at.Format(time.RFC3339Nano), "finished_at": at.Add(time.Millisecond).Format(time.RFC3339Nano), "duration_ms": 1.125, "parsed": parsed}
	}
	rtd := makeExchange("ayi9", "0001-rated-rtd.response.bin", base.Add(-time.Second), map[string]any{"cmd": "RTD", "value": syntheticRTD, "uid": secretMarker})
	writeTest(t, filepath.Join(dir, "0001-rated-rtd.json"), marshal(t, rtd))
	var rows [][]byte
	for i := 0; i < 2; i++ {
		at := base.Add(time.Duration(i) * 3 * time.Second)
		values := []map[string]any{
			vb(upsRoot+"1.1.1.0", 4, hex.EncodeToString([]byte("Generic"))),
			vb(upsRoot+"1.1.2.0", 4, hex.EncodeToString([]byte("ET1K"))),
			vb(upsRoot+"1.1.5.0", 4, hex.EncodeToString([]byte(secretMarker))),
			vb(upsRoot+"1.1.6.0", 4, hex.EncodeToString([]byte(secretMarker))),
			vb("1.3.6.1.2.1.1.5.0", 4, hex.EncodeToString([]byte(secretMarker))),
			vb(upsRoot+"1.2.5.0", 2, "0126"),
			vb(upsRoot+"1.9.1.0", 0x46, "00ffffffffffffffff"),
			vb(upsRoot+"1.9.2.0", 2, "7fffffffffffffff"),
			vb(upsRoot+"1.9.3.0", 2, "8000000000000000"),
			vb(upsRoot+"1.9.4.0", 6, "2b0601020121"),
		}
		for tag, exception := range map[int]string{0x80: "noSuchObject", 0x81: "noSuchInstance", 0x82: "endOfMibView"} {
			x := vb(upsRoot+"1.8."+exception+".0", tag, "")
			// OID 必须为数字；使用 tag 区分三种例外。
			x["oid"] = upsRoot + "1.8." + string(rune('0'+tag-0x80)) + ".0"
			x["exception"] = exception
			values = append(values, x)
		}
		for j := 0; j < 2; j++ {
			x := vb(upsRoot+"1.2.5.0", 0x82, "")
			x["exception"] = "endOfMibView"
			values = append(values, x)
		}
		regs := make([]uint16, 80)
		regs[2], regs[28], regs[8], regs[79] = 2200, 2200, 294, 65535
		qad := makeExchange("ayi9", "qad-"+string(rune('0'+i))+".bin", at, map[string]any{"cmd": "QAD", "value": syntheticQAD, "uid": secretMarker})
		mb := makeExchange("modbus", "modbus-"+string(rune('0'+i))+".bin", at.Add(4*time.Millisecond), map[string]any{"unit_id": 0, "function": 3, "start": 0, "registers": regs})
		sn := makeExchange("snmp", "snmp-"+string(rune('0'+i))+".bin", at.Add(9*time.Millisecond), map[string]any{"varbinds": values, "varbind_count": len(values)})
		r := map[string]any{"sample": i, "requested_mode": mode, "window_started_at": at.Format(time.RFC3339Nano), "window_finished_at": at.Add(10 * time.Millisecond).Format(time.RFC3339Nano), "protocol_results": map[string]any{"qad": qad, "modbus": mb, "snmp": sn}, "rated_file": "0001-rated-rtd.response.bin"}
		rows = append(rows, marshal(t, r))
	}
	// 故意逆序，保证输出不依赖 JSONL 行顺序。
	writeTest(t, filepath.Join(dir, "samples.jsonl"), append(bytes.Join([][]byte{rows[1], rows[0]}, []byte{'\n'}), '\n'))
	return dir
}
func inputs(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	base := time.Date(2026, 10, 14, 12, 0, 0, 123456000, time.UTC)
	return synthPhase(t, root, "mains", base), synthPhase(t, root, "battery", base.Add(17*time.Minute+210*time.Millisecond)), filepath.Join(root, "out.json")
}
func args(m, b, out string) []string { return []string{"--mains", m, "--battery", b, "--out", out} }

func TestImportSanitizesAndPreserves(t *testing.T) {
	m, b, out := inputs(t)
	if err := run(args(m, b, out)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{secretMarker, hex.EncodeToString([]byte(secretMarker)), "raw_hex", "community", "transport"} {
		if bytes.Contains(data, []byte(forbidden)) {
			t.Fatal("脱敏检查失败")
		}
	}
	var c capture
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	if c.Version != 1 || len(c.Samples) != 4 {
		t.Fatal("schema/样本数量错误")
	}
	s := c.Samples[0]
	anchor := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if !s.WindowStarted.Equal(anchor) || s.Index != 0 || c.Samples[1].Index != 1 {
		t.Fatal("锚点或排序错误")
	}
	if s.Modbus.At.Sub(s.AYI9.At) != 4*time.Millisecond || s.SNMP.At.Sub(s.AYI9.At) != 9*time.Millisecond || s.WindowFinished.Sub(s.WindowStarted) != 10*time.Millisecond || s.AYI9.RTDAt.Sub(anchor) != -time.Second || s.AYI9.RTDFinished.Sub(s.AYI9.RTDAt) != time.Millisecond || s.SNMP.Duration != 1.125 {
		t.Fatal("协议时间被改动")
	}
	if c.Samples[2].WindowStarted.Sub(anchor) != 17*time.Minute+210*time.Millisecond {
		t.Fatal("阶段间隔被改动")
	}
	if s.AYI9.QAD != syntheticQAD || s.AYI9.RTD != syntheticRTD || s.AYI9.UID != fixtureUID || s.Modbus.Registers[79] != 65535 {
		t.Fatal("ayi9/寄存器精度丢失")
	}
	v := s.SNMP.Values
	if len(s.SNMP.Duplicates) != 2 || s.SNMP.Duplicates[0].OID != upsRoot+"1.2.5.0" || s.SNMP.Duplicates[0].Value.Exception != "endOfMibView" || s.SNMP.Duplicates[1].Value.Tag != 0x82 {
		t.Fatal("GETBULK 重复 exception 丢失")
	}
	if v[upsRoot+"1.2.5.0"].Int != 294 || v[upsRoot+"1.9.1.0"].Uint != ^uint64(0) || v[upsRoot+"1.9.2.0"].Int != int64(^uint64(0)>>1) || v[upsRoot+"1.9.3.0"].Int != -1<<63 {
		t.Fatal("SNMP 整数精度丢失或未优先使用 value_hex")
	}
	if v[upsRoot+"1.1.5.0"].Text != "ET1K-fixture" || string(v[upsRoot+"1.1.5.0"].Bytes) != "ET1K-fixture" {
		t.Fatal("名称未脱敏")
	}
	for _, oid := range []string{upsRoot + "1.1.6.0", "1.3.6.1.2.1.1.5.0"} {
		if _, ok := v[oid]; ok {
			t.Fatal("非白名单字段未移除")
		}
	}
	for i, exception := range []string{"noSuchObject", "noSuchInstance", "endOfMibView"} {
		x := v[upsRoot+"1.8."+string(rune('0'+i))+".0"]
		if x.Tag != uint8(0x80+i) || x.Exception != exception {
			t.Fatal("SNMP exception 被改动")
		}
	}
	if !bytes.Equal(v[upsRoot+"1.9.4.0"].Bytes, []byte{43, 6, 1, 2, 1, 33}) {
		t.Fatal("OID 原始字节丢失")
	}
	out2 := filepath.Join(filepath.Dir(out), "out2.json")
	if err := run(args(m, b, out2)); err != nil {
		t.Fatal(err)
	}
	data2, err := os.ReadFile(out2)
	if err != nil || !bytes.Equal(data, data2) {
		t.Fatal("输出不可复现")
	}
	if err := run(args(m, b, out)); err == nil {
		t.Fatal("允许了重复覆盖")
	}
	after, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(data, after) {
		t.Fatal("已有输出被改动")
	}
}

func mutateFirst(t *testing.T, dir string, fn func(map[string]any)) {
	t.Helper()
	p := filepath.Join(dir, "samples.jsonl")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	rows := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	var r map[string]any
	if json.Unmarshal(rows[0], &r) != nil {
		t.Fatal("测试数据损坏")
	}
	fn(r)
	rows[0] = marshal(t, r)
	writeTest(t, p, append(bytes.Join(rows, []byte{'\n'}), '\n'))
}
func TestRejectInvalidInputs(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, string)
	}{
		{"missing samples", func(t *testing.T, m string) {
			if err := os.Remove(filepath.Join(m, "samples.jsonl")); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing RTD", func(t *testing.T, m string) {
			if err := os.Remove(filepath.Join(m, "0001-rated-rtd.json")); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing response", func(t *testing.T, m string) {
			if err := os.Remove(filepath.Join(m, "qad-0.bin")); err != nil {
				t.Fatal(err)
			}
		}},
		{"broken JSON", func(t *testing.T, m string) { writeTest(t, filepath.Join(m, "samples.jsonl"), []byte("{")) }},
		{"empty samples", func(t *testing.T, m string) { writeTest(t, filepath.Join(m, "samples.jsonl"), nil) }},
		{"bad RTD", func(t *testing.T, m string) { writeTest(t, filepath.Join(m, "0001-rated-rtd.json"), []byte("null")) }},
		{"missing protocol", func(t *testing.T, m string) {
			mutateFirst(t, m, func(r map[string]any) { delete(r["protocol_results"].(map[string]any), "qad") })
		}},
		{"wrong mode", func(t *testing.T, m string) {
			mutateFirst(t, m, func(r map[string]any) { r["requested_mode"] = "battery" })
		}},
		{"missing index", func(t *testing.T, m string) { mutateFirst(t, m, func(r map[string]any) { delete(r, "sample") }) }},
		{"duplicate index", func(t *testing.T, m string) { mutateFirst(t, m, func(r map[string]any) { r["sample"] = 0 }) }},
		{"bad time", func(t *testing.T, m string) {
			mutateFirst(t, m, func(r map[string]any) { r["window_started_at"] = secretMarker })
		}},
		{"bad register", func(t *testing.T, m string) {
			mutateFirst(t, m, func(r map[string]any) {
				r["protocol_results"].(map[string]any)["modbus"].(map[string]any)["parsed"].(map[string]any)["registers"].([]any)[0] = 65536
			})
		}},
		{"null register", func(t *testing.T, m string) {
			mutateFirst(t, m, func(r map[string]any) {
				r["protocol_results"].(map[string]any)["modbus"].(map[string]any)["parsed"].(map[string]any)["registers"].([]any)[0] = nil
			})
		}},
		{"bad QAD", func(t *testing.T, m string) {
			mutateFirst(t, m, func(r map[string]any) {
				r["protocol_results"].(map[string]any)["qad"].(map[string]any)["parsed"].(map[string]any)["value"] = strings.Replace(syntheticQAD, "222.500", "NaN", 1)
			})
		}},
		{"bad hex", func(t *testing.T, m string) {
			mutateFirst(t, m, func(r map[string]any) {
				r["protocol_results"].(map[string]any)["snmp"].(map[string]any)["parsed"].(map[string]any)["varbinds"].([]any)[0].(map[string]any)["value_hex"] = "zz"
			})
		}},
		{"bad reference", func(t *testing.T, m string) {
			mutateFirst(t, m, func(r map[string]any) { r["rated_file"] = "../elsewhere" })
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, b, out := inputs(t)
			tt.change(t, m)
			err := run(args(m, b, out))
			if err == nil {
				t.Fatal("坏输入未拒绝")
			}
			if strings.Contains(err.Error(), secretMarker) {
				t.Fatal("错误泄露原始数据")
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("错误输入创建了输出")
			}
		})
	}
	for _, a := range [][]string{nil, {"--mains", "https://invalid", "--battery", "b", "--out", "o"}, {"--mains", "m", "--battery", "b", "--out", "o", "extra"}} {
		if run(a) == nil {
			t.Fatal("不合法 CLI 参数未拒绝")
		}
	}
}

func TestSNMPRejectBadScalar(t *testing.T) {
	for _, v := range []map[string]any{
		vb(upsRoot+"1.2.5.0", 2, ""), vb(upsRoot+"1.2.5.0", 2, "008000000000000000"),
		vb(upsRoot+"1.2.5.0", 0x46, "010000000000000000"), vb(upsRoot+"1.2.5.0", 0x41, "ff"),
		vb(upsRoot+"1.2.5.0", 6, "80"), vb(upsRoot+"1.2.5.0", 0x81, "00"), vb(upsRoot+"1.2.5.0", 5, "00"),
	} {
		var raw varbind
		if json.Unmarshal(marshal(t, v), &raw) != nil {
			t.Fatal("测试数据损坏")
		}
		if _, err := decodeSNMP(raw); err == nil {
			t.Fatal("坏 SNMP 标量未拒绝")
		}
	}
}

func TestRejectUnexpectedDeviceNameTag(t *testing.T) {
	m, b, out := inputs(t)
	mutateFirst(t, m, func(r map[string]any) {
		parsed := r["protocol_results"].(map[string]any)["snmp"].(map[string]any)["parsed"].(map[string]any)
		for _, item := range parsed["varbinds"].([]any) {
			value := item.(map[string]any)
			if value["oid"] == upsRoot+"1.1.5.0" {
				value["tag"] = 6
			}
		}
	})
	if err := run(args(m, b, out)); err == nil {
		t.Fatal("异常设备名类型必须拒绝，不能输出原始名称字节")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("拒绝异常输入后不应创建输出")
	}
}
