# 协议驱动与本机模拟器

[返回文档导航](../README.md) · [Profile 与归一化](profiles.md)

## 当前范围

M2 提供只读 SNMP v1/v2c、Modbus TCP、AYI9 JSON 驱动、发送护栏、三协议本机模拟器和显式执行的 `upsprobe` 工具。M3 已将采集调度与状态接入主服务，启动 `ups-warden` 会连接数据库中启用的数据源，见[采集、状态与历史](collection-state-history.md)；M4 已开放设备/源管理及只读连接 test/raw HTTP API，见[监控 API 与 SSE 契约](../reference/monitoring-api.md)。CP-1 三协议只读冒烟已于 2026-10-27 在 ET1K 真机通过（见[单次只读冒烟](#单次只读冒烟)），AYI9 错误密码响应核验与独立面板比对待完成；CP-2 双方联调待验收。开发测试只用临时数据库和 loopback 模拟器，模拟器通过不等于真机兼容性已经确认。

驱动只消费 `driver.ReadPlan`，不依赖 Profile 或具体型号。调用者先编译 Profile，再通过对应包的 `New(Config, plan)` 创建实例；构造函数不联网，`Probe` / `Poll` 才建立连接。业务层只消费归一化结果，不能把 `RawData` 或协议诊断用于规则判断。

## 连接与安全契约

- `guard.Config.Address` 必须明确为 `host:port`；没有扫描、HTTP WebUI 访问或自动协议发现。M4 配置/API 层处理 host/port 并校验，驱动不会猜测未提供的端口。
- 请求超时默认 2 秒，可配置为大于 0、不超过 1 分钟；同时受调用者 context 截止时间约束。一次 `Poll` 可包含多次请求，调用者应设置总截止时间；主服务的采集周期和重试预算由 collector 管理。
- 每个客户端串行使用连接，等待串行资格时也响应 context 取消与截止时间。`Close` 是幂等终态，会取消正在进行的请求；关闭后不能再次采集。
- 每次 `Write` 必须提供单个完整请求，发送前解析字节验证白名单。未知操作、截断、尾随报文、越界寄存器和重复/额外 JSON 键均拒绝。短写关闭连接，不尝试将剩余字节作为另一条指令发送。
- `guard.ErrWriteBlocked` 表示没有任何字节被发送；护栏日志不含报文或凭据。SNMP community、AYI9 密码使用 `secret.String`，错误不回显响应内容。
- 网络失败返回错误；已有成功值可随 `RawData` 返回。消费部分结果时必须同时考虑返回错误和 `RawData.Errors`，不能仅因结果非空就判定成功。快照时间使用采集结束时间。
- 测试架构规则禁止驱动在 guard 外直接拨号，或引入 HTTP/底层 socket 绕行依赖；这属于代码约束，不是操作系统级沙箱。

## 协议行为

| 协议 | 已实现行为 | 限制 |
|---|---|---|
| SNMP | v1/v2c；GET 每批最多 20 个 OID；GETBULK / GETNEXT 子树遍历；四字节 request-id；响应 ID/version/community 匹配；bulk 失败回退 GET | 不支持 SET、Trap 或 v3；遍历最多 20 轮；消息最多 65535 字节、4096 个 varbind |
| Modbus TCP | FC03/FC04；长连接；事务 ID、协议 ID、Unit ID、长度和字节数校验；异常块不妨碍其他块 | 手工计划最多 256 块，每块 1～125 个寄存器，不能越过地址 65535；传输失败后下轮重连 |
| AYI9 JSON | 仅 QAD/RTD；递增十进制 session；流式 JSON 解码兼容有无换行；RTD 缓存和刷新；错误码识别 | 单次交换最多 64 KiB、最多 16 个不匹配响应；密码为最多 15 字节的 UTF-8 文本；超时或响应对象读取/匹配失败关闭连接 |

遍历结束的 `endOfMibView` 可能复用最后一个 OID；驱动保留此前的有效值，不将末尾正常字段覆盖成不支持。

SNMP 的 v1 `noSuchName` 按 `error-index` 剔除并重试，不支持的 OID 缓存到重连。v2c 的 `noSuchInstance` / `noSuchObject` 记为块级 `unsupported` 诊断：ET1K 真机对 `upsIdentAttachedDevices`、`upsTest`、`upsControl` 下共 12 个 OID 都这样返回，属于正常现象。bulk 回退记录 `Kind=fallback` 诊断；回退仍可能无有效观测，不伪装为正常数据。`Probe` 只从已读取的 UPS-MIB 身份字段提取信息，不额外枚举身份地址。

Modbus 异常响应包含异常码，记录为块级 `unsupported`。响应头/功能码/长度不匹配按传输失败处理，关闭连接；成功块保留，未读块记录失败。驱动不做字节序、缩放和物理含义转换，这些仍由 Profile 与归一化负责。

AYI9 每次 Poll 读取 QAD；建连后及到期时读取 RTD，默认周期 10 分钟，范围 1 秒～365 天。RTD 缓存返回独立副本，携带实际刷新时间；刷新失败不将旧数据冒充本轮成功观测。QAD 必须有 23 段，RTD 必须有 10 段，坏段数保留原文并标记失败。QAD/RTD 的非空 UID 不一致时两者均失效并断开连接。

`*ayi9.DeviceError` 提供设备错误码；`errors.Is(err, ayi9.ErrPasswordWrong)` 可识别 103。兼容 `value` 为数字或数字字符串，以及 `code` 字段；`FAILED` 无具体码时为 Code=0。CP-1 只验证了无密码的正常读取，实际网卡的错误响应格式尚未核验，不能把模拟器格式当作真机证据。

## 本机模拟器

从仓库根目录准备工具链并启动：

```bash
mise install
mise x -- go -C backend run ./cmd/upssim --et1k
```

默认自动分配三个 loopback 端口，输出 `SNMP=... Modbus=... AYI9=...`；Ctrl+C 关闭监听器与连接。需要固定端口时使用 `--snmp-port`、`--modbus-port`、`--ayi9-port`，或对应 `--*-address` 指定 loopback IP:port。非 loopback 地址被拒绝。

模拟器不因正常连接空闲而断开，允许三秒等常规轮询间隔。首个帧字节到达后才启用两秒收帧/响应超时，半帧仍会被拒绝；空闲连接受总连接数上限约束。

- 不带 `--et1k` 为标准模式，可用于 RFC 1628 通用 Profile；`--et1k` 模拟 GET 全部 `noSuchName`、GETNEXT 只返回首个 OID、短 request-id 被丢弃及 Modbus FC03 0～79 范围限制。
- 默认 community 为虚构值 `sim-public`，AYI9 密码为空。`--community` / `--password` **只用于虚构测试凭据**，不要将真实设备凭据放进命令行。
- `--ayi9-error-code 103` 可固定返回错误码，验证错误处理；错误密码也会返回 103。
- `--scenario FILE` 加载 schema=1 场景；`--registers FILE` 替换 Modbus 寄存器表。输入文件最大 64 KiB，不需要真实采样或本机凭据。
- 同一物理状态渲染为三协议，不将 W 与 VA 混同。请求记录只保留脱敏的完整合法读请求；非法请求只保留元信息。记录最多 4096 条，超额通过 `DroppedRequests` 显式计数。

示例场景（保存在本地 `data/` 或临时目录）：

```yaml
schema: 1
phases: 1
steps:
  - {at: 10s, state: outage}
  - {at: 60s, state: discharge}
  - {at: 120s, state: low}
  - {at: 150s, state: recovery}
  - {at: 180s, state: flash}
  - {at: 200s, state: offline}
  - {at: 220s, state: online}
```

事件时间必须严格递增；`flash` 固定 5 秒且不允许与期间其他事件重叠。`offline/online` 只改变通信，不改变供电状态。模拟器 `Scenario.StateAt` 是纯函数，集成测试注入时钟，不依赖真实放电。场景状态仅提供采集输入；去抖与事件由 M3 状态引擎处理，通知、规则与关机联动尚未实现。

场景的可选物理数值使用零值作为默认值标记（例如温度 0 会采用默认 25），不用于零点校准；需要精确协议零值可使用自定义寄存器表或录制 fixture。

## 单次只读冒烟

仅在明确获得目标设备授权后执行。以下示例**只指向本机模拟器**，将端口改成 `upssim` 实际输出值：

```bash
UPS_WARDEN_PROBE_ADDRESS=127.0.0.1:1161 \
UPS_WARDEN_PROBE_COMMUNITY=sim-public \
mise x -- go -C backend run ./cmd/upsprobe --profile et1k-snmp --confirm-readonly
```

工具要求 `--profile` 和 `--confirm-readonly`，只调用一次 `Probe`，默认总超时 5 秒，`--timeout` 上限 1 分钟。不尝试其他 Profile、协议或地址，不做持续采样。

| 环境变量 | 用途 |
|---|---|
| `UPS_WARDEN_PROBE_ADDRESS` | 必填，明确的 host:port |
| `UPS_WARDEN_PROBE_COMMUNITY` | SNMP community，空值使用 public；本机模拟器需设 sim-public |
| `UPS_WARDEN_PROBE_SNMP_VERSION` | `1` 或 `2c`；空值为 2c |
| `UPS_WARDEN_PROBE_PASSWORD` | AYI9 密码；空值省略 pass |

输出 JSON 包括 Profile、时间、质量统计及固定核心电气字段，便于与面板比对；无地址、UID、凭据或原始报文。无有效核心字段、密码错误、连接失败均非零退出。字段目录中的 `num` / `bool` 零值可能省略，应结合目录类型和 `quality` 解释，不能将缺失值自动当作 Good。

CP-1 已于 2026-10-27 经项目方授权，在 ET1K 上用本工具完成市电、断电、恢复三个阶段的三协议 Probe，使用默认端口 SNMP UDP 161、Modbus TCP 502、AYI9 TCP 2390；地址和凭据仍只通过本地环境变量提供。结果和发现见[开发计划 M2 实施记录](../planning/backend-plan.md)与 [Profile 指南](profiles.md#回归证据与限制)。还剩两项：经单独授权的错误 pass 响应核验，以及与设备本体面板的独立比对（网卡 Web 页面数据与 AYI9 同源）。自动化测试不访问真实设备，冒烟只能按授权手动执行；既有市电/电池 fixture 也不能替代真机验收。

## 检查

```bash
mise run backend:check
mise run backend:build
mise x -- go -C backend test -count=1 ./internal/driver/... ./internal/sim ./cmd/upsprobe ./cmd/upssim
mise x -- go -C backend test ./internal/driver/guard -run '^$' -fuzz '^FuzzGuardNeverLeaksRejectedBytes$' -fuzztime=15s
```

测试覆盖编码器、发送护栏、随机读计划、取消/重连、部分错误、标准/ET1K 模式三协议归一化、自定义 FC04 映射、密码错误和脱敏。Linux 交叉编译只证明构建兼容；当前 Windows 环境不将其当作 Linux 实机或 race 验证。
