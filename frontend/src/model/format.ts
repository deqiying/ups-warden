/*
 * 字段显示规则（FR-UI-05、foundations「数据质量」、frontend-plan 3.5）：
 * - good：数值加单位；Duration 显示为与后端通知模板一致的“x 分 y 秒”。
 * - invalid / unsupported / 快照缺失的键：「—」加原因，**绝不显示 0**。
 * - stale（comm_stale）或实时连接断开：保留最后读数并置灰，由调用方注明读数时间；不外推。
 * 页面只通过 formatField 读取字段，不自行判断 quality。
 */
import type { Field, Quality } from "../api/types.ts";
import { lookupField, splitFieldKey } from "./fields.ts";
import { enumLabel, reasonLabel } from "./labels.ts";

export const NA_TEXT = "—";

export type FieldDisplay =
  | {
      kind: "value";
      /** 不含单位的显示文本。 */
      text: string;
      unit: string;
      /** 应置灰：字段 stale，或调用方声明的实时数据过期。 */
      stale: boolean;
      /** 剩余时间来自本地估算（runtime_source=estimated），显示「估算」标记。 */
      estimated: boolean;
    }
  | {
      kind: "na";
      text: typeof NA_TEXT;
      /** 原因文案，供 ReasonTip 或行内说明使用。 */
      reason: string;
      /** 原因码；快照缺失为 not_mapped（与后端 Resolve 一致），good 但值为空为 empty。 */
      reasonCode: string;
      quality: Quality | "missing";
    };

export interface FormatFieldOptions {
  /** 实时连接断开或尚未重新同步：数值保留但置灰。 */
  stale?: boolean;
  /** 设备的 runtime_source；runtime_estimate 为 estimated 时显示“约”。 */
  runtimeSource?: string;
}

/** 单位对应的小数位：频率固定一位（效果图 50.0 Hz），百分比取整。 */
const UNIT_DIGITS: Readonly<Record<string, { min: number; max: number }>> = {
  V: { min: 0, max: 1 },
  A: { min: 0, max: 1 },
  Hz: { min: 1, max: 1 },
  "%": { min: 0, max: 0 },
  W: { min: 0, max: 0 },
  VA: { min: 0, max: 0 },
  "°C": { min: 0, max: 1 },
};

const numberFormats = new Map<string, Intl.NumberFormat>();

export function formatNumber(value: number, unit = ""): string {
  const digits = UNIT_DIGITS[unit] ?? { min: 0, max: 2 };
  const cacheKey = `${digits.min}:${digits.max}`;
  let format = numberFormats.get(cacheKey);
  if (!format) {
    format = new Intl.NumberFormat("zh-CN", {
      minimumFractionDigits: digits.min,
      maximumFractionDigits: digits.max,
      useGrouping: false,
    });
    numberFormats.set(cacheKey, format);
  }
  // 避免 -0 显示为 “-0”。
  return format.format(value === 0 ? 0 : value);
}

const DURATION_UNITS = [
  { seconds: 86400, label: "天" },
  { seconds: 3600, label: "小时" },
  { seconds: 60, label: "分" },
] as const;

/**
 * 时长文本，与后端 notify durationText 一致：`0 秒`、`12 分 30 秒`、`1 天 1 小时 1 分 1 秒`
 * （为 0 的高位单位省略，秒总是保留）。`precision: "minute"` 用于估算值：四舍五入到分钟且不显示秒。
 */
export function durationText(seconds: number, precision: "second" | "minute" = "second"): string {
  let rest = Math.floor(Math.max(0, seconds));
  if (precision === "minute") rest = Math.round(rest / 60) * 60;
  const parts: string[] = [];
  for (const unit of DURATION_UNITS) {
    const count = Math.floor(rest / unit.seconds);
    if (count > 0) {
      parts.push(`${count} ${unit.label}`);
      rest %= unit.seconds;
    }
  }
  if (precision === "minute") return parts.length > 0 ? parts.join(" ") : "不足 1 分";
  parts.push(`${rest} 秒`);
  return parts.join(" ");
}

/** 计时器样式：`10:13`、`1:02:03`。用于已放电与倒计时。 */
export function clockText(seconds: number): string {
  const total = Math.floor(Math.max(0, seconds));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const mm = String(m).padStart(2, "0");
  const ss = String(s).padStart(2, "0");
  return h > 0 ? `${h}:${mm}:${ss}` : `${mm}:${ss}`;
}

/** 派生时长字段在非电池供电时为 unsupported/not_mapped，原因应描述业务含义而不是“未映射”。 */
const OFF_BATTERY_REASONS: Readonly<Record<string, string>> = {
  on_battery_elapsed: "未在电池供电",
  runtime_estimate: "仅电池供电时估算",
};

function na(reasonCode: string, quality: Quality | "missing", reason: string): FieldDisplay {
  return { kind: "na", text: NA_TEXT, reason, reasonCode, quality };
}

/**
 * 把一个统一字段转换为显示结构。`key` 可为按相实例键（如 `input_voltage[1]`）。
 * `field` 为 undefined 表示快照中缺少该键。
 */
export function formatField(
  key: string,
  field: Field | undefined,
  options: FormatFieldOptions = {},
): FieldDisplay {
  const base = splitFieldKey(key)?.base ?? key;
  if (!field) return na("not_mapped", "missing", reasonLabel("not_mapped"));

  if (field.quality === "invalid" || field.quality === "unsupported") {
    const offBattery = field.reason === "not_mapped" ? OFF_BATTERY_REASONS[base] : undefined;
    return na(field.reason, field.quality, offBattery ?? reasonLabel(field.reason, field.quality));
  }

  const spec = lookupField(key);
  const stale = field.quality === "stale" || options.stale === true;
  const value = (text: string, unit = "", estimated = false): FieldDisplay => ({
    kind: "value",
    text,
    unit,
    stale,
    estimated,
  });

  switch (spec?.kind) {
    case "number":
      return value(formatNumber(field.num, spec.unit), spec.unit);
    case "duration": {
      const estimated = base === "runtime_estimate" && options.runtimeSource === "estimated";
      return estimated
        ? value(`约 ${durationText(field.num, "minute")}`, "", true)
        : value(durationText(field.num));
    }
    case "bool":
      return value(field.bool ? "是" : "否");
    case "enum":
      return field.str ? value(enumLabel(base, field.str)) : na("empty", field.quality, "无数据");
    default:
      // text 与未知字段：按文本显示；good 的空字符串（omitempty 省略）不伪装成有值。
      return field.str ? value(field.str) : na("empty", field.quality, "无数据");
  }
}

/** 纯文本形式（如表格、通知摘要）：值与单位以空格连接，非 good 显示「—」。 */
export function fieldText(
  key: string,
  field: Field | undefined,
  options?: FormatFieldOptions,
): string {
  const display = formatField(key, field, options);
  if (display.kind === "na") return NA_TEXT;
  return display.unit ? `${display.text} ${display.unit}` : display.text;
}

/** 只有 good 字段的数值可参与计算或比较（例如环形图、阈值着色）；其余返回 null。 */
export function goodNumber(field: Field | undefined): number | null {
  return field && field.quality === "good" ? field.num : null;
}
