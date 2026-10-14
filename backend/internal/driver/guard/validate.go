package guard

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"strconv"

	"ups-warden/internal/driver"
	"ups-warden/internal/driver/snmp/ber"
)

// Validate 只接受一个完整且有界的只读请求，错误不包含报文或凭据。
func Validate(protocol string, p []byte) error {
	if len(p) == 0 || len(p) > 65535 {
		return ErrWriteBlocked
	}
	switch protocol {
	case driver.ProtocolSNMP:
		m, err := ber.Decode(p)
		if err != nil {
			return ErrWriteBlocked
		}
		if m.Version != 0 && m.Version != 1 || len(m.VarBinds) == 0 || m.RequestID < 0x00800000 {
			return ErrWriteBlocked
		}
		switch m.PDU {
		case ber.GetRequest, ber.GetNextRequest:
			if m.ErrorStatus != 0 || m.ErrorIndex != 0 {
				return ErrWriteBlocked
			}
		case ber.GetBulkRequest:
			if m.Version != 1 || m.ErrorStatus < 0 || m.ErrorIndex < 1 || m.ErrorIndex > 65535 {
				return ErrWriteBlocked
			}
		default:
			return ErrWriteBlocked
		}
		for _, v := range m.VarBinds {
			if v.Value.Tag != 5 || len(v.Value.Bytes) != 0 || v.Value.Exception != "" {
				return ErrWriteBlocked
			}
		}
		return nil
	case driver.ProtocolModbus:
		if len(p) != 12 || binary.BigEndian.Uint16(p[2:4]) != 0 || binary.BigEndian.Uint16(p[4:6]) != 6 || (p[7] != 3 && p[7] != 4) {
			return ErrWriteBlocked
		}
		count := int(binary.BigEndian.Uint16(p[10:12]))
		start := int(binary.BigEndian.Uint16(p[8:10]))
		if count < 1 || count > 125 || start+count > 65536 {
			return ErrWriteBlocked
		}
		return nil
	case driver.ProtocolAYI9:
		if len(p) > 4096 {
			return ErrWriteBlocked
		}
		dec := json.NewDecoder(bytes.NewReader(p))
		token, err := dec.Token()
		if err != nil || token != json.Delim('{') {
			return ErrWriteBlocked
		}
		fields := map[string]string{}
		for dec.More() {
			token, err = dec.Token()
			if err != nil {
				return ErrWriteBlocked
			}
			key, ok := token.(string)
			if !ok {
				return ErrWriteBlocked
			}
			if key != "cmd" && key != "session" && key != "pass" {
				return ErrWriteBlocked
			}
			if _, ok := fields[key]; ok {
				return ErrWriteBlocked
			}
			var value any
			if dec.Decode(&value) != nil {
				return ErrWriteBlocked
			}
			text, ok := value.(string)
			if !ok {
				return ErrWriteBlocked
			}
			fields[key] = text
		}
		token, err = dec.Token()
		if err != nil || token != json.Delim('}') {
			return ErrWriteBlocked
		}
		if _, err = dec.Token(); err != io.EOF {
			return ErrWriteBlocked
		}
		if fields["cmd"] != "QAD" && fields["cmd"] != "RTD" {
			return ErrWriteBlocked
		}
		session := fields["session"]
		if len(session) == 0 || len(session) > 20 {
			return ErrWriteBlocked
		}
		for _, c := range session {
			if c < '0' || c > '9' {
				return ErrWriteBlocked
			}
		}
		if _, err := strconv.ParseUint(session, 10, 64); err != nil {
			return ErrWriteBlocked
		}
		if len(fields["pass"]) > 15 {
			return ErrWriteBlocked
		}
		return nil
	default:
		return ErrWriteBlocked
	}
}
