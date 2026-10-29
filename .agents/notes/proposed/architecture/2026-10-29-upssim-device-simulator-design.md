# upssim 多设备模拟守护进程方案

> **文档版本**：v0.1
> **日期**：2026-10-29
> **状态**：proposed（S1～S7 已于 2026-10-29 实施并完成本机验证，实施差异见 §16；方案评审与 Linux 实机验证未完成，评审通过后移入 implemented）
> **范围**：`backend/cmd/upssim` 与 `backend/internal/sim` 的扩展，以及配套的项目级技能 `.agents/skills/upssim`；不改动 `ups-warden` 主服务
> **依据**：[后端实现方案 §19.1](2026-10-11-backend-design.md)、[协议驱动与本机模拟器](../../../../docs/guides/protocol-drivers.md)、[采集、状态与历史](../../../../docs/guides/collection-state-history.md)、[验收与压测](../../../../docs/guides/acceptance.md)、[AGENTS.md](../../../../AGENTS.md)

---

## 1. 背景与目标

### 1.1 现状

`cmd/upssim` 已提供只监听 loopback 的三协议（SNMP UDP、Modbus TCP、AYI9 TCP）模拟 UPS。三种协议共用 `internal/sim` 中的同一份物理状态，支持标准 / ET1K 模式和 schema=1 场景 YAML，并会记录请求。现有工具在联调中有以下局限：

| 局限 | 影响 |
|---|---|
| 一个进程只模拟一台设备，且只能在前台运行 | 多设备联调要开多个终端，关掉终端设备就消失，Windows 下尤其不便 |
| 设备状态在启动时由场景 YAML 一次性确定，运行中无法修改 | 想手动触发“停电”“恢复”“失联”，只能预先写好时间轴或重启模拟器；重启后端口会变化，后端数据源配置随之失效 |
| 默认自动分配端口，固定端口需逐个手工指定，没有统一视图 | 难以避开本机已占用端口，也不方便查询每台设备的连接参数 |

联调时无法连接真实设备，当前只能伪造数据，因此需要一个能常驻后台、可在运行中增删设备和切换状态的模拟工具。

### 1.2 目标

| # | 目标 | 落点 |
|---|---|---|
| G1 | 模拟器可在后台常驻，关闭终端后继续运行，并能查询状态、正常停止 | §6 守护进程 |
| G2 | 运行中通过命令追加模拟设备，并在创建时指定初始状态（市电、电池、低电等） | §3、§4 |
| G3 | 运行中通过命令修改设备的供电模式、通信状态与物理参数，状态切换在物理上连续 | §4.2 锚点重定基 |
| G4 | 每台设备可指定各协议的监听 IP 与端口，提前发现端口冲突；守护进程重启后端口保持不变 | §5、§8 |
| G5 | 保持现有安全边界：只监听 loopback、只响应只读请求、只用虚构凭据，不对外发起连接 | §9 |
| G6 | 兼容现有用法：`upssim --et1k` 单设备前台模式、`sim.Start` 进程内测试和压测不受影响 | §10 |
| G7 | 将 upssim 的使用方式定义为项目级技能，让 AI agent 在联调、验收和排查中按统一流程使用模拟设备，不再伪造数据，也不会误连真实设备 | §11 |

### 1.3 非目标

- 不监听非 loopback 地址，不支持局域网内其他机器访问模拟器。
- 不新增协议，也不模拟 SNMP SET、Trap、Modbus 写功能码或 AYI9 写指令；模拟器仍把这些请求当作非法请求拒绝。
- 不自动向 `ups-warden` 注册设备和数据源。注册需要管理员会话与 CSRF，仍由开发者通过 WebUI 或 API 完成，模拟器只输出所需的连接参数。
- 不提供图形界面，也不注册为 Windows 服务或 systemd unit。
- 不回放真实设备录制数据，录制回放仍使用 `testdata/et1k` fixture。

---

## 2. 总体设计

### 2.1 进程模型

继续使用同一个 `upssim` 二进制，改为“守护进程 + 控制客户端”结构：

```text
┌────────────── 开发者终端 ──────────────┐
│ upssim add / set / list / remove …     │   控制客户端（短生命周期）
└──────────────────┬─────────────────────┘
                   │ HTTP/1.1 JSON，127.0.0.1:<控制端口>
                   │ Authorization: Bearer <token>
┌──────────────────▼─────────────────────────────────────────┐
│ upssim daemon run（后台常驻）                              │
│  ├─ 控制面 HTTP server（只绑定 loopback，校验 token）       │
│  ├─ Registry：name → Device                                │
│  │    └─ Device = *sim.Server（三协议监听器 + 运行期状态）  │
│  ├─ 状态目录：daemon.lock / control.json / devices.json / log │
│  └─ 信号与 /v1/shutdown 优雅退出                           │
└──────────────────▲─────────────────────────────────────────┘
                   │ SNMP UDP / Modbus TCP / AYI9 TCP（loopback）
┌──────────────────┴──────────────┐
│ ups-warden collector（只读驱动） │
└─────────────────────────────────┘
```

- 守护进程独占一个状态目录。同一状态目录同时只允许一个实例运行，通过 `internal/datalock` 文件锁保证。
- 控制客户端从状态目录的 `control.json` 读取控制地址和 token，调用控制面 API 后退出；客户端不直接操作设备监听器。
- 每台模拟设备对应一个 `*sim.Server`，继续复用现有的三协议渲染、ET1K 怪癖、请求记录和连接上限。

### 2.2 代码布局

```text
backend/cmd/upssim/
  main.go            子命令分发；无子命令或首参数以 '-' 开头时进入旧的单设备前台模式
  legacy.go          现有 run() 原样迁移，保持参数与输出不变
  daemon.go          daemon start / run / stop / status
  detach_unix.go     //go:build unix：Setsid 脱离控制终端
  detach_windows.go  //go:build windows：DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP，优先 CREATE_BREAKAWAY_FROM_JOB
  registry.go        设备注册表、端口冲突检查、持久化
  control.go         控制面 HTTP handler 与客户端
  devices.go         add / set / list / remove 子命令
backend/internal/sim/
  live.go            运行期状态：Mode / Link / 参数覆写 / 锚点重定基
  server.go          新增 Apply、Snapshot；state() 改为读取运行期状态
```

设备注册表、控制面和持久化属于 CLI 工具的职责，放在 `cmd/upssim`。`internal/sim` 只新增“运行期可修改状态”的能力，并保持对进程内测试的可用性。架构规则 R-DEP-4（`sim` 只允许被 `cmd/upssim` 和 `_test.go` 引用）不变。

### 2.3 依赖

只使用标准库（`flag`、`net/http`、`encoding/json`、`os/exec`、`syscall`）和仓库已有依赖：`go.yaml.in/yaml/v3` 用于场景解析，`golang.org/x/sys/windows` 提供进程创建标志，`internal/datalock` 用于单实例锁。不引入 cobra 等 CLI 框架。

---

## 3. 命令行设计

### 3.1 命令总览

| 命令 | 作用 |
|---|---|
| `upssim daemon start` | 在后台启动守护进程，就绪后打印控制地址与已恢复的设备；已在运行时幂等返回 0 |
| `upssim daemon run` | 在前台运行守护进程，供调试、IDE 或容器使用；Ctrl+C 退出 |
| `upssim daemon stop` | 请求守护进程优雅退出，并等待锁释放 |
| `upssim daemon status` | 输出 PID、控制地址、启动时间、设备数量 |
| `upssim add NAME [flags]` | 追加模拟设备并立即开始监听 |
| `upssim set NAME [flags]` | 修改设备的模式、通信状态或物理参数 |
| `upssim list [--json]` | 列出所有设备的端点、当前模式与实时快照 |
| `upssim show NAME [--json]` | 查看单台设备详情 |
| `upssim remove NAME` | 关闭设备的监听器与连接，并从持久化中删除 |
| `upssim [legacy flags]` | 旧的单设备前台模式，参数与输出保持不变 |

全局参数 `--state-dir DIR` 指定状态目录，优先级为：`--state-dir` > 环境变量 `UPSSIM_STATE_DIR` > `os.UserConfigDir()/ups-warden/upssim`。默认目录按用户隔离，不会写进仓库；不同状态目录对应相互独立的守护进程。

退出码：`0` 成功；`1` 操作失败（含端口占用、设备不存在等业务错误）；`2` 参数错误；`3` 守护进程未运行。

### 3.2 add 参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `NAME`（位置参数） | 必填 | `^[a-z0-9][a-z0-9-]{0,31}$`，在注册表内唯一 |
| `--mode` | `mains` | 初始供电模式：`mains` / `battery` / `low`，见 §4.1 |
| `--link` | `up` | 初始通信状态：`up` / `down` |
| `--ip` | `127.0.0.1` | 三协议共用的监听 IP，必须是 loopback IP，不解析主机名 |
| `--snmp-port` / `--modbus-port` / `--ayi9-port` | `0` | 各协议端口；`0` 由系统分配，实际端口会持久化（§5.4） |
| `--et1k` | `false` | 启用 ET1K 怪癖（仅单相） |
| `--phases` | `1` | `1` 或 `3`；ET1K 只允许 1 |
| `--load` / `--capacity` / `--temperature` / `--input-voltage` / `--discharge-minutes` | 沿用场景默认值 | 物理参数，取值范围与 `Scenario.normalized` 一致 |
| `--scenario FILE` | 无 | 加载 schema=1 场景，按时间轴运行；与 `--mode` / `--link` 互斥 |
| `--community` / `--password` | `sim-public` / 空 | 只允许虚构测试凭据，不出现在任何输出和日志中 |
| `--ayi9-error-code` | `0` | 固定 AYI9 错误码，例如 `103` |
| `--json` | `false` | 以 JSON 输出创建结果，便于脚本使用 |

`--scenario` 的文件内容由客户端读取（沿用 64 KiB 上限和严格 YAML 校验），随请求发送给守护进程并保存在持久化文件中。因此守护进程不依赖客户端的工作目录，场景文件也不必长期保留。

### 3.3 set 参数

| 参数 | 运行期可改 | 说明 |
|---|---|---|
| `--mode mains\|battery\|low` | 是 | 切换供电模式；设备原本按场景运行时，场景时间轴从此刻起由手动模式接管 |
| `--link up\|down` | 是 | 切换通信状态；`down` 时三协议都不回包，模拟网卡失联 |
| `--flash` | 是 | 一次性动作：立即进入 5 秒闪断，之后恢复到闪断前的模式 |
| `--load` / `--capacity` / `--temperature` / `--input-voltage` / `--discharge-minutes` | 是 | 修改物理参数；`--capacity` 会直接改写当前电量锚点 |
| `--ayi9-error-code` | 是 | 运行中切换错误注入 |
| `--scenario FILE` | 是 | 以当前时刻为起点，从头运行新的场景 |
| `--ip`、各协议端口、`--et1k`、`--phases`、凭据 | **否** | 这些参数决定监听器和后端应选用的 Profile，必须 `remove` 后重新 `add`，避免后端数据源配置在不知情时失效 |

一次 `set` 至少修改一项，且所有修改原子生效：先完整校验，任一项不合法就整体拒绝，不会只改一部分。

### 3.4 使用示例

```bash
mise run sim:build                                  # 构建到 .cache/build/upssim(.exe)
UPSSIM=.cache/build/upssim

$UPSSIM daemon start
$UPSSIM add ups-1 --et1k --snmp-port 11161 --modbus-port 11502 --ayi9-port 12390
$UPSSIM add ups-2 --ip 127.0.0.2 --mode battery --capacity 60 --load 45
$UPSSIM set ups-2 --mode low
$UPSSIM set ups-1 --link down
$UPSSIM set ups-1 --link up
$UPSSIM set ups-1 --flash
$UPSSIM list
$UPSSIM remove ups-2
$UPSSIM daemon stop
```

`list` 的默认输出：

```text
NAME   MODE     LINK  CAP%   ON_BATT  SNMP(udp)          MODBUS(tcp)        AYI9(tcp)          PROFILES
ups-1  mains    up    100.0  -        127.0.0.1:11161    127.0.0.1:11502    127.0.0.1:12390    et1k-snmp / et1k-modbus / et1k-ayi9-json
ups-2  low      up     15.0  73s      127.0.0.2:52011    127.0.0.2:52012    127.0.0.2:52013    rfc1628-generic / - / ayi9-json-generic
```

PROFILES 列给出可直接用于创建数据源的内置 Profile 键：ET1K 模式对应 `et1k-*`；标准模式下 SNMP 对应 `rfc1628-generic`，AYI9 对应 `ayi9-json-generic`，Modbus 没有通用内置 Profile，显示 `-`，需使用自定义 Profile。

---

## 4. 设备模式与状态模型

### 4.1 模式定义

设备状态拆成两个相互独立的维度：**供电模式**（Mode）和**通信状态**（Link），另外提供一次性的**闪断动作**。这三者分别对应现有场景引擎的 step 语义，不新增物理含义：

| CLI 取值 | 对应现有 step | `State` 表现 | 三协议输出（沿用现有渲染） |
|---|---|---|---|
| `--mode mains`（市电） | `normal` / `recovery` | `Mains=true`；电量按 `discharge_minutes` 速率回充到上限 | SNMP `upsOutputSource=3`；AYI9 状态字 `ups_mode=line`、`mains_ok=1`；Modbus 输入电压正常 |
| `--mode battery`（电池） | `outage` / `discharge` | `Mains=false`；电量线性下降；`SecondsOnBattery` 从进入电池模式时开始计时；电量 ≤ 20% 时 `LowBattery` 自动置位 | SNMP `upsOutputSource=5`；AYI9 `ups_mode=battery`、`mains_ok=0`；输入电压、输入频率为 0 |
| `--mode low`（低电） | `low` | 同电池模式，并把电量钳制为 ≤ 15%、`LowBattery=true` | 在电池模式基础上 AYI9 low_battery 位置 1 |
| `--link down` / `up` | `offline` / `online` | 只改 `Online`，不改变供电状态 | 三协议不回包 / 恢复回包 |
| `--flash` | `flash` | 5 秒 `Mains=false`，之后恢复到闪断前的模式与低电标志 | 同电池模式，持续 5 秒 |

约束如下：
- `--mode low` 与 `--capacity` 大于 15 同时出现时，按 `validation_failed` 拒绝。
- 电池模式下电量降到 0 后保持 0，不会自动切到关机状态。
- 闪断进行中再次执行 `set` 时，以新指令为准，并提前结束闪断。闪断只能在市电模式下触发。
- 设备按场景运行时，任何模式、通信、闪断或物理参数修改都会从当前时刻起转为手动控制，状态保持连续。
- 负载、温度、输入电压、放电分钟数沿用场景的“0 表示默认值”约定，设置为 0 会被拒绝。
- 低电标志沿用物理模型：电量 ≤ 20% 时自动置位。从低电恢复市电后，回充超过 20% 前低电标志仍保持；需要立即清除时同时设置 `--capacity`。

### 4.2 运行期状态切换：锚点重定基

现有 `Scenario.StateAt(elapsed)` 是以启动时刻为原点的纯函数。若在运行中切换模式时直接从当前时刻重建场景，会出现两个问题：电量回充上限变成切换时的电量；`SecondsOnBattery` 被清零。为保证状态连续，引入“段（segment）+ 锚点”模型：

```text
segment = {
  start      time.Time   // 本段起点
  anchor     起始条件：capacity、mains、low、online、onBatterySince
  scenario   本段使用的 Scenario（手动模式时为不含 steps 的固定场景）
  maxCap     回充上限：手动控制固定为 100；场景控制取场景的 battery_capacity_pct
}
State(now) = scenario.stateFrom(anchor, now - start)
```

- **切换流程**：`Apply` 持有写锁 → 用旧段计算 `now` 时刻的状态 → 用该状态构造新锚点 → 叠加本次修改（模式、参数、显式 `--capacity`）→ 替换为新段。
- **连续性规则**：
  - 电量始终延续上一段，除非 `--capacity` 显式改写。
  - 从市电切到电池或低电时，`onBatterySince` 取切换时刻。
  - 电池与低电之间切换时，`onBatterySince` 保持不变。
  - 切回市电时清空 `onBatterySince`。
  - 回充上限始终是 `maxCap`，不是切换时的电量。
- **实现方式**：把原 `StateAt` 的推进逻辑提取为不导出的 `stateFrom(start, elapsed)`，`start` 是一段时间轴的起始条件；`StateAt` 以默认起始条件调用它。`Scenario` 结构与 schema=1 的 YAML 格式都没有变化，`StateAt` 仍是无 I/O 的纯函数。
- **并发**：每个协议请求只调用一次 `state()`，读取段时持读锁，因此单个响应不会混入切换前后的两份状态。`AYI9ErrorCode` 等运行期可变配置一并纳入同一把锁，不再在服务 goroutine 中直接读取 `cfg`。
- **时钟**：继续使用 `Config.Now`，测试可注入虚拟时钟验证切换连续性，不依赖真实放电时长。

`internal/sim` 新增的公开接口（均为新增，不改动现有签名）：

```go
// Change 描述一次原子修改；nil 字段表示不修改。
type Change struct {
    Mode          *Mode     // ModeMains / ModeBattery / ModeLow
    Online        *bool
    Flash         bool
    LoadPct, BatteryCapacity, Temperature, InputVoltage, DischargeMinutes *float64
    AYI9ErrorCode *int
    Scenario      *Scenario // 非 nil 时以当前时刻为起点重新运行该场景
}

// Apply 先完整校验再原子替换当前段；校验失败时状态不变。
func (s *Server) Apply(c Change) error

// Snapshot 返回 now 时刻的物理快照及当前模式，用于 list/show。
func (s *Server) Snapshot() (State, Mode)
```

### 4.3 后续扩展：旁路、故障、过载（第二阶段）

统一模型已有 `on_bypass`、`ups_fault`、`overload` 事件，内置 Profile 也有对应映射：SNMP `upsOutputSource=4` 表示 bypass；AYI9 状态字 bit0～3 中 `2=bypass`、`6=fault`，bit8 表示 overload。但模拟器目前的 `State` 只有市电、电池、低电三个状态位，ET1K Modbus 也没有状态寄存器。因此这三种模式不纳入第一版，第二阶段按以下前提实现：

- 在 `State` 中增加 `Bypass`、`Fault`、`Overload`，由 SNMP 和 AYI9 渲染器按现有 Profile 映射输出；Modbus 无法表达这些状态时保持只输出电压值，并在 `add` 输出中说明。
- 输出编码只依据 Profile 映射，**不能作为 ET1K 真机在旁路或故障时如何编码的证据**。

---

## 5. 端口与地址分配

### 5.1 地址规则

- 监听 IP 必须是 loopback IP（复用 `sim.loopbackAddress`），不解析主机名，不允许 `0.0.0.0` 或局域网地址。
- 每台设备的三个协议共用一个 IP，各自使用独立端口。SNMP 走 UDP，Modbus 和 AYI9 走 TCP，冲突检查按 `(传输层, IP, 端口)` 判断，所以 SNMP UDP 11161 与 Modbus TCP 11161 可以同时存在。
- 多台设备建议使用不同的 loopback IP（如 `127.0.0.2`、`127.0.0.3`）。collector 对同一 canonical IP 串行采集，多台设备共用 `127.0.0.1` 时会互相排队，轮询变慢或返回 `source_busy`。现有压测也因此为每台设备分配独立的 `127.0.x.y`。Linux 和 Windows 默认可以直接绑定整个 `127.0.0.0/8`；macOS 只配置了 `127.0.0.1`，需要另行添加 lo0 别名。

### 5.2 冲突检测顺序

1. **参数校验**：端口范围 0..65535，IP 必须是 loopback。
2. **注册表检查**：与其他模拟设备已占用的 `(传输层, IP, 端口)` 比较，冲突时返回 `address_conflict`，并在错误中指出占用者的设备名。
3. **系统绑定**：调用 `sim.Start` 实际监听。绑定失败（端口被本机其他进程占用、权限不足）时返回 `address_in_use`，并提示使用其他端口或 `0`。`sim.Start` 在任一监听器失败时会回收已创建的监听器，因此不会留下只开了部分协议的设备。

端口 `0` 由系统分配，不会与注册表冲突。注册表检查只能发现模拟设备之间的冲突；与本机其他程序的冲突以第 3 步的系统绑定结果为准，不做端口扫描或预探测。

### 5.3 端口建议

- 真实设备的默认端口 SNMP 161、Modbus 502 在 Linux 上属于特权端口，Windows 上 161 也可能被系统 SNMP 服务占用。建议使用 `11161`、`11502`、`12390` 这类高位端口，或者使用 `0` 自动分配。
- 联调中需要后端数据源配置长期有效时，应显式指定固定端口；临时验证可直接用 `0`。

### 5.4 端口持久化

设备首次创建时即使使用端口 `0`，持久化的也是**实际分配到的端口**。守护进程重启后会按原端口恢复，后端已保存的数据源配置无需修改。如果原端口在恢复时已被占用，该设备标记为 `failed` 并保留失败原因（见 §8.2），不会改用其他端口，以免后端在不知情时连接到错误的地址。

---

## 6. 后台挂起（守护进程）

### 6.1 daemon start

1. 解析状态目录。如果 `control.json` 存在且 `GET /v1/status` 可达，说明已在运行，输出现有信息后返回 0。
2. 检查可执行文件路径：如果 `os.Executable()` 位于 `go run` 的临时构建目录（`go-build*` 下），拒绝后台启动，并提示先执行 `mise run sim:build`。原因是 `go run` 退出后会删除临时二进制，Windows 上还会因文件被占用而失败。前台运行的 `daemon run` 不受此限制。
3. 以 `os.Executable() daemon run --state-dir DIR` 启动子进程：stdin 指向空设备，stdout 和 stderr 写入状态目录的日志，并脱离当前终端：
   - Unix：`SysProcAttr{Setsid: true}`；
   - Windows：`CreationFlags = DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP | CREATE_BREAKAWAY_FROM_JOB`、`HideWindow = true`。IDE 终端和自动化工具常把子进程放进 Windows Job，宿主退出时会结束整棵进程树；所在 Job 不允许脱离时退回不带 `CREATE_BREAKAWAY_FROM_JOB` 的方式启动，并在输出中提示“关闭该终端或宿主程序会结束守护进程”。
4. 父进程最多等待 5 秒，以 `control.json` 写入且状态接口可达作为就绪信号。就绪后输出控制地址和恢复的设备列表，然后退出；超时或子进程提前退出时，打印日志末尾若干行并以非零退出码返回。

### 6.2 daemon run

1. 用 `datalock.Acquire(stateDir)` 获取单实例锁，失败时返回“已有实例运行”。
2. 日志：`daemon start` 把子进程的 stdout/stderr 追加写入 `upssim.log`；启动前文件超过 5 MiB 时轮转为 `upssim.log.1`（保留 1 份）。这样能捕获 panic 等早期错误，日志量很小，不需要 lumberjack。前台 `daemon run` 直接输出到 stderr。日志只记录生命周期、设备增删改、绑定失败和恢复结果，不包含凭据或报文内容。
3. 读取 `devices.json` 并逐台恢复设备（§8.2）。
4. 在 `127.0.0.1:<--control-port，默认 0>` 启动控制面，生成 32 字节随机 token，以“临时文件 + rename”的方式原子写入 `control.json`，Unix 下权限为 0600。
5. 等待 SIGINT、SIGTERM 或 `POST /v1/shutdown`。退出时依次停止控制面、关闭所有 `sim.Server`、删除 `control.json`、释放锁。

### 6.3 daemon stop / status

- `stop` 调用 `POST /v1/shutdown`，再以锁可获取或状态接口不可达作为退出完成的判断，最多等待 10 秒。**不按 PID 强制结束进程**，避免 PID 被复用后误杀无关进程。超时时输出 PID 和日志路径，由开发者手动处理。
- `status` 输出 PID、控制地址、启动时间、版本、设备总数和 `failed` 设备数。

### 6.4 平台范围

第一版支持 Linux 与 Windows，与发布目标和 `datalock` 现有实现一致。macOS 需要为 `datalock` 补充 `flock` 实现：把 `lock_linux.go` 的构建约束改为 `unix` 即可。由于这会影响主服务的构建矩阵，单独评审，见 §14。

---

## 7. 控制面协议

### 7.1 传输与认证

- HTTP/1.1 + JSON，只绑定 `127.0.0.1`。请求体上限 64 KiB，JSON 使用 `DisallowUnknownFields`，读、写、空闲超时分别为 5 秒、5 秒、30 秒。
- 每个请求都必须携带 `Authorization: Bearer <token>`，用 `crypto/subtle.ConstantTimeCompare` 校验，失败返回 401。浏览器页面无法读取 token，也不能在简单请求中附加该请求头，可以防止本机网页跨站调用控制面。
- 客户端只连接 `control.json` 中的 loopback 地址；文件中的地址不是 loopback 时拒绝连接，防止被篡改的文件把 token 发往其他主机。
- 选择 loopback HTTP 而不是 Unix socket 或 Windows named pipe，是为了在两个平台上使用同一套实现，并且只依赖标准库。

`control.json` 示例：

```json
{"schema": 1, "pid": 12345, "address": "127.0.0.1:53817", "token": "…64 hex…", "started_at": "2026-10-29T10:00:00+08:00", "version": "dev"}
```

### 7.2 接口

| 方法与路径 | 说明 | 成功 |
|---|---|---|
| `GET /v1/status` | 守护进程状态 | 200 |
| `POST /v1/shutdown` | 请求优雅退出 | 202 |
| `GET /v1/devices` | 设备列表（含实时快照） | 200 |
| `POST /v1/devices` | 创建设备 | 201 |
| `GET /v1/devices/{name}` | 设备详情 | 200 |
| `PATCH /v1/devices/{name}` | 原子修改（`sim.Change` 对应字段） | 200 |
| `DELETE /v1/devices/{name}` | 删除设备 | 200 |

### 7.3 设备资源

```json
{
  "name": "ups-2",
  "status": "running",
  "et1k": false,
  "phases": 1,
  "ip": "127.0.0.2",
  "endpoints": {"snmp": "127.0.0.2:52011", "modbus": "127.0.0.2:52012", "ayi9": "127.0.0.2:52013"},
  "profiles": {"snmp": "rfc1628-generic", "modbus": null, "ayi9": "ayi9-json-generic"},
  "control": "manual",
  "mode": "battery",
  "link": "up",
  "params": {"load_pct": 45, "battery_capacity_pct": 100, "temperature": 25, "input_voltage": 220, "discharge_minutes": 30},
  "ayi9_error_code": 0,
  "password_set": false,
  "snapshot": {"mains": false, "low_battery": false, "battery_capacity_pct": 58.3, "seconds_on_battery": 42, "runtime_seconds": 1049},
  "failure": null
}
```

- `control` 取值为 `manual`（手动模式）或 `scenario`（按场景时间轴运行）；处于 `scenario` 时，`mode` 表示当前时刻场景算出的模式。
- 响应中不包含 community、password 或 token，只用 `password_set` 表示是否设置了密码。

### 7.4 错误模型

错误响应格式为 `{"error": {"code": "...", "message": "..."}}`，`message` 为中文且不包含凭据。

| code | HTTP | 场景 |
|---|---|---|
| `unauthorized` | 401 | token 缺失或不匹配 |
| `validation_failed` | 422 | 参数越界、名称非法、`low` 与大电量冲突、尝试修改不可变字段 |
| `device_exists` | 409 | 名称重复 |
| `device_not_found` | 404 | 设备不存在 |
| `address_conflict` | 409 | 与其他模拟设备的端点冲突，`message` 中包含占用者名称 |
| `address_in_use` | 409 | 系统绑定失败（端口被其他进程占用或权限不足） |
| `limit_exceeded` | 409 | 设备数达到上限（128，覆盖现有 100 台压测规模） |
| `body_too_large` | 413 | 请求体超过 64 KiB |
| `device_failed` | 409 | 修改恢复失败（`status=failed`）的设备 |
| `persist_failed` | 500 | 写入 `devices.json` 失败；`message` 说明运行期状态是否已生效 |

---

## 8. 持久化

### 8.1 devices.json

```json
{
  "schema": 1,
  "devices": [{
    "name": "ups-1", "created_at": "…", "et1k": true, "phases": 1, "ip": "127.0.0.1",
    "endpoints": {"snmp": "127.0.0.1:11161", "modbus": "127.0.0.1:11502", "ayi9": "127.0.0.1:12390"},
    "control": "manual", "mode": "mains", "link": "up",
    "params": {"load_pct": 30, "battery_capacity_pct": 100, "…": "…"},
    "scenario_yaml": null,
    "community": "sim-public", "password": "", "ayi9_error_code": 0
  }]
}
```

- 每次 `add`、`set`、`remove` 成功后，用“临时文件 + rename”原子写入；Unix 下权限为 0600。写入失败时返回 `persist_failed`：`add` 会关闭刚创建的监听器，不登记该设备；`set` 的运行期状态已经生效、无法可靠回滚，返回的错误会明确说明“已生效但未持久化，重启后恢复旧配置”；`remove` 已停止设备，但重启后会重新恢复。
- `scenario_yaml` 保存场景原文（≤ 64 KiB），恢复时重新严格解析。
- 文件中的凭据都是虚构测试值，即便如此也不会出现在任何输出中。

### 8.2 恢复语义

- 守护进程重启后，按持久化的配置、模式和参数逐台恢复。由于期间没有观测，物理时间轴从恢复时刻重新开始：手动模式下电量取最近一次显式设置的电量（未设置过则为 100%；处于 low 模式时钳制为 15%），场景模式从 0 秒重放。
- 某台设备恢复失败（例如原端口被占用）时，该设备 `status=failed` 并记录 `failure`，其他设备照常运行。失败的设备不会被丢弃，开发者可以 `remove` 后重新 `add`。
- `devices.json` 无法解析或 schema 不受支持时，守护进程拒绝启动并说明原因，不会自动覆盖原文件。

---

## 9. 安全约束

- **只读不变**：设备监听器仍由现有 `internal/sim` 处理请求，非只读请求一律拒绝并记录元信息。控制面只修改模拟器自身的状态，守护进程不会对外发起任何连接。
- **只监听 loopback**：设备端点和控制面都只允许 loopback IP，非 loopback 地址在参数校验阶段即被拒绝。
- **凭据**：只允许虚构测试凭据。community、password、token 不出现在命令输出、日志、错误信息和 API 响应中。文档中继续明确：不得把真实设备的凭据放进命令行。
- **资源上限**：设备数 ≤ 128；每台设备沿用现有上限（64 条 TCP 连接、4096 条请求记录、溢出时计数丢弃）；控制面请求体 ≤ 64 KiB。
- **与真实设备隔离**：模拟器不读取 `ups-warden` 数据目录，也不读取 `/data/` 下的设备配置。联调数据库仍按项目约定使用临时目录。

---

## 10. 兼容性与影响面

| 对象 | 影响 |
|---|---|
| `upssim` 旧用法 | 无子命令或首参数以 `-` 开头时进入旧的单设备前台模式，参数、输出格式和 `main_test.go` 保持不变 |
| `sim.Start` / `sim.Config` / `Scenario` YAML | 签名与 schema 不变；`Apply`、`Snapshot`、`Change`、`Mode` 为新增 API；新增的起始条件字段不导出且不参与 YAML |
| 进程内测试与压测 | 不调用 `Apply` 时行为与现在一致；`state()` 改为加读锁，开销可忽略，压测完成后复核 CPU 占比 |
| 架构测试 | R-DEP-4 不变；`ups-warden` 主服务不依赖 `cmd/upssim` 的新代码 |
| `mise.toml` | 新增 `sim:build`（`go build -trimpath -o ../.cache/build/ ./cmd/upssim`）和 `sim`（依赖 `sim:build`，透传参数运行该二进制）；产物放在已被忽略的 `.cache/build/` |
| 文档 | 实施时同步更新 [协议驱动与本机模拟器](../../../../docs/guides/protocol-drivers.md) 的“本机模拟器”一节、[开发指南](../../../../docs/guides/development.md) 的联调说明、[前端开发计划](../../../../docs/planning/frontend-plan.md) 的手工联调步骤，以及 [后端开发计划](../../../../docs/planning/backend-plan.md) 的实施记录 |
| `AGENTS.md` | 新增一行技能入口，与现有的 README 技能入口并列，见 §11.2 |
| `.agents/skills/upssim/` | 新增项目级技能，见 §11；只描述用法与约束，参数事实链接现状文档 |

---

## 11. 项目级技能：upssim

### 11.1 定位

工具提供了能力，但 agent 在联调时仍可能绕开它：直接往数据库写假数据、手写 mock，或拿真实设备的地址去“试一下”。这几种做法都违反项目的设备安全约束。因此把 upssim 的使用方式定义为项目级技能，规定 agent 在需要设备数据时**何时使用、怎样隔离、按什么步骤操作、怎样核对结果、如何清理和汇报**。

技能面向 agent；开发者手动使用时，仍以现状文档中的命令说明为准。三类文档的分工如下，同一事实只在一处维护：

| 层 | 承载内容 | 位置 |
|---|---|---|
| 现状文档 | 命令、参数、默认值、输出格式、错误码（唯一事实来源） | [协议驱动与本机模拟器](../../../../docs/guides/protocol-drivers.md#多设备守护进程) 的“多设备守护进程”一节 |
| 项目级技能 | 触发条件、安全红线、隔离原则、标准工作流、决策规则、清理与汇报口径、联调剧本 | `.agents/skills/upssim/` |
| 本方案 | 设计理由、取舍与放弃的方案 | 本文件 |

技能不复制参数表，需要参数细节时链接现状文档；技能中出现的每条命令都必须能在当前实现中运行。

### 11.2 目录、触发与入口

沿用仓库现有的技能约定（`.agents/skills/<name>/SKILL.md`，frontmatter 含 `name` 与 `description`，正文用简体中文）：

```text
.agents/skills/upssim/
  SKILL.md              触发条件、安全红线、隔离原则、标准工作流、决策规则、失败处理、汇报口径
  references/
    recipes.md          常用联调剧本：停电→低电→恢复、5 秒闪断、失联、多设备、AYI9 错误码、关机计划 dry-run
```

frontmatter 草案（description 沿用现有技能的英文写法，以便匹配触发）：

```yaml
---
name: upssim
description: >-
  Use when UPS Warden development, frontend/backend integration, acceptance or debugging needs
  UPS device data without real hardware: starting or stopping the upssim daemon, adding simulated
  UPS devices, switching mains/battery/low/link-down/flash states, choosing loopback IPs and ports,
  or wiring simulated endpoints into a temporary ups-warden instance. Do not use against real
  devices, for CP-1/CP-4 hardware acceptance, or with production data directories.
---
```

`AGENTS.md` 中新增一行入口：“联调或测试需要 UPS 设备数据时使用 `.agents/skills/upssim` 技能；不得用伪造数据库数据或连接真实设备代替。”

### 11.3 SKILL.md 内容要点

**1. 安全红线**（放在最前，优先于工作流）

- 只操作 loopback 模拟设备。真实设备的 IP、community、密码不得用于 upssim 参数，也不得写入数据源配置。
- `ups-warden` 只使用临时 `--data-dir`，不使用仓库的 `/data/` 或任何含真实设备配置的数据目录。
- 规则保持 dry-run，不执行真实的关机计划或动作。
- 不按 PID 结束进程。未经用户明确要求，不停止、不删除不是本次创建的守护进程或设备。
- 遇到端口被占用时，换端口或用 `0`，不结束占用端口的其他进程。
- 模拟器得出的结果不能表述为真机、CP-1～CP-4 或双方联调的验收。

**2. 隔离原则**

- agent 自己发起的联调使用独立状态目录：会话临时目录下的 `upssim/`，通过 `--state-dir` 或 `UPSSIM_STATE_DIR` 指定。这样不会干扰开发者在默认状态目录中运行的守护进程。
- 只有当用户明确要求操作他当前在用的模拟器时，才使用默认状态目录，并先用 `daemon status`、`list` 确认现状再修改。

**3. 标准工作流**

1. **准备**：`mise install`、`mise run sim:build`；确认目标 `ups-warden` 实例的数据目录是临时目录。
2. **启动**：先 `daemon status`，未运行时再 `daemon start`，并记下这次是否由自己启动，供清理时判断。如果 `daemon start` 提示“当前终端所在的 Windows Job 不允许子进程脱离”，或者已知宿主会在每条命令结束时结束其子进程树，就改用宿主的后台任务机制运行前台 `daemon run`，之后的命令照常执行；不支持后台任务时，把启动、操作、核对和清理放进同一条命令。
3. **规划设备**：按要验证的 Profile 选择 `--et1k` 或标准模式；多台设备使用不同的 loopback IP；需要数据源配置长期有效时显式指定端口，否则用 `0`。
4. **创建**：用 `add … --json` 创建设备，端点和建议的 Profile 键以输出为准，不自行推测端口。
5. **接入**：按 [监控 API 契约](../../../../docs/reference/monitoring-api.md) 在临时 `ups-warden` 中创建设备和数据源（`protocol`、`host`、`port`、`profile_key`、`params`），凭据只用模拟器的虚构值。
6. **驱动状态**：用 `set` 切换状态。切换后等待去抖与轮询周期，再通过 API 或 SSE 核对事件和快照；以实际观察到的结果为准，不用固定 sleep 代替核对。
7. **清理**：删除本次创建的设备；守护进程如果由本次启动就执行 `daemon stop`；临时 `ups-warden` 一并停止。

**4. 决策规则**

| 需要 | 做法 |
|---|---|
| 验证 ET1K 内置 Profile | `--et1k`，只能单相 |
| 验证 `rfc1628-generic` / `ayi9-json-generic` | 标准模式；Modbus 没有通用内置 Profile，需使用自定义 Profile |
| 多台设备 | 每台使用不同的 loopback IP，避免同一 IP 串行采集 |
| `address_in_use` | 换端口或用 `0` |
| `address_conflict` | 换端口，或先确认占用该端口的模拟设备是否还需要 |
| 需要可复现的时间轴 | `--scenario` |
| 需要交互式控制 | `--mode` / `--link` / `--flash` |
| 旁路、故障、过载 | 第一版不支持；如实说明，不用其他手段伪造 |

**5. 失败处理**

- `daemon start` 超时时，先读日志末尾；必要时用前台 `daemon run` 排查。
- 后续命令返回退出码 3（守护进程未运行），而本次并没有执行 `daemon stop` 时，多半是宿主结束了 Job 内的守护进程，处理方式见工作流第 2 步，不要反复重试 `daemon start`。
- 不靠删除 `devices.json`、`control.json` 等状态文件来“修复”问题；需要重置时使用新的临时状态目录。

**6. 汇报口径**

汇报时列出：状态目录、设备名与端点、执行过的状态切换及其时间、实际观察到的事件和字段、清理结果。结论中明确标注“本机模拟器验证”。

### 11.4 references/recipes.md 剧本

每个剧本写明：目的、命令序列、预期观察结果、清理方式。预期结果以事件名和字段名为准；去抖时长与阈值以现状文档和设备配置为准。剧本可以附上默认设置下的实测时序作参考，用于设置核对的超时上限，不作为固定的 sleep。

| 剧本 | 操作 | 预期观察 |
|---|---|---|
| R1 停电→低电→恢复 | `set --mode battery` → `--mode low` → `--mode mains --capacity 100` | `mains_lost` → `battery_low` → `mains_restored`、`battery_low_cleared`。只切 `--mode mains` 时，模拟器的低电标志要等电量回充超过 20% 才清除，ups-warden 还要求电量达到清除阈值，所以 `battery_low_cleared` 会明显延后；恢复时同时指定 `--capacity` 可以让清除只受恢复去抖影响 |
| R2 5 秒闪断 | `set --flash` | 去抖窗口吸收闪断，不产生 `mains_lost` |
| R3 失联与恢复 | `set --link down` → `--link up` | `comm_lost` → `comm_restored`；失联期间供电状态保持不变 |
| R4 多设备总览 | 在 `127.0.0.2`～`127.0.0.4` 上分别添加市电、电池、低电三台设备 | 总览与设备列表同时呈现三种状态，SSE 推送与界面一致 |
| R5 AYI9 错误码 | `add --ayi9-error-code 103` 或用 `set` 切换 | 连接测试返回 `source_password_wrong` |
| R6 关机计划联调 | 配置 dry-run 规则与计划后执行 R1 | 计划倒计时与进度只在 dry-run 下推进，不执行真实动作 |

### 11.5 维护规则

- **事实单点维护**：子命令、参数、输出字段或错误码变化时，在同一变更中更新现状文档；技能只在工作流、红线、决策规则或剧本变化时修改。
- **落地时机**：技能在 CLI 与现状文档就绪后编写（§13 S7），避免描述尚未实现的行为。方案评审阶段不提前创建技能文件。
- **验证**：按 SKILL.md 与 recipes.md 在临时环境中逐条走通（AC-SIM-07），并检查 frontmatter 格式与链接。

---

## 12. 测试与验收

### 12.1 自动化测试

| 层 | 内容 |
|---|---|
| `internal/sim` 单元 | 注入虚拟时钟验证锚点重定基：mains→battery→low→mains 过程中电量连续、`SecondsOnBattery` 延续或清零、回充上限不变；`--flash` 5 秒恢复；`low` 与大电量组合被拒绝；校验失败时状态不变 |
| `internal/sim` 并发 | 并发执行 `Apply` 与三协议请求，确认每个响应只对应一种状态；`-race` 在 Linux CI 中执行 |
| `cmd/upssim` 注册表 | 名称校验、`address_conflict`（同传输层同 IP 同端口）、UDP 与 TCP 同端口共存、`address_in_use` 后无残留监听器、设备数上限 |
| `cmd/upssim` 控制面 | token 缺失或错误返回 401、未知字段与超大请求体被拒绝、PATCH 原子性、错误响应不含凭据、`control.json` 地址非 loopback 时客户端拒绝连接 |
| `cmd/upssim` 持久化 | 往返一致；端口 `0` 持久化为实际端口；恢复时端口被占用的设备标记为 `failed` 且其他设备正常；损坏文件导致拒绝启动 |
| 集成 | 在进程内以 `daemon run` 入口和临时状态目录启动守护进程，经控制面添加设备，再用真实 SNMP/Modbus/AYI9 驱动和内置 Profile 采集，验证切换模式后归一化结果中 `mains_ok`、`low_battery`、`output_source` 等字段的变化；`--link down` 时采集失败 |
| 兼容 | 现有 `cmd/upssim/main_test.go`、`internal/sim` 测试和 `backend:acceptance` 不修改即可通过 |

### 12.2 验收标准

| 编号 | 标准 |
|---|---|
| AC-SIM-01 | 在 Windows 与 Linux 上执行 `daemon start` 后关闭终端，设备仍可被 `ups-warden` 采集；`daemon stop` 后所有端口释放 |
| AC-SIM-02 | 守护进程运行中 `add` 一台 `--mode battery` 的设备，`ups-warden` 在去抖窗口后产生 `mains_lost` 事件 |
| AC-SIM-03 | 执行 `set --mode mains` 后产生 `mains_restored`；`--mode low` 产生低电事件；`--link down` 产生通信丢失；`--flash` 不产生停电告警 |
| AC-SIM-04 | 指定已被其他进程占用的端口时，`add` 返回 `address_in_use` 且不留下任何监听器；与另一台模拟设备端口冲突时返回 `address_conflict` 并指出设备名 |
| AC-SIM-05 | 守护进程重启后，设备按原端口恢复，`ups-warden` 数据源无需修改即可继续采集 |
| AC-SIM-06 | `mise run backend:check` 通过；命令输出、日志和 API 响应中不出现 community、password 或 token |
| AC-SIM-07 | 仅依据 upssim 技能，agent 在临时状态目录和临时 `ups-warden` 数据目录中走通 R1～R3 剧本，观察到预期事件并完成清理；默认状态目录中的守护进程不受影响 |

以上验收只证明模拟器和本机联调链路可用，**不能作为真机兼容性、CP-1/CP-4 验收或双方联调（CP-2）的证据**。

---

## 13. 实施步骤

| 步骤 | 内容 | 交付与检查 |
|---|---|---|
| S1 | `internal/sim`：运行期状态段、锚点重定基、`Apply` / `Snapshot`，运行期可变配置纳入锁保护 | 单元测试与并发测试；现有 sim 测试不修改即通过 |
| S2 | `cmd/upssim`：子命令分发，旧模式迁移到 `legacy.go` | `main_test.go` 不修改即通过 |
| S3 | 注册表、冲突检测、`devices.json` 持久化与恢复 | 注册表与持久化测试 |
| S4 | 控制面 HTTP 与客户端子命令 `add` / `set` / `list` / `show` / `remove` | 控制面测试、集成测试 |
| S5 | `daemon start` / `run` / `stop` / `status`，平台相关的脱离终端实现，日志 | Windows 本机手工验证 AC-SIM-01、05；Linux 交叉编译并在 CI 或实机验证 |
| S6 | `mise.toml` 新增 `sim:build`、`sim` 任务；同步 §10 所列文档 | `mise run backend:check`；文档链接检查 |
| S7 | 项目级技能 `.agents/skills/upssim`（SKILL.md、references/recipes.md），并在 `AGENTS.md` 中新增入口 | 在临时环境中按剧本验证 AC-SIM-07；检查 frontmatter 与链接 |

S1、S2 相互独立，可以并行；S3～S5 依次依赖前一步；S7 依赖 S6，确保技能描述的是已实现、已写入文档的行为。

---

## 14. 风险与待确认事项

| 项 | 说明 | 处理 |
|---|---|---|
| macOS 支持 | `datalock` 没有 darwin 实现，macOS 默认也只有 `127.0.0.1` 可用 | 待确认团队是否有 macOS 开发者；需要时单独评审 `datalock` 构建约束的调整 |
| 同 IP 串行采集 | 多台设备共用 `127.0.0.1` 时，collector 会串行采集 | 在文档和 `add` 输出中提示使用不同的 loopback IP；不修改 collector |
| `go run` 与后台进程 | 临时二进制会被删除 | 检测到 `go run` 时拒绝 `daemon start`，提供 `sim:build` 任务 |
| 第二阶段模式的编码依据 | 旁路、故障、过载的输出编码只来自 Profile 映射 | 第二阶段实现，并明确不作为真机证据 |
| 恢复后物理时间轴重置 | 重启期间没有观测，电量从配置值重新开始 | 作为已知行为写入文档；如需延续电量，后续在退出时保存快照 |
| 默认状态目录 | 使用用户配置目录，不写入仓库 | 待评审确认；也可以改为通过 mise 任务固定为仓库的 `data/upssim` |
| 技能与 CLI 漂移 | 命令行变化后，技能可能引用已失效的命令 | 参数事实只在现状文档维护，技能只链接；CLI 变更时把技能列入文档检查范围，并重新走一遍 AC-SIM-07 |

---

## 15. 备选方案与放弃原因

| 方案 | 放弃原因 |
|---|---|
| 每台设备运行一个前台 `upssim`，借助 `&`、`nohup` 或 `start /b` 挂起 | 没有统一视图，无法在运行中修改状态；各平台写法不同；端口冲突只能靠人工排查 |
| 守护进程监听设备 YAML 文件，修改后热加载 | 操作结果没有同步反馈，校验错误不易发现；编辑器保存时可能读到写了一半的文件 |
| Unix socket / Windows named pipe 作为控制通道 | 需要两套实现；loopback HTTP 加 token 已能满足本机隔离需求 |
| 把模拟设备内置到 `ups-warden` 主服务 | 违反 R-DEP-4 和“生产服务不依赖模拟器”的边界，并增加误连真实设备的风险 |
| 引入 cobra 等 CLI 框架 | 新增长期依赖；子命令数量少，标准库 `flag` 已足够 |
| 在场景 YAML 中增加运行期字段来表达切换 | 会改变 schema=1 契约；把推进逻辑提取为接收起始条件的内部函数，就能在不改动 YAML 的前提下实现锚点重定基 |
| 端口冲突时自动换端口 | 后端数据源会在不知情的情况下指向错误端口；显式失败更容易排查 |
| 只在现状文档中写使用说明，不定义技能 | agent 不会主动套用文档中的安全约束和清理步骤，容易退回伪造数据或误用默认状态目录；技能能在需要设备数据时自动触发统一流程 |
| 把完整的 CLI 参数表复制进技能 | 与现状文档重复维护，容易漂移；技能只保留流程与约束 |

---

## 16. 实施记录（2026-10-29）

### 16.1 交付范围

| 步骤 | 交付 |
|---|---|
| S1 | `internal/sim/live.go`：`Mode`、`Change`、`Status`、`Apply`、`Snapshot`、`Config.Initial`；`scenario.go` 提取 `stateFrom`；AYI9 错误码改为运行期可变 |
| S2 | `cmd/upssim/main.go` 分发子命令；旧前台模式迁至 `legacy.go`，参数与输出不变 |
| S3 | `registry.go`：校验、`(传输层, IP, 端口)` 冲突检测、`devices.json` 原子持久化与按原端口恢复 |
| S4 | `control.go`：loopback HTTP 控制面、Bearer token、64 KiB 请求体、未知字段拒绝；`devices.go`：`add` / `set` / `list` / `show` / `remove` |
| S5 | `daemon.go`、`detach_*.go`：`daemon start` / `run` / `stop` / `status`，单实例锁，Windows 优先脱离 Job |
| S6 | `mise.toml` 新增 `sim:build`、`sim`；同步 [协议驱动与本机模拟器](../../../../docs/guides/protocol-drivers.md)、[开发指南](../../../../docs/guides/development.md)、[前端开发计划](../../../../docs/planning/frontend-plan.md)、[后端开发计划](../../../../docs/planning/backend-plan.md) |
| S7 | `.agents/skills/upssim/`（`SKILL.md`、`references/recipes.md`），`AGENTS.md` 新增入口 |

### 16.2 与方案的差异

- 锚点通过内部函数 `stateFrom` 传入，没有在 `Scenario` 上增加隐藏字段（§4.2）。
- 手动控制的回充上限固定为 100%，不随设备参数变化（§4.2）。
- 没有使用 lumberjack，改为启动前按大小轮转并追加子进程输出（§6.2）。
- Windows 增加脱离 Job 及失败回退（§6.1）。实测中，当前 agent 执行环境的 Job 不允许脱离，后台守护进程会在单条命令结束时被结束；技能为此要求 agent 使用宿主的后台任务机制运行前台 `daemon run`。
- `set` 持久化失败不回滚运行期状态，改为返回明确错误（§8.1）。新增 `body_too_large`、`device_failed`、`persist_failed` 错误码（§7.4）。
- `PATCH` 中的不可修改字段按未知字段拒绝；CLI 对这些参数追加“需 remove 后重新 add”的提示（§3.3）。

### 16.3 验证

- 自动化测试：`internal/sim` 新增连续性、闪断、通信与参数、原子校验、协议可见性、并发切换测试；`cmd/upssim` 新增校验、冲突、`address_in_use` 无残留、持久化与恢复（含端口被占时标记 `failed`）、损坏文件拒绝启动、控制面认证与限制、PATCH 原子与脱敏、`control.json` 地址校验、端到端测试。端到端测试在进程内启动守护进程，经 CLI 增删改设备，用真实 SNMP/AYI9 驱动和内置 Profile 采集归一化，并覆盖退出码、停止、重启恢复和凭据/token 不泄露。现有 `main_test.go` 与 `internal/sim` 测试仍通过；`idle_test.go` 直接构造了包内 `Server`，只替换了构造方式。
- 本机实测（Windows）：构建产物的 `daemon start` / `status` / `stop`、重启后按原端口（含系统分配端口）恢复、被其他进程占用端口时 `address_in_use` 且 UDP 端口无残留、模拟设备之间的 `address_conflict`、日志不含凭据。
- 与临时数据目录的 `ups-warden` 联调（AC-SIM-02/03 与 R1～R3）：5 秒闪断无事件；`battery` 约 10 秒后 `mains_lost`；`low` 后 `battery_low`；`mains --capacity 100` 约 60 秒后 `mains_restored` 与 `battery_low_cleared`；`link down` 后 `comm_lost`，`link up` 后第 7 秒出现 `comm_restored`（受失败退避影响，最长约 30 秒）。
- 技能走通（AC-SIM-07）：在临时目录中按 SKILL.md 工作流和 recipes.md 的公共片段，依次执行 R5、R4、R2、R1 并清理。结果：`daemon status` 未运行时返回 3；R5 连接测试返回 502 / `source_password_wrong`，恢复后返回 200；R4 三台设备的事件和 `output_source` 符合预期；R2 无事件；R1 中 `mains_lost` +12 秒、`battery_low` +1 秒、`mains_restored` 与 `battery_low_cleared` +61 秒；日志中没有凭据和 setup token。走通过程中发现两处文档问题并已修正：数据源刚添加时快照字段可能为空；市电时 `output_source` 为 `normal`。R6 关机计划剧本未实测。
- 未验证：Linux 实机（只通过了 `GOOS=linux go vet`）、关闭真实终端窗口后的存活（AC-SIM-01 的 Windows 部分只在允许脱离 Job 的环境中成立）、`-race`、macOS。
