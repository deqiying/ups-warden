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

### 多设备守护进程

上面的单设备前台模式适合一次性测试。需要同时模拟多台 UPS，或在联调中随时切换某台设备的供电和通信状态时，使用守护进程模式。本节是命令、参数和错误语义的唯一事实来源；agent 的联调流程见 [upssim 技能](../../.agents/skills/upssim/SKILL.md)。

```bash
mise run sim:build                           # 构建到 .cache/build/upssim（Windows 为 upssim.exe）
export UPSSIM_STATE_DIR="$(mktemp -d)"       # 开发测试使用独立临时状态目录
mise run sim -- daemon start
mise run sim -- add ups-1 --et1k --ip 127.0.0.2
mise run sim -- set ups-1 --mode battery
mise run sim -- list
mise run sim -- daemon stop
```

`mise run sim -- <参数>` 会先构建再透传参数。无子命令或首个参数以 `-` 开头时仍是单设备前台模式，原有用法不变。

| 命令 | 作用 |
|---|---|
| `daemon start` | 以脱离终端的子进程启动守护进程，控制面就绪后返回；已在运行时只输出状态。不能从 `go run` 的临时二进制启动 |
| `daemon run` | 前台运行守护进程，Ctrl+C 或 `daemon stop` 退出；适合由宿主的后台任务托管 |
| `daemon stop` | 请求优雅退出，等待状态目录锁释放（最长 10 秒）；不按 PID 强制结束进程 |
| `daemon status` | 输出 PID、控制地址、版本、状态目录和设备数 |
| `add NAME [flags]` | 添加设备并立即监听三协议；输出实际端口和推荐的内置 Profile |
| `set NAME [flags]` | 修改模式、通信状态、物理参数、AYI9 错误码，或切换场景 |
| `list` / `show NAME` | 查看设备、实时快照和端点；加 `--json` 输出机器可读结果 |
| `remove NAME` | 停止并删除设备 |

所有子命令都接受 `--state-dir DIR`，默认依次取 `UPSSIM_STATE_DIR`、`os.UserConfigDir()/ups-warden/upssim`。每个状态目录对应一个独立的守护进程。`daemon start`/`run` 可用 `--control-port` 固定控制面端口，默认自动分配。

`add` 参数：

| 参数 | 默认 | 说明 |
|---|---|---|
| `NAME` | 必填 | 匹配 `^[a-z0-9][a-z0-9-]{0,31}$`，最多 128 台 |
| `--ip` | `127.0.0.1` | 三协议共用的监听 IP，必须是 loopback；多台设备建议各用一个 IP（`127.0.0.2`、`127.0.0.3`……），因为 ups-warden 对同一 IP 串行采集 |
| `--snmp-port` / `--modbus-port` / `--ayi9-port` | `0` | 0 表示自动分配；SNMP 走 UDP，Modbus 和 AYI9 走 TCP，Modbus 与 AYI9 不能同端口 |
| `--et1k` | 否 | 启用 ET1K 怪癖，只允许单相 |
| `--phases` | `1` | 1 或 3 |
| `--mode` | `mains` | 初始供电模式：`mains`、`battery`、`low` |
| `--link` | `up` | 初始通信状态：`up`、`down` |
| `--scenario FILE` | 无 | schema=1 场景，按时间轴运行；与 `--mode`、`--link`、电量和物理参数互斥 |
| `--community` / `--password` | `sim-public` / 空 | 只用于虚构测试凭据，不在输出和日志中出现 |
| `--ayi9-error-code` | `0` | 固定 AYI9 错误码，如 103 |
| 物理参数 | 见下 | `add` 与 `set` 共用 |

物理参数：`--load`（负载 %，默认 30）、`--capacity`（当前电量 %，默认 100）、`--temperature`（°C，默认 25，-40..100）、`--input-voltage`（V，默认 220，1..500）、`--discharge-minutes`（满电放空所需分钟数，默认 30，1..1440）。负载、温度、输入电压和放电时长不能设为 0，因为 0 是场景中的默认值标记。

`set` 额外支持 `--flash`（立即触发 5 秒闪断，仅市电模式可用）、`--ayi9-error-code`（0 恢复正常）、`--scenario FILE`（以当前时刻为起点重新运行场景，不能与其他修改同时使用）。监听地址、端口、ET1K、相数和凭据不能用 `set` 修改，需要 `remove` 后重新 `add`。

**模式语义：**

- `mains`：市电供电，电量按 `discharge_minutes` 的速率回充到 100%。
- `battery`：电池供电，电量线性下降；电量降到 20% 及以下时自动置低电标志。
- `low`：电池供电且低电，电量钳制在 15% 以内（`--capacity` 超过 15 会被拒绝）。
- `--link down`：三协议都不回包，供电状态照常演进；`--link up` 恢复。
- 场景控制下，任何状态或参数修改都会让设备从当前时刻转为手动控制，电量和电池供电计时保持连续。`list` 中模式带 `*` 表示仍按场景运行。
- 低电标志沿用物理模型：从 `low` 切回 `mains` 后，标志会保持到电量回充超过 20%。需要立即清除时，同时指定电量，例如 `set ups-1 --mode mains --capacity 100`。ups-warden 侧的低电触发和清除阈值、去抖时间见[采集、状态与历史](collection-state-history.md)。

**持久化与恢复：** 状态目录包含 `devices.json`（设备定义，含虚构凭据，Unix 权限 0600）、`control.json`（控制地址和本次运行的随机 token）、`upssim.log`（启动前超过 5 MiB 时轮转为 `upssim.log.1`）和目录锁。每次修改成功后原子写入 `devices.json`；守护进程重启后按原端口恢复所有设备，场景设备从头运行，手动设备从最近一次显式设置的电量开始。单台恢复失败时，这台设备标记为 failed，其他设备不受影响，不会改用其他端口；failed 设备只能 `remove` 后重新 `add`。

**控制面：** 只监听 `127.0.0.1`，每个请求都要带 `control.json` 中的 Bearer token，请求体上限 64 KiB，未知字段会被拒绝。客户端只连接 loopback 地址。

**退出码：** 0 成功；1 操作失败；2 参数错误；3 守护进程未运行。

| 错误码 | HTTP | 含义 |
|---|---|---|
| `validation_failed` | 422 | 参数不合法（名称、非 loopback IP、端口范围、互斥项、物理参数为 0 等） |
| `device_exists` | 409 | 设备名已存在 |
| `limit_exceeded` | 409 | 设备数已达 128 |
| `address_conflict` | 409 | 与守护进程内其他设备的同传输层端点冲突，信息中给出冲突设备 |
| `address_in_use` | 409 | 系统绑定失败（端口被其他进程占用或权限不足）；已绑定的监听器会全部释放 |
| `device_not_found` | 404 | 设备不存在 |
| `device_failed` | 409 | 设备恢复时启动失败，需要 `remove` 后重新 `add` |
| `persist_failed` | 500 | 写入 `devices.json` 失败。`set` 的运行期修改已生效，但重启后会恢复为旧配置 |
| `body_too_large` | 413 | 请求体超过 64 KiB |
| `unauthorized` | 401 | 缺少 token 或 token 错误 |

**Windows 注意：** `daemon start` 会优先让子进程脱离当前 Windows Job。IDE 终端、自动化工具等宿主可能不允许脱离；这种情况下仍能启动，但会输出提示：关闭该终端或宿主程序时，守护进程也会结束。处于这类环境时，应改用宿主的后台任务机制运行前台 `daemon run`。第一版面向 Windows 和 Linux（Unix 使用 `setsid` 脱离终端），目前只在 Windows 本机验证，Linux 实机和 macOS 未验证。

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
