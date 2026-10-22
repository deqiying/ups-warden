# 规则、动作与受控主机 API（M6）

[返回文档导航](../README.md) · [监控与认证契约](monitoring-api.md) · [通知契约](notifications-api.md) · [关机计划](shutdown-plans-api.md)

## 范围与安全边界

M6 提供规则评估、通知 / HTTP / 本地脚本 / SSH 动作、受控主机和执行记录。端点前缀均为 `/api/v1`；要求管理员会话，写操作遵循 CSRF、同源检查、严格 JSON 与审计约定。配置和审计同事务，审计失败不保存配置。测试只使用临时数据库、loopback 模拟器和假服务器。

**关机计划编排见 [M7 关机计划 API](shutdown-plans-api.md)**：规则通过 `plan` 动作启动计划，阶段编排、等待离线、倒计时取消、周期幂等与重启恢复都由计划服务负责，本文只描述动作侧契约。真实设备 CP-1、双方联调 CP-2、CP-3 关机编排评审、真实渠道/主机与 Linux 实机验收仍待执行。

- 新建规则默认 `dry_run=true`，不会自动生成规则或选择动作。内置模板默认还会 `enabled=false`。
- dry-run 会保存执行记录和脱敏预览，不解析动作目标 DNS、不连接 SSH/HTTP、不启动脚本、不入通知队列。
- 手动动作即使 dry-run 也需要 `confirm:true`。**未传 `dry_run` 的手动动作按真实执行处理**，调用方应显式填写。
- HTTP 动作不得指向已配置 UPS 数据源主机；该护栏默认开启。脚本与 SSH 是管理员配置的受控主机能力，不构成操作 UPS 本体的许可。
- 有历史执行记录或配置引用的资源删除返回 409，不能通过级联删除抹去证据。

## 规则

| 方法 | 路径 | 说明 |
|---|---|---|
| GET / POST | `/rules` | 分页列表 / 创建 |
| GET / PUT / DELETE | `/rules/{id}` | 查询 / 更新 / 删除；删除需 `confirm:true` |
| GET | `/rules/templates` | 五个表单模板 |
| GET | `/rule-logs` | 规则评估与派发记录 |

列表使用 `{items:[],next_before:0}`；`before` 是 ID 游标，`limit` 默认 50、最大 200，按 ID 倒序。PUT 支持部分字段更新；`scope`、`trigger`、动作列表一旦提交则整体替换该字段。

事件规则示例（动作 ID 必须已存在）：

```json
{
  "name": "停电与恢复通知",
  "enabled": true,
  "dry_run": true,
  "scope": {"type": "device", "device_ids": [1]},
  "trigger": {"type": "event", "events": ["mains_lost"]},
  "actions": [{"action_id": 7}],
  "recovery_actions": [{"action_id": 7}],
  "cooldown_s": 60
}
```

条件触发部分示例：

```json
{
  "type": "condition",
  "any_of": [
    {"all_of": [
      {"field": "power_state", "op": "eq", "value": "on_battery"},
      {"field": "on_battery_elapsed", "op": "gte", "value": 600}
    ]},
    {"all_of": [
      {"field": "battery_capacity_pct", "op": "lte", "value": 40, "for_s": 10}
    ]}
  ]
}
```

### 评估契约

- 作用范围为 `device` / `tags` / `all`；标签按任一标签匹配。只评估启用设备，每个“规则 × 设备”独立维护实例。设备最多 100 个、标签最多 32 个。
- 条件只支持两层 OR of AND，每层最多 16 项。运算符为 `eq/ne/gt/gte/lt/lte`；布尔、文本与枚举只支持 `eq/ne`。value 必须匹配统一字段类型，不能传字符串数字、协议地址或递归表达式。
- 使用统一字段目录；按相字段不带相号时取所有 `good` 相的最大值，显式 `[1]`～`[3]` 只读取对应相。
- 每次收到已提交的 `DeviceUpdate` 都评估，包含状态 actor 的每秒 tick；不依赖有损 SSE 总线。`on_battery_elapsed` 由状态层推进，失联 hold 期间也可继续增长。
- `for_s` 是条件连续成立的秒数，最大 604800。条件不成立或质量非 `good` 时清除该条件计时；重启丢弃未确认的连续观测窗口，不把停机时间当成有效观测。
- 非 `good` 字段不能促成新命中；原因及字段值/质量写日志，只在原因变化时记录。质量不足本身不作为恢复证据：已触发实例保持边沿，直到所有 OR 分支都有可确定为假的条件，才执行恢复动作。这样缺测、失联、重启不会伪造恢复或重复触发。
- 条件从假变真只触发一次。`cooldown_s` 为 0～604800，冷却内的边沿记为 `skipped_cooldown`，即使冷却结束也不补发；被跳过且未执行的边沿不产生恢复动作。
- 事件规则使用状态层事件与 `Clears` 配对执行恢复动作；没有独立恢复事件类型的告警，通过内部清除标记传递，不改变 SSE 事件目录。已处理事件 ID 与时间共同去重，历史清理后复用 ID 不会屏蔽新事件。多个活动事件分别配对恢复，恢复不受触发冷却阻止。
- 新建规则至少选择一个动作，每种动作列表最多 32 项，不允许同一列表重复 ID。只校验动作存在（含 `plan` 动作），不自动启用渠道或确认主机指纹。
- 改名、切换 dry-run、修改动作列表/冷却不会重放已消费边沿。启停保留已消费边沿、清除未确认计时；修改 `scope` 或 `trigger` 会开始新实例，旧日志保留。
- 保存包含 `runtime_estimate` 的规则返回顶层 `warnings` 提醒，不阻止保存；估算续航不应成为关机的唯一依据。
- 模板包含停电、市电恢复、低电、通讯中断通知及 OR 关机条件。关机模板返回 `suggested_action_type:"plan"`，需用户显式选择引用计划的 plan 动作；模板本身默认停用且 dry-run。

### 日志与持久化

`rule_state` 保存已消费边沿、上次触发时间、计时及事件配对元数据；`rule_logs` 与实例状态同事务提交，成功后才派发动作。`GET /rule-logs` 支持 `rule_id`、`device_id` 与通用分页，返回：

`id / rule_id / device_id / at / kind / reason / values / action_run_ids`。

`kind` 包括 `fired`、`recovered`、`skipped_cooldown`、`quality_blocked`、`quality_restored`、`dispatch_failed`。`values` 保留统一字段值和质量，`action_run_ids` 指向动作结果；一条规则多个动作按列表派发，但不等待前一动作完成，不是顺序编排。

提交后派发失败记录 `dispatch_failed`，不撤销已消费边沿或自动重放。动作已接受但关联日志补写失败时，仅在内存保留待补写的结果，重试存储而不重新执行动作；同一输入的其余命中继续派发。规则命中与外部副作用不承诺 exactly-once：进程在命中提交、动作接受或日志补写之间中断时可能缺少关联；检查两类日志判断已知结果，不能把空引用当成完成。重启不补发旧事件/动作。

## 动作

| 方法 | 路径 | 说明 |
|---|---|---|
| GET / POST | `/actions` | 分页列表 / 创建 |
| GET / PUT / DELETE | `/actions/{id}` | 查询 / 更新 / 删除，删除需确认 |
| POST | `/actions/{id}/run` | 手动触发，返回 202 与 `run_id` |
| GET | `/action-runs` | 执行记录，支持 `action_id` 和通用分页 |
| GET | `/scripts` | 脚本目录内可选择的相对路径列表 |

公共字段是 `name`、`type`、`timeout_s`、`config`。超时默认 30 秒，允许 1～3600。PUT 省略字段保留，传入 `config` 整体替换；改变 `type` 会清空旧类型配置与凭据。

### 五种配置

```json
{"name":"通知","type":"notify","config":{"channel_ids":[1]}}
```

```json
{
  "name": "应用 HTTP 接口",
  "type": "http",
  "config": {"method":"POST","expected_status":[200,204]},
  "url": "http://127.0.0.1:9000/test-only",
  "headers": {"X-Test":"example"},
  "body_template": "{\"device\":\"{{device.name}}\",\"event\":\"{{event.type}}\"}"
}
```

```json
{"name":"本地测试脚本","type":"script","config":{"script":"test.ps1","args":["sample"]}}
```

```json
{"name":"SSH 测试动作","type":"ssh","config":{"host_id":1,"command":"echo ups-warden-ok","shutdown":false}}
```

```json
{"name":"启动关机计划","type":"plan","config":{"plan_id":1}}
```

上述 URL、脚本和命令仅为配置形状示例，不会自动创建目标或执行；不要将 HTTP URL 改为 UPS 网卡 WebUI。

- `notify` 的渠道必须存在且启用。`queued` 只表示入通知队列，实际发送结果见 `/notify-logs`；多个渠道部分入队后失败不会回滚已经入队的任务。
- HTTP 方法支持 GET/POST/PUT/PATCH/DELETE/HEAD/OPTIONS，默认 POST；状态码默认 2xx。Body 使用通知同款变量及 JSON 字符串转义，URL 不支持模板；重定向一律拒绝，不使用环境 HTTP 代理，不保存响应正文，避免凭据回显。
- HTTP 的 `url`、`headers`、`body_template` 整体加密存入 `actions.secret`；响应只返回 `url_set/headers_set/body_template_set`。省略或空串保留，`clear_secrets:["headers","body_template"]` 显式清除；必需 URL 清空后校验失败。审计记录轮换但不记录原文。
- HTTP 护栏在保存和每次执行时读取所有 `sources.host`（包含停用源），解析后按 IP 比对；解析失败则拒绝。实际拨号使用本次检查的 IP，TLS 仍校验 URL 主机名，避免二次 DNS 解析绕过。`settings.http_action_guard_ups_hosts` 缺省为 true，开关管理 API 及关闭确认属于 M8；M6 不提供关闭入口，不建议直接修改数据库。
- SSH 读取主机凭据并严格匹配指纹；`shutdown:true` 只影响命令已经被服务端接受后的断连结果，不跳过指纹、认证或超时检查。
- `plan` 动作只保存 `plan_id`（计划必须存在），由规则触发时启动该计划，dry-run 规则创建 dry-run 运行。结果为 `started`、`skipped_active`、`skipped_executed`、`skipped_restored` 或 `failed`，计划本身的进度见 `/plan-runs`。plan 动作不能手动执行（`/actions/{id}/run` 返回 `422 / plan_confirmation_required`），须经 `POST /plans/{id}/run` 输入计划名称；也不能作为计划步骤。被 plan 动作引用的计划不能删除。

### 脚本目录与进程

脚本必须由管理员在服务端放入 `--script-dir`（默认 `<data-dir>/scripts`），API 不创建或在线编辑脚本。目录不存在时脚本列表为空。保存和执行时均检查真实路径，拒绝绝对路径、`..`、符号链接逃逸和非普通文件。

- Linux `.sh` 由 `/bin/sh` 执行，其余文件直接执行；可执行文件应由管理员正确设置权限。
- Windows `.ps1` 优先 `pwsh -NoProfile -NonInteractive -File`，缺失时回退 `powershell.exe`；`.cmd/.bat` 使用 `cmd.exe /d /s /c`。为避免 cmd 二次解析注入，批处理路径与参数只接受安全 ASCII 字符，不支持空白和 shell 元字符；需要复杂参数时用 PowerShell 脚本。
- 参数以数组传入，不拼接本地 shell 命令。只继承 PATH 及 Windows 运行所需的 SystemRoot/WINDIR/TEMP/TMP，不继承主密钥等服务环境变量。
- 注入 `UPS_DEVICE_ID`、`UPS_DEVICE_NAME`、`UPS_EVENT`、`UPS_POWER_STATE`、`UPS_BATTERY_PCT`、`UPS_ON_BATTERY_SECONDS`、`UPS_RUNTIME_ESTIMATE`、`UPS_DRY_RUN`。时长为秒；非有效测量不当作零。
- Linux 超时终止进程组；Windows 使用 `taskkill /T /F` 终止进程树。脚本不能依赖服务完整登录环境。Linux 进程树运行行为未在本轮实机验证。

### 手动执行与结果

```json
{"confirm":true,"dry_run":true,"device_id":1}
```

`device_id` 可省略；指定时必须有运行快照，否则 409 / `snapshot_unavailable`。手动通知事件为 `test`；条件规则的通知上下文分别为 `rule_triggered` / `rule_recovered`，不伪造设备事件记录。

返回 `{accepted:true,run_id:123}`，不等于动作成功。同一动作不会并发执行；重入保存 `skipped_concurrent`。全局最多 32 个在途动作，超出记录 `skipped_capacity`，不进入无界队列。执行中的动作修改或删除返回 409 / `action_busy`。

接受时先写 `action_runs.status=running`，手动触发审计与该记录同事务，失败则不执行。最终状态包括 `dry_run`、`queued`、`succeeded`（HTTP/脚本）、`success`（SSH）、`sent`、`failed`、`timeout`、`cancelled`、两种 skipped、plan 动作的 `started` / `skipped_*` 和 `unknown`。启动将遗留 running 标为 unknown，不自动重放。计划步骤触发的执行记录 `trigger.type` 为 `plan`，并带 `plan_run_id`。

执行记录返回 `id/action_id/trigger/dry_run/status/started_at/ended_at/exit_code/stdout/stderr/error/duration_ms`；stdout/stderr 各最多前 4 KiB。HTTP 敏感配置不进入预览，SSH 输出按已知主机凭据脱敏；管理员编写的脚本和命令仍应避免主动输出其他私有数据。

## 受控主机

| 方法 | 路径 | 说明 |
|---|---|---|
| GET / POST | `/hosts` | 分页列表 / 创建 |
| GET / PUT / DELETE | `/hosts/{id}` | 查询 / 局部更新 / 删除，删除需确认 |
| POST | `/hosts/{id}/hostkey/fetch` | 获取指纹，不认证或执行命令 |
| POST | `/hosts/{id}/hostkey/confirm` | 确认本次获取的指纹 |
| POST | `/hosts/{id}/test-connection` | 指纹校验与 SSH 认证，不打开命令会话 |
| POST | `/hosts/{id}/test-command` | 固定无害测试命令，需要确认 |
| POST | `/hosts/{id}/online-check` | 单个指定 TCP 端口或 ICMP 检测 |

```json
{
  "name":"本机测试 SSH 服务",
  "address":"127.0.0.1",
  "kind":"linux",
  "conn":{"port":2222,"username":"test","auth_method":"password","sudo":false},
  "password":"仅示例，不用于生产",
  "online_check":{"type":"tcp","port":2222}
}
```

`kind` 支持 linux/windows/nas/esxi/macos/other。连接只支持 SSH，`auth_method` 为 password/private_key；私钥字段 `private_key`，可选 `passphrase`。凭据使用 `hosts.secret` AAD 加密，GET 只返回三个 `*_set`，不返回原文或掩码占位值。省略/空串保留，`clear_secrets` 显式清除；认证方式切换清除旧凭据。

### 指纹、命令和断连

1. 保存主机后，POST fetch（JSON `{}`）得到 `fingerprint`。
2. 管理员通过可信途径核对 SHA256 指纹。
3. POST confirm：`{"confirm":true,"fingerprint":"SHA256:实际获取值"}`。

fetch 在主机公钥回调立即终止，不发送认证。候选指纹只存服务内存、有效期五分钟，并绑定主机 ID、地址和 SSH 端口；伪造、过期、重复确认或地址变化均拒绝。地址或端口变更会清除已确认指纹。

未确认或指纹不匹配时拒绝认证与命令执行。测试命令固定 `echo ups-warden-ok`，不接受自定义文本，也不使用 sudo；请求体为 `{"confirm":true}`。普通动作启用 sudo 时添加 `sudo -n --`，要求目标主机自行配置 NOPASSWD，不传输 sudo 密码。

仅在命令已获服务端接受、且 `shutdown:true` 时，EOF/连接重置/无退出状态可记录 `sent`；非零退出、发送前断连、取消或超时仍失败。**sent 不代表关机成功**，需要后续离线确认；关机计划阶段的 `offline_wait_s` 会对 SSH 步骤自动做在线检测，见[关机计划](shutdown-plans-api.md#运行状态机)。

### 在线检测

TCP 仅探测配置的单个端口，不做扫描；连接成功表示在线。ICMP 当前支持 IPv4 单次 echo；Linux 优先非特权 ping socket，失败后尝试 raw socket，其他平台用 raw socket。权限不足明确提示改用 TCP 或配置 ping_group_range / NET_RAW / 管理员权限，不自动修改系统权限。IPv6 主机使用 TCP。探测限时三秒，调用方取消与网络配置错误不伪装成正常结果。

## 验证与生命周期

规则、动作与主机有纯逻辑、临时 SQLite、loopback HTTP/SSH 和临时脚本测试。app 集成验证模拟器 → 归一化 → 状态确认 → 规则 → 通知动作 → 假 Webhook，两秒内送达；dry-run 不产生外发；恢复通知、日志关联与重启不重放均有断言。

关闭开始时先解除规则满队列反压，防止状态 actor 被发布操作卡住；之后依次等待采集/状态停止、规则排空、动作取消并记录、历史/通知结束，最后关闭数据库。无法接受或持久化的输入通过日志和 Close 错误报告，不伪装成成功。设备暂停或配置重载在旧更新处理完后清除未确认计时，保留边沿和冷却。真实 UPS、真实渠道/主机、Linux 实机、ICMP 权限环境、race 和容量验收不能由这些本机测试替代。
