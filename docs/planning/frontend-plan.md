# UPS Warden 前端开发计划

> **文档版本**：v0.1
> **日期**：2026-10-28
> **依据**：[界面效果图与设计规范](../design/README.md)（`docs/design/*.html`、[foundations.html](../design/foundations.html)）、[《需求文档》v0.3](requirements.md) 第 10、12、13 节、`docs/reference/` 下 M4～M8 后端契约、现有[前端脚手架](../../frontend/package.json)
> **范围**：首版（P0）WebUI 从脚手架到可内嵌发布的开发顺序、技术方案、任务拆分、完成标准、检查点、待确认事项与风险；P1 只列待排期清单
> **状态**：草案，待评审；尚未开始实现。开工前需确认第 6 节依赖选型和第 7 节各项的默认处理（检查点 FCP-0）。人日为单人粗略估算，未经实测
> **阅读入口**：[文档导航](../README.md) · [开发指南](../guides/development.md) · [后端开发计划](backend-plan.md)

---

## 1. 现状与前提

- **前端**：React 19 + TypeScript 7 + Vite+（`vp`）脚手架，只有占位页 [App.tsx](../../frontend/src/App.tsx) 和一个 SSR 冒烟测试；运行时依赖只有 `react` / `react-dom`。[vite.config.ts](../../frontend/vite.config.ts) 已把 `/api`、`/healthz` 代理到后端。
- **后端**：M4～M8 API 与 SSE 已实现，并通过本机验证。各契约是联调基线，CP-2 双方联调待验收。前端只使用公开 API（FR-API-01）。OpenAPI 为 P1（FR-API-04），首版类型手写。
- **设计稿**：共 10 张页面效果图（`login.html` 含初始化），另有 `foundations.html` 设计规范、共享的 `apple-ui.css`（token、组件、浅色 / 深色、窄屏、无障碍媒体查询）和 `apple-ui.js`（69 个图标、导航结构、轻交互）。效果图是视觉基准。评审专用部分不进入产品：`?state` 状态浮条、`?embed`、`?theme`、固定示例数据。效果图与契约不一致时按契约实现（设计说明的约定），差异登记在第 7 节。
- **托管**：`backend:build` 会内嵌 `frontend/dist`；后端对非 API 的深层 GET 回退到 `index.html`，因此可以使用基于 History API 的路由。

## 2. 计划原则

1. **契约优先**：字段、错误码、确认字段以 `docs/reference/` 为准。发现缺口先登记到第 7 节，不在前端猜测兼容格式，也不伪造数据。
2. **基础设施先行**：F0 一次做好 token 与组件、API 客户端、SSE 实时存储和字段格式化，各页面复用。页面不直接 `fetch`，也不自行解析字段质量。
3. **先只读监控，后写配置**：先完成登录与监控页面，支撑 CP-2 联调。配置页面按引用依赖排序：通知渠道 → 主机与动作 → 关机计划 → 规则。
4. **危险操作显式确认**：新规则和手动执行默认 dry-run，手动执行时总是显式发送 `dry_run`。关闭 dry-run、关闭护栏、删除、执行计划都要经过界面确认。后端要求的确认字段（`confirm`、`confirm_name`、`confirm_disable_guard`）只在用户完成确认后才发送，不由前端自动补齐。
5. **如实呈现数据质量**：非 good 字段不显示为 0；断线期间不外推实时值；文案只陈述已确认的事实，例如写“已加入发送队列”，不写“已送达”。
6. **最小依赖**：只引入第 6 节确认的依赖。不为 P1 的 English 界面、角色、OpenAPI 预建框架。
7. **开发安全**：联调只用临时数据目录、loopback 模拟器（`upssim`）、假 Webhook 和测试 SSH 服务端（见 [AGENTS.md](../../AGENTS.md)）。不对真实设备、渠道或主机操作；开发中手动执行动作或计划只用 dry-run。

---

## 3. 技术方案

### 3.1 目录结构（建议）

```text
frontend/src/
  main.tsx / App.tsx   路由与全局 Provider
  styles/              tokens.css、components.css（由 apple-ui.css 迁移）
  icons/               SVG symbol 与 <Icon name> 组件（沿用 apple-ui.js 路径）
  api/                 client.ts（fetch、CSRF、错误）、types.ts、按资源分文件的请求函数
  live/                SSE 连接与实时存储（devices / alerts / plan_runs / 连接状态）
  model/               字段目录镜像、事件类型、枚举与错误码文案、格式化
  components/          通用组件
  layout/              AppShell、Sidebar、TabBar、Toolbar
  pages/<page>/        页面、页面私有组件与测试
```

### 3.2 路由（建议）

| 路由 | 页面 | 效果图 |
|---|---|---|
| `/setup`、`/login` | 首次初始化、登录 | `login.html` |
| `/` | 总览 | `overview.html` |
| `/devices`、`/devices/new` | 设备列表、添加设备向导（sheet） | `devices.html` |
| `/devices/:id?tab=live\|history\|events\|sources` | 设备详情 | `device-detail.html` |
| `/alerts?view=active\|history&…` | 告警与事件；筛选条件写入 query | `alerts.html` |
| `/rules`、`/rules/:id` | 规则列表-编辑 | `rules.html` |
| `/plans`、`/plans/:id?tab=config\|run` | 关机计划 | `shutdown-plans.html` |
| `/hosts-actions?tab=hosts\|actions\|runs` | 受控主机、动作、执行记录 | `hosts-actions.html` |
| `/notifications?tab=channels\|templates\|logs` | 通知渠道、模板、发送记录 | `notifications.html` |
| `/settings/:section` | 设置、管理员、审计 | `settings.html` |
| `/profiles` | Profile 管理 | **无效果图**（Q-D1） |

未登录访问任意页面时跳转登录，并保留返回路径；未知路由显示 404 页。

### 3.3 API 客户端

- 请求同源 `/api/v1`。JSON 请求设置 `Content-Type: application/json`。认证后的非 GET/HEAD 请求带 `X-CSRF-Token`，令牌来自登录响应或 `GET /auth/session`，**只保存在内存中**。
- 错误统一解析为 `{status, code, message, details}`，按 `code` 映射中文文案，未知 code 回退显示 `message`。全局处理以下情况：
  - 401：清空会话和实时存储，跳转登录页。
  - 429：读取 `Retry-After`。
  - 503 `configuration_saved_runtime_error`：提示“配置已保存，但运行恢复失败”，并重新读取配置。**不能当作回滚处理**（[监控契约](../reference/monitoring-api.md)）。
- 写请求遵循各资源的部分更新约定，只提交用户修改的字段。凭据字段统一处理：
  - 留空表示保留原值；清除需显式提交 `clear_secrets`。
  - 响应只读 `*_set`，界面显示“已设置 / 未设置”，不显示掩码占位符，输入框不预填。
- 列表分页统一使用 `limit` / `before` 游标和“加载更多”。契约没有 total，界面不显示总数。
- 类型手写在 `api/types.ts`，对照契约与后端 [model](../../backend/internal/model/model.go) 的 JSON 标签。**`Field` 的 `num` / `bool` / `str` 带 `omitempty`：good 字段缺少 `num` 表示 0**，解析时补齐默认值，并写单元测试覆盖。SSE `event` 帧使用 `at`，REST 事件使用 `started_at` / `cleared_at`，两者在 api 层统一为一种视图。

### 3.4 实时数据（SSE）

- 登录后建立唯一的 `EventSource('/api/v1/stream')`。实时存储用 `useSyncExternalStore` 暴露选择器，页面按需订阅。
- 各帧的处理规则：

| 帧 | 处理 |
|---|---|
| `sync` | 全量替换 devices、alerts、plan_runs，标记“已同步” |
| `device_state` | 替换单台设备的状态 |
| `alert` | 替换该设备的活动告警列表；空数组表示全部解除 |
| `event` | 按 id 去重后追加到最近事件 |
| `plan_progress` | 按运行 id 更新或插入；进入终态的运行移出活动集合，并让计划运行查询失效 |
| `notify_failed` | 按日志 id 去重，弹出 toast |

- **断线**：连接状态切到 offline，显示“实时连接已断开”横幅，并降低实时数值的对比度（FR-UI-01）。断线期间的数据不视为连续观测；重连后，收到 `sync` 之前保持离线样式。
- **重连**：浏览器在服务端返回非 200 状态时不会自动重连 EventSource，因此由前端自行管理：指数退避从 1 秒到 30 秒，横幅提供“立即重连”。每次重连失败后先请求 `GET /auth/session`，返回 401 时转到登录页，避免把会话过期误判为断网。重连成功后，用 `GET /notify-logs?ok=false&from=<断开时刻>` 补齐断线期间的 `notify_failed`（[通知契约](../reference/notifications-api.md)）。
- **倒计时**：由 `countdown_until` 和本地时钟计算剩余时间。断线期间继续显示，同时标注“实时连接已断开，状态可能已变化”。

### 3.5 字段与格式化

- `model/fields.ts` 镜像 [catalog.go](../../backend/internal/model/catalog.go) 的 key、kind、unit、phased、history、group、enum，另外补中文名称和枚举文案。按相字段的实例键为 `input_voltage[1]`～`[3]`。后端没有字段目录接口，镜像需要随 `catalog.go` 同步（Q-C8）。
- 显示规则（FR-UI-05、foundations「数据质量」）：

| 质量 | 显示 |
|---|---|
| good | 数值加单位；Duration 显示为“x 分 y 秒” |
| invalid、unsupported、快照中缺失的键 | 「—」加原因，绝不显示 0。原因由 reason code 映射，如 `not_mapped` 显示“Profile 未映射”，`device_unsupported` 显示“设备不支持” |
| stale（通讯中断，`comm_stale`） | 保留最后读数并整体置灰，显示 `last_ok_at`；不参与“正常”判断（Q-D3） |

- 剩余时间：`runtime_source=estimated` 时显示“约 …”加「估算」徽标；`none` 显示「—」。
- 按相字段：单相只显示第 1 相，多相按 `input_phases` / `output_phases` 展开。
- 时间：API 时间为 UTC。界面按 `GET /settings` 返回的 `timezone`，用 `Intl.DateTimeFormat` 的 `timeZone` 选项格式化。P0 界面语言固定为简体中文，`language=en-US` 的界面为 P1。
- 状态色按[设计说明](../design/README.md)的状态色语义使用，蓝色只用于交互；每个状态同时带图标或文字。

### 3.6 样式、组件与动效

- 把 `apple-ui.css` 的 token 和组件样式迁移为全局 CSS，不引入 CSS-in-JS 或 UI 组件库。类名可以重新组织，但颜色、字号、间距、圆角和动效时长保持一致；评审浮条相关样式不迁移。
- 图标：`apple-ui.js` 中的 69 个 SVG symbol 迁移为 `<Icon>` sprite。图标必须伴随文字，或提供 `aria-label`。
- 主题：默认跟随系统的浅色 / 深色。设置页「外观」是本机偏好，只用 `localStorage` 保存主题，不保存任何会话或凭据。深色主题在需求中为 P2（FR-UI-04），但 token 已经包含深色值，额外成本主要是验证。
- 窄屏（≤860px）：侧栏改为底部标签栏，分栏改为上下叠放，宽表格重排为两行列表或横向滚动。
- 动效：P0 只做非手势动效（按下反馈、分段控件滑块、浮层出现），用 CSS 过渡实现。P0 不做 sheet 拖拽关闭和可滑动行，因此不引入弹簧动画库；以后加入手势时，按设计说明引入 Motion（`bounce: 0`）。
- 无障碍：
  - 浮层支持焦点陷阱、关闭后回焦、Esc 关闭。
  - 「—」的原因提示改为可聚焦、可点按（效果图只支持 hover，触屏和键盘用户看不到）。
  - 倒计时只在阶段变化时通过 `role=status` 播报。
  - 尊重 `prefers-reduced-motion`、`prefers-reduced-transparency`、`prefers-contrast: more`。

### 3.7 测试策略

- **单元测试**（现有 `vp test`）覆盖纯函数：字段格式化与质量、错误映射、SSE reducer、请求体构造（显式 `dry_run`、确认字段、`clear_secrets`、只提交修改的字段）。
- **组件测试**：需要 DOM 环境（第 6 节新增 devDependency）。覆盖破坏性确认、计划名称必须完全一致才能提交、关闭 dry-run 的确认、凭据只写输入框、断线横幅。
- **手工联调**：使用临时数据目录，配合 `upssim` 多设备守护进程（`mise run sim -- ...`，agent 按 [upssim 技能](../../.agents/skills/upssim/SKILL.md)操作）随时切换正常、停电、放电、低电、恢复、失联、5 秒闪断，以及假 Webhook 和测试 SSH 服务端。按 1440×900 与 390×844 两种视口、浅色 / 深色两种主题与效果图对照。
- **E2E**（TST-05，P1）：用 Playwright 覆盖添加设备、配置规则、执行计划 dry-run 三条流程。

---

## 4. 里程碑总览

| 里程碑 | 内容 | 估算（人日） | 依赖 | 关键交付 |
|---|---|---|---|---|
| F0 | 工程与设计系统基础 | 6 | FCP-0 | token / 组件 / 图标、外壳与路由、API 客户端、SSE 存储、字段格式化、测试环境 |
| F1 | 初始化与登录 | 2 | F0 | setup、login、锁定、会话过期、登出 |
| F2 | 监控页面 | 7 | F1 | 总览、设备详情（含历史图表与诊断）、告警与事件 |
| F3 | 设备与 Profile 管理 | 6 | F2 | 设备列表、添加 / 编辑向导、数据源、启停删除、Profile 管理 |
| F4 | 通知 | 4 | F1 | 渠道（Telegram / Webhook）、测试、模板、发送记录 |
| F5 | 受控主机与动作 | 5 | F4 | 主机与指纹、连接 / 命令 / 在线检测、五类动作、手动执行、执行记录 |
| F6 | 关机计划 | 5 | F5 | 阶段与步骤编辑、手动执行、倒计时与进度、取消、运行记录 |
| F7 | 规则 | 5 | F5、F6 | 列表与编辑器、条件构建器、模板、dry-run 确认、评估日志 |
| F8 | 设置、账户与审计 | 3 | F1 | 通用、保留期、设备默认值、HTTP 护栏、管理员、改密、审计 |
| F9 | 收尾与发布 | 4 | F0～F8 | 响应式与无障碍走查、错误态补齐、设计走查、内嵌构建、文档同步 |
| | **合计** | **约 47** | | 另加 15～20% 缓冲后约 54～56 人日，约 **11 周**（单人） |

```text
F0 ─▶ F1 ─┬─▶ F2 ─▶ F3 ───────────────────┐
          ├─▶ F4 ─▶ F5 ─▶ F6 ─▶ F7 ────────┼─▶ F9
          └─▶ F8 ──────────────────────────┘
          F3 结束：CP-2 双方联调    F6 结束：可支撑 CP-3 演示
```

如有第二名前端开发者，F2 完成后可以拆成两条线并行：一人做 F3 与 F8，另一人做 F4～F7。

---

## 5. 里程碑详情

任务编号格式为 `F<里程碑>-<序号>`。每个任务都包含对应的单元测试或组件测试，除非另有说明。

### F0 工程与设计系统基础（6 人日）

| 任务 | 内容 | 依据 |
|---|---|---|
| F0-1 | FCP-0 确认后安装依赖、建立目录结构，`pnpm check` / `test` / `build` 保持通过 | 第 6 节 |
| F0-2 | 样式迁移：颜色（浅色 / 深色）、字体与字阶、间距、圆角、阴影、材质、动效时长与缓动、断点、无障碍媒体查询，以及组件样式 | `apple-ui.css`、foundations |
| F0-3 | `<Icon>` 组件与 69 个图标 | `apple-ui.js` |
| F0-4 | 通用组件，见下方清单 | foundations「组件」「安全交互」 |
| F0-5 | AppShell：侧栏四组（监控、自动化、通知、系统）、窄屏底部标签栏五项、工具栏滚动标题、页头、列表-详情分栏；侧栏连接状态与告警角标接入实时存储 | `apple-ui.js` 导航结构 |
| F0-6 | 路由、会话守卫、404 页 | 3.2 |
| F0-7 | API 客户端、错误处理、类型 | 3.3、[监控契约](../reference/monitoring-api.md) |
| F0-8 | SSE 实时存储与重连 | 3.4 |
| F0-9 | 字段目录镜像、格式化、事件 / 枚举 / 错误码文案；读取设置并应用时区 | 3.5、[系统设置](../reference/settings-api.md) |
| F0-10 | DOM 测试环境与组件测试工具，替换占位冒烟测试 | 3.7 |

F0-4 的组件清单：

- 按钮：Button（各变体）。
- 状态标识：Pill、Badge、Severity。
- 输入控件：Switch（含 dry-run 的靛蓝变体）、Segmented、Input、Textarea、Select、Stepper、TokenInput。
- 反馈与浮层：Banner、Toast、Sheet、Dialog。
- 确认对话框：DestructiveDialog（说明会一并清除的内容；返回 409 时提示改为停用）、ConfirmNameDialog（输入名称确认）。
- 列表与数据：List、Row、FormRow、Table、Metric（含质量与原因提示）。
- 进度与状态：Ring、Meter、Spinner、Empty。

**完成标准**：

- 外壳在桌面与窄屏、浅色与深色下与效果图一致。
- 单元测试覆盖：四种质量与缺失键、`omitempty` 造成的 0 值、事件去重、`alert` 替换、`plan_progress` 终态移除、401 / 503 处理。
- `pnpm check`、`pnpm test`、`pnpm build` 通过。

### F1 初始化与登录（2 人日）

| 任务 | 内容 | 依据 |
|---|---|---|
| F1-1 | 启动流程：先请求 `GET /auth/session`；返回 401 时再查 `GET /setup/status`，决定进入初始化页还是登录页 | 监控契约「初始化、登录与用户」 |
| F1-2 | 初始化页，字段与处理见下方 | `login.html` setup |
| F1-3 | 登录页：错误态（`invalid_credentials`，清空密码并抖动）、锁定态（429，按 `Retry-After` 倒计时，结束后回到登录）；密码不预填 | `login.html` login / error / locked |
| F1-4 | 会话：在内存保存 user、CSRF token、`expires_at`；401 全局跳转；`POST /auth/logout` | 3.3 |

F1-2 初始化页：

- 字段：setup token（等宽字体，说明令牌只在启动时向 stderr 输出一次、只存内存）、用户名（统一转小写，提示可用字符）、密码、确认密码。
- 校验：密码至少 8 个字符、最多 1024 字节，两次输入一致。
- 返回 `setup_closed` 时转到登录页。

**完成标准**：

- 对临时数据目录的后端完成整条流程：初始化 → 登录 → 刷新后仍保持登录 → 登出 → 连续失败 5 次后锁定。
- setup token 和密码不进入 URL、浏览器存储或控制台。

### F2 监控页面（7 人日）

| 任务 | 内容 | 依据 |
|---|---|---|
| F2-1 | 总览，组成见下方 | `overview.html` outage / normal / offline |
| F2-2 | 设备详情「实时」：hero（容量环、身份、关键指标）；电池、输出、输入与旁路（按相表格）、状态标志、额定与身份分组；失联横幅与 stale 显示；电池供电时的计划倒计时横幅 | `device-detail.html` battery / normal / lost |
| F2-3 | 设备详情「历史」，见下方 | `device-detail.html`、监控契约「快照、历史」 |
| F2-4 | 设备详情「事件」：`GET /events?device_id=` 游标分页 | 同上 |
| F2-5 | 设备详情「数据源与诊断」，见下方 | 监控契约「数据源与诊断」 |
| F2-6 | 告警与事件，见下方 | `alerts.html` 全部状态 |

F2-1 总览：

- 汇总卡。
- 设备卡：供电胶囊、电池容量、负载、输入 / 输出电压、放电计时、剩余时间（标注估算）、通讯状态；去抖期间显示「确认中 n/m 秒」（由 `pending_since` 和设备配置的确认时长计算）。
- 设备筛选分段与排序，在前端派生。
- 活动告警列表。
- 停电主卡：活动停电周期加计划倒计时与进度，「取消运行」在 F6 接通。
- 正常态、空态与断线态。

F2-3 历史图表：

- 时间范围：1 小时 / 6 小时 / 24 小时 / 7 天 / 30 天。
- 查询参数：`resolution=auto`、`max_points≤1000`，超出时由后端降采样。
- 字段选择器只列 History 字段的实例。
- 自写 SVG 折线：支持多序列；缺失采样处断开不连线；1m 分辨率时绘制 min / max 带。
- 停电区间阴影由该设备时间范围内的 `mains_lost` / `mains_restored` 事件推导。

F2-5 数据源与诊断：

- 显示数据源配置与运行状态（`status`）。
- 只读连接测试 `POST /sources/{id}/test`：409 `source_busy`；502 `probe_failed` / `source_password_wrong`，后者显示“设备密码错误（103）”。
- 原始数据与字段追踪 `GET /sources/{id}/raw`（返回 `raw` 与 `trace`）；404 时显示空态；可下载为 JSON。

F2-6 告警与事件：

- 活动告警：主卡加「需要关注」。按契约不提供确认按钮，告警在条件解除后自动结束。
- 空态。
- 历史事件：按设备、类型（Q-C3）、严重度、时间筛选，游标加载更多；窄屏重排为两行列表。
- `notify_failed` 提示。

**完成标准**：

- 用 `upssim` 守护进程（`set --mode battery/low/mains`、`--link down/up`、`--flash`）或场景文件逐一核对：停电 → 放电 → 低电 → 恢复、停电期间失联（hold）、5 秒闪断。界面与 SSE 推送一致，闪断不出现停电告警。
- 停止后端后，一个重连周期内显示断线横幅；后端恢复后由 `sync` 重建界面。
- 所有非 good 字段都不显示为 0。

**实施记录（2026-10-29）**：F2-1～F2-6 已实现，页面与模型有单元测试。前端用到的监控接口（设备、数据源与 `status`、`/raw`、只读连接测试、历史字段白名单与缺相号 422、事件筛选、SSE `sync`）已用本机模拟器（upssim ET1K SNMP）对临时 ups-warden 核对字段形状。上面完成标准中的界面逐场景核对（停电 → 低电 → 恢复、失联、闪断、断线重连）尚未在浏览器中执行。

### F3 设备与 Profile 管理（6 人日）

| 任务 | 内容 | 依据 |
|---|---|---|
| F3-1 | 设备列表：启停开关 `PUT /devices/{id}/enabled`；数据源摘要（协议、主机、Profile、延迟，Q-C1）；供电状态来自实时存储；删除确认说明会清除的历史与事件，返回 409 时提示改为停用 | `devices.html` list / delete |
| F3-2 | 添加设备向导，四步与保存见下方 | `devices.html` proto / test / pwd / thresholds |
| F3-3 | 编辑设备：名称、备注、位置、标签；`config` 整体替换。编辑数据源：凭据只写、`clear_secrets`、切换协议时提示会清除旧凭据；处理 503 `configuration_saved_runtime_error` | 监控契约「设备」「数据源」 |
| F3-4 | Profile 管理，见下方 | 监控契约「Profile」；无效果图（Q-D1） |

F3-2 添加设备向导：

1. **设备与协议**：SNMP 161 / Modbus 502 / AYI9 2390，附只读说明。
2. **连接**：host、port；SNMP version 与 community、Modbus unit_id、AYI9 password；poll / timeout / retries。
3. **测试与 Profile**：`POST /sources/test`，显示推荐列表和归一化读数预览；`source_password_wrong` 时显示“密码错误（103）”。
4. **阈值与去抖**：预填 `device_defaults`。

保存：依次调用 `POST /devices`、`POST /devices/{id}/sources`；第二步失败时的处理见 Q-C2。

F3-4 Profile 管理：

- 列表，内置 Profile 标为只读。
- 查看声明原文。
- 导入：先 `POST /profiles/validate`，错误时显示 line 与 path。
- 复制、导出、删除（被引用时返回 409）。
- 编辑器与校准视图为 P1。

**完成标准**：

- 用三种协议的模拟器各添加一台设备，均能采集成功。
- AYI9 使用错误密码时，连接测试显示“密码错误（103）”（AC-10 的 UI 部分）。
- 用 RFC 1628 通用 Profile 接入时，不支持的字段显示「不支持」（AC-02 的 UI 部分）。
- 能导入自定义 Profile 并接入新的寄存器表（AC-03 的 UI 部分）；非法 Profile 的错误显示行号。

**实施记录（2026-10-29）**：F3-1～F3-4 已实现，页面与模型有单元测试。路由：`/devices`（列表，`?edit=<id>&section=source` 打开编辑浮层）、`/devices/new`（添加向导）、`/profiles?key=<key>`。实现约定：

- 未保存配置的连接测试带上协议默认 Profile（SNMP `rfc1628-generic`、Modbus `et1k-modbus`、AYI9 `ayi9-json-generic`），不带时序字段，由后端按 5 秒总限额探测；编辑页的连接测试只测已保存配置，避免凭据留空造成误判。
- 向导创建设备后不能回到第一步（基本信息与阈值已保存）；数据源保存失败按 Q-C2 处理。设备创建返回 503 时没有 id，提示用户到列表核对，避免重复添加。
- 编辑数据源切换协议时不提交 `clear_secrets`，旧协议凭据由后端清除。

已用本机模拟器（upssim：标准 SNMP、ET1K Modbus、标准 AYI9、AYI9 固定错误码 103）对临时 ups-warden 核对前端请求体形状：

- 三协议草稿测试均返回 200，推荐 Profile 与默认值一致；错误码 103 返回 502 `source_password_wrong`「设备密码错误（103）」。
- 向导两步保存均返回 201，响应只有 `*_set`，不回显凭据；只 PATCH `poll_ms` 后 `community_set` 保持不变。
- RFC 1628 通用 Profile 下不支持的字段为 `unsupported`。删除缺少 `confirm` 返回 422。
- Profile 复制、key 冲突 409、YAML 导出、删除内置 Profile 返回 409 `builtin_readonly`。
- Profile 语义错误（如重复字段）返回 422 并带 `line` 与 `path`。YAML 语法错误和不在后端白名单内的顶层字段只返回 `path:"$"`，界面显示“校验未通过”，不显示行号。

未完成：上面完成标准中的界面操作尚未在浏览器中逐项执行；AC-03（导入自定义 Profile 接入新寄存器表）未验证。

### F4 通知（4 人日）

| 任务 | 内容 | 依据 |
|---|---|---|
| F4-1 | 渠道列表与编辑，字段见下方；启停；删除前确认，返回 409 时提示改为停用 | `notifications.html` channels、[通知契约](../reference/notifications-api.md) |
| F4-2 | 测试消息 `POST /channels/{id}/test`：202 时显示“已加入发送队列”，并引导到发送记录；处理 409 `channel_disabled`、429、503 | `notifications.html` test |
| F4-3 | 模板，见下方 | `notifications.html` templates |
| F4-4 | 发送记录：按渠道、成功 / 失败、时间筛选，游标分页，显示模板回退标记；与 `notify_failed` 实时提示联动 | `notifications.html` logs |

F4-1 渠道字段：

- **Telegram**：token（只写）、chat_ids（最多 32 个）、消息格式、api_base、proxy_url（只写）。
- **Webhook**：url / headers / signing_key（只写）、method、format、body_template。

F4-3 模板：

- 列表：72 项，即 24 种事件 × 3 种格式。
- 编辑器：变量高亮，可点按变量 chip 插入。
- 预览：见 Q-C4。
- 保存：返回 422 时定位到未知变量。
- 恢复默认：提交 `reset:true`。

**完成标准**：

- 对假 Webhook 完成渠道新建、测试和失败查看；失败时实时提示，断线重连后能从发送记录补齐。
- 凭据不回显。

**实施记录（2026-10-29）**：F4-1～F4-4 已实现，路由为 `/notifications`；API 构造函数、纯函数与页面有单元测试。实现约定：

- 渠道启停单独发 `PUT {enabled}`，不夹带表单中未保存的修改。测试消息使用已保存的配置，渠道停用时按钮禁用，后端返回 409 时仍有处理。
- 删除返回 409 时提供“改为停用”。
- 模板预览按 Q-C4 用示例上下文近似渲染。保存返回 422 `validation_failed` 时没有 details，由本地扫描给出未知变量所在行，横幅文案以服务端 message 为准。
- 发送记录默认时间范围为 7 天，筛选写入 query。

未完成：上面完成标准（假 Webhook 渠道的新建、测试、失败查看与断线补齐）尚未在浏览器中联调。

### F5 受控主机与动作（5 人日）

| 任务 | 内容 | 依据 |
|---|---|---|
| F5-1 | 主机列表与编辑：名称、地址、类型、端口、用户名、认证方式（密码 / 私钥 + 口令，均只写）、sudo、在线检测（TCP 端口 / ICMP）；修改地址或端口时提示会清除已确认的指纹 | `hosts-actions.html` hosts、[规则、动作与主机契约](../reference/rules-actions-hosts-api.md) |
| F5-2 | 指纹流程，见下方 | `hosts-actions.html` fp |
| F5-3 | 连接测试、测试命令（`{confirm:true}`）、在线检测；如实显示 ICMP 权限不足的提示 | 契约「受控主机」 |
| F5-4 | 动作列表（按类型分组）与编辑，五类配置见下方 | `hosts-actions.html` actions |
| F5-5 | 手动执行，见下方 | `hosts-actions.html` run |
| F5-6 | 执行记录：`GET /action-runs?action_id=` 分页，可展开 stdout / stderr；显示 `unknown`、`skipped_*`、`queued` 等全部状态；状态筛选见 Q-C6 | `hosts-actions.html` runs |

F5-2 指纹流程：

1. 获取：`POST /hosts/{id}/hostkey/fetch`。
2. 人工核对：显示 SHA256 指纹；从获取时刻起在前端计算 5 分钟有效期。
3. 确认：`POST /hosts/{id}/hostkey/confirm`，请求体为 `{confirm:true, fingerprint}`。

F5-4 五类动作配置：

| 类型 | 配置 | 说明 |
|---|---|---|
| notify | 渠道 | 只能选择已启用的渠道 |
| http | method、expected_status；url / headers / body_template 只写 | 保存时可能被 HTTP 护栏拒绝，需显示原因 |
| script | 从 `GET /scripts` 选择脚本、参数数组 | — |
| ssh | 主机、命令、shutdown 标记 | — |
| plan | 计划 | 不能手动执行，改为提供「前往计划」 |

F5-5 手动执行：

- 运行方式默认 dry-run。请求体总是包含 `confirm:true` 和显式的 `dry_run`，`device_id` 可选。
- 切换到真实执行时，显示目标与命令摘要，并改变按钮文案（Q-P1）。
- 处理 409 `action_busy` / `snapshot_unavailable`，以及 422 `plan_confirmation_required`。

**完成标准**：

- 用本机测试 SSH 服务端走通指纹获取 → 确认 → 连接测试 → 测试命令；指纹未确认时，界面明确提示无法执行。
- 动作 dry-run 只产生执行记录。
- HTTP 动作指向数据源主机时，显示护栏拒绝。

### F6 关机计划（5 人日）

| 任务 | 内容 | 依据 |
|---|---|---|
| F6-1 | 计划编辑，内容与校验见下方 | `shutdown-plans.html` edit、[关机计划契约](../reference/shutdown-plans-api.md) |
| F6-2 | 手动执行：ConfirmNameDialog 要求名称完全一致（不预填），dry-run 分段选择，可选关联设备；按钮文案随 dry-run 切换；处理 409 `plan_run_active` / `plan_cycle_executed` / `plan_cycle_ended` / `snapshot_unavailable` | `shutdown-plans.html` confirm |
| F6-3 | 当前运行，见下方 | `shutdown-plans.html` countdown / running |
| F6-4 | 取消运行：确认后提交 `{confirm:true}`；处理 202、409 `plan_run_finished`、503 `plan_unavailable`；文案说明已发出的命令不可撤回。总览停电主卡同步接入取消 | 契约「取消与市电恢复」 |
| F6-5 | 运行记录：`GET /plan-runs?plan_id=` 分页，查看单次运行详情 | 契约「运行记录与 SSE」 |

F6-1 计划编辑：

- 顶层：倒计时、执行中市电恢复策略（`on_mains_restored`）、服务重启策略（`on_restart`）、通知渠道（最多 16 个）。
- 阶段时间线：每阶段设置 `delay_s`、`step_timeout_s`、`offline_wait_s`，包含若干并行步骤；末尾固定为「关闭本机」阶段。
- 前端结构性校验：阶段 1～16、每阶段步骤 1～32、同一动作在计划内只出现一次、步骤只能选 ssh / script / http 动作、`self_shutdown` 只能是最后一个阶段且只含一个步骤。语义错误以后端 422 的 `details` 为准。

F6-3 当前运行：

- 由 `sync.plan_runs` 与 `plan_progress` 驱动。
- 倒计时按 `countdown_until` 计算。
- 阶段时间线显示全部步骤状态：`waiting`、`running`、`sent`、`offline`、`failed`、`cancelled`、`unknown`；`sent` 步骤带 `result.offline_timeout=true` 时显示“等待离线超时”。总阶段数由 `steps` 推导（Q-C5）。
- 市电恢复的说明使用设备配置的 `recovery_confirm_s`，不写死 60 秒。

**完成标准**：

- 用模拟器与测试 SSH 服务端演示以下场景（与后端 AC-05 计划部分、AC-06、AC-07 对应）：dry-run 手动执行、倒计时期间取消、倒计时期间市电恢复后自动取消、执行中重启服务后恢复。
- 界面进度与运行记录一致。
- 不执行任何真实关机。

### F7 规则（5 人日）

| 任务 | 内容 | 依据 |
|---|---|---|
| F7-1 | 规则列表（启用、dry-run、最近触发）与编辑器头部（名称、启用、dry-run）；关闭 dry-run 时弹出确认对话框，事件规则与条件规则一致，重新开启不需确认（Q-P2）；新建默认 dry-run | `rules.html` edit / dryoff |
| F7-2 | 作用范围：指定设备（最多 100 台）/ 按标签（最多 32 个）/ 所有设备 | 契约「规则」 |
| F7-3 | 触发条件，见下方 | `rules.html` edit / event |
| F7-4 | 命中动作与恢复动作列表（每列表最多 32 项、不重复）、冷却 `cooldown_s`；保存时显示 `warnings`（如依赖 `runtime_estimate` 的提醒） | 契约「评估契约」 |
| F7-5 | 从模板创建：`GET /rules/templates` 填充表单，关机模板要求显式选择 plan 动作，再 `POST /rules`；模板默认停用且 dry-run | `rules.html` templates |
| F7-6 | 评估日志：`GET /rule-logs?rule_id=`，显示全部 kind（`fired`、`recovered`、`skipped_cooldown`、`quality_blocked`、`quality_restored`、`dispatch_failed`），可链接到对应的动作执行记录 | 契约「日志与持久化」 |
| F7-7 | 删除规则：确认后提交 `{confirm:true}` | 同上 |

F7-3 触发条件：

- 事件触发：从统一事件目录中多选。
- 条件触发：两层 OR of AND 构建器，每层最多 16 项。
  - 每个条件由字段（统一字段，按相字段可省略相号）、运算符、值、`for_s` 组成。
  - 运算符与值的输入控件随字段类型变化：布尔、文本、枚举只允许 eq / ne；数值和时长用数字输入，不提交字符串数字。

**完成标准**：

- 在 UI 上配置“停电确认后通知”规则：模拟器停电后，假 Webhook 收到通知（对应后端 AC-05 通知部分）。
- dry-run 规则只产生日志（AC-09 的 UI 部分）。
- 从关机模板创建并引用 plan 动作的规则，在 dry-run 下创建 dry-run 运行。

### F8 设置、账户与审计（3 人日）

| 任务 | 内容 | 依据 |
|---|---|---|
| F8-1 | 通用：时区、语言即时保存，`PATCH /settings` 只提交单个字段；「外观」只保存在本机 | `settings.html` general |
| F8-2 | 数据保留与设备默认值：使用显式保存栏（还原 / 保存）。前端校验明细 1～90 天、聚合不小于明细且不超过 3650 天、日志 1～3650 天；缩短保留期前确认（Q-P3）。`device_defaults` 整体提交，界面说明只影响新建或之后修改的设备 | `settings.html` retention / defaults、[系统设置契约](../reference/settings-api.md) |
| F8-3 | 安全：HTTP 护栏关闭时确认并提交 `confirm_disable_guard:true`，开启无需确认；会话到期时间；退出登录 | `settings.html` security / guard |
| F8-4 | 管理员：列表、新建、改名、删除（不能删除自己，返回 422；不能删除最后一个管理员，返回 409）、修改密码（说明修改自己的密码会撤销其他会话） | `settings.html` account / password |
| F8-5 | 审计日志：`GET /audit` 分页；diff 中敏感字段只显示“已修改” | `settings.html` audit |

**完成标准**：

- 设置修改后刷新页面仍保持；时区变更后，全站时间显示随之变化。
- 关闭护栏的请求带确认字段，并在审计日志中可见。

### F9 收尾与发布（4 人日）

| 任务 | 内容 |
|---|---|
| F9-1 | 全页面响应式走查（1440×900、390×844），总览与告警按 FR-UI-03 严格验收；浅色 / 深色；reduced-motion、reduced-transparency、contrast:more |
| F9-2 | 补齐缺少效果图的错误态与空态（Q-D2），统一做一轮设计走查 |
| F9-3 | 无障碍：键盘可达、焦点顺序、对比度（Q-D5）、状态不只靠颜色区分 |
| F9-4 | `mise run backend:build` 内嵌构建，用二进制托管的 WebUI 冒烟；检查深层路由回退 |
| F9-5 | 同步文档：[开发指南](../guides/development.md)、[文档导航](../README.md)、README 与 AGENTS.md 中“前端仍为脚手架”的现状描述，以及本计划的实施记录 |

**完成标准**：

- 第 9 节质量门禁全部通过。
- 内嵌二进制下完成一次完整演示：初始化 → 添加模拟器设备 → 配置规则与计划（dry-run）→ 停电场景。
- 剩余的设计偏差全部登记在册。

---

## 6. 依赖选型（待 FCP-0 确认）

按项目规范，新增长期依赖前需说明必要性和替代方案。未经确认不安装；确认后锁定当时的稳定版本并写入 `package.json`。

| 用途 | 建议 | 理由 | 替代方案 |
|---|---|---|---|
| 路由 | `react-router` | 十余个页面，需要路径参数、query 同步、会话守卫和前进 / 后退 | 基于 History API 自写小路由（无嵌套布局与数据加载） |
| 服务器状态 | `@tanstack/react-query` | 大量分页列表、写入后使缓存失效、请求去重与重试，以及由 SSE 事件触发失效 | 自写 `useResource` hooks，需要自行维护缓存与失效 |
| 实时状态 | 不引入；`useSyncExternalStore` 加自写 store | 数据形状固定，更新规则由契约明确定义 | zustand |
| 图表 | 不引入；自写 SVG | 每次最多 1000 个点、少量序列；效果图本身是 SVG，需要定制缺口与停电区间阴影 | uPlot |
| 表单 | 不引入 | 主要表单是定制编辑器（条件构建器、阶段编辑），通用表单库收益低 | react-hook-form |
| 动效 | P0 不引入 | P0 没有手势驱动的动画 | 引入手势时用 Motion |
| 组件测试 | devDependencies：`@testing-library/react`、`@testing-library/user-event`、`happy-dom`（或 `jsdom`） | `vp test` 需要 DOM 环境 | Vitest browser mode + Playwright（更重） |
| 国际化 | P0 不引入 | 只有简体中文；错误码与枚举文案已集中维护 | 做 P1 English 时再评估 |
| E2E（P1） | Playwright | TST-05 | — |

---

## 7. 待确认事项

每项列出默认处理。FCP-0 未给出其他结论时，按默认处理实施。

### 7.1 契约缺口（需后端确认是否补充）

| # | 问题 | 前端默认处理 |
|---|---|---|
| Q-C1 | `GET /devices` 不含数据源摘要。设备列表和设置页的护栏主机说明，都需要逐台请求 `GET /devices/{id}/sources` | P0 逐台请求，并发数限制在 4～6；建议后端在设备列表中附带数据源摘要 |
| Q-C2 | 添加设备需要两次写入（设备、数据源），第二步失败会留下没有数据源的设备 | 向导保留已创建的设备，允许重试保存数据源，也可由用户选择删除；不静默删除 |
| Q-C3 | `GET /events` 的 `type` 只支持单值，效果图为类型多选 | 类型筛选改为单选；如需多选，建议后端支持逗号分隔 |
| Q-C4 | 通知模板没有渲染预览接口，效果图有实时预览 | 用固定示例上下文做近似预览并标注“示例”，以保存时的 422 为准；建议后端提供预览接口 |
| Q-C5 | 计划运行视图不含冻结的计划快照（各阶段 `delay_s`、`offline_wait_s`、自关机标记），`trigger` 结构未写入契约 | 阶段数与步骤由 `steps` 推导。等待离线的总时长和“由哪条规则触发”暂不显示；如果改用当前计划配置推算，须标注“按当前配置”。建议后端返回快照摘要，并在契约中写明 `trigger` 结构 |
| Q-C6 | `GET /action-runs` 不能按状态过滤，效果图有「失败 / Dry-run」筛选 | 只过滤已加载的页面，并在界面注明；建议后端增加 `status` / `dry_run` 参数 |
| Q-C7 | 通知日志只有 `event_id`，效果图显示事件类型 | 显示“事件 #id”；规则、计划和测试消息的日志显示上下文类型 |
| Q-C8 | 没有字段目录接口，前端需要镜像 `catalog.go` | 手工镜像。联调期间，开发构建对未知字段键给出警告；建议后端提供只读目录接口，或生成供前端使用的 JSON |
| Q-C9 | 放电速率（%/min）、高温阈值 50 ℃（`temperature_high` 为 P1，设备 config 中没有对应键）、功率「待校准」徽标，都没有契约字段 | 放电速率由历史数据在前端计算，数据不足时不显示；高温阈值与「待校准」徽标不显示 |

### 7.2 设计稿需修正或补充

| # | 问题 | 默认处理 |
|---|---|---|
| Q-D1 | Profile 管理页（需求 P0：列表、查看、导入、导出、复制）没有效果图，侧栏标注“本轮未出图” | 用现有组件按列表-详情做简版，在 F9 补设计评审 |
| Q-D2 | 缺少的表单或状态，见下方 | 按 foundations 的组件规则实现，在 F9 统一走查 |
| Q-D3 | stale 的显示规则冲突：设计说明写显示「—」；设备详情效果图与 foundations 是旧值置灰加读数时间；后端 stale 字段保留最后读数 | 按 foundations 显示置灰的旧值和最后成功时间；invalid / unsupported 显示「—」 |
| Q-D4 | 与契约不符的效果图，见下方 | 前端按契约实现，并修正效果图 |
| Q-D5 | 设计系统细节，见下方 | 先按 CSS 原值迁移；原因提示改为可点按；对比度与时长在设计确认后调整 |
| Q-D6 | 规则页的 `dryoff` 状态实际表示“dry-run 开启，关闭时需确认”，名称容易误解；事件规则缺少关闭 dry-run 的确认；规则与渠道的删除没有确认框 | 两类规则统一确认；删除统一使用 DestructiveDialog |

Q-D2 缺少的表单或状态：

- 表单：主机新建与编辑、五类动作表单、Webhook 渠道表单、管理员新建 / 改名 / 删除。
- 错误态与确认：删除返回 409 及其他错误态、取消运行的确认对话框。
- 状态展示：`unknown` 步骤、等待离线超时（`result.offline_timeout`）、`plan_resumed` / `aborted`。
- 添加设备向导的「连接」步骤。

Q-D4 与契约不符的效果图：

- 通知显示“重试中（第 3 次）”，而契约只记录最终结果。
- `comm_restored` 在效果图中为 info 级别，后端为 warning。
- 通知预览显示“输入电压 0 V”，违反非 good 字段不显示为 0 的规则。
- 计划页写死“60 秒去抖”，实际取设备的 `recovery_confirm_s`。
- AYI9 密码提示“留空表示使用空密码”，但编辑时空串表示保留原值。
- 运行按钮的“运行 dry-run”文案不随真实执行切换。
- 登录与初始化（F1 已按契约处理）：
  - 效果图的密码框带示例值。实现中密码与令牌都不预填，初始化成功后只预填用户名。
  - 锁定页写死“15 分钟后自动解除”。实现中倒计时取 `Retry-After`，缺失时按 900 秒，文案改为“倒计时结束后自动解除”。
  - 效果图没有画 `invalid_setup_token`、`setup_closed`、网络失败三种状态。实现中令牌错误沿用登录错误态（红框加抖动），`setup_closed` 转到登录页并提示，网络失败保留输入并显示错误。
  - 效果图的侧栏用户行有 `chev-updown`，但没有画展开内容。实现为账户菜单，P0 只有「退出登录」一项；设置页的「退出登录」在 F8 接入。
- 监控页面（F2 已按契约处理）：
  - 总览停电主卡的「取消运行」在 F6 接通，F2 只提供「查看进度」。多台设备同时处于停电周期时每台一张主卡；不属于停电设备的活动运行（手动执行、其他设备）单独显示为计划条。
  - 去抖文案的确认时长取设备配置：疑似停电用 `outage_confirm_s`，市电恢复确认用 `recovery_confirm_s`，不写死秒数。
  - 放电计时与告警「已持续」在实时连接断开期间停在断开时刻，不外推；计划倒计时仍按本地时钟显示，并标注“实时连接已断开，状态可能已变化”。
  - 设备卡不显示「编辑」入口；设备详情页工具栏的「编辑」在 F3 接入，跳转到 `/devices?edit=<id>`。总览告警行链接到告警页。
  - 历史图按单位拆成多张小图，避免不同量纲共用纵轴；默认 1 小时、电池容量与 L1 负载，最多同时选 6 个字段。
  - 告警页不显示通知渠道链、「重试中」与「已通知 n 个渠道」（Q-C7）；事件视图没有停电周期和触发规则字段，主卡不显示这两项；类型筛选为单选（Q-C3）。
  - 窄屏时 `Page` 会隐藏页头右侧区域，告警页在内容区另放一份视图切换。
- 设备与 Profile（F3）：
  - Profile 页无效果图，按 Q-D1 做列表-详情简版。导入用浮层，支持粘贴或选择文件，先校验后导入；导出用下载链接。
  - `DestructiveDialog` 新增 `conflictMessage`：不支持停用的资源（如 Profile）在 409 时说明原因，不显示「改为停用」。
- 通知（F4）：
  - 不显示“重试中”和渠道引用计数（Q-D4、Q-C7）。
  - 预览中的非 good 字段与未知变量显示「—」，不显示为 0。

Q-D5 设计系统细节：

- `--label-3` 对白底的对比度约 3.6:1，却用于「—」和小字。
- 开关、对话框的动效时长与 foundations 不一致。
- reduced-motion 下，滑块直接跳变。
- 原因提示只能通过 hover 查看。

### 7.3 产品与安全决定

| # | 问题 | 默认处理 |
|---|---|---|
| Q-P1 | 手动真实执行脚本或 SSH 动作（例如自关机脚本）时，后端只要求 `confirm:true` | 默认 dry-run；切换到真实执行时显示目标与命令摘要，并再次确认。是否需要后端额外限制，在 CP-3 评审 |
| Q-P2 | 后端不要求为“关闭规则 dry-run”提供确认字段 | 只做界面确认（满足 FR-UI-06），不改后端 |
| Q-P3 | 缩短保留期会删除数据，但后端没有确认字段 | 保存前在界面确认，说明会在下一轮每小时清理时删除 |
| Q-P4 | 窄屏范围：FR-UI-03 为 P1，但效果图覆盖了全部页面 | 全部页面按效果图实现窄屏布局；P0 严格验收外壳、总览与告警，其余页面检查可用、不错位 |

---

## 8. 检查点

| 检查点 | 时间 | 内容 | 参与 |
|---|---|---|---|
| FCP-0 | 开工前 | 确认第 6 节依赖与第 7 节默认处理；后端给出 Q-C 各项补充或不补充的结论 | 项目方、前端、后端 |
| CP-2 | F3 结束 | 与[后端计划](backend-plan.md)的 CP-2 合并进行：联调初始化 / 登录、设备与数据源、快照 / 历史 / 事件 / 告警、SSE 断线重连；契约问题按“冻结后只增不改”处理 | 前端、后端 |
| FCP-1 | 每个里程碑结束 | 设计走查：与效果图对照（1440×900 / 390×844、浅色 / 深色），记录偏差 | 前端、设计 |
| CP-3 | F6 结束后（可选） | 可用计划页演示后端 CP-3 的关机编排（模拟器 + 测试 SSH 服务端）。前端不改变 CP-3 需要评审的已知边界；CP-3 通过前，真实环境不关闭 dry-run | 项目方、后端、前端 |
| CP-4 | F9 结束 | 首版验收包含 WebUI | 项目方 |

---

## 9. 质量门禁

每个任务合并前：

- `frontend/` 下执行 `pnpm check`、`pnpm test`；构建配置或依赖有变化时，追加 `pnpm build`；涉及内嵌发布时，在仓库根目录执行 `mise run backend:build`。
- 非显而易见的契约、边界与失败语义写简体中文注释。请求失败必须在界面显示 code 对应的文案并保留重试入口，不吞错误，不把失败显示为成功。
- 凭据不写入 `localStorage`、`sessionStorage`、URL 或控制台；输入框不预填；只显示 `*_set`。
- 安全交互有测试覆盖：
  - 手动执行动作或计划时，请求体总是包含 `dry_run`。
  - 计划名称完全一致时才允许提交。
  - 关闭护栏的请求带 `confirm_disable_guard`。
  - 删除请求带 `confirm:true`。
  - 确认对话框取消后不发送请求。
- 字段显示有测试覆盖：四种质量、缺失键、`omitempty` 造成的 0 值。
- 联调只使用临时数据目录与 loopback 模拟器，不用含真实设备配置的 `data/` 启动后端。只报告实际执行过的检查。

---

## 10. 风险与应对

| 风险 | 可能性 | 影响 | 应对 |
|---|---|---|---|
| 契约缺口集中在设备列表、计划运行、模板预览 | 中 | 页面返工 | FCP-0 前确认；前端默认处理不伪造数据，后端补充接口后只做增量适配 |
| 规则与计划编辑器复杂（嵌套结构、跨资源引用、校验） | 中 | F6 / F7 延期 | 前端只做结构性校验，语义以 422 `details` 为准；先完成编辑，再做模板与细节动效 |
| SSE 有损，断线与会话过期容易混淆 | 中 | 状态显示错误 | 重连前先查会话；以 `sync` 全量替换；reducer 有单元测试覆盖 |
| 字段目录镜像与后端不同步 | 低 | 字段名称或单位错误 | Q-C8；联调时检查未知字段键 |
| 缺少效果图的页面风格不一致 | 中 | 返工 | 严格复用 foundations 组件；F9 统一走查 |
| 误触发真实执行 | 低 | 主机被关机 | dry-run 默认、显式确认、开发只用模拟器与测试 SSH 服务端 |
| TypeScript 7 / Vite+ 与测试库的兼容性 | 低 | 工具链调整 | F0-10 先验证；不兼容时按第 6 节的替代方案处理 |

---

## 11. P1 待排期清单（首版之后）

| 项 | 需求 |
|---|---|
| English 界面（`language=en-US` 已可保存） | FR-UI-02 |
| Profile 编辑器与校准视图 | FR-PRF-05/06 |
| 关机计划阶段拖拽排序 | 需求第 10 节 |
| 关键流程 E2E（Playwright） | TST-05 |
| operator / viewer 角色界面、告警确认 | 需求 2.1、FR-EVT-04（后端 P1） |
| 由 OpenAPI 生成前端类型 | FR-API-04 |
| 手势动效（sheet 拖拽关闭、可滑动行，届时引入 Motion） | 设计说明「实现注意」 |
| 配置导出 / 导入、备份 / 恢复界面 | FR-DATA-05、FR-DEP-06（随后端 P1） |
| 设备默认凭据安全提示 | SEC-17（P2） |
