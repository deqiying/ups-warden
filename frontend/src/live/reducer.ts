/*
 * SSE 实时存储的纯函数部分：帧解析与状态归并。
 *
 * 契约（docs/reference/monitoring-api.md「SSE」）：
 * - sync：全量替换 devices / alerts / plan_runs；每次（重）连接都会先收到 sync。
 * - device_state：替换单台设备状态。
 * - alert：替换该设备的活动告警列表，空数组表示全部解除。
 * - event：新业务事件，连接边界可能重复，按 id 去重。
 * - plan_progress：运行落库后的完整视图；进入终态的运行移出活动集合。
 * - notify_failed：单目标最终失败的通知日志，按日志 id 去重。
 * 断线期间不是连续观测：重连后以 sync 为准，不外推实时值。
 */
import {
  isActivePlanRun,
  normalizeDeviceState,
  normalizeEventView,
  normalizePlanRun,
  normalizeSseEvent,
} from "../api/normalize.ts";
import type {
  DeviceState,
  EventItem,
  NotificationResult,
  PlanRun,
  WireAlertFrame,
  WireDeviceState,
  WirePlanRun,
  WireSseEvent,
  WireSync,
} from "../api/types.ts";

/** 最近事件与通知失败只保留有限条数，供实时提示与去重；完整记录走查询 API。 */
export const RECENT_EVENT_LIMIT = 200;
export const NOTIFY_FAILED_LIMIT = 100;

export interface LiveData {
  /** 自上次（重）连接以来是否已收到 sync；未同步时数据只能当作过期快照。 */
  synced: boolean;
  /** 最近一次 sync 的本地接收时间（ISO）。 */
  syncedAt: string | null;
  /** 运行中的设备状态，按 device_id 升序。 */
  devices: readonly DeviceState[];
  /** 活动告警，按 device_id 分组；每组按事件 id 降序（与后端一致）。 */
  alerts: Readonly<Record<number, readonly EventItem[]>>;
  /** 活动关机计划运行（countdown / running），按运行 id 升序。 */
  planRuns: readonly PlanRun[];
  /** 最近收到的新事件，最新在前，按 id 去重。 */
  recentEvents: readonly EventItem[];
  /** 最近的通知最终失败记录，最新在前，按日志 id 去重。 */
  notifyFailures: readonly NotificationResult[];
}

export const initialLiveData: LiveData = {
  synced: false,
  syncedAt: null,
  devices: [],
  alerts: {},
  planRuns: [],
  recentEvents: [],
  notifyFailures: [],
};

export type LiveFrame =
  | { type: "sync"; data: WireSync }
  | { type: "device_state"; data: WireDeviceState }
  | { type: "alert"; data: WireAlertFrame }
  | { type: "event"; data: WireSseEvent }
  | { type: "plan_progress"; data: WirePlanRun }
  | { type: "notify_failed"; data: NotificationResult };

export const FRAME_TYPES = [
  "sync",
  "device_state",
  "alert",
  "event",
  "plan_progress",
  "notify_failed",
] as const satisfies readonly LiveFrame["type"][];

/** 帧处理的副作用信号，由 store 转发给订阅方（查询失效、toast）。 */
export interface FrameEffects {
  /** 收到 sync：已结束的运行可能在断线期间离开了活动集合，查询应失效。 */
  resynced: boolean;
  /** 本帧使其进入终态的运行。 */
  finishedRuns: PlanRun[];
  /** 本帧新增（未见过 id）的通知失败。 */
  newNotifyFailures: NotificationResult[];
}

const noEffects = (): FrameEffects => ({
  resynced: false,
  finishedRuns: [],
  newNotifyFailures: [],
});

function sortById<T>(items: T[], id: (item: T) => number): T[] {
  return items.sort((a, b) => id(a) - id(b));
}

function groupAlerts(items: readonly EventItem[]): Record<number, EventItem[]> {
  const out: Record<number, EventItem[]> = {};
  for (const item of items) {
    (out[item.device_id] ??= []).push(item);
  }
  for (const list of Object.values(out)) list.sort((a, b) => b.id - a.id);
  return out;
}

function upsertNotifyFailures(
  existing: readonly NotificationResult[],
  incoming: readonly NotificationResult[],
): { list: readonly NotificationResult[]; added: NotificationResult[] } {
  const seen = new Set(existing.map((r) => r.id));
  const added: NotificationResult[] = [];
  for (const r of incoming) {
    if (seen.has(r.id)) continue;
    seen.add(r.id);
    added.push(r);
  }
  if (added.length === 0) return { list: existing, added };
  const merged = [...added, ...existing].sort((a, b) => b.id - a.id).slice(0, NOTIFY_FAILED_LIMIT);
  return { list: merged, added };
}

/**
 * 归并一帧，返回新状态与副作用。状态不可变：未变化的部分保持原引用，便于选择器比较。
 * `nowIso` 仅用于记录 sync 接收时间。
 */
export function applyFrame(
  state: LiveData,
  frame: LiveFrame,
  nowIso: string,
): { state: LiveData; effects: FrameEffects } {
  const effects = noEffects();
  switch (frame.type) {
    case "sync": {
      const devices = sortById(
        (frame.data.devices ?? []).map(normalizeDeviceState),
        (d) => d.device_id,
      );
      const alerts = groupAlerts((frame.data.alerts ?? []).map(normalizeEventView));
      const planRuns = sortById(
        (frame.data.plan_runs ?? []).map(normalizePlanRun).filter(isActivePlanRun),
        (r) => r.id,
      );
      effects.resynced = true;
      return {
        state: { ...state, synced: true, syncedAt: nowIso, devices, alerts, planRuns },
        effects,
      };
    }
    case "device_state": {
      const next = normalizeDeviceState(frame.data);
      const index = state.devices.findIndex((d) => d.device_id === next.device_id);
      const devices =
        index >= 0
          ? state.devices.map((d, i) => (i === index ? next : d))
          : sortById([...state.devices, next], (d) => d.device_id);
      return { state: { ...state, devices }, effects };
    }
    case "alert": {
      const items = (frame.data.items ?? []).map(normalizeEventView).sort((a, b) => b.id - a.id);
      const alerts: Record<number, readonly EventItem[]> = { ...state.alerts };
      if (items.length === 0) delete alerts[frame.data.device_id];
      else alerts[frame.data.device_id] = items;
      return { state: { ...state, alerts }, effects };
    }
    case "event": {
      if (state.recentEvents.some((e) => e.id === frame.data.id)) return { state, effects };
      const item = normalizeSseEvent(frame.data);
      const recentEvents = [item, ...state.recentEvents]
        .sort((a, b) => b.id - a.id)
        .slice(0, RECENT_EVENT_LIMIT);
      return { state: { ...state, recentEvents }, effects };
    }
    case "plan_progress": {
      const run = normalizePlanRun(frame.data);
      const rest = state.planRuns.filter((r) => r.id !== run.id);
      if (isActivePlanRun(run)) {
        return { state: { ...state, planRuns: sortById([...rest, run], (r) => r.id) }, effects };
      }
      effects.finishedRuns.push(run);
      return { state: { ...state, planRuns: rest }, effects };
    }
    case "notify_failed": {
      const { list, added } = upsertNotifyFailures(state.notifyFailures, [frame.data]);
      effects.newNotifyFailures = added;
      return {
        state: list === state.notifyFailures ? state : { ...state, notifyFailures: list },
        effects,
      };
    }
  }
}

/** 合并重连后从 `/notify-logs` 补齐的失败记录，同样按 id 去重。 */
export function mergeNotifyFailures(
  state: LiveData,
  items: readonly NotificationResult[],
): { state: LiveData; added: NotificationResult[] } {
  const failures = items.filter((r) => !r.ok);
  const { list, added } = upsertNotifyFailures(state.notifyFailures, failures);
  return {
    state: list === state.notifyFailures ? state : { ...state, notifyFailures: list },
    added,
  };
}

/** 断线：保留最后数据供降级显示，但标记为未同步。 */
export function markUnsynced(state: LiveData): LiveData {
  return state.synced ? { ...state, synced: false } : state;
}

/** 解析一帧 SSE 数据；JSON 非法或类型未知时返回 null（由调用方记录诊断）。 */
export function parseFrame(type: string, data: string): LiveFrame | null {
  if (!(FRAME_TYPES as readonly string[]).includes(type)) return null;
  let value: unknown;
  try {
    value = JSON.parse(data);
  } catch {
    return null;
  }
  if (typeof value !== "object" || value === null) return null;
  return { type, data: value } as LiveFrame;
}
