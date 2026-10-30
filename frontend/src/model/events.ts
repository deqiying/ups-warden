/*
 * 事件的显示派生：图标、色调、摘要、持续时间。供总览、设备详情与告警页共用。
 * 事件 summary 只含统一字段（后端 state.summarize：power_state、comm_state、battery_capacity_pct、
 * output_load_pct[1]、input_voltage[1]、on_battery_elapsed、runtime_estimate）。
 */
import type { EventItem, FieldMap, Severity } from "../api/types.ts";
import type { BadgeTone } from "../components/Badge.tsx";
import type { PillTone } from "../components/Pill.tsx";
import type { IconName } from "../icons/icons.ts";
import { durationText, fieldText } from "./format.ts";

const EVENT_ICONS: Readonly<Record<string, IconName>> = {
  mains_lost: "plug",
  mains_restored: "plug",
  battery_low: "battery",
  battery_low_cleared: "battery",
  battery_depleted: "battery",
  comm_lost: "wifi-off",
  comm_restored: "antenna",
  overload: "gauge",
  overload_cleared: "gauge",
  on_bypass: "branch",
  bypass_cleared: "branch",
  ups_fault: "alert",
  ups_shutting_down: "power",
  temperature_high: "thermo",
  self_test_result: "check-circle",
};

export function eventIcon(type: string): IconName {
  return EVENT_ICONS[type] ?? "info";
}

/** 历史事件类型筛选：15 种统一模型事件，按成对的发生 / 解除分组（alerts.html 类型菜单）。 */
export const EVENT_TYPE_GROUPS: readonly { label: string; types: readonly string[] }[] = [
  {
    label: "供电",
    types: ["mains_lost", "mains_restored", "on_bypass", "bypass_cleared", "ups_shutting_down"],
  },
  {
    label: "电池",
    types: ["battery_low", "battery_low_cleared", "battery_depleted", "self_test_result"],
  },
  {
    label: "负载与环境",
    types: ["overload", "overload_cleared", "temperature_high", "ups_fault"],
  },
  { label: "通讯", types: ["comm_lost", "comm_restored"] },
];

/** 统一模型事件的严重度：镜像 backend/internal/model EventSeverity（规则事件选择器使用）。 */
export const EVENT_SEVERITY: Readonly<Record<string, Severity>> = {
  mains_lost: "critical",
  battery_low: "critical",
  battery_low_cleared: "critical",
  battery_depleted: "critical",
  ups_fault: "critical",
  ups_shutting_down: "critical",
  comm_lost: "warning",
  comm_restored: "warning",
  overload: "warning",
  overload_cleared: "warning",
  on_bypass: "warning",
  bypass_cleared: "warning",
  temperature_high: "warning",
  mains_restored: "info",
  self_test_result: "info",
};

export const SEVERITY_PILL_TONE: Readonly<Record<Severity, PillTone>> = {
  critical: "red",
  warning: "orange",
  info: "blue",
};

export const SEVERITY_BADGE_TONE: Readonly<Record<Severity, BadgeTone>> = {
  critical: "red",
  warning: "orange",
  info: "blue",
};

const SEVERITY_RANK: Readonly<Record<Severity, number>> = { critical: 0, warning: 1, info: 2 };

/** 告警排序：严重度优先，同级按开始时间（id）倒序。 */
export function compareAlerts(a: EventItem, b: EventItem): number {
  return SEVERITY_RANK[a.severity] - SEVERITY_RANK[b.severity] || b.id - a.id;
}

/** 摘要中显示的字段与短名；顺序即显示顺序。 */
const SUMMARY_FIELDS: readonly { key: string; label: string }[] = [
  { key: "battery_capacity_pct", label: "容量" },
  { key: "output_load_pct[1]", label: "负载" },
  { key: "input_voltage[1]", label: "输入" },
  { key: "on_battery_elapsed", label: "已放电" },
  { key: "runtime_estimate", label: "剩余" },
];

/**
 * 事件摘要文本，如“容量 89% · 负载 27% · 输入 0 V”。
 * 只显示 good 字段：摘要是一行补充说明，非 good 字段直接省略，绝不以 0 代替。
 */
export function summaryText(summary: FieldMap): string {
  const parts: string[] = [];
  for (const { key, label } of SUMMARY_FIELDS) {
    const field = summary[key];
    if (!field || field.quality !== "good") continue;
    parts.push(`${label} ${fieldText(key, field).replace(/ (%)$/, "$1")}`);
  }
  return parts.join(" · ");
}

/** 事件持续秒数：已结束取结束时间，进行中取 `nowMs`；没有结束时间的非活动事件返回 null。 */
export function eventDurationSeconds(event: EventItem, nowMs: number): number | null {
  const start = Date.parse(event.started_at);
  if (Number.isNaN(start)) return null;
  const end = event.cleared_at ? Date.parse(event.cleared_at) : event.active ? nowMs : NaN;
  if (Number.isNaN(end)) return null;
  return Math.max(0, Math.floor((end - start) / 1000));
}

/** 粗粒度时长：“3 分钟”“2 小时 5 分”“43 秒”，用于列表。 */
export function shortDuration(seconds: number): string {
  if (seconds < 60) return `${Math.floor(seconds)} 秒`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟`;
  return durationText(seconds, "minute");
}
