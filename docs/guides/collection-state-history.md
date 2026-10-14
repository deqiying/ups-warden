# 采集、状态与历史

[文档导航](../README.md) · [开发指南](development.md) · [协议驱动与模拟器](protocol-drivers.md)

## 当前能力与安全边界

M3 已将 `collector → state → history / bus` 接入主服务。当前启动先校验凭据和 Profile，启动历史任务并恢复启用设备的状态 actor，再启动启用源的只读采集，最后开放 HTTP。**已有数据库中启用的设备与数据源会自动连接；空数据库不连接任何设备。** 开发测试只使用临时数据库、假驱动和 loopback 模拟器，不读取本地生产数据目录或环境凭据。

M4 已开放初始化/认证、设备/源/Profile 管理、连接 test/raw、快照/历史/事件告警/审计查询与 SSE，见[监控 API 与 SSE 契约](../reference/monitoring-api.md)；以下模块 API 仍指 Go 内部契约。静态 SPA 已内嵌托管，但前端仍为脚手架、无业务页面。M4 本机回归与 Windows 临时库 + loopback 冒烟已通过；CP-2 契约已交付，双方联调待验收。CP-1 真机验收仍待另行授权执行，本机通过不能替代真实网卡兼容性验证。

## 采集调度

实现入口：[`internal/collector`](../../backend/internal/collector/supervisor.go)。

- `Start` 从数据库读取启用设备及启用数据源；P0 每设备只能有一个启用数据源，冲突或非法配置会阻止启动。
- 内置 Profile 优先于同名数据库项；自定义 Profile 必须通过解析、语义校验，且 key/protocol/version 与数据源匹配。
- `sources.secret` 使用 AAD `sources.secret` 加密；明文为单个 SNMP community 或 AYI9 password 字符串，不是 JSON。原始数据与 Trace 仅保存最近一次尝试的内存副本，不落库。
- `params` 严格拒绝未知键、重复键、null 和尾随 JSON。SNMP 只接受 `version`（`1` / `2c`）；Modbus 只接受 `unit_id`（省略则用 Profile）；AYI9 只接受空对象。
- 同 canonical IP 的轮询与临时连接测试共用容量 1 信号量，IPv4-mapped IPv6 与 IPv4 合并；DNS 必须解析为单个无 zone IP，并固定拨号该 IP，避免别名绕过。多地址 DNS 和 IPv6 zone 暂不支持。
- 首轮有随机抖动；成功后等待配置的轮询间隔，失败按 1/2/4/8/16/30 秒退避。每轮总限额为 `min(0.9 × poll_interval, timeout × (retries+1))`，每次尝试另受 timeout 限制；排队也占总限额。
- 采集事实保留 Go 单调时钟，去抖不受运行期间墙钟校时影响；持久化/输出才转 UTC。驱动 Poll panic 会按失败上报并关闭旧实例，后续按同样退避重建，不永久停止采集；日志不记录 panic 原文。
- 只有身份、额定值或静态相数不算成功采集；至少有一个有效实时观测才上报成功。部分字段无效不使其他有效字段失效。
- `Reload(ctx, sourceID)` 取消并等待旧 worker（含 sink）结束，再装配新配置。源被禁用/删除时仅停止；新配置失败不会自动恢复旧配置。
- `Test(ctx, Source)` 临时 Probe、归一化并推荐内置/有效自定义 Profile，不污染 worker 缓存；默认总限额 5 秒，显式 timeout 最大 1 分钟。排队超时返回 busy。

M4 API 通过 app 的 `ChangeDevices` 串行协调动态配置：暂停相关 collector worker，排空并停止设备 actor，Flush 历史后执行配置与字段级审计同事务，再从数据库恢复 actor/worker。未确认窗口重新计时，已确认周期与活动告警保留。事务失败保持旧配置并尝试恢复旧运行；保存成功但恢复失败返回 503 / `configuration_saved_runtime_error`，不能理解为配置回滚。配置变更还会发布全量 resync。不要在服务运行时手工修改数据库。

## 状态与事件

实现入口：[`internal/state`](../../backend/internal/state/engine.go)。状态层只消费统一模型，不导入驱动或 Profile。

`devices.config` 是单层 JSON 对象（最大 16 KiB），可省略字段使用默认值；拒绝未知/重复键、null、非法类型和范围。默认配置如下：

```json
{
  "outage_confirm_s": 10,
  "recovery_confirm_s": 60,
  "input_voltage_threshold": 50,
  "comm_failures": 3,
  "comm_lost_policy": "hold",
  "assume_on_battery_after_s": 300,
  "battery_low_pct": 30,
  "battery_hysteresis_pct": 5,
  "overload_pct": 90,
  "load_hysteresis_pct": 5
}
```

- 供电判定优先使用 Profile `power_hints`；缺省顺序为 `ups_mode → mains_ok → output_source → native_seconds_on_battery → 最大相 input_voltage`，只使用 good 且可解释的依据。unknown 不算市电恢复。
- 从首次电池观测开始放电计时；连续满 10 秒确认停电并创建周期，恢复市电连续满 60 秒后结束周期。5 秒闪断不产生停电事件。失败或未知观测取消未确认窗口，不能跨失败拼接连续时长。
- 多依据告警会记住实际触发字段（含负载相号），与状态同事务持久化；这些依据必须重新 good 且满足恢复阈值才能清除，不能用另一正常字段代替失效字段证明恢复。
- 低电量默认 ≤30% 触发，≥35% 且可用状态位解除后清除；过载默认 ≥90% 触发、≤85% 清除。invalid / stale 字段不触发数值告警，也不据此清除告警。**恢复市电不等于电池容量已恢复。**
- 支持停电/恢复、通信中断/恢复、低电/清除、过载/清除、旁路/清除，以及电池耗尽、UPS 故障、UPS 正在关机事件。没有独立恢复类型的告警仅关闭原事件，不制造额外事件。
- 原生剩余时间有效时优先；否则在电池模式下用最近 10 分钟容量样本线性回归。至少 5 点、跨度至少 2 分钟、下降速率大于 0.05%/min 才估算，否则为 `invalid` / `estimating`；不把未知当零秒。

连续采集失败达到 `comm_failures` 后进入 lost，观测字段降为 stale：

| 失联策略 | 行为 |
|---|---|
| `hold` | 保留已确认供电状态；原已在电池模式则继续放电计时 |
| `ignore` | 显示 unknown、不生成市电恢复；保留周期，重连后重新确认恢复 |
| `assume_on_battery_after` | 失联达到设定时长后假定停电，起点取最后成功时间（若无则取失联时刻） |

每设备 actor 串行处理；候选状态计算后，**周期、事件新增/清除与关键设备状态在同一事务提交，成功才替换内存状态并发布**。事务失败保留输入退避重试，不发布半提交事件。重启从持久化周期和活动事件恢复，不重复告警；不恢复未确认窗口的连续性，也不把旧字段重新当作新观测。正常退出保存最后成功时间。

## 通道与生命周期

- collector → state：每设备 64 条有界无损队列，背压到采集端，context 可取消等待；正常关闭停止上游后，在独立 5 秒期限内处理已接受事实并保存最后状态，真实落库失败仍上报。
- state → history：1024 行总待写容量（失败批次也占容量），满时丢弃新样本并累计 `Dropped`，定期日志报告。
- state → bus：每订阅者独立 256 条缓冲；溢出通过独立失效信号通知立即退出，不排空积压消息，不阻塞其他订阅者。SSE 每帧写期限 5 秒、每 15 秒心跳重新认证，响应注销/会话撤销/绝对到期；断流后重新订阅并获取全量 sync，不能把断流当连续数据。
- 状态、事件摘要和订阅消息均深拷贝，消费者不能修改引擎内部对象。
- app 关闭顺序：停止 collector → 停止 state 并保存状态 → history 最终刷新 → 关闭 bus/后台任务 → 关闭数据库、释放目录锁。规则引擎尚未实现，未提前启动未来的规则消费队列。

## 历史存储与查询

实现入口：[`internal/history`](../../backend/internal/history/writer.go)。

- 仅写目录标记 `History=true` 且质量 good 的统一数值；不保存原始报文、OID、寄存器、QAD/RTD 段、凭据、Reason 或 Trace。
- 每秒或累计 200 行批量写入；每事务最多 200 行，失败保留有界待写队列。时钟 tick 和失败采集不制造历史测量点。
- 完整分钟聚合 min/max/avg/count，启动补算、每分钟幂等重算，支持迟到数据。不完整当前分钟暂不聚合；明细保留期边缘的部分分钟不覆盖已有完整聚合。
- 默认明细 7 天、分钟聚合 90 天、日志 365 天；每小时聚合后分批清理，每批最多 5000 行。活动事件、活动周期及仍被引用的记录不删除；无 `cleared_at` 的非活动恢复事件按 `started_at` 判断过期。
- `Query{DeviceID, From, To, Resolution, MaxPoints}` 使用 `[From, To)`。`Resolution` 为 `raw`、`1m`、`auto`；auto 在范围 ≤6 小时且 From 位于明细保留期内时选 raw，否则 1m。
- 默认及最大 1000 点，按时间桶降采样并按 count 加权平均；1m 的范围边界按分钟起点判断，不拆分部分分钟。
- 查询只读取已提交数据，不隐式 Flush；显式调用 Cleanup 前应先 Aggregate。聚合当前会扫描保留期内完整分钟，尚未进行满容量性能验收。

## 已验证与未验证

自动化覆盖状态去抖/回差、三种失联策略、原生/估算剩余时间、乱序与副本隔离、事务回滚、重启周期/活动告警去重、队列取消与溢出、历史过滤/重试/补算/清理/auto 查询。loopback 集成覆盖模拟器 → collector 归一化 → state → history/bus 的闪断、停电、低电、失联 hold、恢复与重启；另有主服务自动轮询和退出刷新测试。纯时间逻辑使用 `testing/synctest`，真实 loopback 场景使用显式可控时钟。

真实设备 CP-1、Linux 实机运行、race 和满容量压力测试尚未验证。当前环境 race 命令因未启用 cgo 而不能执行；不能以普通测试或交叉编译代替。
