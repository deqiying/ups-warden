# 监控 API 与 SSE 契约（M4）

[文档导航](../README.md) · [开发指南](../guides/development.md) · [采集与状态语义](../guides/collection-state-history.md)

## 范围与版本

当前后端实现 M4 监控类 API、认证、审计及静态 WebUI 托管。以下 `/api/v1` 契约作为 CP-2 的后端联调基线；前后端双方的联调确认仍待执行。后续保持已有字段语义，破坏性变更须先评审。M5 通知、M6 规则/动作/主机、M7 关机计划及 M8 系统设置已实现，独立契约见[通知服务与 API](notifications-api.md)、[规则、动作与受控主机](rules-actions-hosts-api.md)、[关机计划](shutdown-plans-api.md)、[系统设置](settings-api.md)；前端 WebUI（F0～F9）已按这些契约实现，双方联调仍待验收。

- 下述路径除 `/healthz` 外均以 `/api/v1` 为前缀。
- 请求/响应 JSON、UTF-8，字段 snake_case，时间 RFC3339 UTC，ID 为整数，Profile 使用字符串 key。
- JSON 请求必须设置 `Content-Type: application/json`；最大 512 KiB，拒绝未知字段、重复键、null、过深结构及尾随 JSON。PATCH 只覆盖显式字段；设备 config 是整个对象替换，省略子字段采用[系统设置](settings-api.md) `device_defaults` 的当前值。
- 设备、数据源、用户、事件、告警、审计列表按 ID 降序，`limit=50`，范围 1～200；`before` 为排他的正整数游标。响应 `{"items":[],"next_before":0}`，0 表示无下一页。Profile 列表按 key 排序一次返回，快照返回全部运行设备。
- 删除要求 JSON `{"confirm":true}`，不能靠 HTTP DELETE 方法本身代替确认。
- 成功创建 201，读取/更新/删除 200；未知资源 404，约束/引用冲突 409，语义校验 422，限速 429。错误不包含凭据、SQL 或外部错误原文。

```json
{"code":"validation_failed","message":"配置校验失败","details":{}}
```

常用稳定错误码：`unauthorized`、`invalid_credentials`、`rate_limited`、`origin_rejected`、`csrf_invalid`、`invalid_json`、`json_required`、`body_too_large`、`validation_failed`、`resource_conflict`、`confirmation_required`、`device_not_found`、`source_not_found`、`profile_not_found`、`profile_invalid`、`builtin_readonly`、`snapshot_unavailable`、`diagnostic_unavailable`、`source_busy`、`source_password_wrong`、`probe_failed`、`internal_error`。

**配置已提交而运行恢复失败**返回 503 / `configuration_saved_runtime_error`；此时配置确实已经保存，不能将错误理解为事务回滚。读取配置修正后再次 PATCH/启用即可重试装配。数据库或审计事务失败则保持旧配置，并尝试恢复旧运行。

## 初始化、登录与用户

没有用户时，主进程启动向 stderr 输出一次 `首次初始化 setup token: ...`。令牌仅在内存，重启重新生成，首个管理员创建后失效；不写数据库、轮转日志或 HTTP 响应。不要把 stderr/终端日志公开。

| 方法与路径 | 请求 | 响应 |
|---|---|---|
| `GET /setup/status` | 无 | `{"setup_required":true}`，不会返回 token |
| `POST /setup` | `setup_token, username, password` | 首个 User；并发只允许一个成功，完成后返回 `setup_closed` |
| `POST /auth/login` | `username, password` | Cookie + `{user,csrf_token,expires_at}` |
| `GET /auth/session` | 会话 Cookie | `{user,csrf_token,expires_at}` |
| `POST /auth/logout` | 会话 Cookie、CSRF | 清除 Cookie、撤销会话，`{"ok":true}` |
| `GET /users` | 分页参数 | User 分页 |
| `POST /users` | `username, password` | 新管理员 User |
| `PATCH /users/{id}` | `username` | 修改后的 User |
| `PUT /users/{id}/password` | `password` | `{"ok":true}` |
| `DELETE /users/{id}` | `confirm:true` | 拒绝删除自己（422）或最后管理员（409） |

User：`id, username, role, created_at, last_login_at`，P0 role 仅 `admin`。用户名使用 1～64 个 ASCII 字母、数字、`.`、`_`、`-` 并统一小写；密码至少 8 个 Unicode 字符、最多 1024 字节，argon2id 64 MiB / t=3 / p=2。请求中的密码不出现在响应、审计或日志中。

Cookie 名 `ups_warden_session`，`HttpOnly; SameSite=Strict; Path=/`，直接 HTTPS 下加 `Secure`。数据库只保存会话 token 的 SHA-256 哈希。Cookie 固定在创建会话后 7 天到期，服务端同时强制空闲 24 小时与绝对 7 天期限；`expires_at` 返回当前空闲与绝对期限的较早值。SSE 心跳也会刷新服务端活跃时间，不依赖长连接续发 Cookie。自己改密保留当前会话、撤销其他会话；修改他人密码撤销其全部会话。

除初始化状态/初始化/登录之外，所有 API（含未知 API 路径与 SSE）均需管理员会话。认证后的非 GET/HEAD 请求必须带 `X-CSRF-Token`；所有写请求（含登录与初始化）若含 Origin，必须与请求 scheme/Host 相符。前端应同源访问；当前不信任 `X-Forwarded-*`，不启用跨域 CORS。反向代理必须保持一致的外部来源语义，不能伪造代理头绕过校验。

登录按用户名与 RemoteAddr IP 独立计数：15 分钟内失败 5 次锁 15 分钟，429 含 `Retry-After: 900`；不使用客户端提供的转发 IP。未知用户名也执行同成本密码计算。限速键和密码计算并发均有上限。

## 设备

| 方法与路径 | 行为 |
|---|---|
| `GET/POST /devices` | 分页列出 / 新建 |
| `GET/PATCH/DELETE /devices/{id}` | 查看 / 局部修改 / 确认删除 |
| `PUT /devices/{id}/enabled` | 必须提供 `enabled` 布尔值 |

设备字段：`id, name, note, location, tags, enabled, config`。创建必需 `name`，其余缺省为空字符串、空数组、enabled=true 和[默认状态配置](../guides/collection-state-history.md)。name ≤128 字节，note ≤4096，location ≤256；tags 最多 32 项，每项 1～64 字节。

配置修改与字段级审计同事务。运行中修改会先排空受影响设备的旧采集结果，再保存并恢复 actor/worker；重新开始未确认窗口，保留已确认停电周期和活动告警。禁用设备停止其 worker/actor；不删除历史。删除清理设备状态、事件与周期、级联数据源/历史；被其他业务记录引用时拒绝删除，不强行清除执行证据。

## 数据源与诊断

| 方法与路径 | 行为 |
|---|---|
| `GET/POST /devices/{id}/sources` | 分页列出 / 新建 |
| `PATCH/DELETE /sources/{id}` | 局部更新 / 确认删除 |
| `POST /sources/test` | 未保存配置的只读连接测试 |
| `POST /sources/{id}/test` | 使用已保存配置；请求 `{}` |
| `GET /sources/{id}/raw` | 最近一次内存诊断，暂无数据为 404 |

创建/更新字段：`protocol, host, port, params, profile_key, profile_version, poll_ms, timeout_ms, retries, enabled`。协议为 `snmp` / `modbus` / `ayi9-json`；host 不含 scheme、端口或路径。Profile 必须存在且 protocol/version 一致。每设备最多一个启用源（设备已禁用也不能配置两个启用源）。

- `profile_version` 默认 1；poll_ms 默认 3000、范围 200～3600000；timeout_ms 默认 1000、范围 1～60000；retries 默认 0、范围 0～10；enabled 默认 true。
- SNMP params：`{"version":"2c"}` 或 `{"version":"1"}`；Modbus params：`{"unit_id":1}`（省略沿用 Profile）；AYI9 params：`{}`。
- SNMP 凭据字段 `community`；AYI9 凭据字段 `password`。响应仅有 `community_set/password_set`，不返回明文、掩码或密文。更新省略/空串保留；清除使用 `clear_secrets:["community"]` 或 `["password"]`，不得同时提供新值。更换协议清除旧协议凭据。无 SNMP community 时驱动默认 public，无 AYI9 password 时用空串。
- 列表中的可选 `status`：`last_ok_at, latency_ms, failures, error_code`；无活动 worker 时省略。`error_code=password_wrong` 表示设备返回 AYI9 密码错误（103）。地址和配置仅对已认证管理员可见。
- 保存执行离线配置/只读计划/驱动构造校验，不做额外 Probe；启用源的正常调度会按配置连接。DNS 必须解析为单个 IP，别名与 IPv4-mapped IPv6 共用同 IP 串行约束。

测试请求使用相同 source 字段（不含 id/device_id/status），未指定 timeout_ms 时总限额 5 秒；与正常采集共用信号量。返回：`latency_ms, device_info, snapshot, recommendations, raw, trace`；失败仅返回稳定码（忙为 409 / `source_busy`，AYI9 密码错误为 502 / `source_password_wrong` 并提示“设备密码错误（103）”，其他探测失败为 502 / `probe_failed`），不回显凭据或设备错误原文。`raw` 有 `protocol, at, snmp, modbus, ayi9, errors`，Modbus 键为 `FC:地址`（例如 `3:28`）。Raw/Trace 是诊断，不是统一业务数据，不入历史或审计；已知凭据的设备回显也会过滤。

`device_info`：`model, manufacturer, firmware, agent_version, identity`；`recommendations` 每项为 `key, score, reason`。Trace 每项为 `field, source?, raw?, decoded?, final, quality, reason?`；SNMP 字节字段使用 Base64，凭据过滤先解码再替换后编码，覆盖带前后缀的回显。

## Profile

| 方法与路径 | 请求 / 行为 |
|---|---|
| `GET /profiles` | `items`：key/name/protocol/version/builtin |
| `GET /profiles/{key}` | 上述字段加 `content`（声明原文） |
| `POST /profiles` | `{content}`，严格 YAML/JSON 新建/导入 |
| `POST /profiles/{key}/copy` | `{key,name}`，复制为新的自定义 Profile，version=1 |
| `PUT /profiles/{key}` | `{content}`，key/protocol 不变，version 必须递增 |
| `DELETE /profiles/{key}` | `{confirm:true}`；被数据源引用则 409 |
| `GET /profiles/{key}/export` | YAML 下载，保留声明内容 |
| `POST /profiles/validate` | `{content}`，成功 `{valid:true,profile:{...}}` |

内置 Profile 不可覆盖、修改或删除，须先复制。自定义更新与引用源的 profile_version 同事务递增，再重载受影响设备。声明最大 256 KiB，复用现有 Profile 校验和只读安全限制；校验失败的 details 在可识别时提供 line 和受限 path，不回显输入值。

## 快照、历史、事件与审计

- `GET /snapshots`：`{items:[DeviceState]}`，全部运行设备按 id 排序；`GET /devices/{id}/snapshot` 返回单个 DeviceState，停用或无运行快照为 404。
- DeviceState/Field 使用既有[统一模型](../../backend/internal/model/model.go)的 JSON 标签；`schema_version=1`，数值质量与 unknown 语义见采集指南。只消费统一字段，不引用协议位置。
- `GET /devices/{id}/history?from=...&to=...&fields=battery_capacity_pct,battery_voltage&resolution=auto&max_points=1000`：必需合法 `[from,to)`；resolution 为 auto/raw/1m；max_points 1～1000。fields 只接受 History=true 的统一字段实例。返回 `{resolution,points:[{at,fields:{字段:{min,max,avg,count}}}]}`；不隐式刷新未提交队列。
- `GET /events` 支持分页及 `device_id,type,severity,from,to`；`GET /alerts` 相同过滤但只列 active=true。事件：`id,device_id,type,severity,started_at,cleared_at,active,summary`，summary 仅统一字段，时间 UTC。ack 属于 P1 未实现。
- `GET /audit`：分页返回 `id,at,user_id,ip,kind,target,diff,result`；diff 普通字段为 `{before,after}`，敏感字段仅“已修改”。用户被删除时 user_id 可空；管理员身份变更、登录/登出、设备/源/Profile 修改均记审计。

## SSE

`GET /stream` 使用同一 Cookie 认证，`Content-Type: text/event-stream`。不回放 Last-Event-ID，每次重连重新全量同步。事件格式：

```text
event: sync
data: {"schema_version":1,"devices":[],"alerts":[],"plan_runs":[]}

: heartbeat
```

| 事件名 | data |
|---|---|
| `sync` | 全量 devices（DeviceState）、活动 alerts（Event 视图）、plan_runs（倒计时或执行中的关机计划运行完整视图） |
| `device_state` | 单个 DeviceState |
| `event` | 新业务事件 `id,device_id,type,severity,at,active,clears,summary`，按 id 去重 |
| `alert` | `{device_id,items:[活动 Event 视图]}`，替换该设备的活动告警列表；空数组表示全部解除 |
| `notify_failed` | M5 单目标最终失败的通知日志；字段及查询补偿见[通知契约](notifications-api.md#通知日志与-sse) |
| `plan_progress` | M7 关机计划运行状态落库后的完整运行视图（含 steps），字段见[关机计划](shutdown-plans-api.md#运行记录与-sse) |

设备/数据源/Profile 配置新增、修改、禁用、删除后也会发送 sync，前端用全量替换设备/告警集合。先订阅后读取快照，连接建立边界可能重复事件，前端按 event.id 去重；已同步的较新状态不会被队列内较旧状态覆盖，已移除设备的积压更新不会重新引入该设备。告警清除即使不产生独立恢复事件，也会发送 alert 更新。通知配置修改不触发设备 sync。

每 15 秒注释心跳；每次心跳重新认证，注销、改密撤销或绝对期限到期后最长一个心跳周期关闭流。每订阅者 256 条缓冲，溢出通过独立信号使订阅失效，不排空积压帧；正在写入的帧最多等待 5 秒期限。前端断开后重连并等待 sync，不把断流期间当作连续有效观测。`notify_failed` 重连后通过通知日志补齐；`plan_progress` 以重连 sync 的 `plan_runs` 为准，已结束运行通过 `/plan-runs` 查询。

## WebUI 与开发联调

`mise run backend:build` 先执行现有前端 build，再由 `scripts/build-webui.mjs` 安全复制到 `backend/internal/webui/dist` 后 embed；产物忽略，不提交。直接 `go run` 且 dist 未构建时返回 503 提示页；构建后托管当前 React WebUI，深层 GET/HEAD 路由回退 index（本机已用内嵌二进制验证带 query 的深层路由；不存在的 `/assets/*` 路径同样回退为 200 的 index）。

API/healthz 路径不回退 SPA；拒绝隐藏文件、遍历和目录列表。index no-cache，带 hash 的 assets immutable。开发时仍可使用 Vite 代理 `/api`，但认证/Origin 必须保持同源；完整初始化和登录业务 UI 尚待前端实现。
