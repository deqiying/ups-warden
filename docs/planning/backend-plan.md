# UPS Warden 后端开发计划

> **文档版本**：v0.3
> **日期**：2026-10-11
> **依据**：[《需求文档》v0.3](requirements.md)、[《后端实现方案》v0.2](../../.agents/notes/proposed/architecture/2026-10-11-backend-design.md)
> **范围**：后端首版（P0）从脚手架到可发布版本的开发顺序、任务拆分、交付物、完成标准与风险；P1 只列出待排期清单
> **状态**：M0、M1 已完成；M2~M8 未完成。设计决策 D1~D10 已于 2026-10-11 评审确认，第三方依赖清单已获项目方批准。2026-10-14 的市电与电池模式采样已整理为脱敏回归 fixture，原件保留在本地忽略目录。人日仍为原单人粗略估算，不将本次自动化实现耗时直接换算为后续业务里程碑工期。
> **阅读入口**：[文档导航](../README.md) · [当前开发指南](../guides/development.md)

---

## 1. 计划原则

1. **先打通安全的采集链路，再做联动**。驱动只读防护、归一化和状态判定是一切联动的前提，必须在规则与关机之前完成并有测试覆盖。
2. **每个里程碑都能运行、能验证**。每个里程碑结束时 `gofmt -l .` 无输出、`go vet ./...` 与 `go test ./...` 通过，并有可演示的结果（模拟器驱动）。
3. **模拟器先行**。自动化测试不依赖真实设备（项目 `AGENTS.md` 的设备安全要求），模拟器与驱动同期开发。
4. **尽早给前端稳定的 API**。M4 结束时监控类 API 与 SSE 冻结，前端可并行开发总览、设备详情、设备管理页面。
5. **危险能力默认关闭**。新建的关机规则默认 dry-run；关机计划在 M7 完成全部幂等与恢复测试前不在演示环境启用真实动作。

---

## 2. 里程碑总览

表中列出目标交付物。当前 M0、M1 已满足完成标准，实证见各自“实施记录”；其余里程碑不能因表结构或基础设施已创建就视为完成。

| 里程碑 | 内容 | 估算（人日） | 依赖 | 关键交付 |
|---|---|---|---|---|
| M0 | 工程基础设施 | 4 | — | 配置、日志、数据目录锁、SQLite + 迁移、加密、app 生命周期、架构测试 |
| M1 | 统一模型、Profile、归一化 | 5 | M0 | 字段目录、Profile 解析与校验、5 个内置 Profile、归一化流水线、ET1K fixture 回归 |
| M2 | 协议驱动与模拟器 | 10 | M1 | SNMP / Modbus / ayi9 只读驱动、guard、模拟器（三协议 + 场景）、只读安全测试 |
| M3 | 采集调度、状态引擎、事件、历史 | 8 | M2 | collector、power_state 去抖、放电计时、剩余时间、通讯状态、事件与告警、历史写入与聚合 |
| M4 | 认证、审计、监控类 API、SSE、WebUI 托管 | 8 | M3 | 登录与会话、设备 / 数据源 / Profile / 快照 / 历史 / 事件 API、连接测试、SSE、embed |
| M5 | 通知 | 4 | M4 | 模板渲染、Webhook、Telegram、重试、通知日志、渠道 API |
| M6 | 规则引擎、动作、受控主机 | 8 | M5 | 规则评估、dry-run、执行日志、脚本 / SSH / HTTP 动作、主机指纹与在线检测 |
| M7 | 关机计划编排 | 6 | M6 | 计划执行、倒计时、取消、市电恢复处理、幂等、重启恢复、进度推送 |
| M8 | 验收、性能、发布 | 6 | M7 | AC-01~11 自动化场景、压测、设置 API、Docker / systemd / 交叉编译 |
| | **合计** | **约 59** | | 另预留 15–20% 缓冲后约 68–71 人日，约 **14 周**（单人） |

```text
M0 ─▶ M1 ─▶ M2 ─▶ M3 ─▶ M4 ─┬─▶ M5 ─▶ M6 ─▶ M7 ─▶ M8
                            │
                            └─▶ 前端：总览 / 设备详情 / 设备管理（并行）
                    M5 起 ─▶ 前端：通知、规则、主机、计划页面（按各里程碑 API 冻结点跟进）
```

如有第二名后端开发者，可并行的切分：M2 中三个驱动互相独立；M5（通知）与 M3 后半段（历史）可并行；M6 中受控主机 / SSH 与规则引擎可并行。

---

## 3. 里程碑详情

任务编号格式 `B<里程碑>-<序号>`。每个任务都包含对应的单元测试，除非另有说明。

### M0 工程基础设施（4 人日）

| 任务 | 内容 | 对应需求 |
|---|---|---|
| B0-1 | `internal/config`：`--listen`、`--data-dir`、`--log-level`、`--log-file`、`--log-format`、`--script-dir` 及对应环境变量；保留现有 `UPS_WARDEN_LISTEN` / `UPS_WARDEN_LOG_LEVEL` 行为 | FR-DEP-05 |
| B0-2 | `internal/logging`：slog 初始化、lumberjack 文件滚动、敏感键 `ReplaceAttr` 脱敏 | NFR-07、SEC-16 |
| B0-3 | 数据目录创建与独占文件锁（Linux / Windows） | 方案 3.4 |
| B0-4 | `internal/store`：modernc SQLite 打开、PRAGMA（WAL、`synchronous=FULL`、`busy_timeout`、`foreign_keys`）、读写连接分离、embed 迁移执行器、事务辅助 | FR-DATA-01 |
| B0-5 | `0001_init.sql`：第 14.2 节全部表（后续里程碑只增量迁移） | 方案 14.2 |
| B0-6 | `internal/secret`：主密钥加载 / 生成（环境变量优先，文件 0600）、AES-256-GCM（带 AAD）、`secret.String` 掩码类型 | SEC-13/14 |
| B0-7 | `internal/app`：模块装配、`app.Go`（panic recover + 重启策略）、启动 / 关闭顺序骨架；`main.go` 改为调用 app | NFR-04/05/06 |
| B0-8 | `internal/archtest`：包依赖规则断言（先写入规则，包陆续出现后自动生效） | FR-NORM-07、NFR-09 |
| B0-9 | `mise.toml` tasks：`backend:check`（gofmt / vet / test）、`backend:build`；`.gitignore` 增加 `backend/internal/webui/dist/*`（保留占位文件） | 项目规范 |

**完成标准**：服务能启动、创建数据目录与数据库、重复启动第二个实例时报"数据目录已被占用"并退出；`/healthz` 行为不变；加密往返、错误密钥解密失败、掩码输出均有测试。

**实施记录（B0-1~B0-9 已完成）**：

- 配置、日志、目录锁、SQLite 读写池与 25 张初始表、主密钥与 AES-256-GCM、app 生命周期、包依赖测试和 mise tasks 均已落地；使用方法见[开发指南](../guides/development.md)。
- `mise run backend:check`（格式 / vet / test）、`mise run backend:build`、无缓存 `go test -count=1 -cover ./...` 均通过；Windows amd64 本机构建及 Linux amd64 / arm64 纯 Go 交叉编译通过。
- Windows 实际二进制验证：启动创建数据目录 / 数据库 / 密钥，`/healthz` 响应不变，第二实例提示“数据目录已被占用”并以 1 退出；终止进程后同目录可重新启动。测试还覆盖错误密钥拒绝已有凭据、篡改密文拒绝、掩码、跨进程锁、失败回滚和任务 panic 退避。
- 验证边界：未运行 Linux 实机测试；当前环境无 GCC 且 `CGO_ENABLED=0`，未运行 race 检测；未接触真实 UPS。HTTP 关闭上限已实现，业务模块的分阶段停机随各里程碑接入。
- M1 使用重新采集并保留在本地的 10 组市电与 50 组电池模式三协议样本，不以报告汇总值或模拟数据冒充真机 fixture；未覆盖停电瞬间及市电恢复阶段。

### M1 统一模型、Profile、归一化（5 人日）

| 任务 | 内容 | 对应需求 |
|---|---|---|
| B1-1 | `internal/model`：字段目录（5.1 全部字段 + 单位 + 枚举 + `History` 标记）、`Field` / `Quality` / 原因码、`SourceSnapshot`、`DeviceState`、事件类型与级别常量、`SchemaVersion` | FR-NORM-02~05/09 |
| B1-2 | `internal/profile`：YAML 严格解码、第 6.2 节全部校验规则、带行号的错误信息 | FR-PRF-01~03、FR-PRF-08 |
| B1-3 | 读计划生成：Modbus 地址合并 / 切块 / 范围裁剪；SNMP GET OID 列表；ayi9 计划 | FR-MB-05 |
| B1-4 | 内置 Profile：`rfc1628-generic`、`ayi9-json-generic`、`et1k-snmp`、`et1k-modbus`、`et1k-ayi9-json`（按需求附录 A、B；待校准项加注释） | FR-PRF-04、FR-SNMP-06 |
| B1-5 | `internal/normalize`：第 6.4 节流水线、相数有效性、诊断 Trace | FR-NORM-01~06、FR-AYI9-05/06/08/10 |
| B1-6 | Profile 推荐算法 | FR-DEV-05 |
| B1-7 | ET1K fixture：`testdata/et1k/` 放入同一时刻三协议原始数据与停电期间连续采样（见第 5 节前置条件 P-1）；回归测试断言三协议核心字段在缩放精度内一致 | FR-NORM-10、TST-03 |
| B1-8 | Profile 解析 fuzz 测试 | — |

**完成标准**：5 个内置 Profile 全部通过校验；fixture 回归通过（输入 / 输出电压、频率、负载%、容量%、电池电压、温度）；含写功能码或非白名单指令的 Profile 被校验拒绝。

**实施记录（B1-1~B1-8 已完成）**：

- `model` 维护 49 个基础字段、69 个实例；单源快照提供 64 个观测字段，5 个派生字段保留给状态引擎。协议原始数据只停留在 `driver` / Profile / 归一化诊断层。
- Profile 支持严格 YAML / JSON 解析、带行号与路径的语义错误、只读计划编译及身份推荐；五个 embed 内置 Profile 均在服务开放 HTTP 前加载校验。未知键、重复键、alias / null、写功能码与非 QAD / RTD 指令均被拒绝。
- 归一化覆盖类型解码、缩放与单位、哨兵与范围、枚举、状态位、相数及字段质量；非法 Profile 整体失败，单字段错误不污染其他观测。当前契约见 [Profile 与归一化](../guides/profiles.md)。
- `cmd/fixture-import` 只读本地采样并拒绝覆盖输出；已生成 60 组三协议脱敏 fixture，核心电气字段及连续放电计时回归通过。ET1K 输出电压使用实测一致的 reg28；不把 W 当 VA，功率待校准项继续注释。
- `mise run backend:check`、`mise run backend:build`、无缓存全量测试通过；Profile 解析和 AYI9 归一化均完成短时 fuzz。fixture 不依赖本地原件或真实设备。
- 边界：`driver` 当前仅有类型和接口，没有联网实现；读计划校验不能代替 M2 发送前字节 guard。未运行 Linux 实机与 race 验证；未覆盖停电瞬间、市电恢复或错误密码真机响应。后续真实设备验收须限定授权范围。

### M2 协议驱动与模拟器（10 人日）

| 任务 | 内容 | 对应需求 |
|---|---|---|
| B2-1 | `driver` 公共部分：`Driver` 接口、`RawData`、`ReadPlan`、`DeviceInfo`、guard 连接包装与三种协议的字节校验器 | SEC-01、方案 5.2 |
| B2-2 | `driver/snmp/ber`：BER 编解码（只含 GET / GETNEXT / GETBULK / Response PDU）、4 字节 request-id 生成 | FR-SNMP-01/04/09 |
| B2-3 | `driver/snmp`：GETBULK 续取、GETNEXT walk、GET 分批与 v1 `noSuchName` 剔除重试、策略回退、异常值、占位符 | FR-SNMP-02/03/05/07/08 |
| B2-4 | `driver/modbus`：MBAP 帧、FC03 / FC04、事务 ID 校验、异常响应、长连接重连 | FR-MB-01~03/07 |
| B2-5 | `driver/ayi9`：长连接串行请求、session、pass、`json.Decoder` 响应读取、超时重建连接、QAD / RTD 解析、错误码（含 103）、uid 识别 | FR-AYI9-01~11 |
| B2-6 | `internal/sim`：物理模型 + 场景引擎（YAML 场景：正常、停电、放电、低电、恢复、失联、5 s 闪断） | TST-01/02 |
| B2-7 | 模拟器 SNMP agent（复用 ber）：标准 RFC 1628 模式与 ET1K 怪癖模式 | TST-01、AC-02 |
| B2-8 | 模拟器 Modbus 从站（可加载自定义寄存器表）与 ayi9 服务端（可配置错误码、不支持流水线） | TST-01、AC-03、AC-10 |
| B2-9 | 模拟器请求记录 + `cmd/upssim` 命令行入口 | TST-04 |
| B2-10 | 只读安全测试：三种驱动的 fuzz（随机 ReadPlan → 记录型假连接 → guard 校验）；guard 对构造的写请求字节必须拒绝；架构测试断言驱动包不直接调用 `net.Dial*` | SEC-01、TST-04 |
| B2-11 | 真机只读冒烟脚本（`go test` 外的手动工具，设备地址从本地环境变量读取，只调用 `Probe`） | 方案 19.4 |

**完成标准**：三种驱动对模拟器（含 ET1K 怪癖模式）采集成功，原始数据经 M1 归一化后与模拟器物理状态一致；ayi9 错误密码返回 `ErrPasswordWrong`；安全测试全部通过。项目方用 B2-11 在 ET1K 上完成一次只读冒烟（见检查点 CP-1）。

### M3 采集调度、状态引擎、事件、历史（8 人日）

| 任务 | 内容 | 对应需求 |
|---|---|---|
| B3-1 | `internal/collector`：supervisor（按配置启停 worker、`Reload`）、worker（抖动、轮询、超时、指数退避 ≤ 30 s）、同 IP 信号量、最近 RawData / Trace 缓存、数据源运行状态 | FR-DEV-06~08 |
| B3-2 | 连接测试服务：临时驱动 → Probe → 归一化 → 推荐，受信号量约束 | FR-DEV-04/05 |
| B3-3 | `internal/state`：每设备 actor、单源合并、供电原始判定（`power_hints` + 默认顺序）、去抖状态机 | FR-STA-01~04 |
| B3-4 | 放电计时与停电周期（持久化、重启恢复） | FR-RT-01、FR-IDEM-03 |
| B3-5 | 剩余时间：原生优先、滑动窗口线性回归估算、"估算中"状态 | FR-RT-02/03 |
| B3-6 | 通讯状态与三种失联策略 | FR-COM-01~03 |
| B3-7 | 事件判定器（P0 事件类型）、回差、活动告警、事件持久化、启动不误报 | 6.1、FR-EVT-01~03/05 |
| B3-8 | `internal/bus`：有损扇出发布订阅 | 方案 3.3 |
| B3-9 | `internal/history`：明细样本表 `history_sample` 批量写入（只写归一化统一字段，D6）、1 分钟聚合（幂等重算）、分批清理、查询与降采样；测试断言任何表都不含协议原始数据 | FR-DATA-02~04、FR-NORM-08 |
| B3-10 | 集成测试：模拟器 → collector → state，覆盖 AC-04（闪断）、AC-08（失联 hold）、重启不误报 | AC-04、AC-08、FR-EVT-05 |

时间相关测试统一使用 `testing/synctest`。

**完成标准**：模拟器场景下 `mains_lost` / `mains_restored` / `battery_low` / `comm_lost` 时序正确；5 s 闪断无事件；重启后放电计时延续、活动告警不重复；历史数据能按 auto 分辨率查询。

### M4 认证、审计、监控类 API、SSE、WebUI 托管（8 人日）

| 任务 | 内容 | 对应需求 |
|---|---|---|
| B4-1 | `internal/api` 基础：路由注册、统一错误结构、JSON 编解码与校验辅助、游标分页、敏感字段读写约定 | FR-API-01/08、SEC-14 |
| B4-2 | `internal/auth`：argon2id、首次启动（setup token，D3：仅存内存、重启重新生成、首个管理员创建后失效）、登录 / 登出、服务端会话、CSRF、登录限速与锁定 | SEC-10~12、首次启动向导 |
| B4-3 | 用户管理 API（多个管理员） | 需求 Q2 |
| B4-4 | `internal/audit`：字段级 diff、敏感字段脱敏、与配置修改同事务写入 | SEC-15 |
| B4-5 | 设备、数据源 CRUD（含加密凭据、变更后 `collector.Reload`）、连接测试 API、原始数据查看 API | FR-DEV-01~05/09 |
| B4-6 | Profile API：列表、查看、新建 / 导入、复制、修改、删除（被引用时拒绝）、导出、校验 | FR-PRF-05 |
| B4-7 | 快照、历史、事件、活动告警查询 API | FR-API-02、FR-EVT-02 |
| B4-8 | SSE：全量 sync + 增量推送 + 心跳 + 慢订阅者断开 | FR-API-03、FR-UI-01 |
| B4-9 | `internal/webui`：embed + SPA fallback；mise task 把 `frontend/dist` 复制到 embed 目录 | FR-DEP-01 |
| B4-10 | API 测试：认证拦截、CSRF、限速、凭据不回显、审计记录 | SEC-10~16 |

**完成标准**：除通知 / 规则 / 动作 / 计划外的 P0 API 可用并通过测试；**冻结监控类 API 与 SSE 事件格式**（检查点 CP-2），前端开始联调。

### M5 通知（4 人日）

| 任务 | 内容 | 对应需求 |
|---|---|---|
| B5-1 | 模板渲染器：`{{path}}` 变量、JSON / HTML / 纯文本转义、`_text` 时长变体、渲染失败回退默认模板；各事件默认模板 | FR-NTF-04 |
| B5-2 | 渠道接口、注册表、每渠道发送队列、指数退避重试、通知日志 | FR-NTF-01/05/08 |
| B5-3 | Webhook 渠道：方法、Header、Body 模板、HMAC-SHA256 签名 | FR-NTF-02 |
| B5-4 | Telegram 渠道：多 Chat ID、API 地址、HTTP 代理、HTML 模式、429 处理、**错误中 Token 脱敏** | FR-NTF-09、SEC-16 |
| B5-5 | 渠道 CRUD、测试消息、模板管理、通知日志查询 API；最终失败经 SSE 推送 | FR-NTF-03 |

**完成标准**：用 `httptest` 假服务器验证两种渠道的请求内容、签名、重试次数；任何错误路径的日志与 API 响应中均不含 Token。

### M6 规则引擎、动作、受控主机（8 人日）

| 任务 | 内容 | 对应需求 |
|---|---|---|
| B6-1 | `internal/rule`：作用范围展开、事件触发、两层 OR / AND 条件（D2）、`for_s`、边沿触发、冷却、恢复动作、规则实例状态持久化 | FR-RULE-01~05 |
| B6-2 | 字段质量处理与执行日志（原因变化才记录）、`runtime_estimate` 保存警告 | FR-RULE-09、FR-RT-04、5.2 |
| B6-3 | 内置规则模板 | FR-RULE-06 |
| B6-4 | `internal/action`：执行框架（互斥、超时、输出截断、write-ahead 记录、dry-run、手动触发确认） | FR-ACT-01/04/05、FR-RULE-07 |
| B6-5 | 通知动作、HTTP 动作；UPS 数据源主机护栏（D8：保存与执行时均检查、按解析后的 IP 比对，受系统设置开关控制，默认开启） | 9.2 |
| B6-6 | 本地脚本动作：脚本目录约束、解释器选择、环境变量注入、进程树终止（Linux / Windows） | FR-ACT-02/03 |
| B6-7 | `internal/host`：SSH（密码 / 私钥）、主机指纹获取与确认、严格校验、`sudo -n`、关机命令断连语义 | FR-HOST-01/02/05（自定义命令） |
| B6-8 | 在线检测：TCP 探测、ICMP（非特权优先）、权限不足提示；测试连接、测试命令 | FR-HOST-03/04 |
| B6-9 | 规则、动作、主机、脚本列表、执行记录 API | FR-API-02 |
| B6-10 | 集成测试：AC-05 通知部分（确认后 2 s 内到达假 Webhook）、AC-09（dry-run 无副作用）；测试内 SSH 服务端验证指纹不匹配时拒绝执行 | AC-05、AC-09 |

**完成标准**：停电场景下规则按预期触发通知；dry-run 规则只产生日志；同一动作并发触发被跳过并记录；SSH 指纹不匹配时拒绝执行。

### M7 关机计划编排（6 人日）

| 任务 | 内容 | 对应需求 |
|---|---|---|
| B7-1 | 计划配置校验（阶段、步骤引用、`self_shutdown` 必须在最后）与 CRUD API | FR-PLAN-01/02/07 |
| B7-2 | 运行状态机：倒计时、阶段延迟、阶段内并行、单步超时、等待离线、失败不阻塞并通知 | FR-PLAN-01~03、SEC-05 |
| B7-3 | 幂等：`(plan_id, outage_cycle_id)` 唯一约束、单活动运行、步骤 write-ahead、自关机前置标记 | FR-IDEM-01~03 |
| B7-4 | 取消：API 取消（需确认）、倒计时期间市电恢复自动取消、执行中市电恢复按 `on_mains_restored` 处理 | FR-PLAN-04/05 |
| B7-5 | 重启恢复：`resume` / `abort`、周期已结束时取消、发送通知 | FR-IDEM-04 |
| B7-6 | 进度推送（SSE `plan_progress`）、运行记录查询 API、手动执行（`confirm_name` 校验） | FR-PLAN-06、FR-UI-06 |
| B7-7 | 集成测试：AC-05 计划部分、AC-06、AC-07（同一数据目录重建 app） | AC-05~07 |

**完成标准**：AC-05~07 自动化通过；人为在每个步骤前后中断进程（测试内注入），重启后均不重复发送已执行的步骤。

### M8 验收、性能、发布（6 人日）

| 任务 | 内容 | 对应需求 |
|---|---|---|
| B8-1 | 系统设置 API（保留期、默认阈值、时区、语言、HTTP 动作护栏开关 `http_action_guard_ups_hosts`）并接入清理任务与设备默认值；关闭护栏需确认字段并写审计 | 10 系统设置、FR-DATA-02/03、D8 |
| B8-2 | 端到端验收测试套件：AC-01~11 中可自动化的部分集中为 `internal/acceptance`（或 `_test` 标签） | 15.2 |
| B8-3 | 压测：模拟器 32 台与 100 台设备，3 s 轮询，测量内存、CPU、通知延迟、SSE 延迟 | NFR-01~03 |
| B8-4 | 优雅关闭与 panic 隔离验证 | NFR-04 |
| B8-5 | 交叉编译（linux/amd64、linux/arm64、windows/amd64）mise task；版本号注入 | FR-DEP-01/02 |
| B8-6 | Dockerfile（多阶段）、`compose.yaml` 示例（数据卷、`NET_RAW` 说明）、systemd unit 示例 | FR-DEP-03/04 |
| B8-7 | 文档：部署说明、启动参数、Profile 编写指南、安全说明（主密钥备份、Windows 权限、Docker 自关机限制）；README 同步现状 | — |

**完成标准**：AC 自动化套件全部通过；压测满足 NFR-01/02/03；三平台二进制可启动；Docker 镜像可运行并完成一次模拟器端到端演示；项目方完成真机验收（检查点 CP-4）。

---

## 4. 检查点与评审

| 检查点 | 时间 | 内容 | 参与 |
|---|---|---|---|
| CP-0 | 开发开始前 | 方案第 21.1 节 D1~D10 已于 2026-10-11 确认；剩余：确认需求文档第 17 节待确认事项 | 项目方、后端、前端 |
| CP-1 | M2 结束 | 真机只读冒烟：三种协议各执行一次 `Probe`，比对归一化结果与面板读数；用错误 `pass` 验证 ayi9 错误响应格式（只读请求） | 项目方执行，后端分析 |
| CP-2 | M4 结束 | 冻结监控类 API 与 SSE 事件格式；前端联调开始 | 后端、前端 |
| CP-3 | M7 结束 | 关机编排演示（模拟器 + 测试 SSH 服务端），评审幂等与恢复测试结果后，才允许在真实环境关闭 dry-run | 项目方、后端 |
| CP-4 | M8 结束 | 首版验收：AC 自动化报告 + 真机验收（AC-01、AC-11 抓包） | 项目方 |

---

## 5. 前置条件与外部依赖

| # | 事项 | 需要时间点 | 负责 |
|---|---|---|---|
| P-1 | **已满足 M1 回归条件**：2026-10-14 安全补采 10 组市电与 50 组电池三协议样本，原件长期保留在被忽略的 `data/samples/et1k/`；脱敏最小回归数据已存于 `backend/testdata/et1k/`，无地址、凭据或真实序列号。旧文件名不约束新数据格式；未覆盖切换瞬间与恢复 | M1 开始前 / B1-7 验收前 | 项目方、后端 |
| P-2 | **已确认**：项目方已批准方案 2.2 的各阶段依赖，M0 已锁定 SQLite、lumberjack 与 x/sys，其他依赖仅在对应阶段引入 | M0 开始前 | 项目方 |
| P-3 | 真机冒烟环境：本地可访问 ET1K，设备地址通过本地环境变量提供 | M2 结束 | 项目方 |
| P-4 | 测试用 Telegram Bot 与 Webhook 接收端（可选，自动化测试使用假服务器） | M5 | 项目方 |
| P-5 | 前端与后端约定字段命名、错误码、SSE 事件格式 | M4 中期 | 后端、前端 |

---

## 6. 质量门禁

每个任务合并前：

- `gofmt -l .` 无输出、`go vet ./...`、`go test ./...` 通过（项目 `AGENTS.md` 要求）。
- 新增公开 API 有文档注释；非显而易见的边界条件与失败语义有注释（简体中文）。
- 涉及时间逻辑的测试使用 `testing/synctest`，不使用真实 `time.Sleep` 等待。
- 涉及凭据的代码路径有"不回显、不入日志"的测试。
- 驱动相关改动必须让只读安全测试与架构测试通过。

CI（Linux）额外执行 `go test -race ./...` 与短时 fuzz（每个 fuzz 目标 30 s）。CI 平台的具体搭建不在本计划范围内，如需要可在 M0 中追加任务。

---

## 7. 风险与应对

| 风险 | 可能性 | 影响 | 应对 | 触发后的调整 |
|---|---|---|---|---|
| 自研 SNMP 编解码耗时超出预期 | 中 | M2 延期 | B2-2 优先完成并用 fuzz 加固；模拟器复用编解码 | 若 M2 过半仍未稳定，评估改用 `gosnmp` 并在发送层强制 request-id 范围与 guard 校验（需重新评审 D1） |
| P-1 旧原始附件缺失（已缓解） | 低 | 旧报告数值不能作为完整回归证据 | 已由新采样生成 60 组脱敏 fixture，不使用报告构造值替代 | 切换瞬间与恢复阶段仍由后续场景或独立验收覆盖 |
| ayi9 错误响应格式与文档不一致 | 中 | AC-10 判定不准 | 解析兼容多种形式 | CP-1 后修正解析与模拟器 |
| 关机编排的重启恢复边界复杂 | 中 | M7 延期 | write-ahead 设计 + 注入中断测试 | 优先保证"不重复执行"，`resume` 细节可延后到 M8 缓冲 |
| ICMP / Windows 进程树行为与预期不符 | 低 | M6 局部延期 | 尽早在 Windows 与 Docker 上手动验证 | 提示改用 TCP 探测；Windows 脚本超时行为写入文档 |
| 历史数据量超出部署环境承受能力 | 低 | 磁盘占用 | 只存归一化数据（D6）；保留期可在系统设置中调整 | 增加明细样本采样间隔设置（需另行评审） |
| 前端联调发现 API 不合用 | 中 | 返工 | CP-2 前与前端评审接口 | 冻结后只增不改，破坏性修改需双方确认 |

---

## 8. P1 待排期清单（首版之后）

| 项 | 需求 | 在方案中的落点 |
|---|---|---|
| 多数据源合并、字段级来源覆盖、多源一致性确认 | FR-DEV-03、FR-SRC-01~04 | state 合并步骤（方案 8.1） |
| Profile 编辑器后端增强、校准视图、版本变更提示 | FR-PRF-05/06/07 | normalize Trace、`profile_version` 列 |
| operator / viewer 角色 | 2.1 | 路由角色声明（方案 16.2） |
| API Token、OpenAPI 描述、Prometheus `/metrics` | FR-API-04~06 | api 中间件、可观测性（方案 17） |
| 告警确认（ack） | FR-EVT-04 | 方案为 `events.acked_at` 列预留位置，尚未建表 |
| 通知合并窗口 | FR-NTF-06 | 渠道发送队列 |
| 内置关机命令模板、WinRM、Wake-on-LAN、计划后 WOL | FR-HOST-05、9.2、FR-PLAN-08 | action 类型扩展 |
| 规则模拟器 | FR-RULE-08 | 复用 state + rule 纯函数与历史数据 |
| 温度过高事件 | 6.1 `temperature_high` | 事件判定器 |
| 内置 HTTPS、Windows 服务、配置导出 / 导入、备份 / 恢复、在线编辑脚本 | FR-DEP-04/06、FR-DATA-05、FR-ACT-02 | config、app |
| English 界面相关的后端文案（规划使用稳定错误 `code`，前端可自行本地化） | FR-UI-02 | — |
