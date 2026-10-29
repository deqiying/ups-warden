package sim

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/driver/guard"
)

// readObject 在 JSON 解码前限制帧大小，支持无换行帧与分段 TCP，不让 Decoder 无界读取。
func readObject(reader *bufio.Reader) ([]byte, error) {
	var out []byte
	depth := 0
	quoted, escaped := false, false
	for len(out) < maxPacket {
		b, err := reader.ReadByte()
		if err != nil {
			return out, err
		}
		if len(out) == 0 {
			if b == ' ' || b == '\n' || b == '\r' || b == '\t' {
				continue
			}
			if b != '{' {
				return []byte{b}, fmt.Errorf("JSON 请求必须为对象")
			}
		}
		out = append(out, b)
		if quoted {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				quoted = false
			}
			continue
		}
		switch b {
		case '"':
			quoted = true
		case '{', '[':
			depth++
			if depth > 8 {
				return out, fmt.Errorf("JSON 嵌套超出限制")
			}
		case '}', ']':
			depth--
			if depth == 0 {
				return out, nil
			}
		}
	}
	return out, fmt.Errorf("JSON 请求超过大小限制")
}

type ayi9Request struct {
	Cmd     string `json:"cmd"`
	Session string `json:"session,omitempty"`
	Pass    string `json:"pass,omitempty"`
}

func parseAYI9(packet []byte) (ayi9Request, error) {
	var req ayi9Request
	// Token 逐字段检查重复键，拒绝未知写参数，避免 JSON last-key-wins 改变命令含义。
	dec := json.NewDecoder(bytes.NewReader(packet))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return req, fmt.Errorf("无效请求对象")
	}
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return req, err
		}
		key, ok := t.(string)
		if !ok || seen[key] {
			return req, fmt.Errorf("重复或无效 JSON 字段")
		}
		seen[key] = true
		var raw any
		if err := dec.Decode(&raw); err != nil {
			return req, err
		}
		value, ok := raw.(string)
		if !ok {
			return req, fmt.Errorf("JSON 字段必须为字符串")
		}
		switch key {
		case "cmd":
			req.Cmd = value
		case "session":
			req.Session = value
		case "pass":
			req.Pass = value
		default:
			return req, fmt.Errorf("未知 JSON 字段")
		}
	}
	if _, err := dec.Token(); err != nil {
		return req, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return req, fmt.Errorf("JSON 尾随内容")
	}
	if req.Cmd != "QAD" && req.Cmd != "RTD" {
		return req, fmt.Errorf("非只读命令")
	}
	if len(req.Session) > 128 || len(req.Pass) > 128 {
		return req, fmt.Errorf("请求字段超过限制")
	}
	return req, nil
}

func ayi9Values(state State, command string, et1k bool) string {
	model := "UPS-SIM"
	if et1k {
		model = "ET1K"
	}
	number := func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }
	if command == "RTD" {
		return "(" + strings.Join([]string{"SIM", "1.0", model, number(state.NominalInputVoltage), number(state.NominalOutputVoltage), "0", number(state.OutputFrequency), number(state.NominalBatteryVoltage), fmt.Sprintf("%d/%d", state.Phases, state.Phases), number(state.RatedVA)}, " ")
	}
	values := make([]string, 23)
	for i := range values {
		values[i] = "0.0"
	}
	for i := 0; i < 3; i++ {
		values[i] = number(state.InputVoltage[i])
		values[4+i] = number(state.InputCurrent[i])
		values[8+i] = number(state.OutputVoltage[i])
		values[12+i] = number(state.OutputCurrent[i])
		if i < state.Phases {
			values[15+i] = number(state.LoadPct)
		}
	}
	values[3] = number(state.InputFrequency)
	values[11] = number(state.OutputFrequency)
	values[18] = number(state.BatteryVoltage)
	values[19] = strconv.FormatInt(state.RuntimeSeconds, 10)
	values[20] = number(state.BatteryCapacity)
	values[21] = number(state.Temperature)
	bits := uint16(3)
	if !state.Mains {
		bits = 4 | 1<<7
	}
	if state.LowBattery {
		bits |= 1 << 6
	}
	values[22] = fmt.Sprintf("%016b", bits)
	return "(" + strings.Join(values, " ")
}

func (s *Server) serveAYI9(conn net.Conn) {
	reader := bufio.NewReaderSize(conn, maxPacket+1)
	for {
		// 允许正常空闲；帧间换行不是新帧开始，不应触发两秒断连。
		if err := conn.SetDeadline(time.Time{}); err != nil {
			return
		}
		for skipped := 0; ; skipped++ {
			first, err := reader.ReadByte()
			if err != nil {
				return
			}
			if skipped >= maxPacket {
				s.record(driver.ProtocolAYI9, skipped+1, nil, false)
				return
			}
			if first == ' ' || first == '\n' || first == '\r' || first == '\t' {
				continue
			}
			if err := reader.UnreadByte(); err != nil {
				return
			}
			break
		}
		if err := conn.SetDeadline(time.Now().Add(ioTimeout)); err != nil {
			return
		}
		packet, err := readObject(reader)
		if err != nil {
			if len(packet) > 0 {
				s.record(driver.ProtocolAYI9, len(packet), nil, false)
			}
			return
		}
		req, err := parseAYI9(packet)
		if err != nil {
			s.record(driver.ProtocolAYI9, len(packet), nil, false)
			return
		}
		allowed := guard.Validate(driver.ProtocolAYI9, packet) == nil
		var safe []byte
		if allowed {
			clean := ayi9Request{Cmd: req.Cmd, Session: "0", Pass: "redacted"}
			safe, err = json.Marshal(clean)
			if err != nil {
				return
			}
		}
		s.record(driver.ProtocolAYI9, len(packet), safe, allowed)
		// 串行协议：同一批读取中存在第二个对象时，只回复第一条并关闭连接，不执行后续命令。
		pipelined := false
		if reader.Buffered() > 0 {
			pending, _ := reader.Peek(reader.Buffered())
			pipelined = len(bytes.TrimSpace(pending)) > 0
			if pipelined {
				s.record(driver.ProtocolAYI9, len(pending), nil, false)
			}
		}
		state := s.state()
		if !state.Online {
			if pipelined {
				return
			}
			continue
		}
		code := s.ayi9ErrorCode()
		if req.Pass != s.cfg.Password {
			code = 103
		}
		response := map[string]any{"uid": "sim-ups@ayi9.com", "cmd": req.Cmd, "session": req.Session}
		if code != 0 {
			response["error"] = code
			response["code"] = code
			response["value"] = strconv.Itoa(code)
		} else {
			response["value"] = ayi9Values(state, req.Cmd, s.cfg.ET1K)
		}
		data, err := json.Marshal(response)
		if err != nil {
			return
		}
		if _, err := conn.Write(append(data, '\n')); err != nil {
			return
		}
		if pipelined {
			return
		}
	}
}
