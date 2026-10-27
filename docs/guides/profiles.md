# Profile 与归一化

[返回文档导航](../README.md)

本指南说明 `backend/internal/model`、`profile` 与 `normalize` 的离线契约。Profile 解析、读计划和归一化本身不访问设备。M2 驱动可消费读计划，见[协议驱动与本机模拟器](protocol-drivers.md)；M3 主服务已接入[采集、状态与历史](collection-state-history.md)，启动会采集数据库中启用的数据源。M4 已开放设备与 Profile 管理 HTTP API，完整路由与请求格式见[监控 API 与 SSE 契约](../reference/monitoring-api.md)；CP-2 双方联调待验收，前端无业务页面。

## 内置 Profile

| key | 协议 | 差异 |
|---|---|---|
| `rfc1628-generic` | SNMP | RFC 1628 通用映射；不将有功功率 W 当作视在功率 VA |
| `ayi9-json-generic` | ayi9-json | QAD / RTD 字段字典，相数从 RTD 的 `1/1`、`3/3` 等文本读取 |
| `et1k-snmp` | SNMP | ET1K 缩放与旁路列差异；原生剩余时间 0 无效 |
| `et1k-modbus` | Modbus TCP | FC03、地址 0～79、单相；输出电压使用 reg28 |
| `et1k-ayi9-json` | ayi9-json | ET1K 映射；原生剩余时间 0 无效 |

内置文件位于 `backend/internal/profile/builtin/`，随二进制 embed；`Builtins()` 每次返回独立实例，修改返回对象不会改写内置数据。`IsBuiltinKey` 标识保留键；M4 持久化接口拒绝覆盖、修改或删除内置 Profile，需先复制为新的自定义 key。自定义 Profile 支持新建/导入、复制、更新、删除、导出与校验；更新保持 key/protocol 不变且 version 必须递增，引用源的 profile_version 与配置审计同事务更新，再恢复受影响采集。被数据源引用时拒绝删除。

## 最小示例

```yaml
schema: 1
key: example-modbus
name: 单相只读示例
version: 1
protocol: modbus
modbus:
  unit_id: 0
  readable:
    - {fc: 3, start: 0, end: 79}
  max_registers_per_read: 100
  byte_order: big
  word_order: big
phases:
  input: 1
  output: 1
power_hints:
  indicators: [input_voltage]
fields:
  - field: battery_voltage
    source: {fc: 3, addr: 8}
    type: uint16
    scale: 0.1
  - field: "input_voltage[1]"
    source: {fc: 3, addr: 17}
    type: uint16
    scale: 0.1
```

这只是解析示例，不构成新设备地址映射或设备访问授权。型号地址必须有独立证据，不通过扫描或枚举发现。

## 校验与默认值

- `Parse([]byte)` 接受单个 YAML 或 JSON 对象，最大 256 KiB；拒绝未知键、重复键、额外文档、`null`、alias / merge。错误包含行号和字段路径；程序直接构造的 Profile 无原始行号，使用 `line 0`。
- `schema` 必须为 1；`key` 使用小写 ASCII 字母、数字、`_`、`-`，且首字符为字母或数字；`version` 为正整数。
- 字段名必须存在于统一目录；按相字段必须显式使用 `[1]`、`[2]` 或 `[3]`；同字段不能重复，也不能映射 `power_state`、`runtime_estimate` 等状态引擎派生字段。
- SNMP 默认 `strategy: getbulk`、`bulk_root: 1.3.6.1.2.1.33`、`max_repetitions: 60`；来源必须是合法 OID。
- Modbus 必须声明可读范围，地址为 0 基、范围端点均包含。默认每次最多 100 个寄存器，上限 125；默认 `byte_order: big`、`word_order: big`、Unit ID 0。
- ayi9 默认 `ayi9.rtd_interval_s: 600`；每轮计划读取 QAD，RTD 按周期刷新；只接受 QAD 索引 0～22、RTD 索引 0～9。
- `scale` 缺省为 1、`offset` 缺省为 0；显式 `scale: 0` 不会被当成省略。数值必须有限，范围必须由两个有序有限数构成。
- `power_hints.indicators` 只接受统一字段 `ups_mode`、`mains_ok`、`output_source`、`native_seconds_on_battery`、`input_voltage`，不能写协议地址或派生状态。

## 解码与有效性

| 协议 | 当前映射类型 |
|---|---|
| SNMP | `int`、`string` |
| Modbus | `uint16`、`int16`、`uint32`、`int32`、`float32` |
| ayi9-json | `string`、`number_string`、`si_number`、`bitstring16` |

- `uint32` / `int32` / `float32` 占两个寄存器；字节序控制每个寄存器内部两个字节，字顺序控制两个寄存器排列。
- `bit` 与 `bits` 互斥，范围 0～15；`bits: "0-3"` 包含两端。`bitstring16` 的左端对应 b15，必须恰好包含 16 个二进制字符。
- 布尔字段必须提取单个位，可用 `invert: true` 取反；枚举必须声明原始值到目录枚举的映射。未命中的枚举为 `unknown`，质量 `invalid`，原因 `enum_unmapped`；原始编码只在 Trace 中保留。
- `si_number` 将 `1K` 转为 1000。换算公式为 `(decoded × scale + offset) × unit_factor`。原单位为空表示已使用目录单位；支持 `min→s`、`ms→s`、`kV/mV→V`、`mA→A`、`kW→W`、`kVA→VA`，不做 W 与 VA 的互换。
- `invalid.values` 比较**解码后的原始值**，如寄存器哨兵 `65535`；未加引号的 YAML 数字先按其数值语义规范化，`0xFFFF`、`0b1111111111111111` 与十进制 `65535` 等价，带引号的字符串保持文本语义。`invalid.range` 校验**换算后的统一值**，端点包含。`invalid.placeholders` 补充默认占位符空串与 `---`，不能取消默认占位符。
- 百分比限制在 0～100，时长不能为负，输出为整数秒；NaN、Infinity、溢出、缺失值不转换成有效零。
- 相数可使用静态 `phases.input/output`，或 `phases.from: {cmd: RTD, index: 8}`，不能混用。未声明时使用映射得到的 `input_phases` / `output_phases`；旁路按输入相数处理。有效相数之外标记 `unsupported/phase_inactive`；相数损坏时不信任按相观测；相数未知时不将第二、第三相的数值标成 Good。

统一目录包含 49 个基础字段、展开为 69 个字段实例；单源快照包含 64 个观测字段，5 个派生字段留给状态引擎。质量值使用 `good/invalid/unsupported/stale` 字符串；零值质量不等于 `good`。未映射观测字段为 `unsupported/not_mapped`。

`Normalize(sourceID, raw, profile)` 是纯函数，返回独立 `SourceSnapshot`、有序 Trace 和错误。调用者须先通过 `Parse` / `Validate` 建立有效 Profile，并在使用期间保持只读；程序修改 Profile 后必须重新校验。归一化不逐轮重新校验配置；缺少 Profile 或协议不匹配时整体失败，单字段报文解码失败只降低该字段质量。采集 worker 独占加载后的 Profile，配置变更经停止旧 worker、重载校验再启用的流程生效。业务层只能消费统一快照，不能消费包含 OID、寄存器和原始值的 Trace。

## 读计划与推荐

`Profile.ReadPlan()` 每次重新校验，不执行网络请求：

- SNMP 收集并去重映射 OID，提供 GET 列表和批量策略参数。
- Modbus 按功能码和地址排序，合并间隙不超过 8 个寄存器的读取，限制单块长度且不跨不可读空洞。多寄存器字段必须在至少一个块中完整读取；重叠字段必要时使用重叠块。
- ayi9 仅生成固定 QAD / RTD 读取周期，没有任意指令字段。

推荐是本地身份匹配：型号精确匹配优先于 Agent 版本匹配，再回退到协议通用 Profile；相同得分按 key 排序。Modbus 缺少可信身份时列出全部协议候选，交由用户选择，不自动认定型号。

Profile 校验是第一道保护，**不能替代驱动发送前的字节白名单**。M2 已实现独立 guard 与安全测试，但不得将合法读计划或模拟器验收误述为已完成真机验收。

## 回归证据与限制

[ET1K fixture 说明](../../backend/testdata/et1k/README.md)记录脱敏方式、导入工具和来源。真实原件长期保留在本地被忽略的 `data/samples/et1k/`，不删除、不直接提交；回归数据只保留测试所需的协议值，移除地址、凭据和真实设备标识，整体平移时间但保留间隔。

2026-10-14 的 10 组市电与 50 组电池样本确认：ET1K `output_source` 在这两阶段为 3 / 5；reg28 与 QAD 输出电压一致，reg02 存在最高 0.2 V 的差异。SNMP 电压和温度为整数，比较时按记录精度保留容差，不篡改原始样本。功率缩放等待校准项继续保留注释，不把本轮回归当作面板校准。

2026-10-27 的 CP-1 真机只读冒烟（市电、断电、恢复三个阶段，见[开发计划 M2 实施记录](../planning/backend-plan.md)）确认了以下 ET1K 映射事实：

- 三协议的供电判定字段都能跟随断电和恢复：SNMP `output_source` 为 normal/battery，AYI9 `ups_mode` 为 line/battery、`mains_ok` 为 true/false；Modbus 没有状态字段，靠 `input_voltage` 归零判断。
- 厂商名是占位符；原生剩余时间（SNMP、QAD[19]）在电池阶段也恒为 0，按无效处理，剩余时间由状态引擎估算。AYI9 RTD[7] 额定电池电压恒为 0.0，`et1k-ayi9-json` 将 0 视为无效（`ayi9-json-generic` 没有同类证据，不改）。
- 网卡页面脚本显示，单相时 QAD[12] 是输出电流（A），QAD[13]、QAD[14] 分别是总功率、负载（kW），不是 S/T 相电流。现有映射按三相电流处理，单相时会被相数屏蔽，不产生错误数据；如需这两个 kW 值，应另加映射，并评估内置 Profile 版本变更对已有数据源的影响。
- Modbus reg23 `input_power` 断电时归零，说明对应输入侧，但约 2.8k 的量值仍待校准。

2026-10-14 样本不覆盖断电瞬间、低电告警及错误密码响应；CP-1 只在断电约 1 分钟后和恢复市电后读取，同样不覆盖切换瞬间。这些仍需模拟器场景或后续独立验收。自动化测试不依赖本地原件或真实设备。
