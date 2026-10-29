/*
 * 设备历史曲线的纯函数（F2-3）：字段实例、序列与缺口、坐标刻度、停电区间。
 * 历史只含 good 数值（后端 history 只写 History=true 且 good 的统一字段），缺失采样处断开不连线。
 */
import type { EventItem, FieldMap, HistoryResponse } from "../../api/types.ts";
import {
  FIELD_CATALOG,
  fieldLabel,
  lookupField,
  phaseKeys,
  splitFieldKey,
} from "../../model/fields.ts";
import { formatNumber } from "../../model/format.ts";

export type HistoryRange = "1h" | "6h" | "24h" | "7d" | "30d";

export const HISTORY_RANGES: readonly { value: HistoryRange; label: string; ms: number }[] = [
  { value: "1h", label: "1 小时", ms: 3600_000 },
  { value: "6h", label: "6 小时", ms: 6 * 3600_000 },
  { value: "24h", label: "24 小时", ms: 24 * 3600_000 },
  { value: "7d", label: "7 天", ms: 7 * 86400_000 },
  { value: "30d", label: "30 天", ms: 30 * 86400_000 },
];

export const MAX_POINTS = 1000;
/** 同时选择的字段上限：每张图最多几条曲线，避免难以辨认。 */
export const MAX_HISTORY_FIELDS = 6;
export const DEFAULT_HISTORY_FIELDS: readonly string[] = [
  "battery_capacity_pct",
  "output_load_pct[1]",
];

/** 序列颜色；只用于区分曲线，不承载状态语义。 */
export const SERIES_COLORS = [
  "var(--green)",
  "var(--accent)",
  "var(--purple)",
  "var(--teal)",
  "var(--indigo)",
  "var(--brown)",
] as const;

function phasesFor(key: string, fields: FieldMap): number {
  const group = key.startsWith("output_") ? "output_phases" : "input_phases";
  const field = fields[group];
  if (!field || field.quality !== "good") return 1;
  return Math.min(Math.max(Math.trunc(field.num), 1), 3);
}

/**
 * 可选的历史字段实例：目录中 History=true 的字段；按相字段按设备相数展开
 * （输出类按 output_phases，输入与旁路按 input_phases；未知按单相）。
 */
export function historyFieldOptions(fields: FieldMap): string[] {
  const out: string[] = [];
  for (const spec of FIELD_CATALOG) {
    if (!spec.history) continue;
    if (spec.phased) out.push(...phaseKeys(spec.key, phasesFor(spec.key, fields)));
    else out.push(spec.key);
  }
  return out;
}

/** 字段分组排序用：按目录分组聚在一起。 */
export function historyFieldGroup(key: string): string {
  return lookupField(key)?.group ?? "";
}

export interface SeriesPoint {
  t: number;
  avg: number;
  min: number;
  max: number;
  count: number;
}

export interface Series {
  key: string;
  label: string;
  unit: string;
  color: string;
  /** 连续段；段与段之间是缺失采样（通讯中断、服务停机等），不连线。 */
  segments: SeriesPoint[][];
}

function median(values: number[]): number {
  if (values.length === 0) return 0;
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.floor(sorted.length / 2)];
}

/**
 * 判定“缺口”的间隔阈值（毫秒）。
 * 期望步长取三者最大：时间桶宽度（降采样后每点至多代表一个桶）、分辨率本身（1m 为 60 秒）、
 * 相邻点间隔的中位数（raw 下约等于轮询间隔）。超过期望步长的 2.5 倍视为缺失采样。
 */
export function gapThreshold(
  times: readonly number[],
  resolution: "raw" | "1m",
  rangeMs: number,
): number {
  const diffs: number[] = [];
  for (let i = 1; i < times.length; i++) diffs.push(times[i] - times[i - 1]);
  const bucket = Math.ceil(rangeMs / MAX_POINTS);
  const base = resolution === "1m" ? 60_000 : 0;
  return Math.max(bucket, base, median(diffs)) * 2.5;
}

/** 由历史响应构建序列；字段在某点缺失同样断开。 */
export function buildSeries(
  response: HistoryResponse,
  keys: readonly string[],
  rangeMs: number,
): Series[] {
  const points = response.points
    .map((p) => ({ t: Date.parse(p.at), fields: p.fields ?? {} }))
    .filter((p) => !Number.isNaN(p.t))
    .sort((a, b) => a.t - b.t);
  return keys.map((key, index) => {
    const spec = lookupField(key);
    const own = points.filter((p) => p.fields[key] !== undefined);
    const threshold = gapThreshold(
      own.map((p) => p.t),
      response.resolution,
      rangeMs,
    );
    const segments: SeriesPoint[][] = [];
    let current: SeriesPoint[] = [];
    let last: number | null = null;
    for (const p of points) {
      const v = p.fields[key];
      if (!v) {
        // 该字段在这一采样缺失：断开。
        if (current.length > 0) segments.push(current);
        current = [];
        last = null;
        continue;
      }
      if (last !== null && p.t - last > threshold && current.length > 0) {
        segments.push(current);
        current = [];
      }
      current.push({ t: p.t, avg: v.avg, min: v.min, max: v.max, count: v.count });
      last = p.t;
    }
    if (current.length > 0) segments.push(current);
    return {
      key,
      label: fieldLabel(key),
      unit: spec?.kind === "duration" ? "s" : (spec?.unit ?? ""),
      color: SERIES_COLORS[index % SERIES_COLORS.length],
      segments,
    };
  });
}

export interface SeriesStats {
  min: number;
  max: number;
  /** 按 count 加权的平均值（与后端降采样一致）。 */
  avg: number;
}

export function seriesStats(series: Series): SeriesStats | null {
  let min = Infinity;
  let max = -Infinity;
  let sum = 0;
  let weight = 0;
  for (const segment of series.segments) {
    for (const p of segment) {
      min = Math.min(min, p.min);
      max = Math.max(max, p.max);
      const w = p.count > 0 ? p.count : 1;
      sum += p.avg * w;
      weight += w;
    }
  }
  if (weight === 0) return null;
  return { min, max, avg: sum / weight };
}

export interface Scale {
  min: number;
  max: number;
  ticks: number[];
}

/** “好看”的坐标刻度；百分比固定 0～100，单值时上下各留余量。 */
export function niceScale(lo: number, hi: number, unit: string, count = 4): Scale {
  if (unit === "%" && lo >= 0 && hi <= 100) {
    return { min: 0, max: 100, ticks: [0, 25, 50, 75, 100] };
  }
  if (!Number.isFinite(lo) || !Number.isFinite(hi)) return { min: 0, max: 1, ticks: [0, 1] };
  if (lo === hi) {
    const pad = Math.abs(lo) * 0.1 || 1;
    lo -= pad;
    hi += pad;
  }
  const rough = (hi - lo) / count;
  const magnitude = 10 ** Math.floor(Math.log10(rough));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * magnitude).find((s) => s >= rough) ?? rough;
  const min = Math.floor(lo / step) * step;
  const max = Math.ceil(hi / step) * step;
  const ticks: number[] = [];
  for (let v = min; v <= max + step / 2; v += step) ticks.push(Number(v.toFixed(10)));
  return { min, max, ticks };
}

export function tickLabel(value: number, unit: string): string {
  return formatNumber(value, unit === "%" ? "%" : "");
}

export interface Interval {
  start: number;
  end: number;
}

/**
 * 停电区间：由该设备的 mains_lost 事件推导；结束取 cleared_at，没有时取其后最早的 mains_restored，
 * 仍在进行（active）取 `nowMs`。只返回与 `[from, to)` 相交的部分。
 */
export function outageIntervals(
  lost: readonly EventItem[],
  restored: readonly EventItem[],
  from: number,
  to: number,
  nowMs: number,
): Interval[] {
  const restoredTimes = restored
    .map((e) => Date.parse(e.started_at))
    .filter((t) => !Number.isNaN(t))
    .sort((a, b) => a - b);
  const out: Interval[] = [];
  for (const e of lost) {
    const start = Date.parse(e.started_at);
    if (Number.isNaN(start)) continue;
    let end: number | undefined;
    if (e.cleared_at) end = Date.parse(e.cleared_at);
    else if (e.active) end = nowMs;
    else end = restoredTimes.find((t) => t >= start);
    if (end === undefined || Number.isNaN(end)) continue;
    const s = Math.max(start, from);
    const t = Math.min(end, to);
    if (t > s) out.push({ start: s, end: t });
  }
  return out.sort((a, b) => a.start - b.start);
}

/** 按单位分组：不同单位的字段分别绘图，避免共用一根纵轴造成误读。 */
export function groupByUnit(series: readonly Series[]): Series[][] {
  const groups = new Map<string, Series[]>();
  for (const s of series) {
    const list = groups.get(s.unit) ?? [];
    list.push(s);
    groups.set(s.unit, list);
  }
  return [...groups.values()];
}

/** 历史字段的显示单位（时长字段以秒存储）。 */
export function historyUnitLabel(unit: string): string {
  return unit === "s" ? "秒" : unit;
}

/** 校验并去重 URL 中的字段参数，只保留可选实例。 */
export function sanitizeHistoryFields(
  raw: readonly string[],
  options: readonly string[],
): string[] {
  const allowed = new Set(options);
  const out: string[] = [];
  for (const key of raw) {
    if (allowed.has(key) && !out.includes(key) && splitFieldKey(key)) out.push(key);
    if (out.length >= MAX_HISTORY_FIELDS) break;
  }
  return out;
}
