/*
 * 枚举、事件、质量原因与运行状态的中文文案。
 * 文案优先取自效果图（docs/design/*.html），效果图未出现的值按同类命名补齐；未知值一律回退显示原值，
 * 不猜测含义。
 */
import type { PlanRunStatus, PlanStepStatus, Quality, Severity } from "../api/types.ts";

/** 统一模型枚举字段的取值文案；键与 model/fields.ts 的 enum 一致。 */
export const ENUM_LABELS: Readonly<Record<string, Readonly<Record<string, string>>>> = {
  power_state: {
    online: "市电供电",
    on_battery: "电池供电",
    bypass: "旁路供电",
    fault: "故障",
    shutdown: "已关机",
    unknown: "未知",
  },
  comm_state: { ok: "通讯正常", lost: "通讯中断", unknown: "未知" },
  runtime_source: { native: "设备上报", estimated: "估算", none: "无", unknown: "未知" },
  battery_status: { normal: "正常", low: "电量低", depleted: "电量耗尽", unknown: "未知" },
  output_source: {
    other: "其他",
    none: "无输出",
    normal: "正常（市电）",
    bypass: "旁路",
    battery: "电池",
    booster: "升压",
    reducer: "降压",
    unknown: "未知",
  },
  ups_mode: {
    power_on: "开机",
    standby: "待机",
    bypass: "旁路",
    line: "在线（市电）",
    battery: "电池模式",
    test: "自检",
    fault: "故障",
    converter: "变频",
    eco: "ECO 节能",
    shutdown: "关机",
    unknown: "未知",
  },
  ups_type: { online: "在线式", backup: "后备式", unknown: "未知" },
};

export function enumLabel(key: string, value: string): string {
  return ENUM_LABELS[key]?.[value] ?? value;
}

export const SEVERITY_TEXT: Readonly<Record<Severity, string>> = {
  info: "信息",
  warning: "警告",
  critical: "严重",
};

/** 事件类型：15 种统一模型事件，以及通知上下文中的 test、规则与关机计划事件。 */
export const EVENT_LABELS: Readonly<Record<string, string>> = {
  mains_lost: "市电中断",
  mains_restored: "市电恢复",
  battery_low: "电量低",
  battery_low_cleared: "电量低解除",
  battery_depleted: "电池耗尽",
  comm_lost: "通讯中断",
  comm_restored: "通讯恢复",
  overload: "过载",
  overload_cleared: "过载解除",
  on_bypass: "旁路供电",
  bypass_cleared: "旁路解除",
  ups_fault: "UPS 故障",
  ups_shutting_down: "UPS 正在关机",
  temperature_high: "温度过高",
  self_test_result: "自检结果",
  test: "测试",
  rule_triggered: "规则触发",
  rule_recovered: "规则恢复",
  plan_started: "计划开始",
  plan_step_failed: "计划步骤失败",
  plan_cancelled: "计划取消",
  plan_completed: "计划完成",
  plan_resumed: "计划继续执行",
  plan_aborted: "计划终止",
};

export function eventLabel(type: string): string {
  return EVENT_LABELS[type] ?? type;
}

export const QUALITY_LABELS: Readonly<Record<Quality, string>> = {
  good: "正常",
  invalid: "无效",
  unsupported: "不支持",
  stale: "过期",
};

/** 字段质量原因码（model.Reason*，另有 runtime_estimate 的 estimating）。 */
export const REASON_LABELS: Readonly<Record<string, string>> = {
  placeholder: "占位值",
  invalid_value: "无效值",
  out_of_range: "超出范围",
  not_mapped: "Profile 未映射",
  device_unsupported: "设备不支持",
  phase_inactive: "该相未启用",
  decode_error: "解码失败",
  enum_unmapped: "枚举未映射",
  comm_stale: "通讯中断，显示旧值",
  estimating: "估算中",
};

/** 原因文案：未知原因码回退为质量文案，再回退为原码。 */
export function reasonLabel(reason: string, quality?: Quality): string {
  if (reason && REASON_LABELS[reason]) return REASON_LABELS[reason];
  if (quality && quality !== "good")
    return reason ? `${QUALITY_LABELS[quality]}（${reason}）` : QUALITY_LABELS[quality];
  return reason;
}

export const PLAN_RUN_STATUS_LABELS: Readonly<Record<PlanRunStatus, string>> = {
  countdown: "倒计时",
  running: "执行中",
  completed: "已完成",
  completed_with_errors: "完成（有错误）",
  cancelled: "已取消",
  aborted: "已终止",
};

export const PLAN_STEP_STATUS_LABELS: Readonly<Record<PlanStepStatus, string>> = {
  waiting: "等待",
  running: "执行中",
  sent: "已发送",
  offline: "已离线",
  failed: "失败",
  cancelled: "已取消",
  unknown: "未知",
};

/** 动作执行记录状态（rules-actions-hosts-api.md「手动执行与执行记录」）。 */
export const ACTION_RUN_STATUS_LABELS: Readonly<Record<string, string>> = {
  running: "执行中",
  dry_run: "Dry-run 预演",
  queued: "已入队",
  succeeded: "成功",
  success: "成功",
  sent: "已发送",
  failed: "失败",
  timeout: "超时",
  cancelled: "已取消",
  skipped_concurrent: "跳过（正在执行）",
  skipped_capacity: "跳过（并发已满）",
  started: "已启动计划",
  skipped_active: "跳过（计划已在运行）",
  skipped_executed: "跳过（本停电周期已执行）",
  skipped_restored: "跳过（市电已恢复）",
  unknown: "未知",
};

export function actionRunStatusLabel(status: string): string {
  return ACTION_RUN_STATUS_LABELS[status] ?? status;
}
