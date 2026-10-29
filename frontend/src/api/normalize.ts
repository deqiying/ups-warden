import type {
  DeviceState,
  EventItem,
  Field,
  FieldMap,
  PlanRun,
  PlanStep,
  WireDeviceState,
  WireEventView,
  WireField,
  WirePlanRun,
  WirePlanStep,
  WireSseEvent,
} from "./types.ts";

/**
 * 补齐 omitempty 省略的默认值：good 字段缺少 num 表示 0、缺少 bool 表示 false、缺少 str 表示空串。
 * 非 good 字段同样补齐，但其值没有业务含义，显示层必须先看 quality。
 */
export function normalizeField(wire: WireField): Field {
  return {
    num: typeof wire.num === "number" ? wire.num : 0,
    bool: wire.bool === true,
    str: typeof wire.str === "string" ? wire.str : "",
    quality: wire.quality,
    reason: typeof wire.reason === "string" ? wire.reason : "",
  };
}

export function normalizeFields(wire: Record<string, WireField> | null | undefined): FieldMap {
  const out: Record<string, Field> = {};
  if (!wire || typeof wire !== "object") return out;
  for (const [key, value] of Object.entries(wire)) {
    if (value && typeof value === "object") out[key] = normalizeField(value);
  }
  return out;
}

// Go 零值 time.Time 序列化为公元 1 年，表示“从未发生”，不是有效时间。
const GO_ZERO_TIME = /^0001-01-01T/;

function optionalTime(value: string | null | undefined): string | null {
  if (typeof value !== "string" || value === "" || GO_ZERO_TIME.test(value)) return null;
  return value;
}

export function normalizeDeviceState(wire: WireDeviceState): DeviceState {
  return {
    device_id: wire.device_id,
    schema_version: wire.schema_version,
    at: wire.at,
    fields: normalizeFields(wire.fields),
    power_state_raw: wire.power_state_raw ?? "",
    power_state: wire.power_state ?? "unknown",
    pending_since: optionalTime(wire.pending_since),
    on_battery_since: optionalTime(wire.on_battery_since),
    outage_cycle_id: typeof wire.outage_cycle_id === "number" ? wire.outage_cycle_id : null,
    comm_state: wire.comm_state ?? "unknown",
    last_ok_at: optionalTime(wire.last_ok_at),
    runtime_source: wire.runtime_source ?? "none",
  };
}

/** REST 事件视图（started_at / cleared_at）。 */
export function normalizeEventView(wire: WireEventView): EventItem {
  return {
    id: wire.id,
    device_id: wire.device_id,
    type: wire.type,
    severity: wire.severity,
    started_at: wire.started_at,
    cleared_at: optionalTime(wire.cleared_at),
    active: wire.active,
    clears: null,
    summary: normalizeFields(wire.summary),
  };
}

/** SSE `event` 帧使用 `at`；统一为 started_at，清除时间未知。 */
export function normalizeSseEvent(wire: WireSseEvent): EventItem {
  return {
    id: wire.id,
    device_id: wire.device_id,
    type: wire.type,
    severity: wire.severity,
    started_at: wire.at,
    cleared_at: null,
    active: wire.active,
    clears: wire.clears ? wire.clears : null,
    summary: normalizeFields(wire.summary),
  };
}

function normalizeStep(wire: WirePlanStep): PlanStep {
  const r = wire.result ?? {};
  return {
    stage: wire.stage,
    step: wire.step,
    action_id: wire.action_id,
    host_id: wire.host_id ?? null,
    status: wire.status,
    started_at: optionalTime(wire.started_at),
    ended_at: optionalTime(wire.ended_at),
    result: {
      action_run_id: typeof r.action_run_id === "number" ? r.action_run_id : null,
      action_status: r.action_status ?? "",
      error: r.error ?? "",
      offline_timeout: r.offline_timeout === true,
    },
  };
}

export function normalizePlanRun(wire: WirePlanRun): PlanRun {
  return {
    ...wire,
    device_id: wire.device_id ?? null,
    outage_cycle_id: wire.outage_cycle_id ?? null,
    countdown_until: optionalTime(wire.countdown_until),
    ended_at: optionalTime(wire.ended_at),
    steps: (wire.steps ?? []).map(normalizeStep),
  };
}

/** 活动运行（倒计时或执行中）；其余为终态。 */
export function isActivePlanRun(run: Pick<PlanRun, "status">): boolean {
  return run.status === "countdown" || run.status === "running";
}
