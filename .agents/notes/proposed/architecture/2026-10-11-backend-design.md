# UPS Warden 后端实现方案

> **文档版本**：v0.2
> **日期**：2026-10-11
> **依据**：[《需求文档》v0.3](../../../../docs/planning/requirements.md)、[《协议支持探测报告》](../../../../docs/reference/protocols/et1k-probe-report.md)
> **范围**：后端（`backend/`，Go module `ups-warden`）首版（P0）的实现方案；P1 只说明在本设计中的落点，不展开实现细节
> **配套**：[《后端开发计划》](../../../../docs/planning/backend-plan.md) · [文档导航](../../../../docs/README.md) · [当前开发指南](../../../../docs/guides/development.md)
> **状态（同步至 2026-10-15）**：proposed（整套业务方案未完成；M0～M3 代码与本机验证已完成，M4 认证/审计/监控 API/SSE/静态 SPA 托管已实现，CP-1 真机待验收，CP-2 契约已交付、双方联调待验收）。D1~D10 的 2026-10-11 评审记录保留（第 21.1 节），实际范围见[开发计划实施记录](../../../../docs/planning/backend-plan.md)；M5～M8 未实现，本轮到 M4 停止，整篇不标为 implemented。

---

## 0. 现状与约定

- 当前 M0～M4 代码已落地并通过本机验证；主服务装配基础设施、Profile 校验、采集调度、状态/事件/历史、认证与审计、监控管理 API、SSE 和静态 SPA，启动会连接数据库中启用的数据源。当前行为见[采集、状态与历史](../../../../docs/guides/collection-state-history.md)及[监控 API 与 SSE 契约](../../../../docs/reference/monitoring-api.md)。前端仍为脚手架、无业务页面；通知、规则/动作、关机编排与系统发布待实现。M4 全量检查、回归、fuzz、内嵌构建、Windows 临时库 + loopback 冒烟与 Linux 交叉编译已通过；CP-1 真机、CP-2 双方联调、Linux 实机、race 与容量验收仍未完成，证据见开发计划。
- 文中 `FR-*` / `SEC-*` / `NFR-*` / `AC-*` 编号均指需求文档中的条目。
- 「统一字段」指需求文档 5.1 定义的字段名，例如 `battery_capacity_pct`、`input_voltage[1]`。
- 本记录沿用原有中文方案格式，集中保留技术选择、备选理由、D1~D10 与验收策略，不重复拆成多篇 Note。模块落地时同步当前 docs，并据实际完成范围更新状态；未完成整套方案前，不把整篇改为 implemented。
- M3 实施约束：P0 单源由 supervisor 拒绝冲突；同 canonical IP 串行且固定解析后的拨号 IP，拒绝多地址 DNS，防止主机别名绕过限流。状态先在候选副本计算，再同事务提交周期、事件及关键状态，成功后发布，避免数据库失败使内存与后续动作分歧。重启保留已确认周期和活动告警，但舍弃未确认窗口，不能把服务停机时间当作连续有效观测。历史只接收目录允许的统一字段；明细与聚合沿用初始 schema，未为诊断缓存新增持久化表。
- M4 实施边界与决定（2026-10-15 演进，非原评审历史）：初始化 token 仅主进程 stderr 输出一次、只存内存，避免进入轮转日志或 API；Cookie 固定 7 天绝对期限，服务端执行 24 小时 idle，避免活跃 SSE 无法续发响应头导致 Cookie 提前过期。SSE 每 15 秒心跳重新认证；256 缓冲溢出独立失效信号立即断开、不排空，帧写期限 5 秒，避免慢订阅者继续消费陈旧数据或无限占用连接。
- M4 配置一致性：暂停并排空受影响 collector/actor，Flush 历史，再提交配置与审计同事务，最后按提交状态恢复运行，避免旧采集事实跨配置边界。事务失败保持旧配置并尝试恢复；保存成功恢复失败用 503 / `configuration_saved_runtime_error` 明确区分，不能伪装回滚。Profile 更新递增版本并同步引用，内置键保留、引用时拒绝删除；源凭据省略/空串保留，`clear_secrets` 显式清除，协议变更清旧凭据，响应仅 `*_set`。
- M4 构建只实现静态 SPA 托管：`backend:build` 依赖 `webui:build`，pnpm build 后由 `scripts/build-webui.mjs` 校验并安全复制 embed 产物（当前 4 文件，产物不提交）；业务页面、统一发布与 M5～M8 不因此完成。CP-2 契约交付仅为后端联调基线，未伪造双方联调或真机证据。
- M0～M4 简洁性决定（2026-10-15 演进）：移除启动时参考库重放及全量 DDL 文本比对，迁移只维护版本、历史和事务契约；避免把外部结构变化兼容做成启动框架。Profile 在加载和读计划构造时校验，worker 独占并只读使用，归一化信任该契约；不使用可失效的“已校验”缓存标记。API 必需模块在构造时检查，测试注入替身，不保留空模块降级路径。参数范围沿用 M4 已交付 API，不借精简收窄兼容；错误分类和定位须保留，但不传递含凭据的原始文本。
- 历史聚合不再每分钟重扫整个保留期，改用固定大小的内存水位及最早 dirty 起点，具体行为见[采集、状态与历史](../../../../docs/guides/collection-state-history.md)。未采用持久化水位、额外迁移或逐分钟 dirty 集合：启动一次补算已能覆盖重启缺口；迟到数据重算后缀是保持状态简单的取舍。首次补算和迟到重算的容量表现仍待验收。

---

## 1. 设计目标与硬约束

| # | 约束 | 来源 | 在设计中的落点 |
|---|---|---|---|
| C1 | 驱动层完全只读，白名单写在代码中，Profile 与配置无法绕过 | SEC-01、FR-PRF-08 | 第 5.2 节：编码层 + 发送前字节校验 + 测试三重防护 |
| C2 | 驱动 → Profile → 归一化三层分离；业务模块只使用统一模型 | FR-NORM-01/07 | 第 3.2 节：包依赖规则 + 架构测试强制 |
| C3 | 单二进制、纯 Go（无 cgo），可交叉编译到 linux/amd64、linux/arm64、windows/amd64 | FR-DEP-01/02 | 纯 Go SQLite 驱动；前端 `embed` |
| C4 | 关机相关状态必须可在重启后恢复且不重复执行 | FR-RT-01、FR-IDEM-*、NFR-05 | 第 8、13 节：状态持久化 + 计划步骤 write-ahead |
| C5 | 去抖、失联策略、字段质量必须让"误关机"比"漏关机"更难发生，同时失联时不能卡死关机 | FR-STA-03、FR-COM-03、5.2 | 第 8 节状态引擎 |
| C6 | 任何响应与日志不出现凭据明文 | SEC-13/14/16、FR-API-08 | 第 16 节：`secret` 类型 + 加密存储 + 日志脱敏 |

设计原则：

1. **只做 P0**。数据结构为已规划的 P1（多数据源合并、角色、API Token）预留必要字段，但不提前实现逻辑，也不为 P2 以外的设想增加抽象。
2. **标准库优先**。HTTP 路由使用 Go 1.22+ 的 `net/http.ServeMux` 模式匹配，不引入 Web 框架、ORM、迁移框架。
3. **协议客户端自研**（**决策**，理由见 2.1）。三种协议都只需要极小的只读子集，自研能把"只读"做成结构性保证，并可与模拟器共用编解码。

---

## 2. 关键技术决策

### 2.1 决策表

| 主题 | 选择 | 理由 | 备选 / 放弃原因 |
|---|---|---|---|
| HTTP 路由 | 标准库 `ServeMux`（方法 + 路径模式） | 已在脚手架中使用；资源型 REST 足够 | chi / echo：无必要 |
| 存储 | SQLite，`modernc.org/sqlite`（纯 Go），WAL | 需求 Q3 默认假设；无 cgo，方便交叉编译 | `mattn/go-sqlite3` 需要 cgo |
| SQL 访问 | `database/sql` + 手写 SQL + `embed` 的迁移脚本 | 表数量有限，手写可控；迁移只需顺序执行 | sqlc / ORM / goose：引入工具链或依赖 |
| SNMP | **自研** v1/v2c 最小 BER 编解码（GET / GETNEXT / GETBULK） | ① request-id 必须 4 字节（FR-SNMP-04），需完全控制编码；② 编码器中根本不存在 SET PDU，只读是结构性的；③ 模拟器需要 agent 端编解码，可直接复用；④ 不做 SNMPv3，范围很小 | `gosnmp`：request-id 编码不可控（约 0.4% 概率生成短编码被设备静默丢弃），且库本身支持 SET，只读只能靠约定 |
| Modbus TCP | **自研** FC03 / FC04 客户端 | 报文极简（MBAP + 5 字节 PDU）；写功能码在代码中不存在 | 现有库都暴露写函数 |
| `ayi9-json` | 自研 | 厂商私有协议，无现成库 | — |
| SSH | `golang.org/x/crypto/ssh` | 需求指定 | — |
| 密码哈希 | `golang.org/x/crypto/argon2`（argon2id） | SEC-11；与 SSH 同一依赖 | bcrypt（同在 x/crypto，可作备选） |
| 加密存储 | 标准库 `crypto/aes` + `crypto/cipher`（AES-256-GCM） | SEC-13 | — |
| Profile 格式 | YAML（JSON 作为 YAML 子集同样可导入），`go.yaml.in/yaml/v3` | FR-PRF-01；内置 Profile 需要注释说明实测来源 | 只支持 JSON：可去掉依赖，但内置 Profile 无法写注释 |
| Profile 校验 | Go 结构体严格解码（拒绝未知键）+ 语义校验函数 | 校验规则含跨字段语义（功能码白名单、地址范围），JSON Schema 表达不全 | JSON Schema 库：多一个依赖，仍需补语义校验；P1 可额外导出 schema 给前端编辑器 |
| ICMP 在线检测 | `golang.org/x/net/icmp` | FR-HOST-03；调用系统 `ping` 时 Windows 退出码不可靠 | 只做 TCP 探测：不满足需求 |
| 日志滚动 | `gopkg.in/natefinch/lumberjack.v2` | NFR-07；体积小、无传递依赖 | 自写滚动：重复造轮子 |
| 消息模板 | 自研 `{{path}}` 变量替换（无逻辑） | 需求语法是 `{{device.name}}`，与 `text/template` 的 `{{.x}}` 不同；无逻辑模板没有注入面；可按输出格式做 JSON / HTML 转义 | `text/template`：语法不匹配，且模板可调用方法 |
| 时间相关测试 | 标准库 `testing/synctest` | 去抖、持续条件、倒计时都依赖时间；synctest 提供虚拟时钟，无需在业务代码中注入 Clock 接口 | 自定义 Clock 接口：增加全局抽象 |
| 实时推送 | SSE（标准库实现） | 需求默认假设；单向推送足够 | WebSocket：需要额外依赖 |

### 2.2 计划新增依赖清单

| 依赖 | 用途 | 引入阶段 |
|---|---|---|
| `modernc.org/sqlite` | SQLite 驱动 | M0 |
| `go.yaml.in/yaml/v3` | Profile 解析 | M1 |
| `golang.org/x/crypto` | `ssh`、`argon2` | M4 / M6 |
| `golang.org/x/net` | `icmp` | M6 |
| `gopkg.in/natefinch/lumberjack.v2` | 日志文件滚动 | M0 |
| `golang.org/x/sys` | 数据目录文件锁；P1 Windows 服务 | M0（通常已是 x/crypto 的传递依赖） |

版本在引入时按当期最新稳定版锁定，`go.sum` 入库。

当前 [go.mod](../../../../backend/go.mod) 已锁定 M0 直接依赖 `modernc.org/sqlite v1.60.1`、`gopkg.in/natefinch/lumberjack.v2 v2.2.1`、`golang.org/x/sys v0.49.0`，M1 引入 `go.yaml.in/yaml/v3 v3.0.5`，M4 为 argon2id 引入 `golang.org/x/crypto v0.58.0`，校验和见 `go.sum`；SSH 行为尚未实现。完整依赖清单已获项目方批准，尚未进入的阶段不提前引入依赖。

---

## 3. 总体架构

### 3.1 包结构

```text
backend/
├── cmd/
│   ├── ups-warden/            # 服务入口：参数解析、调用 app 装配
│   └── upssim/                # 协议模拟器（TST-01），开发与 CI 使用，不随服务发布
└── internal/
    ├── app/                   # 组装全部模块，负责启动 / 关闭顺序
    ├── config/                # 启动参数与环境变量（FR-DEP-05）
    ├── logging/               # slog 初始化、文件滚动、敏感字段脱敏
    ├── store/                 # SQLite 打开、PRAGMA、迁移、事务辅助、各表 repository
    │   └── migrations/        # 0001_init.sql …（embed）
    ├── secret/                # 主密钥管理、AES-256-GCM、Secret 类型（掩码输出）
    ├── model/                 # 统一数据模型：字段目录、Value/Field/Quality、Snapshot、事件类型
    ├── driver/                # Driver 接口、RawData、ReadPlan、只读防护 guard
    │   ├── snmp/              #   BER 编解码 + 客户端（ber 子包供模拟器复用）
    │   ├── modbus/
    │   └── ayi9/
    ├── profile/               # Profile 解析、校验、内置 Profile（embed builtin/*.yaml）、读计划、推荐
    ├── normalize/             # RawData + Profile → SourceSnapshot（含诊断 Trace）
    ├── collector/             # 数据源调度：每源一协程、退避、同 IP 并发限制、连接测试
    ├── state/                 # 状态引擎：合并、power_state、去抖、放电计时、剩余时间、通讯状态、事件派生
    ├── history/               # 历史写入、1 分钟聚合、保留期清理、历史查询
    ├── rule/                  # 规则引擎：事件 / 条件触发、持续、冷却、边沿、恢复动作、dry-run、执行日志
    ├── action/                # 动作执行器：notify / ssh / script / http / plan 分发、超时、互斥、输出截断
    ├── notify/                # 渠道接口、Webhook、Telegram、模板渲染、重试、通知日志
    ├── host/                  # 受控主机：SSH 客户端、主机指纹、在线检测
    ├── plan/                  # 关机计划编排、倒计时、取消、持久化、重启恢复
    ├── auth/                  # 用户、密码哈希、会话、CSRF、登录限速、首次启动
    ├── audit/                 # 审计日志（字段级差异 + 脱敏）
    ├── bus/                   # 进程内发布订阅（只用于 SSE 等可丢弃的扇出）
    ├── api/                   # /api/v1 handler、错误结构、SSE
    ├── webui/                 # embed 前端构建产物，SPA fallback
    ├── sim/                   # 模拟器实现（SNMP agent / Modbus 从站 / ayi9 服务端 / 场景引擎）
    └── server/                # 已有：http.Server 组装，挂载 /healthz、/api/v1、WebUI
```

### 3.2 包依赖规则（强制）

```text
              model  ◀────────────────────────────── 所有包都可依赖
                ▲
driver ──▶ profile ──▶ normalize ──▶ collector ──▶ state ──▶ rule ──▶ action ──▶ notify / host / plan
 (协议原始数据)  (字段定位)  (类型/单位/质量)               (只见统一模型)
```

| 规则 | 说明 |
|---|---|
| R-DEP-1 | `state`、`history`、`rule`、`action`、`notify`、`host`、`plan` **不得** import `driver/...`、`profile`、`normalize` |
| R-DEP-2 | `driver/...` 不得 import `profile`（驱动只接收数据形式的 `ReadPlan`，不知道型号） |
| R-DEP-3 | `api` 可以 import `collector`（连接测试、原始数据诊断）和 `profile`（Profile 管理），但业务查询只走统一模型 |
| R-DEP-4 | `sim` 只允许被 `cmd/upssim` 和 `_test.go` 引用 |

在 `internal/archtest` 中用 `go/build` 遍历 import 图实现上述断言，随 `go test ./...` 执行，违反即失败（对应 FR-NORM-07、NFR-09）。

### 3.3 运行时数据流

```text
            ┌───────────── collector ─────────────┐
 Source#1 ──┤ goroutine: Poll → normalize ──────── │──┐  SourceResult（无损 chan）
 Source#2 ──┤ goroutine: Poll → normalize ──────── │──┤
            └──────────────────────────────────────┘  ▼
                                          ┌────── state（每设备一个 actor 协程）──────┐
                                          │ 合并 → 判定 → 去抖 → 计时 → 事件派生 → 持久化 │
                                          └──┬──────────┬─────────────┬─────────────┘
                                 DeviceUpdate│      Event│(先落库再投递) │
                     ┌───────────────────────┤           │             │
                     ▼ 有界缓冲，满则丢弃+计数 ▼ 无损 chan ▼             ▼ 有损扇出
                 history writer         rule engine（单协程 + 1 s tick）   bus → SSE 客户端
                                              │ ActionRequest
                                              ▼
                                   action executor（每次执行一个协程，按动作互斥）
                                     ├─ notify（每渠道一个发送队列）
                                     ├─ ssh / script / http
                                     └─ plan orchestrator（每个运行实例一个协程）
```

通道语义（**决策**）：

| 链路 | 语义 | 原因 |
|---|---|---|
| collector → state | 无损、阻塞（缓冲 64） | 状态判定不能丢样本；state 处理是纯内存计算，不会长时间阻塞 |
| state → rule | 无损、阻塞（缓冲 256） | 事件和关机条件不能丢 |
| state → history | 有界缓冲（1024），满时丢弃并计数、按分钟汇总告警日志 | 历史写入慢不能拖慢判定链路 |
| 任意 → bus → SSE | 每订阅者有界缓冲（256），溢出时关闭该连接，前端重连后全量重同步 | 浏览器慢不能影响后端 |
| 事件 | state 先同步写入 `events` 表，再投递给 rule 和 bus | 规则执行日志可以引用已持久化的事件 ID；重启后不会出现"规则执行了但事件不存在" |

### 3.4 启动与关闭顺序

启动（`internal/app`）：

1. 解析配置 → 初始化日志。
2. 创建数据目录，获取 **数据目录独占锁**（`<data>/ups-warden.lock`，进程级文件锁）。获取失败直接退出：两个实例共用数据目录会重复执行关机（**决策**）。
3. 加载或生成主密钥（第 16.3 节）。
4. 打开 SQLite，执行迁移。
5. 加载内置 Profile，校验失败视为编程错误直接退出。
6. 从数据库恢复：设备状态（`device_state`）、活动告警、规则实例状态、未结束的计划运行。
7. 依次启动：state → history → notify → action / plan（恢复未完成运行，见 13.5）→ rule → collector。
8. 最后启动 HTTP 服务。

目标：冷启动 ≤ 3 s（NFR-06），重启后 ≤ 10 s 恢复采集（NFR-05）。

关闭（收到 SIGINT / SIGTERM 或 Windows 服务停止）：

1. HTTP `Shutdown`（SSE 连接通过 `BaseContext` 取消）。
2. 停止 collector（取消 context，关闭连接）。
3. rule 引擎停止接收新输入。
4. 等待进行中的动作，最长 10 s（NFR-04）；超时的动作标记为 `interrupted` 并写库。
5. 刷新历史写缓冲，持久化 state，关闭数据库，释放锁。

所有长期运行的协程统一用 `app.Go(name, fn)` 启动：`recover` panic 后记录堆栈；采集协程 panic 后按退避重启，核心协程（state / rule）panic 后重启并发出系统告警日志（NFR-04）。

---

## 4. 统一数据模型（`model`）

### 4.1 字段目录

所有统一字段在 `model` 中以 **静态目录** 声明，Profile 校验、归一化、规则编辑器、模板变量、历史存储都以目录为准：

```go
// FieldSpec 描述一个统一字段的类型契约。
type FieldSpec struct {
    Key      string    // 基础名，如 "input_voltage"
    Kind     Kind      // Number / Bool / Enum / Text / Duration
    Unit     string    // "V" "A" "Hz" "VA" "W" "%" "°C" "s"，无单位为空
    Phased   bool      // 是否按相（1..3）
    Enum     []string  // Kind=Enum 时的合法取值（含 "unknown"）
    History  bool      // 是否写入历史曲线
    Derived  bool      // 是否由状态引擎派生（Profile 不能映射）
    Group    string    // ident / nominal / input / output / bypass / battery / flags / derived
}
```

- 按相字段的实例键为 `基础名[相号]`，如 `output_load_pct[1]`；目录同时用于校验 Profile 中的字段名。
- `SchemaVersion` 常量从 1 开始；只增字段，不改语义（FR-NORM-09）。
- 枚举取值完全按需求 5.1；补充 `output_source`：`other / none / normal / bypass / battery / booster / reducer / unknown`（对应 RFC 1628 1..7）。

### 4.2 值与质量

```go
type Quality uint8 // Good / Invalid / Unsupported / Stale

type Field struct {
    Num     float64  // Number / Duration（整数秒以 float64 承载，输出时取整）
    Bool    bool
    Str     string   // Enum / Text
    Quality Quality
    Reason  string   // 非 good 时的原因码，供 UI 提示（FR-UI-05）
}
```

原因码：`placeholder`、`invalid_value`、`out_of_range`、`not_mapped`、`device_unsupported`、`phase_inactive`、`decode_error`、`enum_unmapped`、`comm_stale`。

不可映射的枚举取 `unknown` 并把原始值放入诊断 Trace（FR-NORM-04），不写入 Snapshot，避免原始编码泄漏到业务层。

### 4.3 快照与设备状态

```go
// SourceSnapshot 是单个数据源一次采集的归一化结果。
type SourceSnapshot struct {
    SourceID int64
    At       time.Time          // 采集完成时刻（UTC）
    Fields   map[string]Field   // 键为字段实例名
    Hints    PowerHints         // Profile 声明的供电判定依据（以统一字段名表达，见 6.1）
}

// DeviceState 是状态引擎对外发布的设备视图，业务模块只读它。
type DeviceState struct {
    DeviceID        int64
    SchemaVersion   int
    At              time.Time
    Fields          map[string]Field   // 合并后的观测字段 + 派生字段
    PowerStateRaw   string             // 未去抖判定
    PowerState      string             // 去抖后判定
    PendingSince    *time.Time         // 正在确认中的状态起点（"疑似停电，确认中"）
    OnBatterySince  *time.Time         // 原始进入电池模式时刻（FR-RT-01）
    OutageCycleID   *int64             // 当前停电周期
    CommState       string             // ok / lost
    LastOKAt        time.Time
    RuntimeSource   string             // native / estimated / none
}
```

`on_battery_elapsed` 不在 state 中定时刷新，而是在规则评估、API 输出、模板渲染时由 `OnBatterySince` 与当前时间计算。这样失联期间不需要 state 每秒发布更新，放电计时仍然持续（FR-COM-03 `hold`）。

### 4.4 按相字段的规则语义（**决策**）

规则条件、模板变量中可以写不带相号的字段（如 `output_load_pct`），含义为 **所有质量为 `good` 的相的最大值**；没有任何 `good` 相时视为非 `good`。也可以写 `output_load_pct[2]` 指定相。这样 `overload` 阈值、规则条件在单相 / 三相机型上写法一致。

---

## 5. 驱动层（`driver`）

### 5.1 接口

```go
// Driver 是某种协议的只读采集实现。驱动不包含任何型号逻辑：读什么由 ReadPlan 决定。
type Driver interface {
    Probe(ctx context.Context) (DeviceInfo, RawData, error) // 连接测试：身份 + 一次全量读取
    Poll(ctx context.Context) (RawData, error)              // 单次采集；连接失效时自行重连
    Close() error
}

// ReadPlan 由 profile 包根据 Profile 生成，按协议取其一。
type ReadPlan struct {
    SNMP   *SNMPPlan   // 读取策略、bulk 根、max-repetitions、GET 用 OID 列表
    Modbus *ModbusPlan // 已合并的 (FC, 起始地址, 数量) 块列表、Unit ID
    AYI9   *AYI9Plan   // RTD 刷新间隔（QAD 每次都读）
}

// RawData 是协议原始数据，只用于归一化和诊断，不进入历史、不参与业务判断（FR-NORM-08）。
type RawData struct {
    Protocol string
    At       time.Time
    SNMP     map[string]SNMPValue        // OID → 值（含 noSuchObject / endOfMibView 等异常标记）
    Modbus   map[RegKey]uint16           // (FC, 地址) → 寄存器值；读失败的块记录在 Errors
    AYI9     *AYI9Raw                    // UID、QAD 字段切片、RTD 字段切片、RTD 采集时间
    Errors   []BlockError                // 部分失败的块 / OID，供诊断和质量判定
}
```

`DeviceInfo` 包含 `Model`、`Manufacturer`、`Firmware`、`AgentVersion`、`Identity`（如 ayi9 的 `uid`），用于 Profile 推荐（FR-DEV-05）。

### 5.2 只读防护（SEC-01、FR-PRF-08、TST-04）

三层防护，任一层单独都足以阻止写请求：

| 层 | 机制 |
|---|---|
| L1 类型层 | 编码器只定义读请求类型：SNMP 编码器的 PDU 类型常量只有 `0xA0 GetRequest`、`0xA1 GetNextRequest`、`0xA5 GetBulkRequest`；Modbus 帧构造函数参数类型 `ReadFunc` 只有 `FC03`、`FC04` 两个值；ayi9 请求结构体的 `cmd` 字段类型只有 `QAD`、`RTD` 两个常量，且结构体没有 `new_pass`、`host` 字段 |
| L2 发送层 | 所有驱动必须通过 `driver.guard` 包装后的连接发送数据。guard 在每次 `Write` 前 **解析即将发出的字节**：SNMP 解码外层 SEQUENCE 后检查 PDU tag ∈ {A0, A1, A5}；Modbus 检查 MBAP 后第 1 字节 ∈ {03, 04}；ayi9 用 JSON 解码后检查键集合 ⊆ {`cmd`,`session`,`pass`} 且 `cmd` ∈ {`QAD`,`RTD`}。不满足时拒绝发送、返回 `ErrWriteBlocked` 并记录 error 日志 |
| L3 测试层 | ① 每种驱动的 fuzz 测试：随机生成 Profile / ReadPlan，经驱动发到记录型假连接，断言全部字节通过 guard 校验；② 模拟器记录收到的所有请求，集成测试结束时断言无非白名单请求（对应 AC-11 的自动化部分）；③ 架构测试断言驱动包不直接调用 `net.Dial*`（只能经 guard） |

此外，驱动、连接测试、Profile 推荐都 **不访问设备 HTTP 端口**，系统中没有任何针对设备的 HTTP 客户端代码（SEC-03）。

M2 已落实上述防护，并把每次 Write 限定为完整单帧，拒绝尾随报文、重复 JSON 键和截断输入；短写关闭连接而非重发片段。所有网络测试使用假连接或 loopback 模拟器。结构性只读不等于型号兼容性或真机验收；CP-1 仍须独立授权。

### 5.3 SNMP（`driver/snmp`）

| 项 | 实现 |
|---|---|
| 版本 | v1、v2c；community 默认 `public`，端口默认 161 |
| request-id | 在 `[0x00800000, 0x7FFFFFFF]` 内随机取值，保证 BER INTEGER 恰好 4 字节（≥ 2²³ 需要 4 字节，< 2³¹ 不需要第 5 字节）。单元测试断言编码形如 `02 04 xx xx xx xx`（FR-SNMP-04） |
| 响应匹配 | 按 request-id 匹配，丢弃不匹配的报文；单数据源串行请求 |
| GETBULK | 起始 OID 默认 `1.3.6.1.2.1.33`、`max-repetitions` 默认 60；以本次最后一个 OID 续取，直到 OID 超出起始子树、遇到 `endOfMibView` 或达到安全上限（默认 20 轮，防止异常设备无限循环）（FR-SNMP-03） |
| GETNEXT | v1 或配置为 walk 时使用，终止条件同上 |
| GET | 使用 Profile 中声明的 OID 列表，每个 PDU 最多 20 个 varbind。v1 返回 `noSuchName` 时按 `error-index` 剔除对应 OID 后重试，被剔除 OID 记为不支持并缓存到重连为止 |
| 策略回退 | 默认 GETBULK；GETBULK 超时或出错时，本轮回退到 GET（FR-SNMP-02）。回退结果在诊断信息中标明。ET1K 的 GET 全部返回 `noSuchName`，回退后字段全部为 `unsupported`，这是设备真实能力，属预期 |
| 异常值 | `noSuchName` / `noSuchObject` / `noSuchInstance` / `endOfMibView` 映射为 `SNMPValue{Exception}`，归一化时记为 `unsupported/device_unsupported`（FR-SNMP-07） |
| 值类型 | INTEGER、OCTET STRING、Counter32、Gauge32、TimeTicks、OID、Counter64，其余类型保留原始字节供诊断 |
| 表格行索引 | 由 Profile 显式写出完整 OID（如 `…33.1.3.3.1.3.1` / `.2` / `.3`），驱动不理解表结构（FR-SNMP-05） |

`ber` 子包同时实现编码与解码，模拟器的 SNMP agent 复用它。

### 5.4 Modbus TCP（`driver/modbus`）

| 项 | 实现 |
|---|---|
| 连接 | 长连接复用；读失败或超时后关闭连接，下一次 Poll 重连（FR-MB-07） |
| 帧 | MBAP（事务 ID 递增、协议 0、长度、Unit ID）+ PDU `FC, addr(2), count(2)`；按事务 ID 校验响应 |
| 读计划 | 由 profile 包生成：按 FC 分组 → 地址排序 → 合并间隙 ≤ 8 的相邻地址 → 按"单次最大寄存器数"（默认 100，上限 125）切块 → 裁剪到 Profile 声明的可读范围（FR-MB-05）。多寄存器类型（`uint32` 等）保证不被切块拆开 |
| 异常响应 | 记录异常码（如 `0x02 IllegalDataAddress`）到 `RawData.Errors`，该块内字段记为 `unsupported`，其他块不受影响 |
| 地址 | 统一 0 基协议地址（FR-MB-06 的 40001 换算提示属前端） |

### 5.5 AYI9 云监控卡 JSON API（`driver/ayi9`）

| 项 | 实现 |
|---|---|
| 连接 | TCP 长连接（默认 2390）；同一连接 **严格串行**，收到响应或超时后才发下一条（FR-AYI9-02） |
| 请求 | `{"cmd":"QAD","session":"<递增十进制>","pass":"…"}`；`pass` 为空时省略；`pass` 长度 > 15 在配置校验阶段拒绝（对应设备错误码 106） |
| 响应读取 | 连接上使用 `json.Decoder` 逐个读取 JSON 对象（兼容有无换行）；响应 `session` 不匹配时丢弃并继续读，直到超时 |
| 超时处理 | 超时后 **关闭并重建连接**，避免迟到响应污染下一条请求 |
| 读取频率 | 每次 Poll 读 QAD；RTD 在连接建立后读一次，此后每 10 分钟刷新一次（额定信息几乎不变，可减少设备负载） |
| 解析 | QAD：去掉开头的 `(`，按空白切分，要求 23 段；RTD 要求 10 段；段数不符记为解码错误并保留原始字符串 |
| 错误识别 | `value` 为 `FAILED` 或为错误码 101–106 / 201 / 410 时返回 `*ayi9.DeviceError{Code}`；103 映射为 `ErrPasswordWrong`，连接测试显示"密码错误（103）"（FR-AYI9-07、AC-10）。错误码在响应中的确切格式以模拟器和真机验证为准，解析时同时兼容 `value` 为纯数字和 `code` 字段两种形式 |
| 身份 | 记录 `uid`；以 `@ayi9.com` 结尾时在连接测试结果中附加识别提示（FR-AYI9-09/11） |

### 5.6 连接与并发

- **同 IP 并发限制**（FR-DEV-08）：collector 维护 `map[host]*semaphore`，默认容量 1，按设备可调。同一 IP 上的 SNMP、Modbus、ayi9 数据源以及 **连接测试** 共用同一信号量；连接测试获取信号量超时（默认 5 s）时返回"设备忙"。
- 超时：单次请求默认 2 s；Poll 的总截止时间 = 轮询间隔 × 0.9 和（超时 × (重试 + 1)）中的较小值，防止采集周期堆积。

---

## 6. Profile 与归一化

### 6.1 Profile 格式

字段映射的通用键：

| 键 | 说明 |
|---|---|
| `field` | 统一字段实例名，必须存在于字段目录且不是派生字段 |
| `source` | 协议相关的定位（见下例） |
| `type` | 原始类型：`int` / `uint16` / `int16` / `uint32` / `int32` / `float32` / `string` / `number_string` / `si_number`（如 `1K`）/ `bitstring16` |
| `bit` / `bits` | 位提取：单个位或范围（如 `"0-3"`），位号按 b0 为最低位 |
| `invert` | 布尔取反 |
| `scale` / `offset` | 线性换算：`值 = 原始值 × scale + offset` |
| `unit` | 原始单位，需要换算时填写（如 `min` → s）；与目录单位一致时省略 |
| `enum` | 原始值 → 统一枚举取值 |
| `invalid` | `values`（无效常量，如 `[0]`）、`range`（合法范围）、`placeholders`（默认已包含 `---` 和空串） |
| `note` | 说明，如"待校准（V2）" |

示例一：`et1k-modbus`（节选）

```yaml
schema: 1
key: et1k-modbus
name: ET1K（Modbus TCP）
version: 1
protocol: modbus
match:
  models: []                     # Modbus 无身份信息，只能手动选择
modbus:
  readable: [{ fc: 3, start: 0, end: 79 }]   # 实测：80 及以上返回 IllegalDataAddress
  max_registers_per_read: 100
  byte_order: big
  word_order: big
power_hints:
  indicators: [input_voltage]    # 只能用输入电压推断市电/电池
fields:
  - { field: battery_voltage,      source: { fc: 3, addr: 8 },  type: uint16, scale: 0.1 }
  - { field: battery_capacity_pct, source: { fc: 3, addr: 10 }, type: uint16, scale: 0.1 }
  - { field: input_voltage[1],     source: { fc: 3, addr: 17 }, type: uint16, scale: 0.1 }
  - field: output_power[1]
    source: { fc: 3, addr: 34 }
    type: uint16
    scale: 0.1
    note: 待校准（需求 17.1 V2）
```

示例二：`et1k-snmp`（节选）

```yaml
schema: 1
key: et1k-snmp
name: ET1K（SNMP）
version: 1
protocol: snmp
match:
  models: [ET1K]
  agent_versions: ["ayi9 v1"]
snmp:
  strategy: getbulk
  bulk_root: 1.3.6.1.2.1.33
  max_repetitions: 60
power_hints:
  # V3：停电时 upsOutputSource 取值未验证，暂不使用
  indicators: [native_seconds_on_battery, input_voltage]
fields:
  - field: battery_status
    source: { oid: 1.3.6.1.2.1.33.1.2.1.0 }
    type: int
    enum: { 1: unknown, 2: normal, 3: low, 4: depleted }
  - field: native_runtime_remaining
    source: { oid: 1.3.6.1.2.1.33.1.2.3.0 }
    type: int
    unit: min
    invalid: { values: [0] }     # 实测恒为 0
  - { field: input_voltage[1], source: { oid: 1.3.6.1.2.1.33.1.3.3.1.3.1 }, type: int }
```

示例三：`ayi9-json-generic`（节选）

```yaml
schema: 1
key: ayi9-json-generic
name: AYI9 云监控卡 JSON API（通用）
version: 1
protocol: ayi9-json
phases:
  from: { cmd: RTD, index: 8 }   # "1/1"、"3/3"：超出有效相数的字段记为 phase_inactive
fields:
  - { field: input_voltage[1], source: { cmd: QAD, index: 0 }, type: number_string }
  - { field: input_voltage[2], source: { cmd: QAD, index: 1 }, type: number_string }
  - field: ups_mode
    source: { cmd: QAD, index: 22 }
    type: bitstring16             # 字符串下标 0 对应 b15
    bits: "0-3"
    enum: { 0: power_on, 1: standby, 2: bypass, 3: line, 4: battery, 5: test,
            6: fault, 7: converter, 8: eco, 9: shutdown }
  - { field: mains_ok, source: { cmd: QAD, index: 22 }, type: bitstring16, bit: 7, invert: true }
  - { field: rated_va, source: { cmd: RTD, index: 9 }, type: si_number }
```

`power_hints.indicators` 用 **统一字段名** 声明本型号可信的供电判定依据及顺序（FR-PRF-01 的"派生参数"），不出现任何协议地址，因此可以随 `SourceSnapshot` 交给状态引擎而不违反 R-DEP-1。省略时使用默认顺序（见 8.2）。

### 6.2 校验

保存、导入、启动加载时执行，错误信息带 YAML 行号：

1. 严格解码：未知键报错；`schema` 必须为已支持版本。
2. `protocol` ∈ {`snmp`, `modbus`, `ayi9-json`}；协议专属段与 `source` 形态匹配。
3. `field` 在字段目录中存在、不是派生字段、同一 Profile 内不重复；相号 1..3。
4. Modbus：`fc` ∈ {3, 4}（**写功能码在校验阶段即拒绝**，但只读安全不依赖此处）；地址在 `readable` 范围内；多寄存器类型不越界；`max_registers_per_read` ≤ 125。
5. ayi9：`cmd` ∈ {`QAD`, `RTD`}；QAD 索引 0..22、RTD 索引 0..9。
6. SNMP：OID 格式合法。
7. `enum` 的目标值属于字段目录的枚举集合；`bit`/`bits` 在 0..15。
8. `power_hints.indicators` 只能使用目录中允许作为判定依据的字段。

### 6.3 内置与自定义 Profile 的存储

- 内置 Profile（FR-PRF-04 的 5 个）以 YAML 文件 `profile/builtin/*.yaml` 通过 `embed` 打包，只读，不写入数据库；键名保留，自定义 Profile 不能使用。
- 自定义 Profile 存入 `profiles` 表，保存原始 YAML 文本（保留注释）与 `version`。每次修改 `version + 1`。
- 数据源记录 `profile_key` 与 `profile_version`。P0 中数据源始终使用 Profile 最新版本，版本号只用于 P1 的"变更提示"（FR-PRF-07）。
- 导入 / 导出 / 复制属 P0（FR-PRF-05）；编辑器 P1，但后端保存接口在 P0 即提供（复制后需要能改名和修改）。

### 6.4 归一化流水线（`normalize`）

对 Profile 的每个字段映射，依次执行：

```text
提取 ──▶ 解码 ──▶ 占位符检查 ──▶ 位提取 / 取反 ──▶ 枚举映射 ──▶ scale / offset ──▶ 单位换算 ──▶ 有效性检查 ──▶ Field
 │        │                                                                                   │
 └ 缺失 / 设备异常 → unsupported                                                              └ 不满足 → invalid（不转换成 0）
```

- 未在 Profile 中映射的目录字段 → `unsupported/not_mapped`（FR-DEV-09）。
- 有效相数之外的相 → `unsupported/phase_inactive`（FR-NORM-05、FR-AYI9-08）。
- 每个字段产生一条诊断 `Trace{Field, Source 描述, Raw, Decoded, Final, Quality, Reason}`；collector 为每个数据源只在内存中保留最近一次 RawData 与 Trace（FR-NORM-08），供连接测试、原始数据查看和 P1 校准视图使用。
- 归一化是纯函数，便于用 fixture 做回归测试（FR-NORM-10）。
- M1 采用 2026-10-14 重新采集的 60 组三协议样本，脱敏派生 fixture 保留测量值和时间间隔，原件留在忽略的 `data/`。输出电压选择与 QAD 在全部样本一致的 Modbus reg28，不以旧报告中 reg02 的近似同值替代；W 与 VA 不互换，未校准功率不强行映射。详见 [fixture 证据与边界](../../../../backend/testdata/et1k/README.md)。

### 6.5 Profile 推荐（FR-DEV-05）

连接测试的 `DeviceInfo` 与各 Profile 的 `match` 比对：型号精确匹配 > Agent 版本匹配 > 协议通用 Profile。返回按得分排序的候选列表；Modbus 没有身份信息，返回该协议所有 Profile 由用户选择。

---

## 7. 采集调度（`collector`）

- **Supervisor**：根据数据库中启用的数据源启动 / 停止 worker。设备或数据源配置变更时，API 层在事务提交后调用 `collector.Reload(sourceID)`，supervisor 取消旧 worker 后启动新 worker。
- **Worker**（每数据源一个协程，FR-DEV-06）：
  1. 首次启动随机抖动 0..轮询间隔，避免所有设备同时轮询。
  2. 每个周期：获取同 IP 信号量 → `Poll` → 释放 → `normalize` → 发送 `SourceResult{Snapshot | Err, Latency}` 给 state。
  3. 失败时按 1 s、2 s、4 s … 指数退避，上限 30 s（FR-DEV-07）；成功后恢复正常间隔。
  4. 失败计数与通讯状态判定交给 state（采集层只上报事实）。
- **连接测试**（FR-DEV-04）：用待保存的配置临时构造驱动，执行一次 `Probe` + 归一化，返回延迟、`DeviceInfo`、RawData、Trace、Profile 推荐。只发送白名单读请求（SEC-04），并受同 IP 信号量约束。
- **数据源运行状态**：最后成功时间、最近延迟、最近错误（脱敏）、连续失败次数，供设备详情页展示。

---

## 8. 状态引擎（`state`）

### 8.1 结构

每台设备一个 actor 协程，串行处理三类输入：`SourceResult`、1 s tick、配置变更。actor 独占该设备的全部可变状态，无需加锁。

处理一次输入的步骤：

1. **合并**：P0 每台设备一个数据源（需求 Q7），直接采用该源字段；数据结构已按多源设计（数据源有 `priority`），P1 在此实现 FR-SRC-01~04。
2. **通讯状态**：更新连续失败计数，判定 `comm_state`（8.5）。
3. **供电判定**：计算 `PowerStateRaw`，运行去抖状态机得到 `PowerState`（8.2）。
4. **计时与估算**：维护 `OnBatterySince`、停电周期、剩余时间（8.3、8.4）。
5. **事件派生**：与上一状态比较，生成事件与告警（8.6）。
6. **持久化**：关键状态有变化时写 `device_state`（不是每次采集都写）。
7. **发布**：`DeviceUpdate` 发给 rule、history、bus。

### 8.2 供电状态判定与去抖

原始判定（FR-STA-02），按 `power_hints.indicators` 顺序取 **第一个质量为 `good`** 的依据；未声明时默认顺序为：

| 顺序 | 依据（统一字段） | 判定 |
|---|---|---|
| 1 | `ups_mode` | `battery` → on_battery；`bypass` → bypass；`fault` → fault；`shutdown` → shutdown；`line`/`eco`/`converter` → online；其他继续下一依据 |
| 2 | `mains_ok` | false → on_battery；true → online |
| 3 | `output_source` | `battery` → on_battery；`bypass` → bypass；`normal` → online |
| 4 | `native_seconds_on_battery` | > 0 → on_battery；= 0 → online |
| 5 | `input_voltage`（相最大值） | < 设备阈值（默认 50 V）→ on_battery；否则 online |
| — | 全部不可用 | unknown |

去抖状态机（FR-STA-03/04）：

| 当前确认状态 | 原始判定 | 行为 |
|---|---|---|
| 非 on_battery | on_battery | 记录 `PendingSince` 与 `OnBatterySince`（原始时刻）；持续满"停电确认时长"（默认 10 s）后确认为 on_battery，触发 `mains_lost`，开启停电周期 |
| 非 on_battery（确认中） | 非 on_battery | 取消确认，清空 `PendingSince` 与 `OnBatterySince`（闪断，不产生事件，AC-04） |
| on_battery | online | 记录 `PendingSince`；持续满"市电恢复确认时长"（默认 60 s）后确认为 online，触发 `mains_restored`，关闭停电周期 |
| on_battery（恢复确认中） | on_battery | 取消恢复确认 |
| 其他状态之间（online / bypass / fault / shutdown） | — | 立即切换（这些状态来自显式标志，相关事件均为告警级别，不直接驱动关机） |
| 任意 | unknown | 保持确认状态不变；`unknown` 不会产生 `mains_restored` |

去抖计时使用单调时钟（`time.Now()` 自带单调读数），不受系统时间调整影响；持久化的时间戳使用 UTC 墙钟。

### 8.3 停电周期与放电计时（FR-RT-01、FR-IDEM-03）

- `OnBatterySince` 取 **原始** 进入 on_battery 的时刻（去抖确认前），确认后立即持久化。
- 停电周期从去抖后的 `mains_lost` 开始、到去抖后的 `mains_restored` 结束，存于 `outage_cycles` 表；关机计划幂等以周期 ID 为键（13.3）。
- 重启恢复：从 `device_state` 读回确认状态、`OnBatterySince`、周期 ID。重启后首个样本若为 online，仍需满 60 s 恢复确认才会关闭周期，避免重启瞬间误判恢复。

### 8.4 剩余时间（FR-RT-02/03）

1. `native_runtime_remaining` 为 `good` → `runtime_estimate` = 原生值，`runtime_source=native`。
2. 否则若处于 on_battery：用最近 10 分钟窗口内 `battery_capacity_pct`（`good` 样本）做最小二乘线性回归，得到放电速率（%/min）。窗口跨度不足 2 分钟、样本少于 5 个或速率 ≤ 0.05 %/min 时字段为 `invalid/estimating`（UI 显示"估算中"）；否则 `剩余 = 容量 ÷ 速率 × 60` 秒，`runtime_source=estimated`。
3. 其他情况 `runtime_source=none`。

窗口只在内存中保存，重启后重新积累（估算本就不用于关机判定，FR-RT-04）。

### 8.5 通讯状态与失联策略（FR-COM-01~03）

- 连续失败次数 ≥ N（默认 3）→ `comm_state=lost`，触发 `comm_lost`；下一次成功即恢复并触发 `comm_restored`。
- 失联期间所有观测字段保留最后值，质量改为 `stale/comm_stale`，`LastOKAt` 供 UI 显示。规则只认 `good`，因此容量类条件自然不成立。
- 失联策略（按设备配置）：

| 策略 | 行为 |
|---|---|
| `hold`（默认） | 确认的 `PowerState` 保持不变；`OnBatterySince` 不变，`on_battery_elapsed` 持续增长；"电池模式持续 N 分钟"条件照常可触发（AC-08） |
| `assume_on_battery_after` | 失联超过 X 分钟后，若当前不是 on_battery，则直接确认为 on_battery（不再走去抖）、开启停电周期；`OnBatterySince` 取 **失联前最后一次成功采集时刻**（**决策**：失联期间是否已停电未知，按最早可能时刻计时，偏向保护负载） |
| `ignore` | 失联期间 `PowerState` 置为 `unknown`，不产生 `mains_restored`，停电周期保持；时长与供电条件因 `unknown` 均不成立，不参与关机判定 |

### 8.6 事件与告警（6.1、FR-EVT-01~05）

- 事件判定器是一组纯函数，输入"上一 DeviceState + 当前 DeviceState + 设备阈值"，输出事件列表。
- 成对事件（如 `battery_low` / `battery_low_cleared`）形成活动告警：发生时插入 `events` 行（`active=1`），恢复时更新同一行的 `cleared_at` 并插入一条恢复事件记录。单次事件（如 `battery_depleted`、`ups_fault`）只插入一条记录，`ups_fault` 在 `ups_mode` 离开 `fault` 时关闭。
- 阈值回差（FR-EVT-03）：`battery_low` 在容量 ≤ 30% 时触发、≥ 35% 时清除；`overload` 负载 ≥ 90% 触发、≤ 85% 清除（回差默认 5，按设备可配）；`temperature_high`（P1）同理。
- 依据字段非 `good` 时不改变告警状态（既不触发也不清除）。
- 启动不误报（FR-EVT-05）：actor 初始状态从 `device_state` 与活动告警表恢复，首个样本与恢复的状态比较；已活动的告警不会重复生成。
- 事件记录包含快照摘要：`power_state`、`battery_capacity_pct`、`output_load_pct`、`input_voltage`、`on_battery_elapsed`、`runtime_estimate` 等关键字段（JSON）。

---

## 9. 历史数据（`history`）

| 项 | 设计 |
|---|---|
| 存储内容 | **只存归一化后的统一字段**（D6）。协议原始数据（OID 值、寄存器值、QAD / RTD 字符串）与诊断 Trace 只在内存中为每个数据源保留最近一次，任何表都不持久化（FR-NORM-08） |
| 明细样本表 | `history_sample(device_id, ts, data)`，`WITHOUT ROWID`，主键 `(device_id, ts)`；每次采集写一行，`data` 为 JSON 对象，只包含字段目录中 `History=true` 且质量为 `good` 的统一字段（按字段名读取，FR-NORM-09）。即需求 FR-DATA-02 中的"原始快照"，"原始"指未聚合 |
| 写入 | 单独的 writer 协程，每 1 s 或每 200 行批量提交一次事务 |
| 聚合 | 每个 Writer 首次补算保留期内完整分钟；随后每分钟按成功水位与 Flush 已提交样本的最早 dirty 分钟，幂等重算受影响区间的 `min / max / avg / count`，upsert 写入 `history_1m(device_id, bucket, data)`。与 Flush 共用串行 gate，整轮成功才推进水位；新 Writer 重新补算，保留期边缘残缺分钟不覆盖已有完整聚合 |
| 清理 | 每小时删除过期数据（明细 7 天、聚合 90 天、日志类 365 天，均可在系统设置中修改，FR-DATA-02/03）；按小批量（每批 5000 行）删除，避免长事务阻塞采集写入（FR-DATA-04） |
| 查询 | `resolution=auto` 时：时间范围在明细保留期内且 ≤ 6 小时用明细表，否则用 1 分钟聚合；服务端再按时间桶降采样到 ≤ 1000 点返回 |

容量估算（3 s 轮询、约 20 个历史字段，均为归一化数据）：明细约 50–60 MB / 设备 / 7 天；1 分钟聚合约 60–110 MB / 设备 / 90 天。32 台设备合计约 4–5 GB，可通过保留期设置调整。

---

## 10. 规则引擎（`rule`）

### 10.1 规则结构

```json
{
  "name": "电池模式过久或电量低 → 关机",
  "enabled": true,
  "dry_run": true,
  "scope": { "type": "device", "device_ids": [1] },
  "trigger": {
    "type": "condition",
    "any_of": [
      { "all_of": [ { "field": "power_state", "op": "eq", "value": "on_battery" },
                    { "field": "on_battery_elapsed", "op": "gte", "value": 600 } ] },
      { "all_of": [ { "field": "power_state", "op": "eq", "value": "on_battery" },
                    { "field": "battery_capacity_pct", "op": "lte", "value": 40, "for_s": 10 } ] },
      { "all_of": [ { "field": "low_battery", "op": "eq", "value": true } ] }
    ]
  },
  "actions": [ { "action_id": 7 } ],
  "recovery_actions": [],
  "cooldown_s": 0
}
```

- `scope.type`：`device` / `tags` / `all`。规则以 **（规则, 设备）** 为实例分别评估。
- `trigger.type=event`：`{"type":"event","events":["mains_lost"]}`；配对事件恢复时执行恢复动作（FR-RULE-05）。
- `trigger.type=condition`：两层结构"OR of AND"（**决策**：需求将 OR 分组列为 P1，但 FR-RULE-06 的 P0 关机模板本身就是 OR 条件，因此 P0 即实现两层 OR / AND，不支持更深嵌套）。
- 运算符：`eq ne gt gte lt lte`；每个条件可带 `for_s`（持续时长）。

### 10.2 评估语义

| 项 | 语义 |
|---|---|
| 评估时机 | 收到该设备的 `DeviceUpdate` 时，以及每 1 s tick（保证失联期间 `on_battery_elapsed` 类条件也能到达） |
| 字段质量 | 引用字段非 `good` → 条件不成立；原因写入执行日志，**只在原因变化时记录**，避免每秒刷日志 |
| 持续条件 | 每个条件实例记录"开始成立时刻"；成立不足 `for_s` 视为不成立 |
| 边沿触发 | 整体条件由假变真时触发一次；保持为真期间不重复触发；由真变假时执行恢复动作（FR-RULE-04/05） |
| 冷却 | 距上次触发不足 `cooldown_s` 时跳过并记录"冷却中"；被跳过的边沿不会在冷却结束后补发 |
| 持久化 | 规则实例状态（是否已触发、上次触发时间）写入 `rule_state`，重启后已触发的条件规则不会重复触发 |
| 估算剩余时间警告 | 保存时若规则包含"执行关机计划"动作且条件引用 `runtime_estimate`，响应中返回 `warnings`（FR-RT-04），不阻止保存 |
| dry-run | 规则级 `dry_run=true` 时，下发给执行器的请求带 dry-run 标志（FR-RULE-07） |
| 执行日志 | 每次命中与动作派发写 `rule_logs`：触发原因、引用字段当时的值与质量、动作结果引用（FR-RULE-09） |

内置规则模板（FR-RULE-06）以代码常量提供，`GET /api/v1/rules/templates` 返回，前端据此预填表单；新建的关机模板默认 `dry_run=true`。

---

## 11. 动作执行（`action`、`host`）

### 11.1 通用框架

```go
type Request struct {
    ActionID int64
    DryRun   bool
    Trigger  Trigger   // rule / manual / plan，含规则 ID、事件 ID、操作者
    Context  Vars      // 设备与字段变量，用于模板与环境变量
}
```

- 每个动作一把互斥锁：`TryLock` 失败即跳过并记录 `skipped_concurrent`（FR-ACT-05）。
- 超时默认 30 s，按动作配置（FR-ACT-01）；结果记录状态、退出码、stdout / stderr（各截断到前 4 KB）、耗时、错误。
- 执行前先写 `action_runs`（`status=running`），完成后更新，进程中断时能看到未完成记录。
- 手动触发（FR-ACT-04）：API 要求请求体带 `confirm: true`；后端校验，不只依赖前端二次确认。
- dry-run：渲染出将要执行的命令 / URL / 消息，写入日志，不产生任何外部副作用（AC-09）。

### 11.2 本地脚本

- 脚本目录来自启动参数 `--script-dir`（默认 `<data>/scripts`）。执行前对路径做 `filepath.EvalSymlinks` 后确认仍在脚本目录内，防止 `..` 与符号链接逃逸（FR-ACT-02）。
- 不经过 shell：参数以数组传递。按扩展名选择解释器：Linux `.sh` 用 `/bin/sh`，可执行文件直接执行；Windows `.ps1` 用 `pwsh -File`（找不到时回退 `powershell.exe`），`.cmd`/`.bat` 用 `cmd /c`。
- 注入环境变量 `UPS_DEVICE_ID` 等 8 个（FR-ACT-03），以及最小的 `PATH`。
- 超时后终止整个进程组（Linux `Setpgid` + 向进程组发信号；Windows 通过 `cmd.Cancel` 调用 `taskkill /T /F`）。
- 在线编辑脚本（P1）由启动参数 `--allow-script-edit` 控制，默认关闭。

### 11.3 SSH 与受控主机

- 认证：密码、私钥（可带口令）。
- 主机指纹（FR-HOST-02）：`POST /hosts/{id}/hostkey/fetch` 连接后只读取主机公钥并返回 SHA256 指纹，**不执行认证与命令**；用户确认后 `POST /hosts/{id}/hostkey/confirm` 保存。之后 `HostKeyCallback` 严格比对，不一致直接失败并提示可能的中间人攻击。未确认指纹的主机不能执行命令。
- sudo（**决策**）：开启"使用 sudo"时命令前加 `sudo -n`，要求目标机为该命令配置 NOPASSWD；不在网络上传输 sudo 密码。
- 关机命令的结果语义：命令发出后连接被对端关闭（`EOF` / 连接重置，或无退出状态）记为 `sent` 而不是失败，最终是否关闭由"等待离线"确认。
- 测试连接 / 测试命令（FR-HOST-04）：测试命令固定为 `echo ups-warden-ok`，不允许用户输入。

### 11.4 HTTP 请求动作

- 可配置方法、URL、Header、Body 模板（JSON 转义渲染）、期望状态码（默认 2xx）。
- 安全护栏（D8）：目标主机不得与任何已配置 UPS 数据源的主机相同，防止通过动作访问设备 WebUI（延伸 SEC-03）。保存动作时与执行动作时都检查（数据源可能在动作保存后才添加）；比较前对主机名做 DNS 解析，按 IP 比对。护栏由系统设置 `http_action_guard_ups_hosts` 控制，默认开启；关闭时 API 要求确认字段，并写审计日志。

### 11.5 在线检测（FR-HOST-03）

- TCP 探测：连接指定端口，成功即在线。
- ICMP：`x/net/icmp`，Linux 优先使用非特权 ICMP（`udp4`，依赖 `net.ipv4.ping_group_range`），失败时尝试特权 raw socket；Windows 需要管理员权限。权限不足时在测试连接中给出明确提示，建议改用 TCP 探测。Docker 部署需要 `NET_RAW` 能力（见第 18 节）。

---

## 12. 通知（`notify`）

### 12.1 渠道接口

```go
type Channel interface {
    Send(ctx context.Context, msg Message) error // 单次发送，不含重试
}
type Message struct {
    Severity string
    Title    string
    Body     string         // 已按渠道格式渲染
    Vars     map[string]any // Webhook Body 模板使用
}
```

新增渠道只需实现该接口并在注册表中登记，规则与模板不感知渠道类型（FR-NTF-01）。

### 12.2 Webhook（FR-NTF-02）

- URL、方法（POST / PUT）、自定义 Header、Body 模板（默认模板为包含事件、设备、关键字段的 JSON）。
- 可选签名：`X-UPS-Warden-Timestamp: <unix秒>`、`X-UPS-Warden-Signature: sha256=<hex>`，签名内容为 `timestamp + "." + body`，接收方可用于防重放。
- 2xx 视为成功；默认不跟随重定向。

### 12.3 Telegram Bot（FR-NTF-09）

- `POST {api_base}/bot{token}/sendMessage`，`api_base` 默认 `https://api.telegram.org`，可配置 HTTP 代理。
- 多个 Chat ID 分别发送，逐个记录结果；格式 `text`（不设 `parse_mode`）或 `HTML`（变量值做 HTML 转义）。
- 返回 429 时按 `retry_after` 等待。
- **Token 位于 URL 中**：所有错误在返回前用脱敏函数替换 URL，`*url.Error` 不得原样进入日志或 API（SEC-16）。

### 12.4 模板与发送

- 模板语法 `{{path}}`，路径如 `device.name`、`event.type`、`battery_capacity_pct`、`input_voltage[1]`、`on_battery_elapsed`、`runtime_estimate`、`time`。渲染时按输出格式转义（JSON / HTML / 纯文本）；时长字段另提供 `_text` 变体（如 `{{on_battery_elapsed_text}}` → "12 分 30 秒"），时间按系统时区格式化。
- 未知变量或语法错误 → 回退到该事件的默认模板并记录日志（FR-NTF-04）；非 `good` 字段渲染为 `—`。
- 每个渠道一个发送队列（有界 256）与一个协程，慢渠道不阻塞其他渠道和规则引擎。
- 重试：失败后按 2 s、4 s、8 s 退避，最多 3 次（FR-NTF-05）；最终失败写通知日志并经 SSE 推送提示。
- 通知日志：渠道、事件、内容摘要（前 200 字符）、结果、耗时、错误（脱敏）（FR-NTF-08）。
- "发送测试消息"直接走同一发送路径（FR-NTF-03）。

---

## 13. 关机计划编排（`plan`）

### 13.1 模型

```json
{
  "name": "机房关机",
  "countdown_s": 60,
  "on_mains_restored": "cancel_pending",
  "on_restart": "resume",
  "notify_channel_ids": [1],
  "stages": [
    { "delay_s": 0, "step_timeout_s": 30, "offline_wait_s": 120,
      "steps": [ { "action_id": 11 }, { "action_id": 12 } ] },
    { "delay_s": 10, "step_timeout_s": 30, "offline_wait_s": 300,
      "steps": [ { "action_id": 13 } ] },
    { "self_shutdown": true, "steps": [ { "action_id": 20 } ] }
  ]
}
```

- 步骤引用 SSH / 本地脚本 / HTTP 动作；SSH 动作关联的受控主机用于"等待离线"。
- `on_mains_restored`：`cancel_pending`（默认，FR-PLAN-05）或 `continue`；`on_restart`：`resume`（默认）或 `abort`（FR-IDEM-04）。
- `self_shutdown` 阶段必须是最后一个阶段（保存时校验，FR-PLAN-07）。

### 13.2 运行状态机

```text
countdown ──(倒计时结束)──▶ running(stage i) ──(全部阶段结束)──▶ completed / completed_with_errors
    │                         │
    ├─(用户取消 / 市电恢复)──▶ cancelled
    │                         ├─(市电恢复 + cancel_pending)──▶ 未开始的阶段 cancelled，运行结束为 cancelled
    │                         └─(重启 + abort)──▶ aborted
```

步骤状态：`waiting / running / sent / offline / failed / cancelled / unknown`（`unknown` 表示重启前已发出、结果未知）。

阶段执行：等待 `delay_s` → 并行执行本阶段全部步骤（每步 `step_timeout_s`）→ 对关联主机做在线检测直到离线或 `offline_wait_s` 超时 → 进入下一阶段。任何一步失败或超时只记录并发送通知，不阻塞后续阶段（FR-PLAN-03、SEC-05）。

### 13.3 幂等与持久化（FR-IDEM-01~03）

- `plan_runs` 记录触发来源、设备、停电周期 ID、当前阶段、计划配置快照（运行开始时冻结，运行中修改计划不影响本次运行）。
- 非 dry-run 运行在 `(plan_id, outage_cycle_id)` 上有唯一约束：同一停电周期内同一计划只执行一次；周期结束后可再次触发。手动触发且不在停电周期内的运行不占用该约束（`outage_cycle_id` 为空）。
- 同一计划任何时刻最多一个活动运行（含 dry-run），新的触发被跳过并记录。
- **步骤 write-ahead**：执行外部命令前先提交 `status=running` 并 fsync（SQLite `synchronous=FULL`），之后才发出命令。重启时 `running` 状态的步骤记为 `unknown`，**不会重新执行**。宁可少执行一次，不重复发送关机命令。
- 自关机阶段：执行前先把运行状态写为 `completed` 并在步骤上标记 `sent`，然后再执行本机关机动作，避免本机重启后继续该计划。

### 13.4 倒计时与取消（FR-PLAN-04/05）

- 倒计时截止时刻 `countdown_until` 持久化；UI 通过 SSE 获得剩余秒数。
- 取消来源：API `POST /plan-runs/{id}/cancel`（需要确认）、触发设备去抖后的 `mains_restored`。
- 已发出的关机命令不可撤回，日志如实记录"已发送"。

### 13.5 重启恢复（FR-IDEM-04、AC-07）

启动时加载 `countdown` / `running` 状态的运行：

- 触发设备仍处于同一停电周期：`resume` → 倒计时按剩余时间继续，或从第一个未开始的步骤继续；`abort` → 标记 `aborted`。两种情况都发送通知。
- 停电周期已结束：标记 `cancelled`，原因"重启时市电已恢复"。

### 13.6 进度推送

每次状态变化写库后通过 bus 发布 `plan_progress`（运行 ID、阶段、每台主机的步骤状态），满足 FR-PLAN-06。

---

## 14. 存储设计（`store`）

### 14.1 SQLite 配置

| PRAGMA / 设置 | 值 | 原因 |
|---|---|---|
| `journal_mode` | WAL | FR-DATA-01；读写并发 |
| `synchronous` | FULL | 停电场景中断电是常态，计划步骤与停电周期必须真正落盘；历史写入已批量化，fsync 次数可控 |
| `busy_timeout` | 5000 | 写锁竞争时等待而不是立即失败 |
| `foreign_keys` | ON | 保证关联完整性 |
| 连接池 | 写连接 1 个（所有写操作串行），读连接池 4 个 | SQLite 单写者模型；避免 `SQLITE_BUSY` |

时间统一存 UTC Unix 毫秒整数（FR-DATA-06）；API 输出 RFC 3339 UTC，显示时区由前端按系统设置处理。

### 14.2 表清单

| 表 | 主要列 | 说明 |
|---|---|---|
| `schema_migrations` | version, applied_at | 迁移记录 |
| `settings` | key, value(JSON) | 保留期、默认阈值、时区、语言、HTTP 动作护栏开关等 |
| `users` | id, username, password_hash, role, created_at, last_login_at | P0 全部为 admin；`role` 列为 P1 预留 |
| `sessions` | token_hash, user_id, csrf_token, created_at, expires_at, last_seen_at, ip, user_agent | 服务端会话 |
| `devices` | id, name, note, location, tags(JSON), enabled, config(JSON) | `config` 含阈值、去抖时长、失联策略、并发限制 |
| `sources` | id, device_id, protocol, host, port, params(JSON), secret(BLOB), profile_key, profile_version, poll_ms, timeout_ms, retries, priority, enabled | `secret` 为加密的 community / pass |
| `profiles` | key, name, protocol, version, content(TEXT), created_at, updated_at | 仅自定义 Profile |
| `device_state` | device_id, power_state, power_state_raw, pending_since, on_battery_since, outage_cycle_id, comm_state, last_ok_at, alert_flags(JSON), updated_at | 重启恢复 |
| `outage_cycles` | id, device_id, started_at, ended_at | 停电周期 |
| `events` | id, device_id, type, severity, started_at, cleared_at, active, summary(JSON), acked_at, acked_by | 事件与活动告警；索引 `(device_id, started_at)`、`(active)` |
| `history_sample` / `history_1m` | device_id, ts / bucket, data(JSON) | 只含统一字段，见第 9 节 |
| `rules` | id, name, enabled, dry_run, scope(JSON), trigger(JSON), actions(JSON), recovery_actions(JSON), cooldown_s | |
| `rule_state` | rule_id, device_id, fired, fired_at, since(JSON) | 规则实例状态 |
| `rule_logs` | id, rule_id, device_id, at, kind, reason, values(JSON), action_run_ids(JSON) | |
| `channels` | id, name, type, config(JSON), secret(BLOB), enabled | |
| `notify_templates` | event_type, format, body | 覆盖默认模板 |
| `notify_logs` | id, channel_id, event_id, at, summary, ok, duration_ms, error | |
| `hosts` | id, name, address, kind, conn(JSON), secret(BLOB), hostkey_fingerprint, online_check(JSON) | |
| `actions` | id, name, type, config(JSON), secret(BLOB), timeout_s | 类型：notify / ssh / script / http / plan |
| `action_runs` | id, action_id, trigger(JSON), dry_run, status, started_at, ended_at, exit_code, stdout, stderr, error | |
| `plans` | id, name, config(JSON) | |
| `plan_runs` | id, plan_id, device_id, outage_cycle_id, dry_run, status, trigger(JSON), countdown_until, current_stage, plan_snapshot(JSON), started_at, ended_at | 唯一索引 `(plan_id, outage_cycle_id) WHERE dry_run = 0 AND outage_cycle_id IS NOT NULL` |
| `plan_run_steps` | run_id, stage, step, action_id, host_id, status, started_at, ended_at, result(JSON) | |
| `audit_logs` | id, at, user_id, ip, kind, target, diff(JSON), result | |

迁移：`migrations/NNNN_*.sql` 通过 `embed` 打包，启动时在事务中按序执行未应用的脚本；只前进、不回滚。数据库文件由数据目录备份即可（备份 / 恢复功能为 P1）。

---

## 15. API 设计（`api`）

### 15.1 约定

- 前缀 `/api/v1`，JSON，UTF-8；时间 RFC 3339 UTC；ID 为整数（Profile 用字符串 `key`）。
- 错误结构统一为 `{"code":"device_not_found","message":"设备不存在","details":{…}}`（FR-API-08）；`code` 为稳定的机器可读标识，HTTP 状态码按语义使用（400 / 401 / 403 / 404 / 409 / 422 / 429 / 500）。
- 列表接口使用游标分页：`?limit=50&before=<id>`，响应 `{"items":[…],"next_before":123}`。
- 敏感字段只写不读：响应中以 `"password_set": true` 表示已设置；更新时字段缺省或为空字符串表示不修改，清除用 `"clear_secrets": ["password"]`（SEC-14）。
- 危险操作（删除、手动执行动作、执行关机计划、关闭 dry-run、关闭 HTTP 动作护栏）要求请求体带确认字段，后端校验：执行关机计划要求 `confirm_name` 与计划名称一致（FR-UI-06）。
- 业务查询接口（快照、历史、事件、规则条件）只出现统一字段名；原始数据仅出现在数据源诊断与连接测试接口中。

### 15.2 端点

下表保留完整目标端点；M4 已实现范围与实际请求/响应以[监控 API 与 SSE 契约](../../../../docs/reference/monitoring-api.md)为准。通知、规则、动作、受控主机、关机计划、系统设置及 P1 ack 均未实现。

| 分组 | 端点 |
|---|---|
| 健康检查 | `GET /healthz`（无需登录） |
| 首次启动 | `GET /setup/status`；`POST /setup`（仅当没有任何用户时可用） |
| 认证 | `POST /auth/login`；`POST /auth/logout`；`GET /auth/session`（当前用户与 CSRF token） |
| 用户 | `GET/POST /users`；`PATCH/DELETE /users/{id}`；`PUT /users/{id}/password` |
| 设备 | `GET/POST /devices`；`GET/PATCH/DELETE /devices/{id}`；`PUT /devices/{id}/enabled` |
| 数据源 | `GET/POST /devices/{id}/sources`；`PATCH/DELETE /sources/{id}`；`POST /sources/test`（未保存配置的连接测试）；`POST /sources/{id}/test`；`GET /sources/{id}/raw`（最近一次原始数据与 Trace） |
| Profile | `GET /profiles`；`GET /profiles/{key}`；`POST /profiles`（新建 / 导入）；`POST /profiles/{key}/copy`；`PUT /profiles/{key}`；`DELETE /profiles/{key}`；`GET /profiles/{key}/export`；`POST /profiles/validate` |
| 快照 | `GET /snapshots`（全部设备最新状态）；`GET /devices/{id}/snapshot` |
| 历史 | `GET /devices/{id}/history?fields=…&from=…&to=…&resolution=auto` |
| 事件与告警 | `GET /events?device_id=&type=&severity=&from=&to=`；`GET /alerts`；`POST /alerts/{id}/ack`（P1） |
| 规则 | `GET/POST /rules`；`GET/PUT/DELETE /rules/{id}`；`GET /rules/templates`；`GET /rules/{id}/logs` |
| 通知 | `GET/POST /channels`；`GET/PUT/DELETE /channels/{id}`；`POST /channels/{id}/test`；`GET/PUT /notify-templates`；`GET /notify-logs` |
| 受控主机 | `GET/POST /hosts`；`GET/PUT/DELETE /hosts/{id}`；`POST /hosts/{id}/hostkey/fetch`；`POST /hosts/{id}/hostkey/confirm`；`POST /hosts/{id}/test-connection`；`POST /hosts/{id}/test-command`；`POST /hosts/{id}/check-online` |
| 动作 | `GET/POST /actions`；`GET/PUT/DELETE /actions/{id}`；`POST /actions/{id}/run`；`GET /action-runs`；`GET /scripts`（脚本目录文件列表） |
| 关机计划 | `GET/POST /plans`；`GET/PUT/DELETE /plans/{id}`；`POST /plans/{id}/run`；`GET /plan-runs`；`GET /plan-runs/{id}`；`POST /plan-runs/{id}/cancel` |
| 审计 | `GET /audit` |
| 设置 | `GET/PUT /settings` |
| 实时流 | `GET /stream` |

### 15.3 SSE（FR-API-03、FR-UI-01）

- M4 连接建立先发送全量 `sync`（全部运行设备 `DeviceState`、活动告警，`plan_runs` 固定空数组），随后推送 `device_state`、`event`、`alert`；配置变更后再次 sync。`plan_progress`、`notify_failed` 留待 M7/M5，当前不产生。重连重新全量同步，不做 `Last-Event-ID` 回放。
- 每 15 s 注释心跳重新认证，响应注销、改密撤销和绝对期限到期，不永久持有连接建立时的授权。
- 每订阅者 256 缓冲，溢出独立失效信号通知立即断开，不排空积压更新；前端重连并获取全量 sync。
- SSE 不设置整个长连接的全局写超时，但每帧用 `http.ResponseController` 设置 5 秒写期限，避免慢客户端无限占用。

### 15.4 WebUI 托管

`internal/webui` 通过 `//go:embed all:dist` 打包前端构建产物；非 `/api`、`/healthz` 及其子路径的 GET/HEAD 请求先匹配静态文件，未命中回退 `index.html`（SPA fallback），拒绝隐藏文件、遍历和目录列表。`backend:build` 依赖 `webui:build`，由 `scripts/build-webui.mjs` 安全复制 `frontend/dist` 到 embed 目录（除占位文件外不入库）；未构建时返回 503 提示页。当前仅托管 React 占位页，开发可用 Vite 同源代理 `/api`。

---

## 16. 安全设计

### 16.1 认证与会话（SEC-10~12）

- 除 `/api/v1/setup/status`、`POST /api/v1/setup` 与 `POST /api/v1/auth/login` 外，所有 API（含未知路径和 SSE）都经过管理员认证。`/healthz` 与静态资源无需会话；前端检测未登录并跳转登录页仍是业务 UI 目标，当前未实现。
- 首次启动：数据库中没有用户时允许 `POST /setup` 创建第一个管理员。为防止局域网内他人抢先完成初始化，主进程启动仅向 stderr 输出一次 **setup token**（D3，M4 实施细化为不进入轮转日志），不写数据库或 HTTP 响应。token 只存内存，重启重新生成，首个管理员创建成功后失效；前端向导页面尚未实现。
- 密码：argon2id（m=64 MiB, t=3, p=2，参数随哈希一起存储）；最小长度 8。
- 会话：32 字节随机 token，Cookie `HttpOnly; SameSite=Strict; Path=/`，直接 HTTPS 下 `Secure`；数据库只存 SHA-256 哈希。Cookie 固定 7 天绝对期限、不滚动续发，服务端另执行空闲 24 小时失效；自己改密保留当前会话并撤销其他会话，修改他人密码撤销其全部会话。
- CSRF：认证后的非 GET/HEAD 请求必须携带会话 `X-CSRF-Token`；所有写请求（含初始化/登录）若有 Origin，必须与请求 scheme/Host 一致。不启用 CORS，不信任 `X-Forwarded-*`。
- 登录限速：按"用户名"与"来源 IP"分别计数，15 分钟内失败 5 次锁定 15 分钟；响应统一为"用户名或密码错误"，锁定期间返回 429。

### 16.2 角色

P0 只有管理员（需求 Q2 默认假设：多个管理员账号，角色 P1），当前 API 统一检查 admin。operator / viewer 与路由级角色声明留待 P1，不将规划中的扩展能力描述为已实现。

### 16.3 敏感信息加密（SEC-13）

- 主密钥：优先读环境变量 `UPS_WARDEN_MASTER_KEY`（Base64 编码的 32 字节）；否则读 `<data>/master.key`，不存在时生成并以 0600 权限写入。Windows 上文件权限位不生效，依赖数据目录 ACL，启动时在文档中提示。
- 密文格式：`版本(1B) | nonce(12B) | ciphertext+tag`；AAD 为"表名.列名"，防止密文在不同用途之间被挪用。
- 主密钥丢失则无法解密已有凭据：启动时检测到解密失败给出明确错误，并提示重新填写凭据（不静默清空）。

### 16.4 脱敏（SEC-14/16）

- `secret.String` 类型：`String()`、`LogValue()`、`MarshalJSON()` 都输出 `******`，只有显式 `Reveal()` 才能取得明文，并且只在驱动 / 渠道 / SSH 构造时调用。
- slog `ReplaceAttr` 对已知敏感键（`password`、`pass`、`community`、`token`、`secret`、`private_key`、`authorization`）兜底脱敏。
- 外部错误（HTTP、SSH）在写日志或返回 API 前经过 URL / 凭据清洗（Telegram token 在 URL 中，见 12.3）。

### 16.5 审计（SEC-15）

登录 / 登出、配置增删改（字段级 diff，敏感字段显示为"已修改"而非值）、手动触发、动作与计划执行结果都写 `audit_logs`。配置修改与审计记录在同一事务中写入。

---

## 17. 可靠性与可观测性

| 项 | 设计 |
|---|---|
| panic 隔离 | 所有协程经 `app.Go` 启动，`recover` 后记录堆栈并按策略重启（NFR-04） |
| 优雅关闭 | 见 3.4；等待进行中动作最长 10 s |
| 数据目录锁 | 见 3.4 第 2 步 |
| 日志 | `log/slog`，默认文本格式输出到 stderr；`--log-file` 指定文件时用 lumberjack 滚动（默认 20 MB × 5 份）；`--log-format=json` 可选（NFR-07） |
| 时间 | 时长判定使用单调时钟；持久化与 API 使用 UTC；系统时间跳变不影响去抖与倒计时 |
| 自身健康 | `/healthz` 只表示进程存活；P1 `/metrics` 提供采集延迟、失败计数、队列长度、历史丢弃计数（FR-API-06） |

---

## 18. 部署与构建

本节表格描述目标方案，当前操作以[开发指南](../../../../docs/guides/development.md)为准。M0 已实现数据目录、日志文件及后端 check/build tasks；M4 已实现前端产物安全复制、embed 静态 SPA 与本机单二进制构建。完整发布 tasks、Docker 和 systemd 配置仍未实现，前端仍无业务页面。

| 项 | 方案 |
|---|---|
| 启动参数（FR-DEP-05） | `--listen`（`UPS_WARDEN_LISTEN`，默认 `127.0.0.1:8080`）、`--data-dir`（`UPS_WARDEN_DATA_DIR`，默认 `./data`）、`--log-level`、`--log-file`、`--log-format`、`--script-dir`、`--allow-script-edit`（P1）；`--tls-cert` / `--tls-key`（P1） |
| 构建 | `CGO_ENABLED=0`，`-trimpath`，`-ldflags "-s -w -X main.version=…"`；目标 linux/amd64、linux/arm64、windows/amd64（FR-DEP-02）。构建入口用 `mise.toml` 中的 tasks（先构建前端并复制产物，再交叉编译），不另引入 Makefile |
| Docker（FR-DEP-03） | 多阶段：Node 阶段构建前端 → Go 阶段编译 → 运行镜像使用 `debian:stable-slim`（本地脚本需要 shell；包含 `ca-certificates`、`tzdata`）。数据卷 `/data`，监听 `0.0.0.0:8080`。compose 示例说明：ICMP 检测需要 `cap_add: [NET_RAW]`；容器内本地脚本无法关闭宿主机，宿主机关机应通过 SSH 动作完成 |
| systemd（FR-DEP-04） | 提供 unit 示例：`Restart=on-failure`、`TimeoutStopSec=20`；需要本地关机脚本时以具备权限的用户运行 |
| Windows 服务 | P1：`golang.org/x/sys/windows/svc` |

---

## 19. 测试策略

### 19.1 协议模拟器（TST-01/02，`internal/sim` + `cmd/upssim`）

- **物理模型**：一个设备的物理状态（市电有无、输入 / 输出电压、负载、电池容量、温度、放电秒数）由场景引擎按时间推进。
- **三协议渲染**：同一物理状态同时通过 SNMP agent、Modbus 从站、ayi9 服务端暴露，按 ET1K 的编码方式（SNMP 整数伏、Modbus ×10、ayi9 字符串和状态字）输出，天然支持"三协议归一化一致性"测试。
- **ET1K 怪癖开关**：SNMP GET 全部 `noSuchName`、GETNEXT 只返回第一个 OID、丢弃 1 字节 request-id、只接受单一配置 community（模拟器使用虚构测试值）；Modbus 只支持 FC03、范围 0..79；ayi9 不支持流水线、可配置返回错误码（含 103）。
- **场景脚本**（YAML）：市电正常 → 停电 → 放电 → 低电 → 恢复；网卡失联；5 s 闪断。
- **请求记录**：记录全部收到的请求，用于只读断言。
- 测试中以 `127.0.0.1:0` 在进程内启动；`cmd/upssim` 供开发者本地联调前端。

### 19.2 测试分层

| 层 | 内容 | 工具 |
|---|---|---|
| 单元 | BER / Modbus / ayi9 编解码、Profile 校验、归一化流水线、读计划合并、模板渲染、条件求值、加密、脱敏 | 表驱动测试；编解码与 Profile 解析加 fuzz |
| 时间逻辑 | 去抖、失联策略、持续条件、冷却、倒计时、计划阶段推进 | `testing/synctest` 虚拟时钟 |
| 回归 fixture | ET1K 实测数据（同一时刻三协议快照、停电期间连续采样）→ 归一化结果与期望值比对（TST-03、FR-NORM-10） | `testdata/et1k/` |
| 安全 | 只读白名单 fuzz、模拟器请求记录断言、guard 字节校验（TST-04） | 见 5.2 |
| 架构 | 包依赖规则（3.2） | `internal/archtest` |
| 集成 | 模拟器 + collector + state + rule + action，通知目标用 `httptest` 假服务器，SSH 目标用测试内的 `x/crypto/ssh` 服务端 | 进程内启动 |
| API | handler + 内存 / 临时文件 SQLite | `httptest` |

竞态检测：`go test -race` 需要 cgo，在 Linux CI 中执行；Windows 本地开发只执行普通测试。

### 19.3 验收场景映射

| AC | 自动化测试 |
|---|---|
| AC-01 | fixture 回归 + 模拟器三协议一致性测试（与真实面板比对由项目方手动完成） |
| AC-02 | 模拟器标准 RFC 1628 模式 + `rfc1628-generic` |
| AC-03 | 模拟器自定义寄存器表 + 测试内导入的自定义 Profile |
| AC-04 | 场景：5 s 闪断，断言无 `mains_lost`、无规则日志 |
| AC-05 | 场景：持续停电，断言确认后 2 s 内假 Webhook 收到请求；计划按阶段执行（假 SSH 服务端） |
| AC-06 | 场景：倒计时期间恢复，断言计划 `cancelled` 且发出通知 |
| AC-07 | 计划执行中关闭并重建 app（同一数据目录），断言已执行步骤不重复 |
| AC-08 | 场景：停电中模拟器停止响应，断言 `comm_lost`、计时继续、时长条件触发 |
| AC-09 | dry-run 规则，断言假 Webhook / SSH / 脚本均无调用 |
| AC-10 | 模拟器返回 103，断言连接测试错误码与提示 |
| AC-11 | 模拟器请求记录断言；真实抓包由项目方手动执行 |

### 19.4 真实设备

自动化测试不依赖真实设备。真机只读冒烟由项目方在本地手动执行，设备地址与凭据通过本地环境变量提供，不写入 Git 跟踪文件。

---

## 20. 性能估算（NFR-01~03）

| 项 | 估算 |
|---|---|
| 协程 | 每数据源 1 个 + 每设备 1 个 actor；100 台设备约 200 个协程，可忽略 |
| CPU | 3 s 轮询下每台设备约 0.33 次 / s 采集；归一化与判定为微秒级；主要开销是 SQLite 批量写入（≤ 1 次事务 / s） |
| 内存 | 每设备：最近 RawData + Trace + 估算窗口 + 状态，约 50–100 KB；32 台设备 < 10 MB 业务数据，满足 128 MB 目标 |
| 时效 | 去抖确认后事件同步写库（单行，毫秒级）→ rule 立即评估 → 通知队列；预计 < 500 ms 发出，满足 2 s 目标；快照经 bus 推送 < 100 ms |

M8 用模拟器做 32 台与 100 台设备的压测验证。

---

## 21. 评审决策与风险

### 21.1 已确认的决策（2026-10-11 评审）

| # | 决策 | 结论 | 说明 |
|---|---|---|---|
| D1 | SNMP / Modbus 客户端 | 自研最小只读子集 | 增加约 3–4 人日；换来只读结构性保证、request-id 可控、模拟器复用（2.1、5.3、5.4） |
| D2 | 规则 OR 分组 | 提前到 P0，两层 OR / AND | 满足 FR-RULE-06 的 P0 关机模板（10.1） |
| D3 | 首次启动 setup token | 启用 | 防止局域网抢先初始化，向导多一个输入项（16.1） |
| D4 | `assume_on_battery_after` 的计时起点 | 失联前最后一次成功采集时刻 | 偏向更早关机（8.5） |
| D5 | sudo 方式 | 开启 sudo 模式时使用 `sudo -n` | 要求目标机配置 NOPASSWD，不在网络上传 sudo 密码（11.3） |
| D6 | 历史数据内容 | 只存储归一化后的统一字段，不存储协议原始数据 | 协议原始数据（RawData / Trace）只在内存中为每个数据源保留最近一次，任何表都不持久化；历史明细表与聚合表只含统一字段。32 台设备约 4–5 GB 的估算即为归一化数据的量，可通过保留期设置调整（9） |
| D7 | 按相字段不带相号的语义 | 取 `good` 相的最大值 | 规则 / 模板在单相与三相机型上写法一致（4.4） |
| D8 | HTTP 动作禁止指向 UPS 数据源主机 | 启用，开关可配置 | 系统设置 `http_action_guard_ups_hosts`，默认开启；关闭需二次确认并写审计（11.4） |
| D9 | 非 on_battery 状态之间的切换 | 不去抖，立即切换 | bypass / fault 事件更及时，但可能抖动（8.2） |
| D10 | Profile 格式 | YAML（兼容 JSON） | 新增 `go.yaml.in/yaml/v3` 依赖（6.1） |

### 21.2 风险

| 风险 | 影响 | 缓解 |
|---|---|---|
| 自研 SNMP 与其他品牌 agent 的兼容性 | 非 ET1K 设备可能出现编码边界问题 | BER 解码 fuzz；模拟器覆盖标准 RFC 1628 行为；真机冒烟 |
| 旧 ET1K 原始附件缺失（已缓解） | 旧报告不能证明切换瞬间与恢复行为 | 已由 2026-10-14 新采样生成 60 组脱敏 `testdata/et1k/` fixture；原件长期保留在忽略的 `data/`，后续独立验收补足切换与恢复覆盖 |
| ayi9 错误响应的确切格式未实测 | 103 等错误码解析可能不准 | 解析兼容多种形式；真机冒烟时用错误密码验证（只读请求） |
| V2 功率量纲待校准 | 功率字段数值可能错 10 倍 | 内置 Profile 标注"待校准"；P1 校准视图 |
| ICMP 权限 | 部分部署环境无法使用 ping 检测 | 明确报错并推荐 TCP 探测；文档说明 Docker `NET_RAW` |
| Windows 下脚本进程树终止 | 超时后残留子进程 | `taskkill /T /F`；集成测试覆盖 |
| `synchronous=FULL` 写放大 | 低端存储（SD 卡）写入寿命 | 历史批量写；保留期可调 |
