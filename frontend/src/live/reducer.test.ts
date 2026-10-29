import { describe, expect, it } from "vite-plus/test";
import {
  notifyResult,
  T0,
  wireDevice,
  wireEventView,
  wirePlanRun,
  wireSseEvent,
  wireSync,
} from "../test/fixtures.ts";
import {
  applyFrame,
  initialLiveData,
  markUnsynced,
  mergeNotifyFailures,
  NOTIFY_FAILED_LIMIT,
  parseFrame,
  RECENT_EVENT_LIMIT,
  type LiveData,
  type LiveFrame,
} from "./reducer.ts";

const NOW = "2026-10-27T06:30:00.000Z";

function apply(state: LiveData, frame: LiveFrame) {
  return applyFrame(state, frame, NOW);
}

function synced(frame: Parameters<typeof wireSync>[0] = {}): LiveData {
  return apply(initialLiveData, { type: "sync", data: wireSync(frame) }).state;
}

describe("sync", () => {
  it("全量替换设备、告警与活动运行，并标记已同步", () => {
    const before = synced({ devices: [wireDevice({ device_id: 9 })] });
    const { state, effects } = apply(before, {
      type: "sync",
      data: wireSync({
        devices: [wireDevice({ device_id: 2 }), wireDevice({ device_id: 1 })],
        alerts: [wireEventView({ id: 5, device_id: 2 }), wireEventView({ id: 7, device_id: 2 })],
        plan_runs: [wirePlanRun({ id: 3 }), wirePlanRun({ id: 4, status: "completed" })],
      }),
    });
    expect(state.synced).toBe(true);
    expect(state.syncedAt).toBe(NOW);
    expect(state.devices.map((d) => d.device_id)).toEqual([1, 2]);
    expect(state.alerts[2].map((a) => a.id)).toEqual([7, 5]);
    // sync 中的终态运行不进入活动集合。
    expect(state.planRuns.map((r) => r.id)).toEqual([3]);
    expect(effects.resynced).toBe(true);
  });

  it("null 列表按空处理", () => {
    const state = apply(initialLiveData, {
      type: "sync",
      data: { schema_version: 1, devices: null, alerts: null, plan_runs: null },
    }).state;
    expect(state.devices).toEqual([]);
    expect(state.alerts).toEqual({});
    expect(state.planRuns).toEqual([]);
  });

  it("good 字段省略 num 时补齐为 0（omitempty）", () => {
    const state = synced({
      devices: [wireDevice({ fields: { on_battery_elapsed: { quality: "good" } } })],
    });
    expect(state.devices[0].fields.on_battery_elapsed).toEqual({
      num: 0,
      bool: false,
      str: "",
      quality: "good",
      reason: "",
    });
  });
});

describe("device_state", () => {
  it("替换同一设备，其他设备保持原引用", () => {
    const before = synced({
      devices: [wireDevice({ device_id: 1 }), wireDevice({ device_id: 2 })],
    });
    const { state } = apply(before, {
      type: "device_state",
      data: wireDevice({ device_id: 2, power_state: "on_battery" }),
    });
    expect(state.devices[1].power_state).toBe("on_battery");
    expect(state.devices[0]).toBe(before.devices[0]);
  });

  it("新设备按 id 有序插入", () => {
    const before = synced({
      devices: [wireDevice({ device_id: 1 }), wireDevice({ device_id: 3 })],
    });
    const { state } = apply(before, { type: "device_state", data: wireDevice({ device_id: 2 }) });
    expect(state.devices.map((d) => d.device_id)).toEqual([1, 2, 3]);
  });
});

describe("alert", () => {
  it("替换该设备的活动告警列表", () => {
    const before = synced({
      alerts: [wireEventView({ id: 1, device_id: 1 }), wireEventView({ id: 2, device_id: 2 })],
    });
    const { state } = apply(before, {
      type: "alert",
      data: { device_id: 1, items: [wireEventView({ id: 8, device_id: 1, type: "overload" })] },
    });
    expect(state.alerts[1].map((a) => a.id)).toEqual([8]);
    expect(state.alerts[2]).toBe(before.alerts[2]);
  });

  it("空数组表示该设备告警全部解除", () => {
    const before = synced({ alerts: [wireEventView({ id: 1, device_id: 1 })] });
    expect(
      apply(before, { type: "alert", data: { device_id: 1, items: [] } }).state.alerts,
    ).toEqual({});
    expect(
      apply(before, { type: "alert", data: { device_id: 1, items: null } }).state.alerts,
    ).toEqual({});
  });
});

describe("event", () => {
  it("按 id 去重，新事件在前，at 统一为 started_at", () => {
    let state = synced();
    state = apply(state, { type: "event", data: wireSseEvent({ id: 1 }) }).state;
    const again = apply(state, { type: "event", data: wireSseEvent({ id: 1, type: "changed" }) });
    expect(again.state).toBe(state);
    state = apply(state, {
      type: "event",
      data: wireSseEvent({ id: 2, clears: "mains_lost" }),
    }).state;
    expect(state.recentEvents.map((e) => e.id)).toEqual([2, 1]);
    expect(state.recentEvents[1].started_at).toBe(T0);
    expect(state.recentEvents[0].clears).toBe("mains_lost");
  });

  it(`最多保留 ${RECENT_EVENT_LIMIT} 条`, () => {
    let state = synced();
    for (let id = 1; id <= RECENT_EVENT_LIMIT + 5; id++) {
      state = apply(state, { type: "event", data: wireSseEvent({ id }) }).state;
    }
    expect(state.recentEvents).toHaveLength(RECENT_EVENT_LIMIT);
    expect(state.recentEvents[0].id).toBe(RECENT_EVENT_LIMIT + 5);
  });
});

describe("plan_progress", () => {
  it("活动运行按 id 更新或插入", () => {
    const before = synced({ plan_runs: [wirePlanRun({ id: 2 })] });
    let { state } = apply(before, {
      type: "plan_progress",
      data: wirePlanRun({ id: 1, status: "running" }),
    });
    expect(state.planRuns.map((r) => r.id)).toEqual([1, 2]);
    ({ state } = apply(state, {
      type: "plan_progress",
      data: wirePlanRun({ id: 2, current_stage: 1, status: "running" }),
    }));
    expect(state.planRuns[1].current_stage).toBe(1);
  });

  it.each(["completed", "completed_with_errors", "cancelled", "aborted"] as const)(
    "进入终态 %s 时移出活动集合并报告",
    (status) => {
      const before = synced({ plan_runs: [wirePlanRun({ id: 1 }), wirePlanRun({ id: 2 })] });
      const { state, effects } = apply(before, {
        type: "plan_progress",
        data: wirePlanRun({ id: 1, status }),
      });
      expect(state.planRuns.map((r) => r.id)).toEqual([2]);
      expect(effects.finishedRuns.map((r) => [r.id, r.status])).toEqual([[1, status]]);
    },
  );
});

describe("notify_failed", () => {
  it("按日志 id 去重，只有新记录产生提示", () => {
    let state = synced();
    const first = apply(state, { type: "notify_failed", data: notifyResult({ id: 5 }) });
    expect(first.effects.newNotifyFailures.map((r) => r.id)).toEqual([5]);
    state = first.state;
    const dup = apply(state, { type: "notify_failed", data: notifyResult({ id: 5 }) });
    expect(dup.state).toBe(state);
    expect(dup.effects.newNotifyFailures).toEqual([]);
  });

  it("补齐结果与 SSE 记录合并去重，并忽略 ok=true", () => {
    let state = apply(synced(), { type: "notify_failed", data: notifyResult({ id: 5 }) }).state;
    const merged = mergeNotifyFailures(state, [
      notifyResult({ id: 5 }),
      notifyResult({ id: 6 }),
      notifyResult({ id: 7, ok: true }),
    ]);
    expect(merged.added.map((r) => r.id)).toEqual([6]);
    state = merged.state;
    expect(state.notifyFailures.map((r) => r.id)).toEqual([6, 5]);
    expect(mergeNotifyFailures(state, [notifyResult({ id: 6 })]).state).toBe(state);
  });

  it(`最多保留 ${NOTIFY_FAILED_LIMIT} 条`, () => {
    const items = Array.from({ length: NOTIFY_FAILED_LIMIT + 3 }, (_, i) =>
      notifyResult({ id: i + 1 }),
    );
    const { state } = mergeNotifyFailures(synced(), items);
    expect(state.notifyFailures).toHaveLength(NOTIFY_FAILED_LIMIT);
    expect(state.notifyFailures[0].id).toBe(NOTIFY_FAILED_LIMIT + 3);
  });
});

describe("markUnsynced", () => {
  it("保留数据但标记未同步；未同步时不产生新对象", () => {
    const state = synced({ devices: [wireDevice()] });
    const offline = markUnsynced(state);
    expect(offline.synced).toBe(false);
    expect(offline.devices).toBe(state.devices);
    expect(markUnsynced(offline)).toBe(offline);
  });
});

describe("parseFrame", () => {
  it("解析已知帧类型", () => {
    expect(parseFrame("alert", '{"device_id":1,"items":[]}')).toEqual({
      type: "alert",
      data: { device_id: 1, items: [] },
    });
  });

  it("未知类型、非法 JSON、非对象返回 null", () => {
    expect(parseFrame("unknown", "{}")).toBeNull();
    expect(parseFrame("sync", "{")).toBeNull();
    expect(parseFrame("sync", "1")).toBeNull();
    expect(parseFrame("sync", "null")).toBeNull();
  });
});
