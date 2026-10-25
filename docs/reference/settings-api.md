# 系统设置 API

[返回文档导航](../README.md)

M8 系统设置契约，已通过本机自动化测试；前端尚无设置页面。通用约定（`/api/v1` 前缀、管理员会话、CSRF、严格 JSON 解码、错误格式）见[监控 API 与 SSE 契约](monitoring-api.md)。实现见 [settings.go](../../backend/internal/settings/settings.go) 与 [api/settings.go](../../backend/internal/api/settings.go)。

## 字段

| 字段 | 默认值 | 范围与作用 |
|---|---|---|
| `detail_retention_days` | `7` | 1～90。历史明细样本保留天数；也决定 `auto` 分辨率能否选 raw |
| `aggregate_retention_days` | `90` | 不小于明细保留期，最大 3650。分钟聚合保留天数 |
| `log_retention_days` | `365` | 1～3650。审计、规则/通知日志、已结束的动作与计划运行、非活动事件及已结束停电周期的保留天数（仍被引用的记录不删） |
| `device_defaults` | 内置设备默认阈值 | 与设备 `config` 相同的对象（字段见[采集、状态与历史](../guides/collection-state-history.md#状态与事件)），新建/PATCH 设备时为省略字段提供默认值 |
| `timezone` | `"UTC"` | IANA 时区名，拒绝空串与 `Local`。只保存供前端显示；API 时间仍为 RFC3339 UTC |
| `language` | `"zh-CN"` | `zh-CN` 或 `en-US`。只保存供前端显示 |
| `http_action_guard_ups_hosts` | `true` | HTTP 动作禁止访问任何数据源主机（D8），见[规则、动作与受控主机](rules-actions-hosts-api.md) |

每个设置在 `settings` 表中保存为一行 JSON；未写入的键使用默认值，未知键忽略。已保存值无效（含 `null`）时启动失败并报告键名，不静默回退默认值。

## 接口

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/settings` | 返回全部字段的当前值 |
| PATCH | `/settings` | 只覆盖显式字段，返回更新后的全部字段 |

```json
{
  "detail_retention_days": 7,
  "aggregate_retention_days": 90,
  "log_retention_days": 365,
  "device_defaults": {"outage_confirm_s": 10, "recovery_confirm_s": 60, "...": "..."},
  "timezone": "UTC",
  "language": "zh-CN",
  "http_action_guard_ups_hosts": true
}
```

PATCH 规则：

- 沿用项目 PATCH 约定，不提供 PUT；拒绝未知字段、重复键与 `null`。
- `device_defaults` 整体替换：省略的子字段取**内置**默认值，而不是上一次保存的系统默认值。
- 合并后整体校验，任一字段越界返回 `422 / validation_failed`，不部分保存。
- 把 `http_action_guard_ups_hosts` 从 `true` 改为 `false` 时必须同时携带 `"confirm_disable_guard": true`，否则返回 `422 / confirmation_required`。该字段只用于确认、不保存；重新开启或保持关闭不需要确认。
- 保存与审计（`kind=settings.update`，记录修改前后完整设置）同一事务；提交后保留期立即作用于历史聚合、清理和查询。

## 生效范围

- **设备默认阈值只在经 API 写设备配置时使用**。设备保存的是完整配置对象，修改 `device_defaults` 不会改变已有设备；要让已有设备采用新值，需对该设备 PATCH `config`。
- 保留期在进程启动时读取，PATCH 成功后无需重启。缩短保留期后，下一轮每小时清理才会删除超期数据。
- 设置变更不暂停采集，也不重建设备运行时。
