# ET1K 脱敏回归资料

`capture.json` 是 2026-10-14 本地只读采样的最小脱敏派生资料：**10 组市电 + 50 组电池，共 60 组**，每组包含 SNMP、Modbus FC03 和 ayi9 QAD，另保留各阶段的 RTD 原始值及读取时间。它不是模拟器生成的数据，也不是完整设备抓包。

原件未删除、未修改，仅保留在被 `.gitignore` 忽略的根目录 `data/samples/et1k/`；不随回归资料分发。不要将原件中的地址、凭据、完整 SNMP 报文或真实 UID 复制到仓库。

## 离线生成

在仓库根目录执行，先按 `mise.toml` 准备工具链；将通用占位目录替换为本机采样目录：

```bash
mise x -- go -C backend run ./cmd/fixture-import \
  --mains "../data/samples/et1k/<mains-capture>" \
  --battery "../data/samples/et1k/<battery-capture>" \
  --out "testdata/et1k/<new-capture>.json"

mise x -- go -C backend test ./cmd/fixture-import
```

CLI 只读显式指定的本地文件，不连接设备、不读取 manifest、环境凭据或网络地址。输入必须包含 `samples.jsonl`、`0001-rated-rtd.json` 及记录引用的普通响应文件（仅检查响应文件存在，不读取其内容）。缺失文件、坏记录、不合法字段、非连续索引或不合法协议时间会返回非零退出码；输出父目录必须已存在，**已存在输出绝不覆盖**。重新生成时使用新文件名，人工核验后再按项目流程替换。测试只使用临时目录内的合成输入，不依赖原件或设备。

## 数据格式与脱敏

- 顶层为 `{"version":1,"samples":[...]}`；样本按 `mains`、`battery` 和各阶段原始零基 `index` 排序，JSON map 键稳定排序。
- `snmp.values` 为 UPS-MIB `1.3.6.1.2.1.33.` 下的 OID 到 `driver.SNMPValue` 的映射，保留 `tag/int/uint/text/bytes/exception` 的 JSON 约定。整数优先按 `tag/value_hex` 无损解码，不经浮点中间值；OCTET STRING 和 OBJECT IDENTIFIER 的 `bytes` 用 JSON 标准 base64 表示。
- 身份组仅保留 manufacturer/model/firmware/agent_version（`1.1.1..4`）；设备名（`1.1.5`）改为 `ET1K-fixture`，设备名异常类型会被拒绝，无载荷的 NULL / exception 保留；其余身份字段移除，未知自由文本使用占位。UID 固定为 `fixture-et1k@ayi9.com`。不导出 transport、community、target、完整 `raw_hex` 或原件文件路径。
- GETBULK 会在最后一个 OID 后重复返回 `endOfMibView`。`snmp.values` 保留该 OID 首次返回值，后续同 OID 的结果按原顺序放入可选 `snmp.duplicate_values`（每项 `{"oid":...,"value":SNMPValue}`），避免异常覆盖电气标量，同时不丢失 tag、exception 或重复次数。
- `modbus.registers` 保留全部 80 个原始 `uint16`，数组下标就是零基寄存器地址。`ayi9.qad/rtd` 保留原始 value 字符串，不重排、不重新格式化电气值。
- 第一组市电 `window_started_at` / QAD `at` 平移至 `2000-01-01T00:00:00Z`；所有时间采用同一偏移，不对齐三协议，保留阶段间隔。`at` 表示原 `started_at`；附加 `finished_at`、`duration_ms`、窗口时间以及 `rtd_finished_at/rtd_duration_ms` 保留真实读取耗时。先于首轮采样的市电 RTD 因此位于 1999-12-31，属预期行为。

## 采样边界与重要观察

- 三协议串行读取，**非完全同步**。市电组窗口为 50–72 ms，电池组为 35–75 ms；10 组市电跨 27.004 s，50 组电池跨 147.015 s（均为首尾 QAD 开始时间差）。两阶段首组间隔为 1007.210 s。
- 电池首组 SNMP `upsSecondsOnBattery` 已为 **75 秒**；资料**没有停电切换瞬间，也没有市电恢复阶段**，不可据此断言切换检测延迟或恢复行为。
- 零基 Modbus `reg28 / 10` 与 QAD 输出电压在 **60/60 组完全一致**：市电 219.8–220.1 V，电池 219.8–220.2 V。`reg02 / 10` 始终为 220.0 V，与 QAD 在 30/60 组不相等；因此不能用 `reg02` 的表面接近值替代 `reg28` 的实测输出。内置 ET1K Profile 采用 reg28，详见 [Profile 指南](../../../docs/guides/profiles.md)。
- 各协议有不同的整数缩放、小数精度、截断及采样时差。全部原始电气数值保留，**不为了回归通过而改写、四舍五入或人为对齐**；归一化回归应根据字段单位与协议精度设置容差，并保留缺失/异常语义。

生成后已逐组比对原件与派生资料的电气数值、SNMP tag/exception、各协议/RTD 时间及耗时，并扫描 IP 模式、真实 UID/设备名（含编码形式）和敏感字段。只报告扫描是否通过，不输出命中的敏感内容。
