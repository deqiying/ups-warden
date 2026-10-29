import type { NotificationResult, PlanRun } from "../api/types.ts";
import {
  applyFrame,
  initialLiveData,
  markUnsynced,
  mergeNotifyFailures,
  type LiveData,
  type LiveFrame,
} from "./reducer.ts";

export type ConnectionPhase = "idle" | "connecting" | "open" | "offline";

export interface ConnectionState {
  /** idle：未启动（未登录）；connecting：正在建立；open：已连接；offline：断开，等待重连。 */
  phase: ConnectionPhase;
  /** 本轮断线开始时刻（ISO）；收到重连后的 sync 才清空，用于补齐通知失败与“数据停在”提示。 */
  disconnectedAt: string | null;
  /** 连续失败次数（第 N 次重连）。 */
  attempt: number;
  /** 下次自动重连的本地时间戳（ms）；无计划时为 null。 */
  retryAt: number | null;
}

export interface LiveSnapshot {
  data: LiveData;
  connection: ConnectionState;
}

/** store 对外广播的副作用，由应用层转为查询失效或 toast。 */
export type LiveEffect =
  | { type: "resynced" }
  | { type: "plan_run_finished"; run: PlanRun }
  | { type: "notify_failed"; items: NotificationResult[]; source: "live" | "backfill" }
  | { type: "notify_backfill_failed" };

const idleConnection: ConnectionState = {
  phase: "idle",
  disconnectedAt: null,
  attempt: 0,
  retryAt: null,
};

/**
 * 实时数据的唯一来源：以不可变快照配合 useSyncExternalStore 暴露给页面。
 * 只在内存中保存，登出或会话失效时 reset。
 */
export class LiveStore {
  private snapshot: LiveSnapshot = { data: initialLiveData, connection: idleConnection };
  private readonly listeners = new Set<() => void>();
  private readonly effectListeners = new Set<(effect: LiveEffect) => void>();
  private readonly now: () => Date;

  constructor(now: () => Date = () => new Date()) {
    this.now = now;
  }

  getSnapshot = (): LiveSnapshot => this.snapshot;

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  onEffect(listener: (effect: LiveEffect) => void): () => void {
    this.effectListeners.add(listener);
    return () => this.effectListeners.delete(listener);
  }

  dispatch(frame: LiveFrame): void {
    const { state, effects } = applyFrame(this.snapshot.data, frame, this.now().toISOString());
    this.setData(state);
    if (effects.resynced) this.emit({ type: "resynced" });
    for (const run of effects.finishedRuns) this.emit({ type: "plan_run_finished", run });
    if (effects.newNotifyFailures.length > 0) {
      this.emit({ type: "notify_failed", items: effects.newNotifyFailures, source: "live" });
    }
  }

  /** 合并断线补齐的通知失败；已通过 SSE 收到的同 id 记录不重复提示。 */
  mergeBackfill(items: readonly NotificationResult[]): void {
    const { state, added } = mergeNotifyFailures(this.snapshot.data, items);
    this.setData(state);
    if (added.length > 0) this.emit({ type: "notify_failed", items: added, source: "backfill" });
  }

  reportBackfillFailed(): void {
    this.emit({ type: "notify_backfill_failed" });
  }

  /** 断线时保留最后数据，但标记为未同步，界面据此降级显示。 */
  markUnsynced(): void {
    this.setData(markUnsynced(this.snapshot.data));
  }

  setConnection(next: Partial<ConnectionState>): void {
    const current = this.snapshot.connection;
    const merged = { ...current, ...next };
    if (
      merged.phase === current.phase &&
      merged.disconnectedAt === current.disconnectedAt &&
      merged.attempt === current.attempt &&
      merged.retryAt === current.retryAt
    ) {
      return;
    }
    this.snapshot = { ...this.snapshot, connection: merged };
    this.notify();
  }

  /** 登出或会话失效：丢弃全部实时数据。 */
  reset(): void {
    this.snapshot = { data: initialLiveData, connection: idleConnection };
    this.notify();
  }

  private setData(data: LiveData): void {
    if (data === this.snapshot.data) return;
    this.snapshot = { ...this.snapshot, data };
    this.notify();
  }

  private notify(): void {
    for (const listener of this.listeners) listener();
  }

  private emit(effect: LiveEffect): void {
    for (const listener of this.effectListeners) listener(effect);
  }
}

/**
 * 实时数值是否应按“过期”显示：断线、重连中，或重连后尚未收到 sync。
 * 首次连接建立前（从未有数据）不算过期，由页面显示加载态。
 */
export function isLiveStale(snapshot: LiveSnapshot): boolean {
  const { phase, disconnectedAt } = snapshot.connection;
  if (phase === "offline") return true;
  return (
    disconnectedAt !== null ||
    (phase === "open" && !snapshot.data.synced && snapshot.data.syncedAt !== null)
  );
}
