package sim

import (
	"fmt"
	"math"
	"sort"
	"time"

	"ups-warden/internal/driver"
	"ups-warden/internal/driver/guard"
	"ups-warden/internal/driver/snmp/ber"
)

func snmpValues(state State, et1k bool) []ber.VarBind {
	values := map[string]driver.SNMPValue{}
	integer := func(oid string, v float64) {
		values["1.3.6.1.2.1.33."+oid] = driver.SNMPValue{Tag: 2, Int: int64(math.Round(v))}
	}
	text := func(oid, v string) { values["1.3.6.1.2.1.33."+oid] = driver.SNMPValue{Tag: 4, Text: v} }
	model, agent := "UPS-SIM", "sim v1"
	if et1k {
		model = "ET1K"
		agent = "ayi9 v1"
	}
	text("1.1.1.0", "SIM")
	text("1.1.2.0", model)
	text("1.1.3.0", "1.0")
	text("1.1.4.0", agent)
	text("1.1.5.0", "UPS-SIM")
	status := 2
	if state.LowBattery {
		status = 3
	}
	if state.BatteryCapacity == 0 {
		status = 4
	}
	integer("1.2.1.0", float64(status))
	integer("1.2.2.0", float64(state.SecondsOnBattery))
	integer("1.2.3.0", float64(state.RuntimeSeconds)/60)
	integer("1.2.4.0", state.BatteryCapacity)
	integer("1.2.5.0", state.BatteryVoltage*10)
	integer("1.2.6.0", 0)
	integer("1.2.7.0", state.Temperature)
	integer("1.3.2.0", float64(state.Phases))
	integer("1.4.3.0", float64(state.Phases))
	integer("1.5.2.0", float64(state.Phases))
	source := 3
	if !state.Mains {
		source = 5
	}
	integer("1.4.1.0", float64(source))
	integer("1.4.2.0", state.OutputFrequency*10)
	integer("1.5.1.0", state.InputFrequency*10)
	for i := 0; i < state.Phases; i++ {
		phase := i + 1
		integer(fmt.Sprintf("1.3.3.1.1.%d", phase), float64(phase))
		integer(fmt.Sprintf("1.3.3.1.2.%d", phase), state.InputFrequency*10)
		integer(fmt.Sprintf("1.3.3.1.3.%d", phase), state.InputVoltage[i])
		integer(fmt.Sprintf("1.3.3.1.4.%d", phase), state.InputCurrent[i]*10)
		power := state.OutputW[i]
		if !state.Mains {
			power = 0
		}
		integer(fmt.Sprintf("1.3.3.1.5.%d", phase), power)
		integer(fmt.Sprintf("1.4.4.1.1.%d", phase), float64(phase))
		integer(fmt.Sprintf("1.4.4.1.2.%d", phase), state.OutputVoltage[i])
		integer(fmt.Sprintf("1.4.4.1.3.%d", phase), state.OutputCurrent[i]*10)
		// RFC 列 4 为 W；只有 ET1K 怪癖模式按既有 Profile 输出 VA×10。
		power = state.OutputW[i]
		if et1k {
			power = state.OutputVA[i] * 10
		}
		integer(fmt.Sprintf("1.4.4.1.4.%d", phase), power)
		integer(fmt.Sprintf("1.4.4.1.5.%d", phase), state.LoadPct)
		integer(fmt.Sprintf("1.5.3.1.1.%d", phase), float64(phase))
		column := 3
		if et1k {
			column = 2
		}
		integer(fmt.Sprintf("1.5.3.1.%d.%d", column, phase), state.BypassVoltage[i])
		integer(fmt.Sprintf("1.5.3.1.%d.%d", column+1, phase), state.InputCurrent[i]*10)
		integer(fmt.Sprintf("1.5.3.1.%d.%d", column+2, phase), state.OutputW[i])
	}
	integer("1.9.1.0", state.NominalInputVoltage)
	integer("1.9.2.0", 500)
	integer("1.9.3.0", state.NominalOutputVoltage)
	integer("1.9.4.0", state.OutputFrequency*10)
	integer("1.9.5.0", state.RatedVA)
	integer("1.9.6.0", state.RatedW)
	out := make([]ber.VarBind, 0, len(values))
	for oid, value := range values {
		out = append(out, ber.VarBind{OID: oid, Value: value})
	}
	sort.Slice(out, func(i, j int) bool { return ber.CompareOID(out[i].OID, out[j].OID) < 0 })
	return out
}

func nextBinding(values []ber.VarBind, oid string) ber.VarBind {
	index := sort.Search(len(values), func(i int) bool { return ber.CompareOID(values[i].OID, oid) > 0 })
	if index == len(values) {
		return ber.VarBind{OID: oid, Value: driver.SNMPValue{Tag: 0x82, Exception: "endOfMibView"}}
	}
	return values[index]
}

func (s *Server) serveSNMP() {
	buffer := make([]byte, maxPacket+1)
	for {
		n, peer, err := s.udp.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		if n > maxPacket {
			s.record(driver.ProtocolSNMP, n, nil, false)
			continue
		}
		req, err := ber.Decode(buffer[:n])
		if err != nil {
			s.record(driver.ProtocolSNMP, n, nil, false)
			continue
		}
		allowed := guard.Validate(driver.ProtocolSNMP, buffer[:n]) == nil
		var safe []byte
		if allowed {
			clean := req
			clean.Community = "redacted"
			safe, err = ber.Encode(clean)
			if err != nil {
				allowed = false
			}
		}
		s.record(driver.ProtocolSNMP, n, safe, allowed)
		// 短 ID 可被解码但 ET1K 静默丢弃；community 不匹配也不返回响应。
		if req.Community != s.cfg.Community || s.cfg.ET1K && req.RequestIDBytes == 1 {
			continue
		}
		if req.PDU != ber.GetRequest && req.PDU != ber.GetNextRequest && req.PDU != ber.GetBulkRequest {
			continue
		}
		if len(req.VarBinds) == 0 || len(req.VarBinds) > 64 {
			continue
		}
		if req.PDU == ber.GetBulkRequest && (req.ErrorIndex < 1 || req.ErrorIndex > 100 || req.ErrorStatus > 64) {
			continue
		}
		if req.PDU != ber.GetBulkRequest && (req.ErrorStatus != 0 || req.ErrorIndex != 0) {
			continue
		}
		state := s.state()
		if !state.Online {
			continue
		}
		values := snmpValues(state, s.cfg.ET1K)
		resp := ber.Message{Version: req.Version, Community: req.Community, PDU: ber.Response, RequestID: req.RequestID}
		switch req.PDU {
		case ber.GetRequest:
			if s.cfg.ET1K {
				resp.ErrorStatus = 2
				resp.ErrorIndex = 1
				resp.VarBinds = req.VarBinds
				break
			}
			for i, v := range req.VarBinds {
				found := false
				for _, candidate := range values {
					if candidate.OID == v.OID {
						resp.VarBinds = append(resp.VarBinds, candidate)
						found = true
						break
					}
				}
				if !found {
					if req.Version == 0 {
						resp.ErrorStatus = 2
						resp.ErrorIndex = i + 1
						resp.VarBinds = req.VarBinds
						break
					}
					resp.VarBinds = append(resp.VarBinds, ber.VarBind{OID: v.OID, Value: driver.SNMPValue{Tag: 0x80, Exception: "noSuchObject"}})
				}
			}
		case ber.GetNextRequest:
			list := req.VarBinds
			if s.cfg.ET1K {
				list = list[:1]
			}
			for i, v := range list {
				next := nextBinding(values, v.OID)
				if req.Version == 0 && next.Value.Tag == 0x82 {
					resp.ErrorStatus = 2
					resp.ErrorIndex = i + 1
					resp.VarBinds = req.VarBinds
					break
				}
				resp.VarBinds = append(resp.VarBinds, next)
			}
		case ber.GetBulkRequest:
			nonrepeaters := min(req.ErrorStatus, len(req.VarBinds))
			for _, v := range req.VarBinds[:nonrepeaters] {
				resp.VarBinds = append(resp.VarBinds, nextBinding(values, v.OID))
			}
			cursors := append([]ber.VarBind(nil), req.VarBinds[nonrepeaters:]...)
			for repetition := 0; repetition < req.ErrorIndex; repetition++ {
				for i, v := range cursors {
					if len(resp.VarBinds) >= 512 {
						break
					}
					next := nextBinding(values, v.OID)
					resp.VarBinds = append(resp.VarBinds, next)
					cursors[i] = next
				}
				if len(resp.VarBinds) >= 512 {
					break
				}
			}
		}
		out, err := ber.Encode(resp)
		if err != nil {
			continue
		}
		if len(out) > maxPacket {
			resp.ErrorStatus = 1
			resp.ErrorIndex = 0
			resp.VarBinds = nil
			out, err = ber.Encode(resp)
			if err != nil {
				continue
			}
		}
		if err := s.udp.SetWriteDeadline(time.Now().Add(ioTimeout)); err != nil {
			return
		}
		if _, err := s.udp.WriteToUDP(out, peer); err != nil {
			continue
		} // 客户端关闭或 UDP 发送失败不终止其他请求。
	}
}
