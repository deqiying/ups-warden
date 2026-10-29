/*
 * 后端公开 API 的手写类型（OpenAPI 为 P1）。
 *
 * 对照来源：docs/reference/*.md 与 backend/internal/model、backend/internal/api 的 JSON 标签。
 * `Wire*` 为线上原始形状；不带前缀的类型是经 api/normalize.ts 补齐默认值后的视图，
 * 页面与组件只使用后者。时间均为 RFC3339 UTC 字符串，显示时按设置时区格式化。
 */

/** 字段质量；业务判断只能读取 good（model.Quality）。 */
export type Quality = "good" | "invalid" | "unsupported" | "stale";

/**
 * 线上 Field。`num` / `bool` / `str` 带 omitempty：good 数值为 0、布尔为 false、文本为空时会被省略，
 * 解析时必须补齐默认值，不能把“缺少 num”理解为无值。
 */
export interface WireField {
  num?: number;
  bool?: boolean;
  str?: string;
  quality: Quality;
  reason?: string;
}

/** 补齐默认值后的 Field；非 good 时 num/bool/str 无业务含义，不能当作 0 使用。 */
export interface Field {
  num: number;
  bool: boolean;
  str: string;
  quality: Quality;
  /** 原因码（model.Reason*，另有 runtime_estimate 的 `estimating`）；good 时通常为空串。 */
  reason: string;
}

export type FieldMap = Readonly<Record<string, Field>>;

export interface WireDeviceState {
  device_id: number;
  schema_version: number;
  at: string;
  fields: Record<string, WireField> | null;
  power_state_raw: string;
  power_state: string;
  pending_since?: string;
  on_battery_since?: string;
  outage_cycle_id?: number;
  comm_state: string;
  /** Go 零值时间（从未成功采集）为 0001-01-01T00:00:00Z。 */
  last_ok_at: string;
  runtime_source: string;
}

/** 设备运行状态视图（DeviceState）。 */
export interface DeviceState {
  device_id: number;
  schema_version: number;
  at: string;
  fields: FieldMap;
  power_state_raw: string;
  power_state: string;
  pending_since: string | null;
  on_battery_since: string | null;
  outage_cycle_id: number | null;
  comm_state: string;
  /** 从未成功采集时为 null（线上为 Go 零值时间）。 */
  last_ok_at: string | null;
  runtime_source: string;
}

export type Severity = "info" | "warning" | "critical";

/** REST `GET /events` / `GET /alerts` 与 SSE `sync.alerts`、`alert.items` 的事件视图。 */
export interface WireEventView {
  id: number;
  device_id: number;
  type: string;
  severity: Severity;
  started_at: string;
  cleared_at: string | null;
  active: boolean;
  summary: Record<string, WireField> | null;
}

/** SSE `event` 帧：新业务事件，使用 `at` 而非 `started_at`。 */
export interface WireSseEvent {
  id: number;
  device_id: number;
  type: string;
  severity: Severity;
  at: string;
  active: boolean;
  clears?: string;
  summary: Record<string, WireField> | null;
}

/** REST 与 SSE 事件统一后的视图。 */
export interface EventItem {
  id: number;
  device_id: number;
  type: string;
  severity: Severity;
  started_at: string;
  /** SSE 帧不携带清除时间，此时为 null。 */
  cleared_at: string | null;
  active: boolean;
  /** 仅 SSE 帧可能携带：被本事件清除的原活动事件类型。 */
  clears: string | null;
  summary: FieldMap;
}

export type PlanRunStatus =
  | "countdown"
  | "running"
  | "completed"
  | "completed_with_errors"
  | "cancelled"
  | "aborted";

export type PlanStepStatus =
  | "waiting"
  | "running"
  | "sent"
  | "offline"
  | "failed"
  | "cancelled"
  | "unknown";

export interface WirePlanStep {
  stage: number;
  step: number;
  action_id: number;
  host_id: number | null;
  status: PlanStepStatus;
  started_at: string | null;
  ended_at: string | null;
  result: {
    action_run_id?: number;
    action_status?: string;
    error?: string;
    offline_timeout?: boolean;
  } | null;
}

export interface PlanStep {
  stage: number;
  step: number;
  action_id: number;
  host_id: number | null;
  status: PlanStepStatus;
  started_at: string | null;
  ended_at: string | null;
  result: {
    action_run_id: number | null;
    action_status: string;
    error: string;
    /** `sent` 步骤等待离线超时。 */
    offline_timeout: boolean;
  };
}

export interface WirePlanRun {
  id: number;
  plan_id: number;
  plan_name: string;
  device_id: number | null;
  outage_cycle_id: number | null;
  dry_run: boolean;
  status: PlanRunStatus;
  /** 结构未写入契约（Q-C5），前端不解析。 */
  trigger: unknown;
  countdown_until: string | null;
  current_stage: number;
  started_at: string;
  ended_at: string | null;
  steps: WirePlanStep[] | null;
}

export interface PlanRun extends Omit<WirePlanRun, "steps"> {
  steps: PlanStep[];
}

/** 通知单目标发送结果（GET /notify-logs 项与 SSE notify_failed）。 */
export interface NotificationResult {
  id: number;
  channel_id: number;
  event_id: number | null;
  at: string;
  target: string;
  summary: string;
  ok: boolean;
  duration_ms: number;
  error: string;
  attempts: number;
  template_fallback: boolean;
}

/** SSE `sync` 帧。 */
export interface WireSync {
  schema_version: number;
  devices: WireDeviceState[] | null;
  alerts: WireEventView[] | null;
  plan_runs: WirePlanRun[] | null;
}

/** SSE `alert` 帧：替换该设备的活动告警列表；空数组表示全部解除。 */
export interface WireAlertFrame {
  device_id: number;
  items: WireEventView[] | null;
}

export interface User {
  id: number;
  username: string;
  /** P0 仅 admin。 */
  role: string;
  created_at: string;
  last_login_at: string | null;
}

/** `POST /auth/login` 与 `GET /auth/session` 的响应。CSRF token 只保存在内存。 */
export interface SessionResponse {
  user: User;
  csrf_token: string;
  /** 空闲与绝对期限中较早者。 */
  expires_at: string;
}

/** 设备状态配置（devices.config / settings.device_defaults）。 */
export interface DeviceConfig {
  outage_confirm_s: number;
  recovery_confirm_s: number;
  input_voltage_threshold: number;
  comm_failures: number;
  comm_lost_policy: "hold" | "ignore" | "assume_on_battery_after";
  assume_on_battery_after_s: number;
  battery_low_pct: number;
  battery_hysteresis_pct: number;
  overload_pct: number;
  load_hysteresis_pct: number;
}

/** `GET /settings` 响应。 */
export interface Settings {
  detail_retention_days: number;
  aggregate_retention_days: number;
  log_retention_days: number;
  device_defaults: DeviceConfig;
  /** IANA 时区名；只供前端显示，API 时间仍为 UTC。 */
  timezone: string;
  language: "zh-CN" | "en-US";
  http_action_guard_ups_hosts: boolean;
}

/** 游标分页响应；契约没有 total，界面不显示总数。`next_before=0` 表示无下一页。 */
export interface Page<T> {
  items: T[];
  next_before: number;
}

/** 统一错误体。 */
export interface WireError {
  code: string;
  message: string;
  details?: Record<string, unknown>;
}
