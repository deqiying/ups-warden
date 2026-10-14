package sim

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"time"

	"ups-warden/internal/driver"
)

// ParseRegisters 解析独立 YAML 寄存器表：schema: 1，registers: [{fc: 3, addr: 100, value: 2200}]。
// 表项最多 4096 个，不允许重复地址或写功能码；非 nil 表完全替换默认表。
func ParseRegisters(data []byte) (map[driver.RegKey]uint16, error) {
	var doc struct {
		Schema    int `yaml:"schema"`
		Registers []struct {
			FC    driver.ReadFunc `yaml:"fc"`
			Addr  uint16          `yaml:"addr"`
			Value uint16          `yaml:"value"`
		} `yaml:"registers"`
	}
	if err := strictYAML(data, &doc); err != nil {
		return nil, err
	}
	if doc.Schema != 1 || len(doc.Registers) > 4096 {
		return nil, fmt.Errorf("寄存器 schema 必须为 1 且不超过 4096 项")
	}
	result := make(map[driver.RegKey]uint16, len(doc.Registers))
	for _, r := range doc.Registers {
		if r.FC != driver.FC03 && r.FC != driver.FC04 {
			return nil, fmt.Errorf("寄存器只允许 FC03/FC04")
		}
		key := driver.RegKey{FC: r.FC, Addr: r.Addr}
		if _, ok := result[key]; ok {
			return nil, fmt.Errorf("寄存器地址重复")
		}
		result[key] = r.Value
	}
	return result, nil
}

func scaled(v, scale float64) uint16 {
	return uint16(math.Round(math.Max(0, math.Min(65535, v*scale))))
}
func registers(state State) map[driver.RegKey]uint16 {
	regs := make(map[driver.RegKey]uint16, 80)
	for addr := uint16(0); addr < 80; addr++ {
		regs[driver.RegKey{FC: driver.FC03, Addr: addr}] = 0
	}
	values := map[uint16]uint16{
		8: scaled(state.BatteryVoltage, 10), 10: scaled(state.BatteryCapacity, 10), 13: scaled(state.Temperature, 10),
		16: scaled(state.InputFrequency, 10), 17: scaled(state.InputVoltage[0], 10), 23: scaled(state.OutputVA[0], 1),
		27: scaled(state.OutputFrequency, 10), 28: scaled(state.OutputVoltage[0], 10), 34: scaled(state.OutputVA[0], 10),
		45: scaled(state.LoadPct, 10), 49: scaled(state.InputFrequency, 10), 50: scaled(state.BypassVoltage[0], 10),
	}
	for addr, value := range values {
		regs[driver.RegKey{FC: driver.FC03, Addr: addr}] = value
	}
	return regs
}

func (s *Server) serveModbus(conn net.Conn) {
	for {
		// 空闲长连接允许跨越轮询间隔；只有开始收帧后才限制收帧时间。
		if err := conn.SetDeadline(time.Time{}); err != nil {
			return
		}
		header := make([]byte, 7)
		if _, err := io.ReadFull(conn, header[:1]); err != nil {
			return
		}
		if err := conn.SetDeadline(time.Now().Add(ioTimeout)); err != nil {
			return
		}
		n, err := io.ReadFull(conn, header[1:])
		n++
		if err != nil {
			if n > 0 {
				s.record(driver.ProtocolModbus, n, nil, false)
			}
			return
		}
		length := int(binary.BigEndian.Uint16(header[4:6]))
		if binary.BigEndian.Uint16(header[2:4]) != 0 || length < 2 || length > 254 {
			s.record(driver.ProtocolModbus, 7, nil, false)
			return
		}
		packet := make([]byte, 6+length)
		copy(packet, header)
		n, err = io.ReadFull(conn, packet[7:])
		if err != nil {
			s.record(driver.ProtocolModbus, 7+n, nil, false)
			return
		}
		fc := packet[7]
		allowed := len(packet) == 12 && (fc == 3 || fc == 4)
		var start, count uint16
		if allowed {
			start = binary.BigEndian.Uint16(packet[8:10])
			count = binary.BigEndian.Uint16(packet[10:12])
			allowed = count > 0 && count <= 125 && uint32(start)+uint32(count) <= 65536
		}
		if allowed {
			s.record(driver.ProtocolModbus, len(packet), packet, true)
		} else {
			s.record(driver.ProtocolModbus, len(packet), nil, false)
		}
		state := s.state()
		if !state.Online {
			continue
		}
		exception := byte(0)
		switch {
		case fc != 3 && fc != 4:
			exception = 1
		case !allowed:
			exception = 3
		case s.cfg.ET1K && fc != 3:
			exception = 1
		case s.cfg.ET1K && uint32(start)+uint32(count) > 80:
			exception = 2
		}
		table := s.cfg.Registers
		if table == nil {
			table = registers(state)
		}
		response := []byte{fc, byte(count * 2)}
		if exception == 0 {
			for i := uint16(0); i < count; i++ {
				v, ok := table[driver.RegKey{FC: driver.ReadFunc(fc), Addr: start + i}]
				if !ok {
					exception = 2
					break
				}
				response = binary.BigEndian.AppendUint16(response, v)
			}
		}
		if exception != 0 {
			response = []byte{fc | 0x80, exception}
		}
		out := append([]byte(nil), header...)
		binary.BigEndian.PutUint16(out[4:6], uint16(len(response)+1))
		out = append(out, response...)
		if _, err := conn.Write(out); err != nil {
			return
		}
	}
}
