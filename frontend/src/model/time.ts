/*
 * 时间显示：API 时间均为 UTC（RFC3339），界面按 `GET /settings` 的 `timezone` 格式化（settings-api.md）。
 * 时区无效或尚未加载时回退为浏览器时区。
 */
import { NA_TEXT } from "./format.ts";

interface Parts {
  year: string;
  month: string;
  day: string;
  hour: string;
  minute: string;
  second: string;
  weekday: string;
}

const formatters = new Map<string, Intl.DateTimeFormat>();
const validZones = new Map<string, boolean>();

/** 校验 IANA 时区名；无效返回 undefined（使用浏览器时区）。 */
export function resolveTimeZone(timeZone: string | null | undefined): string | undefined {
  if (!timeZone) return undefined;
  let valid = validZones.get(timeZone);
  if (valid === undefined) {
    try {
      new Intl.DateTimeFormat("zh-CN", { timeZone });
      valid = true;
    } catch {
      // RangeError：时区名不被运行环境支持；回退浏览器时区并记录一次。
      console.warn(`无效的时区设置：${timeZone}，改用浏览器时区`);
      valid = false;
    }
    validZones.set(timeZone, valid);
  }
  return valid ? timeZone : undefined;
}

function formatter(timeZone: string | undefined): Intl.DateTimeFormat {
  const key = timeZone ?? "";
  let format = formatters.get(key);
  if (!format) {
    format = new Intl.DateTimeFormat("zh-CN", {
      timeZone,
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      weekday: "long",
      hourCycle: "h23",
    });
    formatters.set(key, format);
  }
  return format;
}

function toDate(value: string | number | Date): Date | null {
  const date = value instanceof Date ? value : new Date(value);
  return Number.isNaN(date.getTime()) ? null : date;
}

function parts(date: Date, timeZone: string | undefined): Parts {
  const out: Parts = {
    year: "",
    month: "",
    day: "",
    hour: "",
    minute: "",
    second: "",
    weekday: "",
  };
  for (const part of formatter(resolveTimeZone(timeZone)).formatToParts(date)) {
    if (part.type in out) out[part.type as keyof Parts] = part.value;
  }
  return out;
}

const trimZero = (text: string) => String(Number(text));

/** `14:20:58` */
export function formatTime(
  value: string | number | Date | null | undefined,
  timeZone?: string,
): string {
  const date = value == null ? null : toDate(value);
  if (!date) return NA_TEXT;
  const p = parts(date, timeZone);
  return `${p.hour}:${p.minute}:${p.second}`;
}

/** `10月28日 14:05`（侧栏会话期限、列表时间） */
export function formatDateTime(
  value: string | number | Date | null | undefined,
  timeZone?: string,
): string {
  const date = value == null ? null : toDate(value);
  if (!date) return NA_TEXT;
  const p = parts(date, timeZone);
  return `${trimZero(p.month)}月${trimZero(p.day)}日 ${p.hour}:${p.minute}`;
}

/** `2026-10-28 14:05:31`（审计、执行记录等需要精确时间处） */
export function formatFullDateTime(
  value: string | number | Date | null | undefined,
  timeZone?: string,
): string {
  const date = value == null ? null : toDate(value);
  if (!date) return NA_TEXT;
  const p = parts(date, timeZone);
  return `${p.year}-${p.month}-${p.day} ${p.hour}:${p.minute}:${p.second}`;
}

/** `10月27日 星期二`（页头 eyebrow） */
export function formatDateHeading(value: string | number | Date, timeZone?: string): string {
  const date = toDate(value);
  if (!date) return NA_TEXT;
  const p = parts(date, timeZone);
  return `${trimZero(p.month)}月${trimZero(p.day)}日 ${p.weekday}`;
}
