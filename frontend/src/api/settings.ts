import { api } from "./client.ts";
import type { DeviceConfig, Settings } from "./types.ts";

/*
 * 系统设置 API（docs/reference/settings-api.md）。
 * - PATCH 只覆盖显式字段并返回更新后的全部设置；任一字段越界 422 validation_failed，不部分保存；
 * - `device_defaults` 整体替换（省略的子字段取内置默认值，而不是上一次保存的值），因此始终整体提交；
 * - 把 `http_action_guard_ups_hosts` 从 true 改为 false 必须同时带 `confirm_disable_guard:true`，
 *   否则 422 confirmation_required；该字段只用于确认、不保存，重新开启不需要。
 */

export function getSettings(signal?: AbortSignal): Promise<Settings> {
  return api.request<Settings>("/settings", { signal });
}

/** PATCH /settings 请求体：所有字段可选，`confirm_disable_guard` 仅在关闭护栏时携带。 */
export interface SettingsPatch {
  detail_retention_days?: number;
  aggregate_retention_days?: number;
  log_retention_days?: number;
  device_defaults?: DeviceConfig;
  timezone?: string;
  language?: Settings["language"];
  http_action_guard_ups_hosts?: boolean;
  confirm_disable_guard?: true;
}

export function updateSettings(patch: SettingsPatch): Promise<Settings> {
  return api.request<Settings>("/settings", { method: "PATCH", body: patch });
}

// ───────────────────────── 通用（时区、语言） ─────────────────────────

/** 通用设置即时保存，每次请求只含一个字段。 */
export function buildGeneralPatch<K extends "timezone" | "language">(
  key: K,
  value: Settings[K],
): Pick<Settings, K> {
  return { [key]: value } as Pick<Settings, K>;
}

// ───────────────────────── 数据保留 ─────────────────────────

export const RETENTION_LIMITS = {
  detailMin: 1,
  detailMax: 90,
  aggregateMax: 3650,
  logMin: 1,
  logMax: 3650,
} as const;

export interface RetentionDraft {
  detail_retention_days: number;
  aggregate_retention_days: number;
  log_retention_days: number;
}
export type RetentionField = keyof RetentionDraft;

export const RETENTION_FIELDS: readonly RetentionField[] = [
  "detail_retention_days",
  "aggregate_retention_days",
  "log_retention_days",
];

export function retentionOf(settings: Settings): RetentionDraft {
  return {
    detail_retention_days: settings.detail_retention_days,
    aggregate_retention_days: settings.aggregate_retention_days,
    log_retention_days: settings.log_retention_days,
  };
}

/**
 * 与后端 settings.Validate 对齐的前端校验：明细 1～90 天；聚合不小于明细且不超过 3650 天；
 * 日志 1～3650 天。最终以后端 422 为准。
 */
export function validateRetention(draft: RetentionDraft): Partial<Record<RetentionField, string>> {
  const errors: Partial<Record<RetentionField, string>> = {};
  const { detail_retention_days: detail, aggregate_retention_days: aggregate } = draft;
  const logDays = draft.log_retention_days;
  const detailOk =
    Number.isInteger(detail) &&
    detail >= RETENTION_LIMITS.detailMin &&
    detail <= RETENTION_LIMITS.detailMax;
  if (!detailOk) errors.detail_retention_days = "明细样本保留期需为 1～90 天的整数";
  if (!Number.isInteger(aggregate) || aggregate < 1 || aggregate > RETENTION_LIMITS.aggregateMax) {
    errors.aggregate_retention_days = "分钟聚合保留期需为 1～3650 天的整数";
  } else if (detailOk && aggregate < detail) {
    errors.aggregate_retention_days = `分钟聚合保留期不能少于明细样本保留期（${detail} 天）`;
  }
  if (
    !Number.isInteger(logDays) ||
    logDays < RETENTION_LIMITS.logMin ||
    logDays > RETENTION_LIMITS.logMax
  ) {
    errors.log_retention_days = "日志与记录保留期需为 1～3650 天的整数";
  }
  return errors;
}

/** 只含修改过的保留期字段。 */
export function buildRetentionPatch(
  original: RetentionDraft,
  draft: RetentionDraft,
): Pick<SettingsPatch, RetentionField> {
  const patch: Pick<SettingsPatch, RetentionField> = {};
  for (const key of RETENTION_FIELDS) {
    if (draft[key] !== original[key]) patch[key] = draft[key];
  }
  return patch;
}

/** 被缩短的保留期字段：缩短会在下一轮每小时清理时删除超期数据（Q-P3），保存前必须确认。 */
export function shortenedRetention(
  original: RetentionDraft,
  draft: RetentionDraft,
): RetentionField[] {
  return RETENTION_FIELDS.filter((key) => draft[key] < original[key]);
}

// ───────────────────────── 设备默认值 ─────────────────────────

/** 内置设备默认阈值，镜像后端 state.DefaultConfig；「恢复内置默认值」使用。 */
export const BUILTIN_DEVICE_DEFAULTS: Readonly<DeviceConfig> = {
  outage_confirm_s: 10,
  recovery_confirm_s: 60,
  input_voltage_threshold: 50,
  comm_failures: 3,
  comm_lost_policy: "hold",
  assume_on_battery_after_s: 300,
  battery_low_pct: 30,
  battery_hysteresis_pct: 5,
  overload_pct: 90,
  load_hysteresis_pct: 5,
};

/** device_defaults 整体替换，因此总是提交完整对象。 */
export function buildDefaultsPatch(config: DeviceConfig): { device_defaults: DeviceConfig } {
  return { device_defaults: { ...config } };
}

// ───────────────────────── 安全（HTTP 护栏） ─────────────────────────

/**
 * 护栏开关请求体：关闭（false）带 `confirm_disable_guard:true`，调用方须在用户确认后才构造；
 * 开启（true）不带确认字段。
 */
export function buildGuardPatch(enabled: boolean): SettingsPatch {
  return enabled
    ? { http_action_guard_ups_hosts: true }
    : { http_action_guard_ups_hosts: false, confirm_disable_guard: true };
}
