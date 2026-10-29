/*
 * 告警与事件页的纯函数：query 参数解析/写入、时间范围换算、按日分组。
 * 页内状态（视图、历史筛选）全部写入 query string，刷新与分享链接后保持一致。
 */
import type { EventItem, Severity } from "../../api/types.ts";
import { EVENT_TYPE_GROUPS } from "../../model/events.ts";
import {
  formatDateHeading,
  formatDateTime,
  formatFullDateTime,
  formatTime,
} from "../../model/time.ts";

export type AlertsView = "active" | "history";
export type SeverityFilter = "all" | Severity;
export type RangeFilter = "24h" | "7d" | "30d" | "all";

export interface HistoryFilters {
  /** 设备 id；null 表示全部设备。 */
  device: number | null;
  /** 事件类型（契约只支持单值，Q-C3）；null 表示全部类型。 */
  type: string | null;
  severity: SeverityFilter;
  range: RangeFilter;
}

export const DEFAULT_RANGE: RangeFilter = "7d";
export const DEFAULT_FILTERS: HistoryFilters = {
  device: null,
  type: null,
  severity: "all",
  range: DEFAULT_RANGE,
};

const SEVERITY_VALUES: readonly SeverityFilter[] = ["all", "critical", "warning", "info"];
const RANGE_VALUES: readonly RangeFilter[] = ["24h", "7d", "30d", "all"];
const KNOWN_TYPES: ReadonlySet<string> = new Set(EVENT_TYPE_GROUPS.flatMap((g) => g.types));

const RANGE_MS: Readonly<Record<Exclude<RangeFilter, "all">, number>> = {
  "24h": 24 * 3600_000,
  "7d": 7 * 24 * 3600_000,
  "30d": 30 * 24 * 3600_000,
};

/** 非法或缺省的 view 一律回退为 active。 */
export function parseView(params: URLSearchParams): AlertsView {
  return params.get("view") === "history" ? "history" : "active";
}

/** 解析历史筛选；无法识别的值按默认处理，不把脏参数传给后端。 */
export function parseFilters(params: URLSearchParams): HistoryFilters {
  const device = Number(params.get("device"));
  const type = params.get("type");
  const severity = params.get("severity") as SeverityFilter | null;
  const range = params.get("range") as RangeFilter | null;
  return {
    device: Number.isInteger(device) && device > 0 ? device : null,
    type: type && KNOWN_TYPES.has(type) ? type : null,
    severity: severity && SEVERITY_VALUES.includes(severity) ? severity : "all",
    range: range && RANGE_VALUES.includes(range) ? range : DEFAULT_RANGE,
  };
}

export function isDefaultFilters(filters: HistoryFilters): boolean {
  return (
    filters.device === null &&
    filters.type === null &&
    filters.severity === DEFAULT_FILTERS.severity &&
    filters.range === DEFAULT_FILTERS.range
  );
}

/** 返回写入了视图的新 URLSearchParams；默认视图不写入，保持地址简洁。 */
export function withView(params: URLSearchParams, view: AlertsView): URLSearchParams {
  const next = new URLSearchParams(params);
  if (view === "history") next.set("view", "history");
  else next.delete("view");
  return next;
}

/** 返回写入了筛选补丁的新 URLSearchParams；等于默认值的筛选项不写入。 */
export function withFilters(
  params: URLSearchParams,
  patch: Partial<HistoryFilters>,
): URLSearchParams {
  const merged = { ...parseFilters(params), ...patch };
  const next = new URLSearchParams(params);
  const put = (key: string, value: string | null) => {
    if (value === null) next.delete(key);
    else next.set(key, value);
  };
  put("device", merged.device === null ? null : String(merged.device));
  put("type", merged.type);
  put("severity", merged.severity === "all" ? null : merged.severity);
  put("range", merged.range === DEFAULT_RANGE ? null : merged.range);
  return next;
}

/** 时间范围换算为 `from`（RFC3339 UTC，秒精度）；`all` 不限制起点。 */
export function rangeFrom(range: RangeFilter, nowMs: number): string | undefined {
  if (range === "all") return undefined;
  return new Date(nowMs - RANGE_MS[range]).toISOString().replace(/\.\d{3}Z$/, "Z");
}

/** 设置时区下的日期键（`2026-10-27`），无效时间返回空串。 */
export function dayKey(value: string | number, timeZone?: string): string {
  const text = formatFullDateTime(value, timeZone);
  return /^\d{4}-\d{2}-\d{2}/.test(text) ? text.slice(0, 10) : "";
}

/** 日期标题：今天 / 昨天加前缀，如“今天 · 10月27日 星期二”。 */
export function dayHeading(value: string, nowMs: number, timeZone?: string): string {
  const heading = formatDateHeading(value, timeZone);
  const key = dayKey(value, timeZone);
  if (key && key === dayKey(nowMs, timeZone)) return `今天 · ${heading}`;
  if (key && key === dayKey(nowMs - 24 * 3600_000, timeZone)) return `昨天 · ${heading}`;
  return heading;
}

/** 当天只显示时分秒，跨天显示“月日 时:分”。 */
export function clockOrDate(value: string, nowMs: number, timeZone?: string): string {
  const key = dayKey(value, timeZone);
  return key && key === dayKey(nowMs, timeZone)
    ? formatTime(value, timeZone)
    : formatDateTime(value, timeZone);
}

export interface DayGroup {
  key: string;
  heading: string;
  items: EventItem[];
}

/** 按设置时区的日期分组，保持首次出现的顺序（接口按 id 降序，近似时间倒序）。 */
export function groupByDay(
  items: readonly EventItem[],
  nowMs: number,
  timeZone?: string,
): DayGroup[] {
  const groups = new Map<string, DayGroup>();
  for (const item of items) {
    const key = dayKey(item.started_at, timeZone);
    let group = groups.get(key);
    if (!group) {
      group = { key, heading: dayHeading(item.started_at, nowMs, timeZone), items: [] };
      groups.set(key, group);
    }
    group.items.push(item);
  }
  return [...groups.values()];
}
