# 关机计划 API（M7）

[返回文档导航](../README.md) · [规则、动作与受控主机](rules-actions-hosts-api.md) · [通知契约](notifications-api.md) · [监控与 SSE](monitoring-api.md)

## 范围与安全边界

M7 提供关机计划配置、倒计时、阶段编排、取消、市电恢复处理、停电周期幂等、重启恢复、运行记录与 SSE 进度。端点前缀均为 `/api/v1`；要求管理员会话，写操作遵循 CSRF、同源检查、严格 JSON 与审计约定，审计与配置/运行写入同事务。

- 计划只编排已保存的 **SSH / 本地脚本 / HTTP 动作**，不读取协议数据，也不向 UPS 发送任何指令。
- 触发方式只有两种：规则引用 `type:"plan"` 的动作（`config.plan_id`），或 `POST /plans/{id}/run` 手动执行（需输入计划名称）。plan 动作不能经 `/actions/{id}/run` 执行，返回 `422 / plan_confirmation_required`。
- 新建规则默认 dry-run；dry-run 的 plan 动作创建 **dry-run 运行**：步骤只写 `dry_run` 预览，不连接主机、不等待离线、不发送计划通知。
- 本机测试只使用临时数据库、loopback 模拟器、本机假 SSH/HTTP/Webhook。**CP-3 关机编排演示与评审尚未完成**；在真实环境关闭 dry-run 之前须先评审幂等与恢复测试结果。

## 计划配置

| 方法 | 路径 | 说明 |
|---|---|---|
| GET / POST | `/plans` | 分页列表 / 创建（201） |
| GET / PUT / DELETE | `/plans/{id}` | 查询 / 部分更新 / 删除；删除需 `confirm:true` |
| POST | `/plans/{id}/run` | 手动执行，需 `confirm_name` |

```json
{
  "name": "机房关机",
  "countdown_s": 60,
  "on_mains_restored": "cancel_pending",
  "on_restart": "resume",
  "notify_channel_ids": [1],
  "stages": [
    {"delay_s": 0, "step_timeout_s": 30, "offline_wait_s": 120, "steps": [{"action_id": 11}, {"action_id": 12}]},
    {"delay_s": 10, "step_timeout_s": 30, "offline_wait_s": 300, "steps": [{"action_id": 13}]},
    {"self_shutdown": true, "steps": [{"action_id": 20}]}
  ]
}
```

示例中的 ID 仅为形状示例，不会自动创建动作或主机。

- 创建从默认值开始：`countdown_s=60`、`on_mains_restored=cancel_pending`、`on_restart=resume`、`notify_channel_ids=[]`；省略的 `step_timeout_s` 为 30。PUT 只替换提供的顶层字段，`stages` 与 `notify_channel_ids` 一旦提交则整体替换。
- 校验失败返回 `422 / validation_failed`：名称 1～128 字符；1～16 个阶段，每阶段 1～32 个步骤；`countdown_s`、`delay_s`、`offline_wait_s` 为 0～3600，`step_timeout_s` 为 1～3600；`on_mains_restored` 为 `cancel_pending|continue`，`on_restart` 为 `resume|abort`。
- 同一动作在一个计划中最多出现一次；步骤只能引用已存在的 ssh/script/http 动作。通知渠道最多 16 个、不可重复，且必须存在并启用。
- `self_shutdown` 阶段必须是最后一个阶段、只含一个步骤且 `offline_wait_s=0`。
- 运行开始时冻结计划快照（`plan_snapshot`），之后修改计划只影响后续运行。
- 删除：被 plan 动作引用或已有运行记录的计划返回 `409 / resource_conflict`，保留执行证据。被计划引用的步骤动作和通知渠道同样不能删除。
- 审计 kind：`plan.create`、`plan.update`、`plan.delete`、`plan.run`、`plan_run.cancel`。

## 执行

### 手动执行

```json
{"confirm_name": "机房关机", "dry_run": true, "device_id": 1}
```

`confirm_name` 必须与计划名称完全一致，dry-run 也不例外，否则 `422 / confirmation_required`。**未传 `dry_run` 按真实执行处理**，调用方应显式填写。`device_id` 可省略；指定时使用该设备的运行快照，没有快照返回 `409 / snapshot_unavailable`。设备正处于停电周期时，真实运行同样占用该周期的唯一执行。

成功返回 `202 {accepted:true, run_id}`，只表示运行已创建。其他结果：

| 状态 / code | 含义 |
|---|---|
| 409 `plan_run_active` | 该计划已有倒计时或执行中的运行（含 dry-run） |
| 409 `plan_cycle_executed` | 该计划已在本停电周期真实执行过 |
| 409 `plan_cycle_ended` | 快照中的停电周期已结束，请刷新后重试 |
| 404 `plan_not_found` | 计划不存在 |

### 规则触发

规则命中后派发 plan 动作，请求携带触发设备的状态快照与停电周期。`action_runs` 中 plan 动作的结果为：`started`（stdout 含运行 ID）、`skipped_active`、`skipped_executed`、`skipped_restored`（触发时周期已结束），或 `failed`（“关机计划启动失败”）。跳过不会排队补发。

### 运行状态机

运行状态：`countdown → running → completed | completed_with_errors | cancelled | aborted`。

1. `countdown_s>0` 时先进入倒计时，截止时刻持久化为 `countdown_until`，客户端据此自行计算剩余秒数。
2. 阶段串行执行。每阶段先等待 `delay_s`，再**并行**执行全部步骤；`step_timeout_s` 覆盖动作自身超时。
3. 阶段内步骤结束后，对已 `sent` 的 SSH 步骤并行检查关联受控主机的在线状态：立即检查一次，此后每 5 秒检查，直到离线（步骤记 `offline`）或 `offline_wait_s` 超时。超时时步骤保持 `sent`，`result.offline_timeout=true`，并发送失败通知。在线检测出错不视为离线。dry-run 与 `offline_wait_s=0` 不等待。
4. 失败、超时或离线超时只记录并通知，不阻塞后续阶段。
5. 全部阶段结束后推导终态：有 `cancelled` 步骤为 `cancelled`；有 `failed` / `unknown` 或离线超时为 `completed_with_errors`；否则为 `completed`。

步骤状态：

| 状态 | 含义 |
|---|---|
| `waiting` | 未开始 |
| `running` | 已写入 write-ahead 记录，动作执行中 |
| `sent` | 动作成功类终态（HTTP/脚本 `succeeded`、SSH `success`/`sent`、`dry_run`）；**不代表关机完成** |
| `offline` | 已发送且确认主机离线 |
| `failed` | 动作失败、超时，或执行前动作已不存在/类型不再适用 |
| `cancelled` | 未开始即被取消 |
| `unknown` | 服务中断时执行中，结果未知，**不会重放** |

每个步骤对应一条 `action_runs` 记录（`trigger={"type":"plan","plan_run_id":…}`），`result.action_run_id` 指向它，命令输出、错误详情和凭据脱敏规则均以动作记录为准；`result.action_status` 与 `result.error` 只给出摘要。

### 幂等

- 同一计划任何时刻最多一个活动运行（含 dry-run）。
- 非 dry-run 运行在 `(plan_id, outage_cycle_id)` 上唯一：同一停电周期只真实执行一次，周期结束后可再次触发。dry-run 和不在停电周期内的手动运行不占用该约束。
- **步骤 write-ahead**：动作执行记录与步骤 `waiting → running` 在同一事务提交后才执行；条件更新未命中（已被取消）时事务回滚，不执行。
- **自关机阶段**：执行前在同一事务中把步骤记为 `sent`、运行记为结束，再关闭本机；本机重启后不会继续该计划。若动作随后返回失败，步骤补记 `failed`，运行由 `completed` 改为 `completed_with_errors`。

### 取消与市电恢复

- `POST /plan-runs/{id}/cancel`，请求体 `{"confirm":true}`，缺少确认返回 422。同一事务把全部 `waiting` 步骤记为 `cancelled` 并写审计，返回 `202 {accepted:true}`；执行中的步骤不被打断，运行在其结果落库后结束为 `cancelled`。已结束返回 `409 / plan_run_finished`，不存在返回 `404 / plan_run_not_found`，服务正在关闭返回 `503 / plan_unavailable`。
- 市电恢复以触发设备**去抖确认的 `mains_restored`**（停电周期结束）为准：倒计时期间总是取消；执行中按 `on_mains_restored`，`cancel_pending` 取消未开始的步骤，`continue` 继续执行。
- 已发出的命令不可撤回，取消通知会明确说明。没有停电周期的运行（如未指定设备的手动执行）不受市电恢复影响。

### 重启恢复

服务关闭不写入运行终态。服务关闭过程中到达的触发（如关闭前规则排空派发的 plan 动作）照常创建运行但不启动，留给下次启动恢复，避免触发丢失。启动时加载 `countdown` / `running` 运行：

- 中断时 `running` 的步骤记为 `unknown`（`result.error` 说明“未自动重放”）。
- 触发周期已结束，且运行仍在倒计时或 `on_mains_restored=cancel_pending`：剩余步骤取消，按步骤推导终态，通知原因为“重启时市电已恢复”。`continue` 的执行中运行与在线行为一致，按 `on_restart` 处理。
- `on_restart=resume`：发送 `plan_resumed`，从第一个未开始的步骤继续；倒计时沿用原截止时刻；部分执行的阶段不重复 `delay_s`。
- `on_restart=abort`：剩余步骤取消，运行记为 `aborted` 并发送 `plan_aborted`。

**已知边界**：状态引擎重启后会清空市电恢复的待确认计时，需要重新完成去抖。若重启前市电已恢复但尚未确认，或在停机期间恢复，被恢复的运行在新的去抖窗口内仍会继续执行步骤，确认后才按上述策略取消。这与在线运行的去抖语义一致，需在 CP-3 评审确认。

## 运行记录与 SSE

- `GET /plan-runs` 支持 `plan_id`（非法返回 `400 / invalid_filter`）及通用 `before` / `limit` 分页，按 ID 倒序返回 `{items,next_before}`。`GET /plan-runs/{id}` 返回单个运行。
- 运行视图：`id / plan_id / plan_name / device_id / outage_cycle_id / dry_run / status / trigger / countdown_until / current_stage / started_at / ended_at / steps[]`；步骤为 `stage / step / action_id / host_id / status / started_at / ended_at / result{action_run_id,action_status,error,offline_timeout}`。时间均为 UTC。
- SSE `sync` 的 `plan_runs` 为全部活动运行（countdown/running）的完整视图。每次运行状态落库后推送 `plan_progress`，data 为同一完整视图。总线有损，断线重连后以 `sync` 为准，已结束运行通过查询 API 获取。

## 通知

计划运行使用计划自身的 `notify_channel_ids`，不经过规则通知动作。事件类型与级别：

| 事件 | 级别 | 时机 |
|---|---|---|
| `plan_started` | warning | 运行创建（含倒计时开始） |
| `plan_step_failed` | critical | 步骤失败、执行前动作失效或离线超时 |
| `plan_cancelled` | info | 用户取消或市电恢复 |
| `plan_completed` | info；有错误时 warning | 运行结束（含自关机前） |
| `plan_resumed` | warning | 重启后继续 |
| `plan_aborted` | warning | 重启后按配置终止 |

模板变量 `{{detail}}` 携带计划名称、运行 ID、阶段/步骤与原因，不含凭据。dry-run 不发送计划通知；入队失败只记录日志，不影响运行。默认模板与自定义方式见[通知契约](notifications-api.md#模板管理与渲染)。

## 验证与限制

`internal/plan` 单元测试覆盖校验、阶段串行/阶段内并行、单步超时、失败不阻塞、dry-run 无副作用、单活动运行与停电周期幂等、倒计时/执行中的市电恢复、执行中与阶段延迟中注入重启不重放、倒计时恢复、abort、周期已结束（含 `continue` 继续）、关闭后到达的触发在重启后恢复及自关机前置标记。app 集成测试用 loopback 模拟器、本机假 SSH 主机、假 HTTP 受控端和假 Webhook 验证：

- AC-05 计划部分：停电确认 → 规则 → 倒计时 → SSH 关机并等待离线（一台离线、一台超时）→ HTTP 阶段，并检查进度、动作关联与通知；
- AC-06：倒计时期间市电恢复，取消并通知；
- AC-07：同一数据目录重建 app 后 resume 与 abort 各自生效。

API 测试覆盖 CRUD、确认、冲突码、审计、引用保护及 SSE。

这些测试未连接真实 UPS、真实渠道或受控主机，也未执行真实关机。CP-3 演示评审、CP-1 剩余项、CP-2、Linux 实机、ICMP 权限环境、race 与容量验收仍未完成；前端尚无计划页面。
