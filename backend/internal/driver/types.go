// Package driver 定义只读驱动契约与协议原始数据；不依赖型号或 Profile。
package driver

import (
	"context"
	"time"
)

const (
	ProtocolSNMP   = "snmp"
	ProtocolModbus = "modbus"
	ProtocolAYI9   = "ayi9-json"
)

// Driver 只暴露身份探测与采集，没有设备控制方法。
type Driver interface {
	Probe(context.Context) (DeviceInfo, RawData, error)
	Poll(context.Context) (RawData, error)
	Close() error
}

// DeviceInfo 是用于 Profile 推荐的身份信息，不包含连接凭据。
type DeviceInfo struct {
	Model        string `json:"model"`
	Manufacturer string `json:"manufacturer"`
	Firmware     string `json:"firmware"`
	AgentVersion string `json:"agent_version"`
	Identity     string `json:"identity"`
}

// ReadPlan 按协议恰好启用一项；由 Profile 编译，驱动仍须独立验证发送字节。
type ReadPlan struct {
	SNMP   *SNMPPlan
	Modbus *ModbusPlan
	AYI9   *AYI9Plan
}

// SNMPPlan 包含批量读取策略和 GET 所需 OID 列表。
type SNMPPlan struct {
	Strategy       string
	BulkRoot       string
	MaxRepetitions int
	OIDs           []string
}

// ReadFunc 的合法值仅为读保持寄存器与读输入寄存器。
// Go 允许显式类型转换，因此不能以该类型代替发送前白名单检查。
type ReadFunc uint8

const (
	FC03 ReadFunc = 3
	FC04 ReadFunc = 4
)

// ReadBlock 是不跨越可读范围的连续寄存器块；Count 上限为 125。
type ReadBlock struct {
	FC    ReadFunc
	Start uint16
	Count uint16
}

// ModbusPlan 是合并并切块后的只读计划；字节序只在归一化层处理。
type ModbusPlan struct {
	UnitID uint8
	Blocks []ReadBlock
}

// AYI9Plan 每次读取 QAD，按 RTDInterval 刷新额定数据。
type AYI9Plan struct{ RTDInterval time.Duration }

// SNMPValue 保留 BER 类型及原始标量；仅一个值字段随 Tag 生效。
// Tag 使用 BER tag 数值，Exception 为 noSuchName/noSuchObject/noSuchInstance/endOfMibView。
type SNMPValue struct {
	Tag       uint8  `json:"tag"`
	Int       int64  `json:"int,omitempty"`
	Uint      uint64 `json:"uint,omitempty"`
	Text      string `json:"text,omitempty"`
	Bytes     []byte `json:"bytes,omitempty"`
	Exception string `json:"exception,omitempty"`
}

// RegKey 区分不同功能码下相同地址的寄存器。
type RegKey struct {
	FC   ReadFunc
	Addr uint16
}

// AYI9Raw 保留已分词内容和原始字符串；RTDAt 用于识别额定信息刷新时间。
// 段数不符合 QAD=23、RTD=10 时，归一化按 decode_error 处理，不补零。
type AYI9Raw struct {
	UID     string    `json:"uid"`
	QAD     []string  `json:"qad"`
	RTD     []string  `json:"rtd"`
	QADText string    `json:"qad_text,omitempty"`
	RTDText string    `json:"rtd_text,omitempty"`
	RTDAt   time.Time `json:"rtd_at"`
}

// BlockError 是部分采集失败的位置与诊断原因；不能包含密码或 community。
// OID、FC/Start/Count、Command 分别对应三种协议；Kind=unsupported 表示设备明确不支持。
type BlockError struct {
	OID     string
	FC      ReadFunc
	Start   uint16
	Count   uint16
	Command string
	Kind    string
	Message string
}

// RawData 只供归一化与诊断消费，不得存入业务历史表。
type RawData struct {
	Protocol string
	At       time.Time
	SNMP     map[string]SNMPValue
	Modbus   map[RegKey]uint16
	AYI9     *AYI9Raw
	Errors   []BlockError
}
