# UPS Warden 需求文档

> **文档版本**：v0.3（草案）
> **日期**：2026-10-11
> **依据**：[《协议支持探测报告》](../reference/protocols/et1k-probe-report.md)（ET1K 历史探测记录）及项目方需求说明
> **状态**：需求草案（proposed），不代表已实现能力。标注为「默认假设」的条目是暂定决策，需在第 17 节「待确认事项」中定稿；标注为「已确认」的条目已经项目方确认。
> **阅读入口**：[文档导航](../README.md) · [当前开发指南](../guides/development.md) · [后端方案与评审决策](../../.agents/notes/proposed/architecture/2026-10-11-backend-design.md)

---

## 0. 文档约定

| 约定 | 说明 |
|---|---|
| 优先级 | **P0** = 首版（MVP）必须；**P1** = 首版之后的第一批增强；**P2** = 远期可选 |
| 需求编号 | `FR-<模块>-<序号>` 为功能需求；`NFR-<序号>` 为非功能需求；`SEC-<序号>` 为安全需求 |
| 事实标注 | 「实测」= 引用 2026-10-11 探测报告的观察，不代表本次重新验证；「推断」= 依据文档或规范推出，尚未在设备上验证 |
| 术语 | 见 1.4 |

---

## 1. 项目概述

### 1.1 背景

局域网内的 UPS 通常通过网卡（SNMP / Modbus / 厂商私有协议）对外提供状态数据。停电时，NAS、服务器、PC 等设备需要在电池耗尽前有序关机，同时管理员需要及时收到通知。ET1K 的历史探测记录显示：未发现原生 NUT 服务，SNMP 读取存在 GET / GETNEXT 与 request-id 编码限制；这些观察不等于已验证所有第三方工具的兼容性。本项目目标是通过 WebUI 统一管理设备与联动配置。

### 1.2 目标

建设一个 **通过 WebUI 管理** 的 UPS 监控与联动系统：

1. 在 WebUI 中添加、配置和管理多台 UPS 设备；
2. 实时查看 UPS 状态与历史趋势；
3. 根据 UPS 状态（停电、低电量、过载、通讯中断等）发送消息通知；
4. 根据 UPS 状态执行脚本、远程关闭局域网设备，并支持按顺序编排关机；
5. 支持 **SNMP、Modbus TCP、AYI9 云监控卡 JSON API** 三种协议，并通过 **设备 Profile** 机制适配多种型号，而不是只针对 ET1K 硬编码。

### 1.3 非目标（首版明确不做）

| 项 | 原因 |
|---|---|
| HTTP JSON 协议（如 ET1K 的 `monitor_data.htm`） | 通用性太低，按项目方要求排除；系统 **不访问任何设备 WebUI 路径** |
| NUT 协议 | 不在本次协议范围内 |
| 控制 UPS 本体（开关机、蜂鸣器、自检等写指令） | 实测存在事故风险（见报告第 7 节），首版驱动层 **完全只读**（见 SEC-01） |
| 局域网 UPS 自动扫描 / 端口扫描 | 设备会对高并发连接限流，扫描结果不可信，且有误触写端点的风险 |
| Modbus RTU（串口）/ Modbus ASCII | 首版只支持 Modbus TCP |
| SNMPv3 | 历史探测未获得响应，不能据此断言所有配置下均不支持；首版只规划 v1/v2c（是否纳入后续版本见第 17 节） |

### 1.4 术语

| 术语 | 定义 |
|---|---|
| 设备（Device） | 系统中的一台逻辑 UPS |
| 数据源（Source） | 设备的一个协议连接（如 `SNMP @ <UPS_HOST>:161`，地址为占位符）。一台设备可以绑定多个数据源 |
| 驱动（Driver） | 某种协议的采集实现：SNMP、Modbus TCP、AYI9 云监控卡 JSON API |
| Profile | 型号适配描述：把协议原始数据（OID / 寄存器 / QAD 字段）映射为统一字段，包含地址、类型、缩放、枚举、有效性规则 |
| 快照（Snapshot） | 一次轮询得到的、归一化后的设备状态 |
| 事件（Event） | 由状态变化派生出的离散事实，如"市电中断""电池低" |
| 规则（Rule） | "触发条件 → 动作"的联动配置 |
| 动作（Action） | 通知、执行脚本、远程关机、HTTP 请求等 |
| 受控主机（Host） | 需要被远程关机的局域网设备 |
| 关机计划（Shutdown Plan） | 按顺序、分组执行的多主机关机编排 |
| QAD / RTD | AYI9 云监控卡 JSON API 中的实时数据 / 额定信息指令 |
| `ayi9-json` | AYI9 云监控卡 JSON API 在代码和配置中的协议标识，详见 4.4 |

---

## 2. 用户与使用场景

### 2.1 角色

| 角色 | 权限 | 优先级 |
|---|---|---|
| 管理员（admin） | 全部功能，包括设备、规则、动作、主机凭据和用户管理 | P0 |
| 操作员（operator） | 查看状态，确认告警，手动触发已配置好的动作，但不能修改配置 | P1 |
| 只读（viewer） | 只能查看状态、历史和事件 | P1 |

### 2.2 典型场景

| 编号 | 场景 | 期望 |
|---|---|---|
| S1 | 家庭 / 小型办公室：1 台 UPS 带 NAS + 路由器 + PC | 停电后推送通知；电池模式持续 N 分钟或电量低于阈值时，依次关闭 PC 和 NAS |
| S2 | 小型机房：多台不同品牌、不同协议的 UPS | 用一个面板统一查看，各 UPS 按负载分别编排关机 |
| S3 | 短暂闪断（几秒） | 不误报、不误关机（去抖） |
| S4 | 关机过程中市电恢复 | 未执行的步骤取消，已执行的步骤记录在案；可选市电稳定后 WOL 唤醒 |
| S5 | UPS 网卡失联 | 产生通讯中断告警；如果失联前处于电池模式，按策略继续评估关机条件 |
| S6 | 接入新型号 | 不改代码，通过选择或导入 Profile 完成接入，并能在 WebUI 中对照原始值进行校准 |

---

## 3. 总体架构与技术栈

### 3.1 技术栈

| 层 | 技术 | 说明 |
|---|---|---|
| 后端 | Go（版本以根目录 `mise.toml` 为准） | 规划为单进程服务：采集、规则、动作、API、静态资源托管；目前仅有 HTTP 脚手架 |
| 前端 | React 19 + TypeScript 7 | SPA |
| 前端构建 | Vite 8 + Vite+ 1.x | 开发、构建、测试、lint、格式化统一使用 Vite+ 工具链（`vp`） |
| 运行时 / 包管理 | Node.js 24 LTS + pnpm 12 | 仅用于前端开发与构建，运行时不依赖 Node；版本由仓库根目录 `mise.toml` 管理 |
| 存储 | SQLite（**默认假设**） | 内嵌、零运维；推荐纯 Go 实现（如 `modernc.org/sqlite`），避免 cgo，方便交叉编译 |
| 实时推送 | SSE（**默认假设**） | 服务端向浏览器单向推送快照和事件 |

需求草案最初列出的依赖候选为 SNMP `gosnmp`、Modbus TCP 客户端库、SSH `golang.org/x/crypto/ssh`。后续[后端方案](../../.agents/notes/proposed/architecture/2026-10-11-backend-design.md)第 2.1、21.1 节记录了 SNMP / Modbus 改为自研最小只读子集的评审决定及理由；M2 已实现该只读子集与本机验证，真机 CP-1 待验收。原候选列表不是当前依赖清单。

### 3.2 逻辑架构

```
┌──────────────────────────── ups-warden（单二进制）────────────────────────────┐
│                                                                              │
│  WebUI (React SPA, embed) ── REST API /api/v1 ── SSE /api/v1/stream           │
│                                   │                                          │
│  ┌──────────────┐   Snapshot   ┌──┴───────────┐   Event   ┌───────────────┐  │
│  │ 采集调度器    │ ───────────▶ │ 状态引擎      │ ────────▶ │ 规则引擎       │  │
│  │ (每数据源一协程)│             │ 归一化/合并/  │           │ 条件/持续/冷却 │  │
│  └──────┬───────┘              │ 判定/去抖     │           └──────┬────────┘  │
│         │                      └──────┬───────┘                  │           │
│  ┌──────┴──────────────────┐         │ 历史写入           ┌──────┴────────┐  │
│  │ Driver（只读）+ Profile  │         ▼                    │ 动作执行器     │  │
│  │ SNMP │ Modbus │ ayi9-json│   ┌──────────┐               │ 通知/脚本/SSH/ │  │
│  └──────┬──────────────────┘   │ SQLite    │◀── 审计 ──── │ HTTP/关机计划  │  │
│         │                      └──────────┘               └──────┬────────┘  │
└─────────┼────────────────────────────────────────────────────────┼───────────┘
          ▼                                                        ▼
   UPS 网卡（UDP161 / TCP502 / TCP2390）                 通知渠道 / 局域网主机
```

### 3.3 部署形态

| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-DEP-01 | 以单个可执行文件发布，前端静态资源通过 `embed` 内嵌 | P0 |
| FR-DEP-02 | 支持平台：`linux/amd64`、`linux/arm64`、`windows/amd64` | P0 |
| FR-DEP-03 | 提供 Docker 镜像（含 `docker compose` 示例） | P0 |
| FR-DEP-04 | 提供 systemd unit 示例；Windows 服务方式运行 | P0 / P1 |
| FR-DEP-05 | 启动参数：监听地址、数据目录、日志级别，可通过命令行参数或环境变量设置；其余配置在 WebUI 中维护并存入数据库 | P0 |
| FR-DEP-06 | 可选内置 HTTPS（指定证书文件），也可放在反向代理之后 | P1 |

> 部署提示：ups-warden 所在主机本身也应由 UPS 供电，并作为关机计划的最后一步关闭（见 FR-PLAN-07）。

---

## 4. 设备接入与协议

### 4.1 通用要求

| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-DEV-01 | WebUI 中可以新增、编辑、删除、启用和停用设备；设备属性包括名称、备注、位置、标签 | P0 |
| FR-DEV-02 | 每台设备至少绑定 1 个数据源；数据源属性包括协议、主机、端口、凭据、Profile、轮询间隔、超时、重试次数 | P0 |
| FR-DEV-03 | 一台设备可以绑定多个数据源（例如 SNMP 和 `ayi9-json` 同时接入），按字段优先级合并（见 4.6） | P1 |
| FR-DEV-04 | **连接测试**：保存前可以对单个数据源发起一次只读采集，展示连通性、延迟、设备身份、原始数据和归一化结果 | P0 |
| FR-DEV-05 | 连接测试成功后，根据读到的型号自动推荐 Profile（如 SNMP `upsIdentModel` 或 RTD 中的机型）；无法识别时（如 Modbus 没有身份信息）由用户手动选择 | P0 |
| FR-DEV-06 | 每个数据源独立调度，一个数据源故障不影响其他设备 | P0 |
| FR-DEV-07 | 轮询间隔可配置，范围 1–60 s，默认 3 s；超时默认 2 s；失败后按指数退避重连，最长退避不超过 30 s | P0 |
| FR-DEV-08 | 同一设备 IP 上的并发连接数默认限制为 1（实测设备对高并发有限流），可调 | P0 |
| FR-DEV-09 | 驱动按 Profile 声明各字段的能力；不支持的字段在 UI 中显示「不支持」，不能显示为 0 | P0 |
| FR-DEV-10 | 驱动 **只发送白名单内的读请求**（详见各协议小节和 SEC-01） | P0 |

驱动接口基线（只读）：

```go
type Driver interface {
    Probe(ctx context.Context) (DeviceInfo, RawData, error) // 身份与原始数据，用于连接测试
    Poll(ctx context.Context) (RawData, error)              // 单次采集
    Close() error
}
// Profile 负责 RawData → Snapshot 的映射；驱动本身不包含型号逻辑。
```

### 4.2 SNMP（v1 / v2c，UDP 161）

| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-SNMP-01 | 支持 SNMP v1 和 v2c，可配置 community（默认 `public`）、端口（默认 161） | P0 |
| FR-SNMP-02 | 支持三种读取策略，按数据源配置：`GET`、`GETNEXT`（walk）、`GETBULK`（v2c）。默认 `GETBULK`，失败时回退到 `GET` | P0 |
| FR-SNMP-03 | GETBULK 参数可配置：起始 OID（默认 `1.3.6.1.2.1.33`）、`max-repetitions`（默认 60）；一次结果不完整时自动续取，直到超出起始子树或遇到 `endOfMibView` | P0 |
| FR-SNMP-04 | request-id **必须使用 4 字节 BER 编码**（实测：ET1K 会静默丢弃 1 字节编码的请求） | P0 |
| FR-SNMP-05 | 支持表格型 OID 的行索引（按相 1..3），用于三相机型 | P0 |
| FR-SNMP-06 | 提供内置 Profile「RFC 1628 UPS-MIB 通用」，作为未知型号的默认映射 | P0 |
| FR-SNMP-07 | 识别 `noSuchName` / `noSuchObject` / `endOfMibView`，对应字段标记为「不支持」 | P0 |
| FR-SNMP-08 | 占位字符串（如 `---`）视为空值 | P0 |
| FR-SNMP-09 | 禁止发送 `SET` PDU | P0 |
| FR-SNMP-10 | 接收 SNMP Trap（UDP 162）作为加速事件检测的辅助通道，轮询仍是主数据来源 | P2 |
| FR-SNMP-11 | SNMPv3（authPriv） | 待确认 |

ET1K 的特殊行为（实测，内置 Profile 需要覆盖）：GET 对所有 OID 返回 `noSuchName`；GETNEXT 只返回第一个 OID；只有 GETBULK 可用；`upsEstimatedMinutesRemaining` 恒为 0（应标记为无效）；部分功率值的量纲与 RFC 1628 不一致，需要在 Profile 中设置缩放（见附录 A）。

### 4.3 Modbus TCP（TCP 502）

| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-MB-01 | 支持 Modbus TCP，可配置主机、端口（默认 502）、Unit ID（默认 1） | P0 |
| FR-MB-02 | 读取功能码只允许 **FC03**（Holding）和 **FC04**（Input），按 Profile 字段分别指定 | P0 |
| FR-MB-03 | 禁止发送任何写功能码（FC05/06/15/16/22/23 等） | P0 |
| FR-MB-04 | Profile 字段支持的数据类型：`uint16`、`int16`、`uint32`、`int32`、`float32`；可配置字节序和字序；支持位提取（bit 0–15）；支持 `scale`、`offset` 和枚举映射 | P0 |
| FR-MB-05 | 自动把相邻地址合并为批量读；单次最多读取的寄存器数可配置（默认 100，协议上限 125）；Profile 可以声明可读地址范围（ET1K：0..79），避免越界 | P0 |
| FR-MB-06 | 地址统一使用 0 基协议地址；Profile 编辑器提供"40001 记法 ↔ 0 基"换算提示 | P1 |
| FR-MB-07 | 复用 TCP 连接，断线后自动重连 | P0 |
| FR-MB-08 | Modbus 没有身份信息，也没有时间字段，系统需要自行计时（见 5.4） | P0 |

> ET1K 的寄存器表来自实测反推，只确认了 15 个，厂商 PDF 未取得。内置 Profile 中只收录已确认的寄存器，未确认的寄存器不映射（见附录 A.2）。

### 4.4 AYI9 云监控卡 JSON API（`ayi9-json`）

| 项 | 内容 |
|---|---|
| 协议标识（代码、配置、Profile） | `ayi9-json` |
| 界面显示名 | AYI9 云监控卡 JSON API |
| 厂商 | 深圳市依玖技术有限公司（AYI9，ayi9.com） |
| 官方文档 | 《云监控卡 API 接口说明（Http 和 TCPIP 通讯）》 |
| 厂商原文叫法 | API 文档：「CLOUD 云智能管理卡动环 API 接口」的 TCP/IP 模式；用户手册：动环系统接口中的「TCP + Json」 |
| 常见别名 | CLOUD 云智能管理卡、云监控卡、云卡；网卡 WebUI「集成设置」页中的 `TCP:2390, JSON format` |
| 默认传输 | TCP 2390，长连接，请求-应答 |

> 命名说明：厂商没有为该协议单独命名，"2390"只是默认端口，"JSON"只是报文格式。AYI9 网卡会装在山特、科华、科士达、维缔、EATON 等多个品牌的 UPS 上，并且存在贴牌（转载的文档里不出现 AYI9），用户通常只知道 UPS 品牌。因此按网卡厂商和产品命名，不按 UPS 品牌或端口命名。


| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-AYI9-01 | 支持 TCP 长连接、请求-应答模式，请求和响应都是单行 JSON；端口可配置（默认 2390） | P0 |
| FR-AYI9-02 | **同一连接上串行发送请求**，等收到响应或超时后再发下一条（实测不支持流水线） | P0 |
| FR-AYI9-03 | 指令 **白名单固定写在代码中**：只允许 `QAD`、`RTD`；Profile 和 UI 都无法扩展白名单 | P0 |
| FR-AYI9-04 | 请求携带递增的 `session`，用于匹配响应；可配置 `pass`（默认空，加密存储） | P0 |
| FR-AYI9-05 | 解析 QAD（23 字段）和 RTD（10 字段），字段字典见附录 B；值为 `---` 时视为空值 | P0 |
| FR-AYI9-06 | 解析工作状态字 QAD[22]（16 位，b15…b0）。可靠子集为状态码 b0–b3、b5、b6、b7、b8，其中 b5 和 b7 已经实测；b10–b11 解析结果标注为「低可信」 | P0 |
| FR-AYI9-07 | 识别错误响应：`value=FAILED` 和错误码 101–106、201、410；`103 PASSWORD_ERROR` 需在 UI 中明确提示"密码错误" | P0 |
| FR-AYI9-08 | 单相机型只有 R 相有效；三相字段按 RTD[8]（输入输出相数，如 `3/3`）判断哪些相有效 | P0 |
| FR-AYI9-09 | 记录 `uid`（序列号@域名），作为设备身份 | P0 |
| FR-AYI9-10 | QAD[19]（剩余时间，秒）在 ET1K 上恒为 0；Profile 可声明「0 视为无效」 | P0 |
| FR-AYI9-11 | 添加设备时，为该协议提供识别提示，帮助用户判断自己的网卡是否支持：网卡 WebUI「集成设置」页有 `TCP:2390, JSON format` 选项；SNMP agent 版本为 `ayi9 v1`；连接测试读到的 `uid` 以 `@ayi9.com` 结尾时提示"已识别为 AYI9 云监控卡"。`@ayi9.com` 后缀只在 ET1K 上实测过，官方文档示例的 `uid` 不带域名，不能作为必要条件 | P1 |

> 适用范围：`ayi9-json` 是 AYI9 的厂商私有协议，适用于装有 AYI9 云监控卡（含贴牌产品）或兼容固件的各品牌 UPS。QAD/RTD 的字段位置由协议固定，不同型号之间的差异（有效相数、哪些字段无效、缩放）由 Profile 描述。厂商同一套 JSON API 还提供 HTTP 模式（HTTP POST 收发），首版只支持 TCP 模式。

### 4.5 设备 Profile 机制（多型号适配核心）

| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-PRF-01 | Profile 是声明式文件（YAML 或 JSON），内容包括：适用协议、匹配规则（型号字符串等）、字段映射列表、有效性规则、派生参数 | P0 |
| FR-PRF-02 | 字段映射项：`{统一字段, 来源(OID / 寄存器+功能码 / QAD·RTD 索引 / 状态字位), 类型, scale, offset, 枚举映射, 单位, 相序}` | P0 |
| FR-PRF-03 | 有效性规则：常量无效值（如 `0`、`---`）、取值范围、"0 视为无效"等；不满足时字段质量标为 `invalid` | P0 |
| FR-PRF-04 | 内置 Profile：`rfc1628-generic`（SNMP 通用）、`ayi9-json-generic`（AYI9 云监控卡 JSON API 通用）、`et1k-snmp`、`et1k-modbus`、`et1k-ayi9-json` | P0 |
| FR-PRF-05 | 支持在 WebUI 中导入、导出、复制、编辑自定义 Profile，保存前做 schema 校验；内置 Profile 只读，只能复制后修改 | P1（导入/导出/复制 P0） |
| FR-PRF-06 | **校准视图**：对指定数据源实时并排展示"原始值 → 缩放后值 → 统一字段"，便于对照 UPS 面板校准新型号 | P1 |
| FR-PRF-07 | Profile 带版本号；数据源记录引用的 Profile 版本；Profile 变更后提示受影响的数据源 | P1 |
| FR-PRF-08 | 系统在代码层面保证：无论 Profile 怎么配置，都不能产生写请求（SNMP SET、Modbus 写功能码、`ayi9-json` 非白名单指令） | P0 |

### 4.6 多数据源合并

| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-SRC-01 | 设备有多个数据源时，每个统一字段按"数据源优先级 + 字段质量"取值：优先取最高优先级数据源中质量为 `good` 的值 | P1 |
| FR-SRC-02 | 字段级别可以覆盖来源，例如"已放电时间"固定取 SNMP | P1 |
| FR-SRC-03 | 关键状态（市电 / 电池）支持 **多源一致性确认**：可配置为"任一来源判定为电池模式即算"或"多数一致才算"，默认"任一来源" | P1 |
| FR-SRC-04 | 只要有一个数据源可用，设备就是「在线」；全部失联时设备才是「通讯中断」 | P1 |

### 4.7 字段可得性（以 ET1K 为例）

| 统一字段 | SNMP | Modbus | AYI9 云监控卡 JSON API |
|---|---|---|---|
| 市电 / 电池模式 | ✅ 输入组 / `upsOutputSource` / `upsSecondsOnBattery` | ✅ 输入电压推断 | ✅ 状态字 b7 + 状态码 |
| 输入电压 / 频率 | ✅ | ✅ | ✅ |
| 输出电压 / 频率 / 负载% | ✅ | ✅ | ✅ |
| 电池容量% / 电压 | ✅ | ✅ | ✅ |
| 机内温度 | ✅ | ✅ | ✅ |
| 已放电时间（原生） | ✅ `33.1.2.2.0` | ❌ | ❌ |
| 剩余时间（原生） | ⚠️ 恒 0 | ❌ | ⚠️ 恒 0 |
| 电池低标志 | ✅ `upsBatteryStatus` | ❌ | ✅ b6 |
| 过载 / 关机中标志 | ❌ | ❌ | ✅ b8 / b5 |
| 额定信息 | ✅ `33.1.9.x` | ⚠️ 部分 | ✅ RTD |
| 设备身份 | ✅ | ❌ | ✅ `uid` + RTD |

原生不提供的字段由状态引擎派生（见 5.3、5.4）。

---

## 5. 统一数据模型与状态判定

### 5.0 归一化原则（统一数据模型）

不同协议、不同型号对同一物理量的表达差异很大，例如电压在 SNMP 中是整数伏，在 Modbus 中是 ×10 的 `uint16`，在 `ayi9-json` 中是带小数的字符串；状态在 SNMP 中是枚举，在 `ayi9-json` 中是 16 位状态字。系统必须先把它们 **归一化为统一数据模型**，存储和所有业务模块都只基于统一模型工作。

```
协议原始数据（RawData）──▶ Profile 映射 ──▶ 归一化转换 ──▶ 统一模型（Snapshot）──▶ 存储 / 事件 / 规则 / 通知 / 动作 / API / WebUI
  OID·寄存器·QAD 字段        字段定位        类型·单位·枚举·质量
```

| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-NORM-01 | 采集链路固定分为三层：驱动只产出协议原始数据；Profile 描述"原始字段 → 统一字段"的映射；归一化层负责转换并输出统一模型。三层职责不得混合 | P0 |
| FR-NORM-02 | **类型统一**：数值统一为浮点数，计数和时长统一为整数秒，开关量统一为布尔，状态统一为本节定义的枚举字符串，文本去除首尾空白和占位符 | P0 |
| FR-NORM-03 | **单位统一**：电压 V、电流 A、频率 Hz、视在功率 VA、有功功率 W、百分比 %（0–100）、温度 ℃、时间 s。缩放（如 ×0.1）、偏移和单位换算（如分钟 → 秒、`1K` → 1000）全部在归一化层完成 | P0 |
| FR-NORM-04 | **枚举统一**：SNMP 枚举、`ayi9-json` 状态码 / 状态位、Modbus 位字段统一映射为 5.1 中的枚举取值；无法映射时取 `unknown`，并保留原始值用于诊断 | P0 |
| FR-NORM-05 | **相序统一**：多相数据统一以 1..3 索引；单相设备只有相 1 有效，其余相标记为 `unsupported` | P0 |
| FR-NORM-06 | **无效值统一**：占位符（`---`）、Profile 声明的无效常量、越界值不转换成 0，而是标记质量（见 5.2） | P0 |
| FR-NORM-07 | **业务隔离**：历史存储、事件、规则、通知模板、动作环境变量、API 和 WebUI 只能使用统一字段；业务代码不得引用 OID、寄存器地址或 QAD / RTD 索引 | P0 |
| FR-NORM-08 | 原始数据只用于诊断（连接测试、校准视图、原始数据查看），每个数据源只在内存中保留最近一次；不写入历史，也不参与业务判断 | P0 |
| FR-NORM-09 | 统一模型有 schema 版本号；新增字段向后兼容，历史数据按字段名读取，不依赖列顺序 | P0 |
| FR-NORM-10 | 归一化层用 ET1K 三协议同一时刻的实测数据做回归测试：同一物理量经三条协议归一化后，结果在缩放精度内一致 | P0 |

### 5.1 统一字段（Snapshot）

| 分组 | 字段 | 单位 |
|---|---|---|
| 身份 | `manufacturer`、`model`、`firmware`、`agent_version`、`serial`、`name` | — |
| 额定 | `nominal_input_voltage`、`nominal_input_freq`、`nominal_output_voltage`、`nominal_output_freq`、`rated_va`、`rated_w`、`nominal_battery_voltage`、`input_phases`、`output_phases` | V / Hz / VA / W |
| 输入 | `input_voltage[p]`、`input_current[p]`、`input_power[p]`、`input_freq` | V / A / W / Hz |
| 输出 | `output_voltage[p]`、`output_current[p]`、`output_power[p]`、`output_load_pct[p]`、`output_freq`、`output_source` | V / A / VA / % / Hz |
| 旁路 | `bypass_voltage[p]`、`bypass_current[p]`、`bypass_power[p]`、`bypass_freq` | V / A / VA / Hz |
| 电池 | `battery_status`（normal / low / depleted / unknown）、`battery_capacity_pct`、`battery_voltage`、`battery_current`、`temperature`、`native_seconds_on_battery`、`native_runtime_remaining` | % / V / A / ℃ / s |
| 标志 | `mains_ok`、`low_battery`、`overload`、`shutting_down`、`buzzer_muted`、`ups_mode`（power_on / standby / bypass / line / battery / test / fault / converter / eco / shutdown / unknown）、`ups_type`（online / backup）、`self_test_result` | — |
| 派生 | `power_state`、`on_battery_elapsed`、`runtime_estimate` + `runtime_source`（native / estimated / none）、`comm_state` | s |

`[p]` 表示按相（1..3）。

### 5.2 字段质量

每个字段都带 `quality`：`good` / `invalid`（读到了但不可信）/ `unsupported`（Profile 未映射或设备不支持）/ `stale`（来自上一次成功采集）。规则引擎 **只用 `good` 的值做判断**；数值条件遇到非 `good` 的值时视为"条件不成立"，并在规则执行日志里记下原因。

### 5.3 供电状态判定（`power_state`）

| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-STA-01 | `power_state` 取值：`online`（市电）、`on_battery`、`bypass`、`fault`、`shutdown`、`unknown` | P0 |
| FR-STA-02 | 判定依据按以下顺序取第一个可用的：① 显式状态（`ayi9-json` 状态码 / b7；SNMP `upsOutputSource`=5 或 `upsSecondsOnBattery`>0）② 输入电压低于阈值（默认 50 V，可按设备配置） | P0 |
| FR-STA-03 | **去抖**：进入 `on_battery` 需要持续满「停电确认时长」（默认 10 s）；恢复 `online` 需要持续满「市电恢复确认时长」（默认 60 s）。两者都可以按设备配置 | P0 |
| FR-STA-04 | 原始判定和去抖后的判定都要保存，UI 上能看到"疑似停电，确认中" | P0 |

### 5.4 放电计时与剩余时间

| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-RT-01 | 系统 **自己记录** 进入 `on_battery`（去抖确认前的原始时刻）的时间戳，得到 `on_battery_elapsed`；时间戳持久化，服务重启后能延续 | P0 |
| FR-RT-02 | 剩余时间分两级：优先使用质量为 `good` 的原生值；否则用滑动窗口内的放电速率估算：`剩余 ≈ 当前容量% ÷ 放电速率(%/min)` | P0 |
| FR-RT-03 | 估算值标注 `runtime_source=estimated`，并在 UI 上显著标识；样本不足（如放电开始不满 2 分钟）时显示"估算中" | P0 |
| FR-RT-04 | 关机类规则 **不推荐** 使用估算剩余时间：规则编辑器使用该字段时给出警告，推荐使用"容量阈值 + 放电持续时长"条件 | P0 |

> 参考值（实测）：ET1K 在约 25% 负载下放电速率约 1.0–1.1 %/min。

### 5.5 通讯状态

| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-COM-01 | 连续 N 次采集失败（默认 3 次）判定为 `comm_lost`；随后一次成功即恢复 | P0 |
| FR-COM-02 | 通讯中断期间，字段质量变为 `stale`，UI 显示最后一次更新的时间 | P0 |
| FR-COM-03 | **失联时的策略**（按设备配置）：`hold`（沿用最后已知的供电状态，放电计时继续；容量类条件不再更新）——**默认**；`assume_on_battery_after`（失联超过 X 分钟后按电池模式处理）；`ignore`（只告警，不参与关机判定） | P0 |

---

## 6. 事件与告警

### 6.1 事件类型

| 事件 | 触发 | 默认级别 | 优先级 |
|---|---|---|---|
| `mains_lost` / `mains_restored` | `power_state` 在去抖后进入 / 离开 `on_battery` | 严重 / 信息 | P0 |
| `battery_low` / `battery_low_cleared` | 低电量标志，或容量低于设备阈值（默认 30%，回差 5%） | 严重 | P0 |
| `battery_depleted` | `battery_status=depleted` | 严重 | P0 |
| `comm_lost` / `comm_restored` | 见 5.5 | 警告 | P0 |
| `overload` / `overload_cleared` | 过载标志，或负载% 超过阈值（默认 90%） | 警告 | P0 |
| `on_bypass` / `bypass_cleared` | `ups_mode=bypass` | 警告 | P0 |
| `ups_fault` | `ups_mode=fault` | 严重 | P0 |
| `ups_shutting_down` | 关机标志（`ayi9-json` 状态字 b5） | 严重 | P0 |
| `temperature_high` | 温度超过阈值（默认 50 ℃，按设备配置） | 警告 | P1 |
| `self_test_result` | 自检结果变化 | 信息 | P2 |

### 6.2 要求

| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-EVT-01 | 事件持久化，包含：设备、类型、级别、发生 / 恢复时间、触发时的快照摘要 | P0 |
| FR-EVT-02 | 成对事件形成"活动告警"，恢复时自动关闭；UI 显示当前活动告警列表 | P0 |
| FR-EVT-03 | 阈值类事件支持回差（hysteresis），避免在阈值附近反复触发 | P0 |
| FR-EVT-04 | 活动告警可以人工确认（ack），确认后不影响规则执行 | P1 |
| FR-EVT-05 | 服务启动时不会把"已有状态"误报为新事件：先从持久化的状态恢复，再进行对比 | P0 |

---

## 7. 规则引擎

| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-RULE-01 | 规则 = 作用范围（某台设备 / 某些标签 / 全部）+ 触发方式 + 动作列表 + 启停开关 | P0 |
| FR-RULE-02 | 触发方式一：**事件触发**（如 `mains_lost`） | P0 |
| FR-RULE-03 | 触发方式二：**条件触发**。采用两层 OR / AND 分组（原草案将 OR 列为 P1，后端方案 D2 已确认提前到 P0，以满足 FR-RULE-06）。条件可用字段：`power_state`、`on_battery_elapsed`、`battery_capacity_pct`、`low_battery`、`output_load_pct`、`temperature`、`comm_state` 等；运算符为 `= ≠ > ≥ < ≤`；支持"条件需持续满 X 秒" | P0 |
| FR-RULE-04 | 每条规则可设置冷却时间，冷却期内不重复触发；条件型规则在条件解除前只触发一次（边沿触发） | P0 |
| FR-RULE-05 | 可选配"恢复动作"：条件解除或配对事件恢复时执行（如发送市电恢复通知） | P0 |
| FR-RULE-06 | 内置规则模板：停电通知、市电恢复通知、低电量通知、通讯中断通知、"电池模式 ≥ N 分钟 或 容量 ≤ M% 或 低电量标志 → 执行关机计划" | P0 |
| FR-RULE-07 | 规则支持 **dry-run** 模式：只记录"将会执行什么"，不实际执行动作 | P0 |
| FR-RULE-08 | **规则模拟器**：选择一段历史数据或模拟场景（如回放停电录制数据），预览规则的触发时间线 | P1 |
| FR-RULE-09 | 每次规则评估命中和动作执行都写入执行日志，包括触发原因、当时的字段值和结果 | P0 |

---

## 8. 消息通知

| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-NTF-01 | 通知渠道（**已确认**）只支持两种：**通用 Webhook** 与 **Telegram Bot**；渠道实现通过统一接口接入，后续新增渠道不影响规则与模板 | P0 |
| FR-NTF-02 | 通用 Webhook：可配置 URL、方法（POST / PUT）、自定义 Header、Body 模板（JSON），可选 HMAC-SHA256 签名头；以 2xx 视为成功 | P0 |
| FR-NTF-09 | Telegram Bot：可配置 Bot Token、Chat ID（可多个）、可选 API 地址（便于自建反向代理）与 HTTP 代理；消息使用纯文本或 HTML 格式 | P0 |
| FR-NTF-03 | 每个渠道都可以"发送测试消息" | P0 |
| FR-NTF-04 | 消息模板：每种事件有默认模板，可自定义；支持变量 `{{device.name}}`、`{{event.type}}`、`{{battery_capacity_pct}}`、`{{on_battery_elapsed}}`、`{{runtime_estimate}}`、`{{time}}` 等；模板渲染失败时退回默认模板并记录日志 | P0 |
| FR-NTF-05 | 发送失败时按指数退避重试，默认 3 次；最终失败记录到通知日志，并在 UI 上提示 | P0 |
| FR-NTF-06 | 短时间内同一设备的多条通知可以合并（窗口可配置，默认关闭）；严重级别的通知不参与合并 | P1 |
| FR-NTF-07 | 免打扰时段（只对信息级生效） | P2 |
| FR-NTF-08 | 通知日志：渠道、内容摘要、结果、耗时、错误信息（不包含凭据） | P0 |

---

## 9. 动作执行：脚本与局域网设备关机

### 9.1 受控主机

| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-HOST-01 | 受控主机属性：名称、地址、类型标签（Linux / Windows / NAS / ESXi / 其他）、连接方式、凭据、在线检测方式 | P0 |
| FR-HOST-02 | SSH 凭据支持密码和私钥（可带口令）。首次连接展示主机指纹，由用户确认后保存，之后严格校验（防中间人） | P0 |
| FR-HOST-03 | 在线检测：ICMP ping 或 TCP 端口探测（如 22 / 445 / 3389），用于关机后确认已离线 | P0 |
| FR-HOST-04 | 提供"测试连接"和"测试命令（只执行无害命令，如 `echo`）" | P0 |
| FR-HOST-05 | 内置常见关机命令模板：Linux `systemctl poweroff` / `shutdown -h now`、Windows（OpenSSH）`shutdown /s /t 0`、群晖 DSM、ESXi、macOS；可自定义命令。是否需要 sudo 由用户配置 | P1（自定义命令 P0） |

### 9.2 动作类型

| 动作 | 说明 | 优先级 |
|---|---|---|
| 发送通知 | 发往一个或多个渠道 | P0 |
| SSH 远程命令 | 在受控主机上执行命令，用于关机或其他操作 | P0 |
| 本地脚本 | 在 ups-warden 所在主机上执行 **脚本目录内** 的脚本（见 FR-ACT-02），可用于关闭本机 | P0 |
| HTTP 请求 | 可配置方法、URL、Header、Body 模板和期望状态码（如联动 Home Assistant、智能插座） | P0 |
| 执行关机计划 | 见 9.3 | P0 |
| Wake-on-LAN | 市电稳定恢复后唤醒主机 | P1 |
| WinRM | Windows 远程关机（不依赖 OpenSSH） | P1 |

| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-ACT-01 | 每个动作都有超时（默认 30 s）；超时或失败时记录退出码、stdout/stderr（截断至 4 KB）、耗时 | P0 |
| FR-ACT-02 | 本地脚本必须放在配置的脚本目录内，WebUI 中只能选择脚本并填写参数；在 WebUI 中在线编辑脚本内容需要服务端配置开关，默认关闭 | P0 / P1 |
| FR-ACT-03 | 脚本执行时注入环境变量：`UPS_DEVICE_ID`、`UPS_DEVICE_NAME`、`UPS_EVENT`、`UPS_POWER_STATE`、`UPS_BATTERY_PCT`、`UPS_ON_BATTERY_SECONDS`、`UPS_RUNTIME_ESTIMATE`、`UPS_DRY_RUN` | P0 |
| FR-ACT-04 | 所有动作都支持手动触发（需要二次确认），并支持 dry-run | P0 |
| FR-ACT-05 | 同一动作不会并发执行；前一次还在运行时，新的触发会被跳过并记录 | P0 |

### 9.3 关机计划（编排）

| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-PLAN-01 | 关机计划由若干 **有序阶段** 组成，同一阶段内的主机并行执行，阶段之间串行 | P0 |
| FR-PLAN-02 | 每个阶段可配置：开始前延迟、单步超时、"等待主机离线"的超时时间 | P0 |
| FR-PLAN-03 | 某一步失败 **不阻塞** 后续步骤，但要记录失败并发送通知 | P0 |
| FR-PLAN-04 | 计划启动时可设置倒计时（默认 60 s），倒计时期间可以在 UI 中取消；倒计时期间市电恢复（去抖后）时自动取消 | P0 |
| FR-PLAN-05 | 计划执行中市电恢复：尚未开始的阶段取消（可配置为"继续执行"），已发出的关机命令不可撤回，如实记录 | P0 |
| FR-PLAN-06 | 计划执行进度在 UI 上实时显示（每台主机：等待 / 执行中 / 已发送 / 已离线 / 失败 / 已取消） | P0 |
| FR-PLAN-07 | 支持把"关闭 ups-warden 所在主机"作为最后一个阶段 | P0 |
| FR-PLAN-08 | 市电稳定恢复超过 X 分钟后，可选对计划内主机执行 WOL | P1 |

### 9.4 幂等与重启恢复

| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-IDEM-01 | 关机计划的执行状态（触发原因、当前阶段、每一步的结果）**持久化** | P0 |
| FR-IDEM-02 | 同一次停电周期内，同一关机计划只执行一次；服务重启后不会重复触发已执行的步骤 | P0 |
| FR-IDEM-03 | 停电周期的界定：从去抖后的 `mains_lost` 开始，到去抖后的 `mains_restored` 结束；周期结束后计划恢复为可再次触发 | P0 |
| FR-IDEM-04 | 服务重启时如果发现计划执行到一半且仍处于停电周期，按配置选择继续剩余步骤（默认）或终止，并发送通知 | P0 |

---

## 10. WebUI 功能

| 页面 | 内容 | 优先级 |
|---|---|---|
| 首次启动向导 | 创建管理员账号，可选添加第一台设备 | P0 |
| 登录 | 账号 + 密码 | P0 |
| 总览面板 | 每台设备一张卡片：供电状态（颜色区分）、电池容量、负载%、输入 / 输出电压、放电计时、剩余时间（标注估算）、通讯状态；顶部显示活动告警汇总 | P0 |
| 设备详情 | 实时数据（分组展示，按相展开）、额定信息、身份信息、数据源状态（延迟、最后成功时间、错误）、原始数据查看、历史曲线（容量 / 负载 / 电压 / 温度，可选时间范围）、事件列表 | P0 |
| 设备管理 | 新增 / 编辑向导（协议 → 连接参数 → 连接测试 → 选择 Profile → 阈值与去抖 → 保存）、启停、删除（二次确认） | P0 |
| Profile 管理 | 列表、查看、导入 / 导出 / 复制；编辑器与校准视图 | P0 / P1 |
| 告警与事件 | 活动告警、历史事件筛选（设备 / 类型 / 级别 / 时间） | P0 |
| 规则 | 列表、编辑（作用范围 / 触发 / 条件 / 动作 / 冷却 / 恢复动作 / dry-run）、执行日志 | P0 |
| 通知渠道 | 渠道配置、测试、模板编辑、通知日志 | P0 |
| 受控主机与动作 | 主机管理、连接测试、动作配置、手动触发 | P0 |
| 关机计划 | 阶段编排（拖拽排序 P1）、执行实时进度、历史执行记录 | P0 |
| 审计日志 | 配置变更、动作执行、登录记录 | P0 |
| 系统设置 | 数据保留期、默认阈值、时区、语言、账号管理、备份 / 恢复 | P0（备份 / 恢复 P1） |

| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-UI-01 | 实时数据通过 SSE 推送；断线后自动重连，并提示"实时连接已断开" | P0 |
| FR-UI-02 | 界面语言：简体中文 P0，English P1 | P0 / P1 |
| FR-UI-03 | 响应式布局，移动端可以查看总览和告警 | P1 |
| FR-UI-04 | 浅色 / 深色主题 | P2 |
| FR-UI-05 | 数值显示单位；无效或不支持的字段显示「—」并附提示原因，不显示 0 | P0 |
| FR-UI-06 | 危险操作（删除、手动关机、执行关机计划、关闭 dry-run）需要二次确认；手动执行关机计划需要输入计划名称确认 | P0 |

---

## 11. 数据存储与历史

| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-DATA-01 | 配置、状态、事件、日志统一存放在 SQLite（WAL 模式），位于数据目录 | P0 |
| FR-DATA-02 | 历史数据分层保留（**默认假设**）：归一化后的统一字段明细保留 7 天；1 分钟聚合（min / max / avg）保留 90 天；均可配置。不存储协议原始数据（FR-NORM-08、后端方案 D6） | P0 |
| FR-DATA-03 | 事件、通知日志、动作日志、审计日志默认保留 365 天 | P0 |
| FR-DATA-04 | 后台定期清理和聚合，不影响采集实时性 | P0 |
| FR-DATA-05 | 配置导出 / 导入（JSON），导出时可选择是否包含加密凭据 | P1 |
| FR-DATA-06 | 时间统一以 UTC 存储，按系统设置的时区显示 | P0 |

---

## 12. API

| 编号 | 需求 | 优先级 |
|---|---|---|
| FR-API-01 | REST JSON API，前缀 `/api/v1`；前端只使用公开 API | P0 |
| FR-API-02 | 资源：`devices`、`sources`、`profiles`、`snapshots`（最新）、`history`、`events`、`alerts`、`rules`、`channels`、`hosts`、`actions`、`plans`、`plan-runs`、`audit`、`users`、`settings` | P0 |
| FR-API-03 | 实时流 `GET /api/v1/stream`（SSE）：推送快照更新、事件、计划进度 | P0 |
| FR-API-04 | 提供 OpenAPI 描述，前端类型由它生成 | P1 |
| FR-API-05 | API Token（只读 / 读写），用于外部集成 | P1 |
| FR-API-06 | Prometheus `/metrics` 导出（设备指标 + 服务自身指标） | P1 |
| FR-API-07 | MQTT 发布 / Home Assistant 自动发现 | P2 |
| FR-API-08 | 统一错误结构：`{code, message, details}`；任何响应都不回显凭据明文 | P0 |

---

## 13. 安全需求

### 13.1 设备侧安全（对应探测报告 R1–R6）

| 编号 | 需求 | 对应 |
|---|---|---|
| SEC-01 | 驱动层只读：SNMP 只用 GET / GETNEXT / GETBULK；Modbus 只用 FC03 / FC04；`ayi9-json` 只用 `QAD` / `RTD`。白名单写在代码中，Profile 和配置都无法绕过；需要有单元测试证明没有写请求发出 | R1 |
| SEC-02 | 首版不实现 UPS 本体控制。以后如果要实现，必须满足：默认关闭、需要服务端配置开关、dry-run、二次确认、幂等、审计（另立需求） | R2 / R3 |
| SEC-03 | 系统不访问设备的 HTTP 端口和 WebUI 路径 | R4 |
| SEC-04 | 不对设备做端口扫描、OID 爆破或 `cmd` 字典探测；连接测试只发送白名单内的读请求 | R6 |
| SEC-05 | 关机编排满足：去抖、每步超时、先关负载、单步失败不阻塞但记录审计 | R5 |

### 13.2 系统自身安全

| 编号 | 需求 | 优先级 |
|---|---|---|
| SEC-10 | 所有页面和 API 都需要登录（健康检查 `/healthz` 除外） | P0 |
| SEC-11 | 密码使用 argon2id 或 bcrypt 哈希；登录失败限速并临时锁定 | P0 |
| SEC-12 | 会话 Cookie 设置 `HttpOnly`、`SameSite=Strict`，HTTPS 下加 `Secure`；写操作需要 CSRF 校验 | P0 |
| SEC-13 | 敏感信息（community、`ayi9-json` pass、SSH 密码 / 私钥、Telegram Bot Token、Webhook 鉴权头 / 签名密钥）使用 AES-256-GCM 加密存储；主密钥来自环境变量或数据目录中的密钥文件（权限 0600，首次启动时自动生成） | P0 |
| SEC-14 | 敏感信息在 API 和 UI 中只显示掩码；编辑时留空表示"不修改" | P0 |
| SEC-15 | 审计日志记录：登录 / 登出、配置增删改（字段级差异，敏感字段脱敏）、手动触发、动作和计划的执行结果 | P0 |
| SEC-16 | 日志中不出现凭据明文 | P0 |
| SEC-17 | 设备安全提示：当设备使用默认 community `public`、`ayi9-json` 密码为空时，在设备详情中显示安全提示（不阻断） | P2 |

---

## 14. 非功能需求

| 编号 | 需求 | 指标 |
|---|---|---|
| NFR-01 | 容量 | 单实例在 3 s 轮询下支持 ≥ 32 台设备（目标 100 台） |
| NFR-02 | 资源 | 32 台设备时常驻内存 ≤ 128 MB，空闲 CPU 占用 < 2%（参考：4 核 ARM64） |
| NFR-03 | 时效 | 去抖确认后 ≤ 2 s 发出通知或启动动作；快照从采集完成到推送至 UI ≤ 1 s |
| NFR-04 | 可靠性 | 采集、规则、动作之间相互隔离，单点 panic 不影响整个进程（recover + 记录日志）；进程退出时优雅关闭（等待进行中的动作，最长 10 s） |
| NFR-05 | 恢复 | 服务重启后 ≤ 10 s 恢复采集；状态、停电周期、计划进度能从持久化数据中恢复 |
| NFR-06 | 启动 | 冷启动 ≤ 3 s（不含首次采集） |
| NFR-07 | 日志 | 结构化日志（`log/slog`），级别可调，支持输出到文件并滚动 |
| NFR-08 | 兼容 | 浏览器支持 Chrome / Edge / Firefox / Safari 最新两个大版本 |
| NFR-09 | 可维护 | 驱动、Profile、状态引擎、规则、动作模块边界清晰；新增型号只需要新增 Profile，新增协议只需要新增 Driver |
| NFR-10 | 编码 | 源码和文档使用 UTF-8（无 BOM）、LF；注释和文档使用简体中文 |

---

## 15. 测试与验收

### 15.1 测试设施

| 编号 | 需求 | 优先级 |
|---|---|---|
| TST-01 | **协议模拟器**（Go 实现，可同时用于开发和 CI）：SNMP agent 复现 ET1K 特性（GET 返回 noSuchName、只支持 GETBULK、丢弃 1 字节 request-id）；Modbus TCP 从站（0..79，只支持 FC03）；`ayi9-json` 服务端（QAD / RTD，不支持流水线，支持返回错误码） | P0 |
| TST-02 | 模拟器支持场景脚本：市电正常 → 停电 → 放电 → 低电 → 恢复；网卡失联；短暂闪断 | P0 |
| TST-03 | 用 ET1K 实测数据（同一时刻三协议快照、停电期间连续采样）作为解析和 Profile 的回归 fixture | P0 |
| TST-04 | 安全测试：断言三种驱动在任意 Profile 下都只发出白名单内的请求 | P0 |
| TST-05 | 前端单元测试与关键流程 E2E（添加设备、配置规则、执行计划 dry-run） | P1 |

### 15.2 关键验收场景

| 编号 | 场景 | 通过标准 |
|---|---|---|
| AC-01 | 分别用 SNMP、Modbus、`ayi9-json` 接入 ET1K | 三者的核心字段（输入 / 输出电压、频率、负载%、容量%、电池电压、温度）与设备面板一致，误差在缩放精度以内 |
| AC-02 | 使用 RFC 1628 通用 Profile 接入标准 SNMP UPS（模拟器） | 标准字段正确显示，未实现的字段显示「不支持」 |
| AC-03 | 用自定义 Profile 接入一个新寄存器表的 Modbus 设备（模拟器） | 不改代码完成接入 |
| AC-04 | 5 s 闪断 | 不产生 `mains_lost`，不触发任何规则 |
| AC-05 | 持续停电 | 确认时长到达后 2 s 内收到通知；达到条件后关机计划按阶段执行，进度和日志完整 |
| AC-06 | 倒计时期间市电恢复 | 计划自动取消并发送通知 |
| AC-07 | 计划执行中重启服务 | 不重复执行已完成的步骤，按配置继续或终止 |
| AC-08 | 停电期间网卡失联（`hold` 策略） | 产生 `comm_lost`；放电计时继续；时长条件到达后照常触发关机 |
| AC-09 | dry-run 规则 | 只有日志，没有任何外部副作用 |
| AC-10 | `ayi9-json` 密码错误 | 连接测试显示"密码错误（103）" |
| AC-11 | 抓包审计 | 三种协议都没有任何写请求 |

---

## 16. 范围外与后续路线

| 项 | 计划 |
|---|---|
| UPS 本体控制（`ayi9-json` 的 `SOFF` 等） | 待安全评估后另立需求（见 SEC-02） |
| SNMPv3 | 待确认 |
| SNMP Trap 接收 | P2 |
| Modbus RTU / RTU over TCP | 视实际机型需求而定 |
| NUT 服务端 / 客户端兼容 | 远期可选，便于已有 NUT 客户端接入 |
| MQTT / Home Assistant | P2 |
| 多实例 / 高可用 | 不规划 |

---

## 17. 待确认事项

| # | 问题 | 默认假设 |
|---|---|---|
| Q1 | 首版通知渠道的范围 | **已确认**：只做通用 Webhook 与 Telegram Bot |
| Q2 | 用户体系：只有单管理员，还是多用户多角色 | P0 支持多个管理员账号，角色 P1 |
| Q3 | 存储选型 | SQLite（纯 Go 驱动） |
| Q4 | 远程关机方式：首版是否必须支持 WinRM（无 OpenSSH 的 Windows） | 首版只做 SSH，WinRM 放 P1 |
| Q5 | 是否需要 SNMPv3（其他品牌网卡常见） | 首版不做 |
| Q6 | 网卡失联时的默认策略 | `hold` |
| Q7 | 首版是否要求一台设备绑定多个数据源 | P1；首版每台设备一个数据源 |
| Q8 | 默认阈值：停电确认 10 s / 恢复确认 60 s / 低电 30% / 过载 90% / 高温 50 ℃ | 如左，可配置 |
| Q9 | 部署目标是否包含 macOS、群晖套件或 Windows 服务 | Linux + Windows 二进制 + Docker；Windows 服务 P1 |
| Q10 | 历史数据保留期 | 统一字段明细 7 天 / 1 分钟聚合 90 天；明细不指协议原始报文，见后端方案 D6 |

### 17.1 需复核的协议事实

| # | 事项 | 说明 |
|---|---|---|
| V1 | SNMP 旁路表列偏移 | 报告称"实测第 2 列为电压而非频率，与 RFC 1628 有偏差"。按 RFC 1628，`upsBypassFrequency` 是标量 `33.1.5.1`，表内第 2 列就是 `upsBypassVoltage`，**可能其实没有偏差**（推断，建议对照 MIB 原文复核）。无论结论如何，Profile 都能覆盖 |
| V2 | SNMP / Modbus 功率量纲 | ET1K 在 27% 负载、1 kVA 下，SNMP 输出功率和 Modbus `reg34` 都是 `2700`，与 W / VA 原值不符，疑似 ×10。内置 Profile 中暂按 ×0.1 处理并标为待校准 |
| V3 | SNMP 输出源在电池模式下的取值 | 停电期间 `upsOutputSource` 是否变为 5 未记录；ET1K Profile 暂时以输入电压和 `upsSecondsOnBattery` 判定 |
| V4 | `ayi9-json` 状态字 b10–b11 | 厂商文档存在歧义，暂不参与判定 |

---

## 附录 A：ET1K 内置 Profile 映射（实测）

### A.1 SNMP（`et1k-snmp`，读取策略 GETBULK，起始 `1.3.6.1.2.1.33`）

| 统一字段 | OID（前缀 `1.3.6.1.2.1.`） | 缩放 | 备注 |
|---|---|---|---|
| `model` | `33.1.1.2.0` | — | `ET1K` |
| `firmware` | `33.1.1.3.0` | — | |
| `agent_version` | `33.1.1.4.0` | — | |
| `manufacturer` | `33.1.1.1.0` | — | `---` 视为空 |
| `battery_status` | `33.1.2.1.0` | 枚举 1/2/3/4 | unknown / normal / low / depleted |
| `native_seconds_on_battery` | `33.1.2.2.0` | ×1 s | 唯一的原生放电计时 |
| `native_runtime_remaining` | `33.1.2.3.0` | ×60 s | **0 视为无效** |
| `battery_capacity_pct` | `33.1.2.4.0` | ×1 | |
| `battery_voltage` | `33.1.2.5.0` | ×0.1 | |
| `temperature` | `33.1.2.7.0` | ×1 | |
| `input_freq` | `33.1.3.3.1.2.1` | ×0.1 | |
| `input_voltage[1]` | `33.1.3.3.1.3.1` | ×1 | 整数伏 |
| `output_source` | `33.1.4.1.0` | 枚举 | RFC 1628 |
| `output_freq` | `33.1.4.2.0` | ×0.1 | |
| `output_voltage[1]` | `33.1.4.4.1.2.1` | ×1 | |
| `output_power[1]` | `33.1.4.4.1.4.1` | ×0.1（待校准，V2） | |
| `output_load_pct[1]` | `33.1.4.4.1.5.1` | ×1 | |
| `bypass_freq` | `33.1.5.1.0` | ×0.1 | |
| `bypass_voltage[1]` | `33.1.5.3.1.2.1` | ×1 | |
| `nominal_input_voltage` | `33.1.9.1.0` | ×1 | |
| `nominal_output_voltage` | `33.1.9.3.0` | ×1 | |
| `nominal_output_freq` | `33.1.9.4.0` | ×0.1 | |
| `rated_va` | `33.1.9.5.0` | ×1 | |
| `rated_w` | `33.1.9.6.0` | ×1 | |

### A.2 Modbus TCP（`et1k-modbus`，FC03，可读范围 0..79，类型 `uint16`）

| 统一字段 | 寄存器 | 缩放 |
|---|---|---|
| `output_voltage[1]` | 28（2026-10-14 的 60 组样本均与 QAD 一致；reg02 最高相差 0.2 V，见[回归资料](../../backend/testdata/et1k/README.md)） | ×0.1 |
| `battery_voltage` | 8 | ×0.1 |
| `battery_capacity_pct` | 10 | ×0.1 |
| `temperature` | 13 | ×0.1 |
| `input_freq` | 16 | ×0.1 |
| `input_voltage[1]` | 17 | ×0.1 |
| `input_power[1]` | 23 | ×1（待校准） |
| `output_freq` | 27 | ×0.1 |
| `output_power[1]` | 34 | ×0.1（待校准，V2） |
| `output_load_pct[1]` | 45 | ×0.1 |
| `bypass_freq` | 49 | ×0.1 |
| `bypass_voltage[1]` | 50 | ×0.1 |

未确认的寄存器（reg01、reg05、reg64）不映射。供电状态由 `input_voltage` 低于阈值推断。

### A.3 AYI9 云监控卡 JSON API（`et1k-ayi9-json`）

使用附录 B 的通用字段字典；Profile 差异：单相（只有 R 相有效）、QAD[19] 0 视为无效。

---

## 附录 B：AYI9 云监控卡 JSON API 字段字典

### B.1 QAD（23 字段，响应 `value` 以 `(` 开头，空格分隔）

| 索引 | 统一字段 |
|---|---|
| 0–2 | `input_voltage[1..3]` |
| 3 | `input_freq` |
| 4–6 | `input_current[1..3]` |
| 7 | 故障电压（只保留原始值） |
| 8–10 | `output_voltage[1..3]` |
| 11 | `output_freq` |
| 12–14 | `output_current[1..3]` |
| 15–17 | `output_load_pct[1..3]` |
| 18 | `battery_voltage` |
| 19 | `native_runtime_remaining`（秒） |
| 20 | `battery_capacity_pct` |
| 21 | `temperature` |
| 22 | 工作状态字（见 B.3） |

### B.2 RTD（10 字段）

| 索引 | 统一字段 |
|---|---|
| 0 | `manufacturer` |
| 1 | `firmware`（硬件版本） |
| 2 | `model` |
| 3 | `nominal_input_voltage` |
| 4 | `nominal_output_voltage` |
| 5 | 额定电流（只保留原始值） |
| 6 | `nominal_output_freq` |
| 7 | `nominal_battery_voltage` |
| 8 | `input_phases` / `output_phases`（如 `1/1`） |
| 9 | `rated_va`（如 `1K` → 1000；需要解析 `K` 后缀） |

### B.3 工作状态字（QAD[22]，字符串下标 0 对应 b15）

| 位 | 统一字段 | 可信度 |
|---|---|---|
| b0–b3 | `ups_mode`：0 power_on / 1 standby / 2 bypass / 3 line / 4 battery / 5 test / 6 fault / 7 converter / 8 eco(HE) / 9 shutdown | 实测（3、4） |
| b4 | `buzzer_muted` | 文档 |
| b5 | `shutting_down` | 实测 |
| b6 | `low_battery` | 文档 |
| b7 | `mains_ok` 取反 | 实测 |
| b8 | `overload` | 文档 |
| b10–b11 | `self_test_result` | 低可信（V4） |
| b15 | `ups_type`：0 online / 1 backup | 文档 |

---

## 附录 C：探测报告风险项追溯

| 报告编号 | 本文档对应 |
|---|---|
| R1 驱动不暴露写操作 | SEC-01、FR-SNMP-09、FR-MB-03、FR-AYI9-03、FR-PRF-08、TST-04 |
| R2 写操作保护；UPS 本体控制不在当前范围 | SEC-02、FR-RULE-07、FR-ACT-04、SEC-15；主机动作不构成设备写请求许可 |
| R3 二次确认 + 幂等 | FR-UI-06、FR-IDEM-01~04 |
| R4 禁止访问设备 HTTP / WebUI | SEC-03（比旧建议“不探测未声明 URL”更严格） |
| R5 关机编排的去抖、超时与顺序 | FR-STA-03、FR-PLAN-01~07、SEC-05；不采用旧建议“再通知 UPS” |
| R6 禁止 `cmd` 爆破 | SEC-04、FR-AYI9-03 |
