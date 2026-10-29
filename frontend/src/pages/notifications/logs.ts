/*
 * 发送记录页的纯函数：筛选条件与 query string 的互转、请求参数换算、耗时与事件列文案。
 * 筛选条件全部写入 query（刷新与分享链接后保持一致），默认值不写入以保持地址简洁。
 */
import type { NotifyLogQuery } from "../../api/notifications.ts";

export type ResultFilter = "all" | "failed" | "ok";
export type LogRange = "24h" | "7d" | "30d" | "all";

export interface LogFilters {
  /** 渠道 id；null 表示全部渠道。 */
  channel: number | null;
  result: ResultFilter;
  range: LogRange;
}

export const DEFAULT_LOG_RANGE: LogRange = "7d";
export const DEFAULT_LOG_FILTERS: LogFilters = {
  channel: null,
  result: "all",
  range: DEFAULT_LOG_RANGE,
};

const RESULT_VALUES: readonly ResultFilter[] = ["all", "failed", "ok"];
const RANGE_VALUES: readonly LogRange[] = ["24h", "7d", "30d", "all"];
const RANGE_MS: Readonly<Record<Exclude<LogRange, "all">, number>> = {
  "24h": 24 * 3600_000,
  "7d": 7 * 24 * 3600_000,
  "30d": 30 * 24 * 3600_000,
};

/** 解析筛选；无法识别的值按默认处理，不把脏参数传给后端。 */
export function parseLogFilters(params: URLSearchParams): LogFilters {
  const channel = Number(params.get("channel"));
  const result = params.get("result") as ResultFilter | null;
  const range = params.get("range") as LogRange | null;
  return {
    channel: Number.isInteger(channel) && channel > 0 ? channel : null,
    result: result && RESULT_VALUES.includes(result) ? result : "all",
    range: range && RANGE_VALUES.includes(range) ? range : DEFAULT_LOG_RANGE,
  };
}

export function isDefaultLogFilters(filters: LogFilters): boolean {
  return (
    filters.channel === null &&
    filters.result === DEFAULT_LOG_FILTERS.result &&
    filters.range === DEFAULT_LOG_FILTERS.range
  );
}

/** 返回写入了筛选补丁的新 URLSearchParams；等于默认值的筛选项不写入。 */
export function withLogFilters(
  params: URLSearchParams,
  patch: Partial<LogFilters>,
): URLSearchParams {
  const merged = { ...parseLogFilters(params), ...patch };
  const next = new URLSearchParams(params);
  const put = (key: string, value: string | null) => {
    if (value === null) next.delete(key);
    else next.set(key, value);
  };
  put("channel", merged.channel === null ? null : String(merged.channel));
  put("result", merged.result === "all" ? null : merged.result);
  put("range", merged.range === DEFAULT_LOG_RANGE ? null : merged.range);
  return next;
}

/** 时间范围换算为 `from`（RFC3339 UTC，秒精度）；`all` 不限制起点。 */
export function logRangeFrom(range: LogRange, nowMs: number): string | undefined {
  if (range === "all") return undefined;
  return new Date(nowMs - RANGE_MS[range]).toISOString().replace(/\.\d{3}Z$/, "Z");
}

/** 筛选 → GET /notify-logs 参数（不含分页游标）。 */
export function logQuery(filters: LogFilters, nowMs: number): NotifyLogQuery {
  return {
    channel_id: filters.channel ?? undefined,
    ok: filters.result === "all" ? undefined : filters.result === "ok",
    from: logRangeFrom(filters.range, nowMs),
  };
}

/** 耗时：不足 1 秒显示毫秒，其余显示秒（保留 1 位小数）。 */
export function durationMsText(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "—";
  if (ms < 1000) return `${Math.round(ms)} ms`;
  return `${(ms / 1000).toFixed(1)} s`;
}

/**
 * 关联事件列（Q-C7）：契约只有 event_id，没有事件类型。
 * 测试消息、条件规则与关机计划的 event_id 为 null 且彼此不可区分，统一显示“无关联事件”。
 */
export function eventColumnText(eventId: number | null): string {
  return eventId === null ? "无关联事件" : `事件 #${eventId}`;
}
