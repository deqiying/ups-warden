# 界面效果图与设计规范

[返回文档入口](../README.md)

本目录是按后端 M4～M8 接口契约重新设计的前端视觉方案：Apple 生产环境 Web / macOS 设计语言，静态 HTML + 内联 SVG。它是 `frontend/` 实现的视觉基准，**不是已实现的页面**，不连接后端，也不调用任何 API。

## 打开方式

- 用浏览器直接打开 [index.html](index.html)（`file://` 即可）。所有资源都在本目录内，没有外部字体、图片或脚本。
- 画廊可切换桌面 / 手机视口与浅色 / 深色；点按缩略图打开可交互效果图，状态标签直达对应场景。
- 视觉规则、组件与动效以 [foundations.html](foundations.html) 为准。

## 文件

| 文件 | 内容 |
|---|---|
| `index.html` | 效果图画廊：全部页面缩略图、说明、对应接口与状态直达链接 |
| `foundations.html` | 设计规范：原则、颜色与状态语义、字体、材质、形状与布局、组件、数据质量、安全交互、动效、图标、无障碍 |
| `overview.html` | 总览 |
| `device-detail.html` | 设备详情 |
| `devices.html` | 设备列表与添加设备向导 |
| `alerts.html` | 告警与事件 |
| `rules.html` | 规则 |
| `shutdown-plans.html` | 关机计划 |
| `hosts-actions.html` | 受控主机与动作 |
| `notifications.html` | 通知渠道、模板与发送记录 |
| `settings.html` | 系统设置、管理员与审计日志 |
| `login.html` | 登录与首次初始化 |
| `assets/apple-ui.css` | 共享设计 token 与组件样式（浅色 / 深色、窄屏、无障碍媒体查询） |
| `assets/apple-ui.js` | 效果图专用脚本：图标、侧栏 / 标签栏、评审状态、轻交互 |

## 页面与接口对照

| 页面 | 评审状态（`?state=`） | 主要接口 |
|---|---|---|
| 总览 | `outage` 停电中、`normal` 全部正常、`offline` 实时断开 | `GET /snapshots`、`GET /alerts`、`GET /plan-runs`、`GET /stream` |
| 设备详情 | `battery` 电池供电、`normal` 市电、`lost` 通讯中断 | `GET /devices/{id}/snapshot`、`GET /devices/{id}/history`、`GET /events` |
| 设备 | `list`、`proto`、`test`、`pwd` 密码错误、`thresholds`、`delete` | `/devices`、`POST /devices/{id}/sources`、`PUT /devices/{id}/enabled`、`POST /sources/test`、`GET /profiles` |
| 告警与事件 | `active`、`failed` 通知失败、`empty`、`history`、`filter` | `GET /alerts`、`GET /events`；SSE `alert` / `event` / `notify_failed` |
| 规则 | `edit` 条件规则、`event` 事件规则、`dryoff` 关闭 dry-run、`templates` | `/rules`、`GET /rules/templates`、`GET /rule-logs` |
| 关机计划 | `edit`、`countdown`、`running`、`confirm` 手动执行 | `/plans`、`POST /plans/{id}/run`、`GET /plan-runs`、`POST /plan-runs/{id}/cancel`；SSE 计划进度 |
| 主机与动作 | `hosts`、`fp` 确认指纹、`actions`、`run` 手动执行、`runs` | `/hosts`、`POST /hosts/{id}/hostkey/fetch\|confirm`、`/actions`、`POST /actions/{id}/run`、`GET /action-runs`、`GET /scripts` |
| 通知渠道 | `channels`、`test` 测试已入队、`templates`、`logs` | `/channels`、`POST /channels/{id}/test`、`GET\|PUT /notify-templates`、`GET /notify-logs` |
| 设置 | `general`、`retention`、`defaults`、`security`、`guard` 关闭护栏、`account`、`password`、`audit` | `GET\|PATCH /settings`、`/users`、`PUT /users/{id}/password`、`GET /auth/session`、`GET /audit` |
| 登录与初始化 | `login`、`error`、`locked` 频率限制、`setup` | `GET /setup/status`、`POST /setup`、`POST /auth/login` |

字段、错误码与边界以 [docs/reference](../reference/) 下的契约为准；效果图与契约不一致时按契约修正效果图。

## 视觉基础

- **字体**：系统字体栈。Apple 平台为 SF Pro / PingFang SC，其他平台回退 Segoe UI / Microsoft YaHei；指标数字用 SF Rounded（`ui-rounded`），代码与主机名用 SF Mono（`ui-monospace`）。字距随字号变化：大标题负字距，正文接近 0。
- **颜色**：Apple 系统色，只通过语义 token 引用（`--green`、`--green-ink`、`--green-tint` 等），浅色 / 深色各一套。强调色蓝色只用于交互，不表示状态。
- **材质**：侧栏、工具栏、标签栏、浮层使用半透明材质（`backdrop-filter`），内容在其下滚动；模态任务配合暗化遮罩。
- **布局**：桌面为侧栏 + 内容区，列表-详情页用分栏；宽度 ≤860px 时侧栏变为底部标签栏，分栏改为上下叠放，宽表格重排为两行列表或横向滚动。

## 状态色语义

| 颜色 | 含义 | 示例 |
|---|---|---|
| 绿色 | 市电供电、正常、成功 | 「市电」胶囊、发送成功 |
| 橙色 | 电池供电、倒计时、将真实执行 | 停电横幅、关机倒计时、关闭 dry-run 后的规则 |
| 黄色 | 待确认、旁路 | 疑似停电（去抖中）、旁路供电 |
| 灰色 | 通讯中断、数据过期、已停用 | 失联设备、过期读数 |
| 红色 | 严重告警、删除、关闭本机 | 低电量严重告警、删除按钮、自关机阶段 |
| 靛蓝 | dry-run（只记录不执行） | dry-run 徽章与运行记录 |
| 蓝色 | 强调色，仅用于交互 | 主要按钮、链接、选中项 |

状态不只靠颜色区分：每个状态同时带图标或文字。

## 数据质量与安全交互

- **字段质量**：非 good 字段显示「—」并给出原因（无效、不支持、过期），绝不显示为 0；估算值带「估算」标记；过期数据整体降低对比度并显示最后成功时间。通知模板中的时长使用 `_text` 变量（如 `{{runtime_estimate_text}}`）。
- **dry-run**：新建规则与手动运行默认 dry-run；关闭 dry-run、关闭 HTTP 护栏需要确认对话框，重新开启不需要。手动运行动作或计划时总是显式发送 `dry_run`。
- **关机计划**：手动执行需输入计划名称（`confirm_name`）；倒计时期间始终可取消。
- **删除**：使用破坏性确认对话框，说明会一并清除的内容；被引用时提示改为停用（后端返回 409）。
- **告警**：没有「确认 / 知道了」按钮，告警随条件解除自动结束（后端不提供 ack）。
- **设置的保存方式**：通用设置即时生效；数据保留与设备默认值使用显式保存栏（还原 / 保存），因为 `PATCH /settings` 整体校验，且缩短保留期会删除数据。

## 评审机制

效果图用 `body[data-states]` 声明可评审的状态，`assets/apple-ui.js` 据此渲染右下角的「效果图状态」浮条。该浮条只用于评审，**不属于产品界面**。

| URL 参数 | 作用 |
|---|---|
| `?state=<key>` | 打开指定状态，例如 `rules.html?state=dryoff` |
| `?theme=dark` / `?theme=light` | 强制深色 / 浅色；省略时跟随系统 |
| `?embed=1` | 缩略图模式，隐藏评审浮条（画廊使用） |

页内的分段控件、向导按钮等通过 `data-goto` / `data-match` 切换同一组状态，与浮条和 URL 保持同步。

## 实现注意

- **样式迁移**：`apple-ui.css` 的 token 与组件可直接迁入 `frontend/`；组件类名只是效果图约定，React 实现可重新组织，但颜色、字号、间距、圆角与动效时长保持一致。
- **动效**：效果图只用 CSS 过渡演示按下反馈、分段控件滑块、浮层出现等非拖动动效。前端实现中，可拖动或可打断的元素（底部面板、抽屉、可滑动行）应使用弹簧动画库（如 Motion，默认 `bounce: 0` 临界阻尼），从当前值开始动画并继承手势速度；不要用 CSS 过渡或关键帧实现手势驱动的动画。
- **减少动态效果**：`prefers-reduced-motion` 时位移与弹簧改为淡入淡出；`prefers-reduced-transparency` 时材质改为不透明；`prefers-contrast: more` 时加强边框。
- **图标**：参照 SF Symbols 比例绘制的 24×24 描边 SVG，不是 SF Symbols 本身；实现时可沿用 `apple-ui.js` 中的路径。
- **实时数据**：效果图中的时间与数值是固定示例（2026-10-27 14:21 停电场景）；实现时由 SSE 与查询接口驱动。

## 示例数据约定

- 设备、主机、渠道与账户均为虚构；主机名使用 `.lan` 占位（如 `ups-a.lan`），不含真实设备地址、community 或密码。
- 审计日志中的客户端 IP 只使用 RFC 5737 文档地址段（`192.0.2.0/24`、`198.51.100.0/24`）。
- 新增或修改效果图时遵守同样约定，并保持跨页面数据一致（设备数、名称、主机名、时间线）。

## 限制

- 效果图未经可用性测试，也未与真实后端联调；以浏览器在 1440×900 与 390×844 视口、浅色 / 深色下的人工检查为准。
- 非 Apple 平台没有 SF 字体，界面会回退到系统字体，字形与字距与效果图在 macOS 上的呈现略有差异。
