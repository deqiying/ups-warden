import { describe, expect, it, vi } from "vite-plus/test";
import { notifyResult, wireDevice, wirePlanRun, wireSync } from "../test/fixtures.ts";
import { isLiveStale, LiveStore, type LiveEffect } from "./store.ts";

const NOW = new Date("2026-10-27T06:30:00.000Z");

function setup() {
  const store = new LiveStore(() => NOW);
  const effects: LiveEffect[] = [];
  store.onEffect((effect) => effects.push(effect));
  const listener = vi.fn();
  store.subscribe(listener);
  return { store, effects, listener };
}

describe("LiveStore", () => {
  it("sync 更新快照、通知订阅方并广播 resynced", () => {
    const { store, effects, listener } = setup();
    store.dispatch({ type: "sync", data: wireSync({ devices: [wireDevice()] }) });
    const snapshot = store.getSnapshot();
    expect(snapshot.data.synced).toBe(true);
    expect(snapshot.data.syncedAt).toBe(NOW.toISOString());
    expect(snapshot.data.devices).toHaveLength(1);
    expect(listener).toHaveBeenCalledTimes(1);
    expect(effects).toEqual([{ type: "resynced" }]);
  });

  it("运行进入终态时广播 plan_run_finished", () => {
    const { store, effects } = setup();
    store.dispatch({ type: "sync", data: wireSync({ plan_runs: [wirePlanRun({ id: 1 })] }) });
    effects.length = 0;
    store.dispatch({ type: "plan_progress", data: wirePlanRun({ id: 1, status: "cancelled" }) });
    expect(effects).toEqual([
      { type: "plan_run_finished", run: expect.objectContaining({ id: 1, status: "cancelled" }) },
    ]);
  });

  it("通知失败：SSE 新记录提示一次，补齐时已见过的 id 不重复提示", () => {
    const { store, effects } = setup();
    store.dispatch({ type: "notify_failed", data: notifyResult({ id: 5 }) });
    store.dispatch({ type: "notify_failed", data: notifyResult({ id: 5 }) });
    store.mergeBackfill([notifyResult({ id: 5 }), notifyResult({ id: 6 })]);
    store.mergeBackfill([notifyResult({ id: 6 })]);
    expect(effects).toEqual([
      { type: "notify_failed", source: "live", items: [expect.objectContaining({ id: 5 })] },
      { type: "notify_failed", source: "backfill", items: [expect.objectContaining({ id: 6 })] },
    ]);
  });

  it("补齐失败广播 notify_backfill_failed", () => {
    const { store, effects } = setup();
    store.reportBackfillFailed();
    expect(effects).toEqual([{ type: "notify_backfill_failed" }]);
  });

  it("数据未变化时不产生新快照也不通知", () => {
    const { store, listener } = setup();
    const before = store.getSnapshot();
    store.markUnsynced();
    store.setConnection({ phase: "idle" });
    store.mergeBackfill([]);
    expect(store.getSnapshot()).toBe(before);
    expect(listener).not.toHaveBeenCalled();
  });

  it("setConnection 合并局部字段", () => {
    const { store, listener } = setup();
    store.setConnection({ phase: "offline", attempt: 2 });
    store.setConnection({ retryAt: 42 });
    expect(store.getSnapshot().connection).toEqual({
      phase: "offline",
      attempt: 2,
      retryAt: 42,
      disconnectedAt: null,
    });
    expect(listener).toHaveBeenCalledTimes(2);
  });

  it("reset 丢弃全部实时数据并回到 idle", () => {
    const { store } = setup();
    store.dispatch({ type: "sync", data: wireSync({ devices: [wireDevice()] }) });
    store.setConnection({ phase: "open" });
    store.reset();
    const snapshot = store.getSnapshot();
    expect(snapshot.data.synced).toBe(false);
    expect(snapshot.data.devices).toEqual([]);
    expect(snapshot.connection.phase).toBe("idle");
  });

  it("取消订阅后不再收到通知", () => {
    const store = new LiveStore(() => NOW);
    const listener = vi.fn();
    const effect = vi.fn();
    store.subscribe(listener)();
    store.onEffect(effect)();
    store.dispatch({ type: "sync", data: wireSync() });
    expect(listener).not.toHaveBeenCalled();
    expect(effect).not.toHaveBeenCalled();
  });
});

describe("isLiveStale", () => {
  it("首次连接建立前不算过期", () => {
    const store = new LiveStore(() => NOW);
    expect(isLiveStale(store.getSnapshot())).toBe(false);
    store.setConnection({ phase: "connecting" });
    expect(isLiveStale(store.getSnapshot())).toBe(false);
    store.setConnection({ phase: "open" });
    expect(isLiveStale(store.getSnapshot())).toBe(false);
  });

  it("断线、重连中与重连后尚未 sync 都算过期；sync 后恢复", () => {
    const store = new LiveStore(() => NOW);
    store.setConnection({ phase: "open" });
    store.dispatch({ type: "sync", data: wireSync() });
    expect(isLiveStale(store.getSnapshot())).toBe(false);

    store.markUnsynced();
    store.setConnection({ phase: "offline", disconnectedAt: NOW.toISOString() });
    expect(isLiveStale(store.getSnapshot())).toBe(true);

    store.setConnection({ phase: "connecting" });
    expect(isLiveStale(store.getSnapshot())).toBe(true);

    // 已打开但 sync 未到：仍按过期显示，不把旧数据当作实时值。
    store.setConnection({ phase: "open", disconnectedAt: null });
    expect(isLiveStale(store.getSnapshot())).toBe(true);

    store.dispatch({ type: "sync", data: wireSync() });
    expect(isLiveStale(store.getSnapshot())).toBe(false);
  });
});
