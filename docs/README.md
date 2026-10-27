# UPS Warden 文档

[返回项目首页](../README.md)

## 当前状态

后端 **M0～M8 已实现并通过本机验证**；M6 提供规则、通知联动、HTTP/脚本/SSH 动作、受控主机与执行记录，M7 提供关机计划编排、取消/市电恢复、停电周期幂等、重启恢复与 SSE 进度，M8 提供系统设置 API、AC 自动化套件与 32/100 台压测、优雅关闭与 panic 隔离、三平台交叉编译与 Docker/systemd 部署示例，验证证据见[开发计划](planning/backend-plan.md)。M2 真机检查点 CP-1 的三协议只读冒烟已于 2026-10-27 通过，错误密码核验与独立面板比对待完成；CP-2 后端契约已交付，双方联调待验收；CP-3 关机编排评审未完成；CP-4 首版真机验收未完成；Docker 镜像与 Linux 实机运行未验证。前端仍为脚手架、无业务页面。自动联动需要显式配置规则与动作，默认 dry-run；本机验证不代替真实设备、渠道或主机验收。

| 模块 | 已有实现 | 尚未实现 |
|---|---|---|
| 后端 | M0～M3 基础设施、统一模型、Profile/归一化、只读驱动、采集/状态/事件/历史；M4 认证、API、审计、SSE；M5 通知；M6 规则、通知联动、动作、主机及执行日志；M7 关机计划编排；M8 系统设置 API、优雅关闭与 panic 隔离；`GET /healthz` | — |
| 前端 | React 占位页、开发代理、检查与测试脚本 | 初始化/登录、总览、设备、规则、设置等业务页面及 API 调用 |
| 构建与运行 | mise 工具链及仓库内缓存；`backend:check`；`webui:build`；`backend:build` 内嵌静态 SPA；`backend:release` 三平台交叉编译与版本注入；`backend:acceptance`、`backend:load`；Dockerfile、compose 与 systemd 示例 | Docker 镜像构建与 Linux 实机运行验证 |

现状依据与具体运行方式集中在[开发指南](guides/development.md)。

## 按阅读目标查找

| 目标 | 文档 | 性质与边界 |
|---|---|---|
| 启动、联调、查询参数或运行检查 | [开发指南](guides/development.md) | 当前源码与配置对应的操作说明 |
| 发布、Docker / systemd 部署、主密钥备份与权限 | [部署指南](guides/deployment.md) | M8 产物；Docker 与 Linux 实机未验证 |
| 运行 AC 自动化套件、压测及查看实测结果 | [验收与压测](guides/acceptance.md) | M8 本机模拟器结果；不替代 CP-1/CP-4 真机验收 |
| 理解字段映射、只读计划与离线回归 | [Profile 与归一化](guides/profiles.md) | 当前 M1 契约与 ET1K 采样限制；不是联网驱动 |
| 本机测试三协议、理解发送护栏或运行只读冒烟 | [协议驱动与本机模拟器](guides/protocol-drivers.md) | M2 当前行为；CP-1 只读冒烟已通过，错误密码核验待完成 |
| 理解采集调度、状态去抖、重启恢复与历史查询 | [采集、状态与历史](guides/collection-state-history.md) | M3 内部契约与 M4 动态配置生命周期 |
| 联调初始化、管理、监控查询与实时推送 | [监控 API 与 SSE 契约](reference/monitoring-api.md) | M4 / CP-2 后端契约交付，双方联调待验收 |
| 配置通知渠道、测试消息、模板与失败查询 | [通知服务与 API 契约](reference/notifications-api.md) | M5 通知及 M6 规则上下文；真实外部渠道未验收 |
| 配置规则、动作、SSH 指纹及查询执行结果 | [规则、动作与受控主机 API](reference/rules-actions-hosts-api.md) | M6 本机验证；dry-run、安全护栏和重启边界；plan 动作入口 |
| 配置关机计划、手动执行、取消与查询运行进度 | [关机计划 API](reference/shutdown-plans-api.md) | M7 本机验证；幂等、市电恢复与重启恢复边界；CP-3 评审未完成 |
| 修改保留期、设备默认阈值、时区语言或 HTTP 动作护栏 | [系统设置 API](reference/settings-api.md) | M8 本机验证 |
| 了解功能目标、优先级、字段和验收要求 | [需求文档](planning/requirements.md) | v0.3 草案；已确认项与待确认项按正文区分 |
| 理解架构边界、技术选择和放弃的方案 | [后端实现方案](../.agents/notes/proposed/architecture/2026-10-11-backend-design.md) | 整体仍为 proposed；M0～M8 代码已落地，真机与评审检查点未完成 |
| 查看开发顺序、交付条件和外部依赖 | [后端开发计划](planning/backend-plan.md) | M0/M1/M3/M5/M6/M7/M8 已完成本机验证；M2 的 CP-1 只读冒烟通过、剩余项待完成；M4 实现/CP-2 联调待验收；M7 待 CP-3；M8 待 CP-4 |
| 查阅 ET1K 字段样本、协议限制和事故证据 | [协议支持探测报告](reference/protocols/et1k-probe-report.md) | 2026-10-11 历史记录；新采样证据见 Profile 指南与 fixture 说明 |
| 评审前端界面的视觉与交互设计 | [界面效果图](design/index.html)、[设计说明](design/README.md) | 按 M4～M8 契约设计的静态 HTML 效果图与设计规范，不是 `frontend/` 中的已实现页面 |
| 查询协作、缓存与设备安全规则 | [AGENTS.md](../AGENTS.md) | 项目强制约束 |

## 文档分工

本仓库是前后端同仓的小型项目，使用集中式文档；不为尚未实现的模块预建空目录。

```text
docs/
  README.md                              文档入口与现状摘要
  guides/development.md                  当前开发、参数、接口与检查说明
  guides/deployment.md                   发布、Docker / systemd 部署与安全说明
  guides/acceptance.md                   AC 自动化映射、压测方法与实测结果
  guides/profiles.md                     Profile、只读计划与离线归一化契约
  guides/protocol-drivers.md             只读驱动、本机模拟器与显式冒烟工具
  guides/collection-state-history.md     采集、状态、事件、历史与生命周期
  planning/
    requirements.md                      需求草案
    backend-plan.md                      开发任务与验收计划
  reference/monitoring-api.md            M4 监控 API、认证、审计与 SSE 联调契约
  reference/notifications-api.md         M5 通知渠道、模板、日志与失败 SSE 契约
  reference/rules-actions-hosts-api.md   M6 规则、动作、受控主机与执行日志契约
  reference/shutdown-plans-api.md        M7 关机计划、运行、取消、恢复与进度契约
  reference/settings-api.md              M8 系统设置契约
  reference/protocols/
    et1k-probe-report.md                  历史协议证据与安全复盘
  design/README.md                       界面效果图说明、页面与接口对照、设计约定
  design/index.html                      效果图画廊；foundations.html 为设计规范
.agents/notes/proposed/architecture/
  2026-10-11-backend-design.md            整体待实施方案，含已落地模块与验收边界
```

- **现状说明**以实现、配置和验证结果为依据；能力变化时原地更新，不写开发流水账。
- **规划区**保留需求与任务，不充当安装手册或 API 契约。日期和文档版本描述原记录，不表示当前发布版本。
- **决策记录**沿用原中文方案的章节、决策表和评审格式；不为了迁移复制为多篇 Note，也不把已评审等同于已实现。落地时同步现状 docs 和对应决定的状态。
- **历史证据**保留原观察、未验证事项与事故理由；后续方案未采用的建议必须明确标注。协议字段的物理含义见探测报告，统一字段映射见需求附录，不在入口重复维护。
- **安全要求**以 [AGENTS.md](../AGENTS.md) 为准。不得照历史记录重放设备 HTTP 访问、端口扫描或写指令；实际设备地址、community、密码及未脱敏数据只放本地被忽略的根目录 `/data/`。

## 尚待补齐的证据

- 旧探测原始附件未随报告提供；2026-10-14 重新采集的 10 组市电与 50 组电池三协议样本已整理为 [ET1K 脱敏 fixture](../backend/testdata/et1k/README.md)，原件保留在本地 Git 忽略目录；未覆盖停电瞬间、市电恢复和错误密码响应。
- 需求第 17 节的未决事项与协议校准项仍保留待确认状态；本次文档归类不替代产品评审或真机验收。
