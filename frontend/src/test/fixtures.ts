/* 测试用线上数据构造器：只填契约必需字段，其余按需覆盖。 */
import type {
  NotificationResult,
  SessionResponse,
  Settings,
  WireDeviceState,
  WireEventView,
  WirePlanRun,
  WireSseEvent,
  WireSync,
} from "../api/types.ts";

export const T0 = "2026-10-27T06:20:58Z";

export function wireDevice(overrides: Partial<WireDeviceState> = {}): WireDeviceState {
  return {
    device_id: 1,
    schema_version: 1,
    at: T0,
    fields: { battery_capacity_pct: { num: 89, quality: "good" } },
    power_state_raw: "online",
    power_state: "online",
    comm_state: "ok",
    last_ok_at: T0,
    runtime_source: "none",
    ...overrides,
  };
}

export function wireEventView(overrides: Partial<WireEventView> = {}): WireEventView {
  return {
    id: 1,
    device_id: 1,
    type: "mains_lost",
    severity: "critical",
    started_at: T0,
    cleared_at: null,
    active: true,
    summary: null,
    ...overrides,
  };
}

export function wireSseEvent(overrides: Partial<WireSseEvent> = {}): WireSseEvent {
  return {
    id: 1,
    device_id: 1,
    type: "mains_lost",
    severity: "critical",
    at: T0,
    active: true,
    summary: null,
    ...overrides,
  };
}

export function wirePlanRun(overrides: Partial<WirePlanRun> = {}): WirePlanRun {
  return {
    id: 1,
    plan_id: 1,
    plan_name: "机房关机",
    device_id: 1,
    outage_cycle_id: 12,
    dry_run: true,
    status: "countdown",
    trigger: null,
    countdown_until: "2026-10-27T06:21:45Z",
    current_stage: 0,
    started_at: T0,
    ended_at: null,
    steps: [],
    ...overrides,
  };
}

export function wireSync(overrides: Partial<WireSync> = {}): WireSync {
  return { schema_version: 1, devices: [], alerts: [], plan_runs: [], ...overrides };
}

export function notifyResult(overrides: Partial<NotificationResult> = {}): NotificationResult {
  return {
    id: 1,
    channel_id: 1,
    event_id: 1,
    at: T0,
    target: "chat:1",
    summary: "市电中断",
    ok: false,
    duration_ms: 120,
    error: "timeout",
    attempts: 3,
    template_fallback: false,
    ...overrides,
  };
}

export function sessionResponse(overrides: Partial<SessionResponse> = {}): SessionResponse {
  return {
    user: { id: 1, username: "admin", role: "admin", created_at: T0, last_login_at: T0 },
    csrf_token: "csrf-1",
    expires_at: "2026-10-28T06:05:00Z",
    ...overrides,
  };
}

export function settings(overrides: Partial<Settings> = {}): Settings {
  return {
    detail_retention_days: 7,
    aggregate_retention_days: 90,
    log_retention_days: 90,
    device_defaults: {
      outage_confirm_s: 10,
      recovery_confirm_s: 30,
      input_voltage_threshold: 160,
      comm_failures: 3,
      comm_lost_policy: "hold",
      assume_on_battery_after_s: 300,
      battery_low_pct: 30,
      battery_hysteresis_pct: 5,
      overload_pct: 90,
      load_hysteresis_pct: 5,
    },
    timezone: "Asia/Shanghai",
    language: "zh-CN",
    http_action_guard_ups_hosts: true,
    ...overrides,
  };
}

/** 可手动触发事件的 EventSource 替身。 */
export class FakeEventSource {
  static instances: FakeEventSource[] = [];
  readonly url: string;
  closed = false;
  private readonly listeners = new Map<string, ((event: Event) => void)[]>();

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  static reset(): void {
    FakeEventSource.instances = [];
  }

  static latest(): FakeEventSource {
    const last = FakeEventSource.instances.at(-1);
    if (!last) throw new Error("尚未创建 EventSource");
    return last;
  }

  addEventListener(type: string, listener: (event: Event) => void): void {
    const list = this.listeners.get(type) ?? [];
    list.push(listener);
    this.listeners.set(type, list);
  }

  close(): void {
    this.closed = true;
  }

  open(): void {
    this.fire("open", new Event("open"));
  }

  fail(): void {
    this.fire("error", new Event("error"));
  }

  send(type: string, data: unknown): void {
    const text = typeof data === "string" ? data : JSON.stringify(data);
    this.fire(type, new MessageEvent(type, { data: text }));
  }

  private fire(type: string, event: Event): void {
    for (const listener of this.listeners.get(type) ?? []) listener(event);
  }
}
