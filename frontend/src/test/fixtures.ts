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

// ───────────────────────── F5：主机 / 动作 / 计划 / 执行记录 ─────────────────────────

type HostWire = import("../api/hosts.ts").Host;
type ActionWire = import("../api/actions.ts").Action;
type ActionRunWire = import("../api/actions.ts").ActionRun;
type PlanWire = import("../api/plans.ts").Plan;

/** 已设置密码、指纹未确认的 SSH 主机（NAS）。 */
export function wireHost(overrides: Partial<HostWire> = {}): HostWire {
  return {
    id: 1,
    name: "NAS-01",
    address: "nas-01.lan",
    kind: "nas",
    conn: { port: 22, username: "admin", auth_method: "password", sudo: false },
    hostkey_fingerprint: "",
    online_check: { type: "tcp", port: 22 },
    password_set: true,
    private_key_set: false,
    passphrase_set: false,
    ...overrides,
  };
}

/** SSH 关机命令动作（引用主机 #1）。 */
export function wireAction(overrides: Partial<ActionWire> = {}): ActionWire {
  return {
    id: 1,
    name: "关闭 NAS",
    type: "ssh",
    timeout_s: 30,
    config: { host_id: 1, command: "poweroff", shutdown: true },
    url_set: false,
    headers_set: false,
    body_template_set: false,
    ...overrides,
  };
}

export function wireActionRun(overrides: Partial<ActionRunWire> = {}): ActionRunWire {
  return {
    id: 1,
    action_id: 1,
    trigger: { type: "manual", user_id: 1 },
    dry_run: true,
    status: "dry_run",
    started_at: T0,
    ended_at: T0,
    exit_code: null,
    stdout: "",
    stderr: "",
    error: "",
    duration_ms: 3,
    ...overrides,
  };
}

/** 两阶段计划：阶段 0 关闭 NAS（动作 #1），最后阶段为自关机（动作 #2）。 */
export function wirePlan(overrides: Partial<PlanWire> = {}): PlanWire {
  return {
    id: 1,
    name: "机房关机",
    countdown_s: 60,
    on_mains_restored: "cancel_pending",
    on_restart: "resume",
    notify_channel_ids: [1],
    stages: [
      { delay_s: 0, step_timeout_s: 30, offline_wait_s: 120, steps: [{ action_id: 1 }] },
      {
        delay_s: 5,
        step_timeout_s: 30,
        offline_wait_s: 0,
        self_shutdown: true,
        steps: [{ action_id: 2 }],
      },
    ],
    ...overrides,
  };
}

type WirePlanStepWire = import("../api/types.ts").WirePlanStep;

/** 计划运行中的一个步骤（线上格式）；默认是第 1 个阶段（下标 0）的第 1 步、尚未开始。 */
export function wirePlanStep(overrides: Partial<WirePlanStepWire> = {}): WirePlanStepWire {
  return {
    stage: 0,
    step: 0,
    action_id: 1,
    host_id: null,
    status: "waiting",
    started_at: null,
    ended_at: null,
    result: null,
    ...overrides,
  };
}
