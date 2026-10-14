// Package profile 描述协议字段到统一模型的声明式映射，并编译只读计划。
package profile

import "ups-warden/internal/model"

// Profile 是 schema=1 的严格声明；所有外部内容必须经 Parse 或 Validate 校验。
type Profile struct {
	Schema     int              `yaml:"schema"`
	Key        string           `yaml:"key"`
	Name       string           `yaml:"name"`
	Version    int              `yaml:"version"`
	Protocol   string           `yaml:"protocol"`
	Match      Match            `yaml:"match"`
	SNMP       *SNMPConfig      `yaml:"snmp"`
	Modbus     *ModbusConfig    `yaml:"modbus"`
	AYI9       *AYI9Config      `yaml:"ayi9"`
	Phases     *Phases          `yaml:"phases"`
	PowerHints model.PowerHints `yaml:"power_hints"`
	Fields     []Mapping        `yaml:"fields"`
	lines      map[string]int
}

// Match 是连接测试身份与 Profile 的匹配条件，不触发设备探测。
type Match struct {
	Models        []string `yaml:"models"`
	AgentVersions []string `yaml:"agent_versions"`
}

// SNMPConfig 限定读取策略，不允许声明 PDU 写操作。
type SNMPConfig struct {
	Strategy       string `yaml:"strategy"`
	BulkRoot       string `yaml:"bulk_root"`
	MaxRepetitions int    `yaml:"max_repetitions"`
}

// ModbusConfig 限定可读区间、单次长度及数值的字节和字顺序。
type ModbusConfig struct {
	Readable            []ReadableRange `yaml:"readable"`
	MaxRegistersPerRead int             `yaml:"max_registers_per_read"`
	ByteOrder           string          `yaml:"byte_order"`
	WordOrder           string          `yaml:"word_order"`
	UnitID              int             `yaml:"unit_id"`
}

// ReadableRange 使用闭区间、0 基地址，仅允许 FC03/FC04。
type ReadableRange struct {
	FC    int `yaml:"fc"`
	Start int `yaml:"start"`
	End   int `yaml:"end"`
}

// AYI9Config 只声明 RTD 刷新周期（秒），QAD 每轮必读。
type AYI9Config struct {
	RTDIntervalSeconds int `yaml:"rtd_interval_s"`
}

// Phases 使用静态相数，或从 RTD 的 input/output 文本读取；两者不能混用。
// 未声明时使用归一化后的 input_phases/output_phases；未知时不能把其他相的零当有效值。
type Phases struct {
	Input  int     `yaml:"input"`
	Output int     `yaml:"output"`
	From   *Source `yaml:"from"`
}

// Source 是协议定位；指针用于区分合法的 0 地址/索引和未提供的键。
type Source struct {
	OID   string `yaml:"oid"`
	FC    *int   `yaml:"fc"`
	Addr  *int   `yaml:"addr"`
	Cmd   string `yaml:"cmd"`
	Index *int   `yaml:"index"`
}

// Mapping 描述一个统一字段的解码与换算。Scale 缺省为 1，显式 0 不被默认值覆盖。
type Mapping struct {
	Field   string            `yaml:"field"`
	Source  Source            `yaml:"source"`
	Type    string            `yaml:"type"`
	Bit     *int              `yaml:"bit"`
	Bits    string            `yaml:"bits"`
	Invert  bool              `yaml:"invert"`
	Scale   *float64          `yaml:"scale"`
	Offset  float64           `yaml:"offset"`
	Unit    string            `yaml:"unit"`
	Enum    map[string]string `yaml:"enum"`
	Invalid InvalidRule       `yaml:"invalid"`
	Note    string            `yaml:"note"`
}

// InvalidRule 的 Values 比较解码后的原始值，Range 校验换算后的统一值（含端点）。
// Placeholders 补充空串和 ---，不能取消这两个默认占位符。
type InvalidRule struct {
	Values       []string  `yaml:"values"`
	Range        []float64 `yaml:"range"`
	Placeholders []string  `yaml:"placeholders"`
}

// RegisterCount 返回 Modbus 解码所需寄存器数；其他类型为 1，语义合法性由 Validate 检查。
func (m Mapping) RegisterCount() int {
	switch m.Type {
	case "uint32", "int32", "float32":
		return 2
	}
	return 1
}
