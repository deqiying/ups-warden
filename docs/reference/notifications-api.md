# 通知服务与 API 契约（M5）

[返回文档导航](../README.md) · [认证、错误结构与 SSE 基线](monitoring-api.md)

## 范围与运行边界

M5 提供 Webhook、Telegram、模板、独立发送队列、重试、通知日志和管理 API。所有端点以 `/api/v1` 为前缀，要求管理员会话；写操作遵循现有 CSRF、同源检查、严格 JSON 和配置审计约定。

**通知服务不直接订阅设备事件，也不向所有启用渠道广播。** 除渠道测试外，M6 已通过显式规则与通知动作调用 `notify.Service.Enqueue`，M7 关机计划向计划配置的渠道发送运行通知；均只接受统一模型。配置与 dry-run 语义见[规则、动作与受控主机](rules-actions-hosts-api.md)、[关机计划](shutdown-plans-api.md#通知)。本机模拟停电通知通过测试，但不等同于真实渠道验收；前端仍无通知业务页面。

- 每渠道一个工作协程、256 条等待队列；慢渠道不阻塞其他渠道。入队只表示接受，不表示送达；队列满拒绝本次请求，不静默丢弃已有任务。
- 首次发送失败后额外重试 3 次，间隔 2、4、8 秒，单目标最多 4 次尝试。Telegram 429 的 `parameters.retry_after` 大于当前退避时采用该等待值，等待可取消。
- 每次 HTTP 请求最多 10 秒；不跟随重定向，不自动使用环境代理。Telegram 支持显式 HTTP/HTTPS 代理，TLS 使用系统证书校验。
- Telegram 多 Chat ID 按配置顺序分别发送、重试、落库；已成功的目标不因另一个目标失败而重发。
- 队列只在内存中，重启不重放。渠道修改/停用和服务关闭会取消在途请求，待发送项记录为取消，不再外发。网络超时或取消不能证明接收端没有处理请求，不能承诺 exactly-once。
- 发送结果落库成功后才发布失败 SSE。存储失败停止该渠道并记录安全诊断，后续入队拒绝，关闭返回错误；不将落库失败伪装为发送成功。修复存储问题后可重新保存渠道或重启服务。
- 渠道配置与审计同事务；事务失败重新加载旧配置，请求取消不影响恢复过程。保存成功但运行恢复失败返回 `503 / configuration_saved_runtime_error`，不能当作事务回滚。

## 渠道管理

| 方法与路径 | 行为 |
|---|---|
| `GET /channels` | 列表，`before`、`limit` 游标分页，默认 50、最大 200，ID 倒序 |
| `POST /channels` | 新建，返回 201；需要 `name`、`type` 及该类型必需配置，`enabled` 默认 true |
| `GET /channels/{id}` | 单个渠道 |
| `PUT /channels/{id}` | 更新，返回 200；顶层省略字段保留，提供 `config` 时整体替换该对象并补默认值 |
| `DELETE /channels/{id}` | 请求体 `{"confirm":true}`；有通知日志等外键引用时返回 409，不删除历史证据，可改为停用 |
| `POST /channels/{id}/test` | 请求体 `{}`，启用渠道走相同队列/模板/重试路径；返回 202，不等待最终结果 |

渠道 `type` 仅支持 `webhook`、`telegram`。切换类型会清空旧类型的配置与凭据，必须一并提供新类型必需字段。`name` 为非空、最多 128 字节。

### Webhook 请求示例

```json
{
  "name": "本机测试接收端",
  "type": "webhook",
  "enabled": true,
  "url": "http://127.0.0.1:9090/notify",
  "headers": {"X-API-Key": "replace-with-test-key"},
  "signing_key": "replace-with-test-signing-key",
  "config": {
    "method": "POST",
    "format": "json",
    "body_template": "{\"event\":\"{{event.type}}\",\"device\":\"{{device.name}}\",\"capacity\":\"{{battery_capacity_pct}}\"}"
  }
}
```

- `url` 必需，只支持 HTTP/HTTPS，可带 query，但不允许 userinfo 或 fragment。
- `method` 默认 POST，只支持 POST/PUT；`format` 固定 `json`。`body_template` 可省略，届时使用对应事件的全局模板。
- `headers` 可选，自定义值整体加密；默认 `Content-Type: application/json`。拒绝 CR/LF、重复大小写 Header、Host、Content-Length、Transfer-Encoding 和两个签名保留头。
- 设置 `signing_key` 后增加 `X-UPS-Warden-Timestamp: <Unix 秒>` 和 `X-UPS-Warden-Signature: sha256=<hex>`；HMAC-SHA256 输入是 `timestamp + "." + 实际请求体`。
- **2xx 即成功**，不以响应正文判断成败；关闭正文，不因其内容、大小或读取错误重复发送。其他状态失败并重试，不跟随重定向。

### Telegram 请求示例

```json
{
  "name": "Telegram 测试",
  "type": "telegram",
  "enabled": true,
  "token": "replace-with-test-bot-token",
  "config": {
    "chat_ids": ["123456", "-100123456"],
    "format": "html"
  }
}
```

- `token` 和 `chat_ids` 必需。Chat ID 为 1～32 个不重复的非零 int64 字符串或 `@username`；用户名大小写不敏感去重。
- `api_base` 默认 `https://api.telegram.org`，可设置自建反向代理；仅 HTTP/HTTPS，禁止 userinfo、query、fragment。不得在该非敏感字段中放凭据。
- 顶层 `proxy_url` 可选，可携带代理 userinfo，整体加密。仅支持 HTTP/HTTPS 代理，不支持 SOCKS。
- `format` 默认 `text`，可为 `html`。POST 到 `{api_base}/bot{token}/sendMessage`，请求 JSON 含 `chat_id`、`text`；HTML 时设置 `parse_mode: "HTML"`，text 时省略。
- 同时满足 HTTP 2xx 与 JSON `ok=true` 才成功；响应最多读取 64 KiB。响应中的 description、原始 URL 错误和 Token 不进入错误文本、日志或 SSE。

### 敏感字段约定

`url`、`headers`、`signing_key`、`token`、`proxy_url` 均只写不读，整体以 AES-256-GCM / AAD `channels.secret` 存储。Webhook URL 可能含路径或 query 凭据，因此不回显完整 URL，而非仅隐藏 query。

响应仅包含 `id,name,type,config,enabled,url_set,headers_set,signing_key_set,token_set,proxy_url_set`。凭据省略或空字符串保留，空 Header 对象也保留；清除时用 `clear_secrets`，例如：

```json
{"clear_secrets":["headers","signing_key"]}
```

同一字段不能同时设置和清除，重复或未知清除键被拒绝。清除后仍需满足完整渠道校验，必需的 URL/Token 不能留下空值，即使渠道已停用。凭据轮换写审计，但只记录“已修改”。`name`、模板和 `config` 是非敏感业务文本，请勿手工粘贴凭据。

### 测试消息与错误

成功入队返回：

```json
{"accepted":true,"channel_id":1}
```

测试事件类型为 `test`，无持久化事件 ID，设备名为“UPS Warden 测试设备”，未采集字段显示 `—`。停用渠道返回 `409 / channel_disabled`；队列满返回 `429 / notification_queue_full`；关闭、正在变更或工作协程不可用返回 `503 / notification_unavailable`。未接受的请求没有发送结果日志；测试请求审计 `channel.test` 的 `requested` 也不等于送达。

## 模板管理与渲染

- `GET /notify-templates` 返回 `{items:[{event_type,format,body,customized}]}`：15 种统一模型事件、`test`、M6 条件规则的 `rule_triggered` / `rule_recovered`，以及 M7 关机计划的 `plan_started` / `plan_step_failed` / `plan_cancelled` / `plan_completed` / `plan_resumed` / `plan_aborted`，每种有 json/html/text 默认模板，共 72 项。规则与计划上下文不新增伪造的设备事件记录。
- `PUT /notify-templates` 写入一项 `{event_type,format,body}`，或通过 `{event_type,format,reset:true}` 删除覆盖、恢复默认；`reset:true` 不可同时提供非空 body。
- 保存校验事件、格式、变量、语法和 JSON 合法性；未知变量或格式返回 422。模板及渲染输出各限 64 KiB。
- 语法为严格 `{{path}}`，不执行函数、条件或方法，不接受占位符内空白。例如 `{{device.name}}`、`{{event.type}}`、`{{event.severity}}`、`{{device.id}}`、`{{battery_capacity_pct}}`、`{{input_voltage[1]}}`、`{{on_battery_elapsed}}`、`{{runtime_estimate}}`、`{{time}}`、`{{detail}}`。`{{detail}}` 仅计划通知填充（计划名称、运行 ID、阶段/步骤与原因），其他事件渲染为空串。
- 字段来自统一模型目录；按相字段必须使用 `[1]`～`[3]`。非 `good` 字段渲染为 `—`，不能当作零。所有 Duration 字段提供 `_text`，例如 `{{on_battery_elapsed_text}}` 显示 `12 分 30 秒`；时间采用进程本地时区 RFC3339，系统时区设置 API 尚未实现。
- JSON 变量按**字符串内容**转义，模板自行加双引号；HTML 只转义变量值，固定标签由管理员编写；纯文本原样输出。
- 渲染优先使用 Webhook 渠道 `body_template`，否则使用全局事件/格式模板；失败时回退该事件的内置默认模板，普通日志记录安全告警，发送结果 `template_fallback=true`。数据库读取失败不伪装成“无覆盖模板”；默认模板也不能渲染时拒绝入队。

## 通知日志与 SSE

`GET /notify-logs` 支持 `before`、`limit`（默认 50，最大 200）、`channel_id`、`event_id`、`ok=true|false`、`from`、`to`（RFC3339，`from <= at < to`），按 ID 倒序返回 `{items,next_before}`。

每项包括：

| 字段 | 语义 |
|---|---|
| `id`、`channel_id`、`event_id` | 日志/渠道/关联事件 ID；测试消息、条件规则与关机计划上下文 event_id 为 null |
| `at` | 该目标完成时间，UTC RFC3339 |
| `target` | Webhook 为 `webhook`，Telegram 为对应 Chat ID |
| `summary` | 渲染内容脱敏后最多 200 个 Unicode 字符，覆盖原文及 JSON/HTML 转义的凭据形式 |
| `ok`、`error` | 是否成功、无凭据的错误类别/HTTP 状态；成功时 error 为空 |
| `attempts` | 0～4，取消未发送为 0；含首次发送 |
| `duration_ms` | 当前目标发送与重试等待耗时，不含队列等待 |
| `template_fallback` | 是否使用了默认模板回退 |

`GET /stream` 在已有 M4 SSE 事件之外增加 **`notify_failed`**，data 为上述完整单目标失败结果，**落库成功后才推送**。没有设备快照时也可收到测试通知的失败事件；前端按日志 id 去重。重连 sync 不回放失败记录，应再查询 `/notify-logs` 补齐，不能依赖有损总线保存通知证据。

日志沿用历史维护模块的默认 365 天保留期。`0002_notify_results.sql` 仅增量增加 target/attempts/template_fallback；旧日志保留，缺少接收目标时 target 为空。不会改写初始迁移，也不会级联删除被引用的渠道或事件。

## 验证与限制

通知测试使用临时 SQLite、内存 `httptest` handler、loopback 假服务器和 `synctest`。覆盖请求内容/签名、两类渠道、代理、重定向拒绝、2/4/8 秒重试、429、Token 脱敏、模板质量/转义/回退、队列边界/隔离、逐目标结果、配置审计回滚、取消落库、重启不重放、SSE 和迁移保留旧日志；具体执行证据见[开发计划 M5](../planning/backend-plan.md#m5-通知4-人日)。

未连接真实 UPS 或真实 Webhook/Telegram。CP-1 真机、CP-2 双方联调、Linux 实机、race 与容量验收仍未完成；真实通知投递需另行提供测试目标并明确授权。
