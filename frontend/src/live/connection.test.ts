import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import type { NotificationResult } from "../api/types.ts";
import { FakeEventSource, notifyResult, wireDevice, wireSync } from "../test/fixtures.ts";
import {
  BACKFILL_LIMIT,
  BACKFILL_MARGIN_MS,
  BACKOFF_SECONDS,
  backoffDelayMs,
  LiveConnection,
  type SessionProbeResult,
} from "./connection.ts";
import { LiveStore, type LiveEffect } from "./store.ts";

const START = new Date("2026-10-27T06:30:00.000Z");

/** 让已完成的 Promise 回调（探测、补齐）执行完毕。 */
async function flush(): Promise<void> {
  await vi.advanceTimersByTimeAsync(0);
}

function setup(
  overrides: {
    probe?: () => Promise<SessionProbeResult>;
    fetchNotifyFailures?: (from: string) => Promise<readonly NotificationResult[]>;
  } = {},
) {
  const store = new LiveStore(() => new Date());
  const effects: LiveEffect[] = [];
  store.onEffect((effect) => effects.push(effect));
  const probeSession = vi.fn(overrides.probe ?? (async () => "ok" as const));
  const fetchNotifyFailures = vi.fn(overrides.fetchNotifyFailures ?? (async () => []));
  const onUnauthorized = vi.fn();
  const connection = new LiveConnection({
    store,
    createSource: (url) => new FakeEventSource(url),
    probeSession,
    fetchNotifyFailures,
    onUnauthorized,
    now: () => Date.now(),
  });
  return { store, effects, connection, probeSession, fetchNotifyFailures, onUnauthorized };
}

function phase(store: LiveStore) {
  return store.getSnapshot().connection.phase;
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(START);
  FakeEventSource.reset();
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("backoffDelayMs", () => {
  it("按 1、2、4、8、16 秒退避，之后固定 30 秒", () => {
    expect([1, 2, 3, 4, 5, 6, 7, 20].map(backoffDelayMs)).toEqual([
      1000, 2000, 4000, 8000, 16000, 30000, 30000, 30000,
    ]);
    expect(backoffDelayMs(0)).toBe(1000);
    expect(BACKOFF_SECONDS.at(-1)).toBe(30);
  });
});

describe("LiveConnection", () => {
  it("start 建立唯一连接：connecting → open → sync", () => {
    const { store, connection } = setup();
    connection.start();
    connection.start();
    expect(FakeEventSource.instances).toHaveLength(1);
    const source = FakeEventSource.latest();
    expect(source.url).toBe("/api/v1/stream");
    expect(phase(store)).toBe("connecting");
    source.open();
    expect(phase(store)).toBe("open");
    source.send("sync", wireSync({ devices: [wireDevice()] }));
    expect(store.getSnapshot().data.synced).toBe(true);
    expect(store.getSnapshot().data.devices).toHaveLength(1);
    expect(connection.isRunning).toBe(true);
  });

  it("断线：关闭连接、标记未同步、先探测会话再按退避重连", async () => {
    const { store, connection, probeSession } = setup();
    connection.start();
    const first = FakeEventSource.latest();
    first.open();
    first.send("sync", wireSync({ devices: [wireDevice()] }));

    first.fail();
    expect(first.closed).toBe(true);
    const snapshot = store.getSnapshot();
    expect(snapshot.data.synced).toBe(false);
    // 断线时保留最后数据供降级显示，不清空也不外推。
    expect(snapshot.data.devices).toHaveLength(1);
    expect(snapshot.connection).toMatchObject({
      phase: "offline",
      attempt: 1,
      disconnectedAt: START.toISOString(),
    });
    expect(probeSession).toHaveBeenCalledTimes(1);

    await flush();
    expect(store.getSnapshot().connection.retryAt).toBe(START.getTime() + 1000);
    await vi.advanceTimersByTimeAsync(999);
    expect(FakeEventSource.instances).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(FakeEventSource.instances).toHaveLength(2);
    expect(store.getSnapshot().connection).toMatchObject({ phase: "connecting", retryAt: null });
  });

  it("连续失败按退避递增；open 不重置次数，sync 才重置", async () => {
    const { store, connection } = setup();
    connection.start();
    const delays: number[] = [];
    for (let i = 0; i < 7; i++) {
      FakeEventSource.latest().fail();
      await flush();
      const { retryAt } = store.getSnapshot().connection;
      const delay = (retryAt ?? 0) - Date.now();
      delays.push(delay);
      await vi.advanceTimersByTimeAsync(delay);
      // 仅 open 未收到 sync 的连接不算恢复。
      FakeEventSource.latest().open();
    }
    expect(delays).toEqual([1000, 2000, 4000, 8000, 16000, 30000, 30000]);
    expect(store.getSnapshot().connection.attempt).toBe(7);
    // 本轮断线起点保持为第一次失败的时刻。
    expect(store.getSnapshot().connection.disconnectedAt).toBe(START.toISOString());

    FakeEventSource.latest().send("sync", wireSync());
    expect(store.getSnapshot().connection).toMatchObject({
      phase: "open",
      attempt: 0,
      disconnectedAt: null,
    });
  });

  it("探测返回 unauthorized：停止重连并交给会话失效处理", async () => {
    const { store, connection, onUnauthorized } = setup({ probe: async () => "unauthorized" });
    connection.start();
    FakeEventSource.latest().fail();
    await flush();
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
    expect(connection.isRunning).toBe(false);
    expect(phase(store)).toBe("idle");
    await vi.advanceTimersByTimeAsync(60_000);
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it("探测失败（网络错误）不当作登出，继续退避重连", async () => {
    const { connection, onUnauthorized } = setup({
      probe: () => Promise.reject(new Error("network")),
    });
    connection.start();
    FakeEventSource.latest().fail();
    await flush();
    await vi.advanceTimersByTimeAsync(1000);
    expect(onUnauthorized).not.toHaveBeenCalled();
    expect(FakeEventSource.instances).toHaveLength(2);
  });

  it("重连后的 sync 从断线前 30 秒补齐通知失败并合并去重", async () => {
    const { connection, fetchNotifyFailures, effects } = setup({
      fetchNotifyFailures: async () => [notifyResult({ id: 5 }), notifyResult({ id: 6 })],
    });
    connection.start();
    const first = FakeEventSource.latest();
    first.open();
    first.send("sync", wireSync());
    // 首次 sync 不需要补齐。
    expect(fetchNotifyFailures).not.toHaveBeenCalled();
    first.send("notify_failed", notifyResult({ id: 5 }));

    vi.setSystemTime(new Date(START.getTime() + 60_000));
    const disconnectedAt = new Date().toISOString();
    first.fail();
    await flush();
    await vi.advanceTimersByTimeAsync(1000);
    const second = FakeEventSource.latest();
    second.open();
    effects.length = 0;
    second.send("sync", wireSync());
    await flush();

    expect(fetchNotifyFailures).toHaveBeenCalledWith(
      new Date(Date.parse(disconnectedAt) - BACKFILL_MARGIN_MS).toISOString(),
    );
    expect(effects).toEqual([
      { type: "resynced" },
      { type: "notify_failed", source: "backfill", items: [expect.objectContaining({ id: 6 })] },
    ]);
    expect(BACKFILL_LIMIT).toBe(200);
  });

  it("补齐失败时广播 notify_backfill_failed，不影响连接", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    const { store, connection, effects } = setup({
      fetchNotifyFailures: () => Promise.reject(new Error("boom")),
    });
    connection.start();
    FakeEventSource.latest().fail();
    await flush();
    await vi.advanceTimersByTimeAsync(1000);
    FakeEventSource.latest().send("sync", wireSync());
    await flush();
    expect(effects).toContainEqual({ type: "notify_backfill_failed" });
    expect(phase(store)).toBe("open");
  });

  it("reconnectNow 跳过等待；已连接时不做任何事", async () => {
    const { connection } = setup();
    connection.start();
    connection.reconnectNow();
    expect(FakeEventSource.instances).toHaveLength(1);

    FakeEventSource.latest().fail();
    await flush();
    connection.reconnectNow();
    expect(FakeEventSource.instances).toHaveLength(2);
    // 原定时器已取消，不会再额外建立连接。
    await vi.advanceTimersByTimeAsync(60_000);
    expect(FakeEventSource.instances).toHaveLength(2);
  });

  it("stop 关闭连接回到 idle，旧连接与旧补齐的迟到回调一律忽略", async () => {
    let resolveBackfill: (items: NotificationResult[]) => void = () => {};
    const { store, connection, effects } = setup({
      fetchNotifyFailures: () =>
        new Promise((resolve) => {
          resolveBackfill = resolve;
        }),
    });
    connection.start();
    FakeEventSource.latest().fail();
    await flush();
    await vi.advanceTimersByTimeAsync(1000);
    const source = FakeEventSource.latest();
    source.send("sync", wireSync());

    connection.stop();
    expect(source.closed).toBe(true);
    expect(store.getSnapshot().connection).toEqual({
      phase: "idle",
      attempt: 0,
      disconnectedAt: null,
      retryAt: null,
    });

    effects.length = 0;
    resolveBackfill([notifyResult({ id: 9 })]);
    await flush();
    source.send("device_state", wireDevice({ device_id: 7 }));
    source.fail();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(effects).toEqual([]);
    expect(store.getSnapshot().data.devices).toEqual([]);
    expect(phase(store)).toBe("idle");

    // 再次 start 建立新连接。
    connection.start();
    expect(FakeEventSource.instances).toHaveLength(3);
  });

  it("stop 后到达的探测结果不再触发重连或登出", async () => {
    let resolveProbe: (result: SessionProbeResult) => void = () => {};
    const { connection, onUnauthorized } = setup({
      probe: () =>
        new Promise((resolve) => {
          resolveProbe = resolve;
        }),
    });
    connection.start();
    FakeEventSource.latest().fail();
    connection.stop();
    resolveProbe("unauthorized");
    await flush();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(onUnauthorized).not.toHaveBeenCalled();
    expect(FakeEventSource.instances).toHaveLength(1);
  });

  it("无法解析的帧只记录诊断，不中断连接", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const { store, connection } = setup();
    connection.start();
    const source = FakeEventSource.latest();
    source.open();
    source.send("sync", "{not json");
    expect(warn).toHaveBeenCalledWith("实时帧解析失败：sync");
    expect(phase(store)).toBe("open");
    expect(source.closed).toBe(false);
  });

  it("创建连接失败同样按退避重试", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const store = new LiveStore();
    let calls = 0;
    const connection = new LiveConnection({
      store,
      createSource: (url) => {
        calls += 1;
        if (calls === 1) throw new Error("bad url");
        return new FakeEventSource(url);
      },
      probeSession: async () => "ok",
      fetchNotifyFailures: async () => [],
      onUnauthorized: () => {},
      now: () => Date.now(),
    });
    connection.start();
    expect(phase(store)).toBe("offline");
    await flush();
    await vi.advanceTimersByTimeAsync(1000);
    expect(calls).toBe(2);
    expect(phase(store)).toBe("connecting");
  });
});
