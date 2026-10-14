package profile

import (
	"slices"
	"strings"
	"time"
	"ups-warden/internal/driver"
)

func readableUnion(ranges []ReadableRange) []ReadableRange {
	rs := slices.Clone(ranges)
	slices.SortFunc(rs, func(a, b ReadableRange) int {
		if a.FC != b.FC {
			return a.FC - b.FC
		}
		return a.Start - b.Start
	})
	var out []ReadableRange
	for _, r := range rs {
		if len(out) > 0 {
			last := &out[len(out)-1]
			if last.FC == r.FC && r.Start <= last.End+1 {
				last.End = max(last.End, r.End)
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// ReadPlan 编译仅含协议白名单读取的计划，不发送任何请求。
// 重叠多字字段必要时使用重叠块，保证每个字段在至少一个块中完整读取。
func (p *Profile) ReadPlan() (driver.ReadPlan, error) {
	var result driver.ReadPlan
	if err := p.Validate(); err != nil {
		return result, err
	}
	switch p.Protocol {
	case "snmp":
		c := p.snmpConfig()
		oids := map[string]bool{}
		for _, m := range p.Fields {
			oids[m.Source.OID] = true
		}
		list := make([]string, 0, len(oids))
		for oid := range oids {
			list = append(list, oid)
		}
		slices.Sort(list)
		result.SNMP = &driver.SNMPPlan{Strategy: c.Strategy, BulkRoot: c.BulkRoot, MaxRepetitions: c.MaxRepetitions, OIDs: list}
	case "ayi9-json":
		c := p.ayi9Config()
		result.AYI9 = &driver.AYI9Plan{RTDInterval: time.Duration(c.RTDIntervalSeconds) * time.Second}
	case "modbus":
		c := p.Modbus
		plan := &driver.ModbusPlan{UnitID: uint8(c.UnitID)}
		for _, r := range readableUnion(c.Readable) {
			var spans []ReadableRange
			for _, m := range p.Fields {
				if *m.Source.FC == r.FC && *m.Source.Addr >= r.Start && *m.Source.Addr+m.RegisterCount()-1 <= r.End {
					spans = append(spans, ReadableRange{FC: r.FC, Start: *m.Source.Addr, End: *m.Source.Addr + m.RegisterCount() - 1})
				}
			}
			slices.SortFunc(spans, func(a, b ReadableRange) int {
				if a.Start != b.Start {
					return a.Start - b.Start
				}
				return b.End - a.End
			})
			var blocks []ReadableRange
			for _, span := range spans {
				if len(blocks) > 0 {
					last := &blocks[len(blocks)-1]
					end := max(last.End, span.End)
					if span.Start-last.End-1 <= 8 && end-last.Start+1 <= c.MaxRegistersPerRead {
						last.End = end
						continue
					}
				}
				blocks = append(blocks, span)
			}
			for _, b := range blocks {
				plan.Blocks = append(plan.Blocks, driver.ReadBlock{FC: driver.ReadFunc(b.FC), Start: uint16(b.Start), Count: uint16(b.End - b.Start + 1)})
			}
		}
		result.Modbus = plan
	}
	return result, nil
}

// Recommendation 是不含设备地址的 Profile 推荐说明。
type Recommendation struct {
	Key    string
	Score  int
	Reason string
}

// Recommend 按型号精确匹配、Agent 版本、通用 Profile 排序；Modbus 全部留给人工选择。
func Recommend(protocol string, info driver.DeviceInfo, profiles []*Profile) []Recommendation {
	var out []Recommendation
	seen := map[string]bool{}
	for _, p := range profiles {
		if p == nil || p.Protocol != protocol || seen[p.Key] {
			continue
		}
		r := Recommendation{Key: p.Key}
		switch {
		case protocol == "modbus":
			r.Reason = "Modbus 无可验证身份，请人工选择"
		case info.Model != "" && slices.Contains(p.Match.Models, info.Model):
			r.Score = 100
			r.Reason = "型号精确匹配"
		case info.AgentVersion != "" && slices.Contains(p.Match.AgentVersions, info.AgentVersion):
			r.Score = 50
			r.Reason = "Agent 版本匹配"
		case len(p.Match.Models) == 0 && len(p.Match.AgentVersions) == 0:
			r.Score = 10
			r.Reason = "协议通用 Profile"
		default:
			continue
		}
		seen[p.Key] = true
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b Recommendation) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		return strings.Compare(a.Key, b.Key)
	})
	return out
}
