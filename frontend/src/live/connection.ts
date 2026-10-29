/*
 * SSE 连接管理：唯一的 EventSource、退避重连、会话探测与断线补齐。
 *
 * 契约（docs/reference/monitoring-api.md「SSE」、notifications-api.md「通知日志与 SSE」）：
 * - `GET /api/v1/stream` 用 Cookie 认证；不回放 Last-Event-ID，每次（重）连接先收到 sync。
 * - 服务端返回非 200（如 401）时浏览器不会自动重连，因此任何 error 都由这里关闭并按退避重连，
 *   不依赖浏览器的内建重连，行为在各种失败下保持一致。
 * - 每次失败后先探测 `GET /auth/session`：401 交给会话失效处理，避免把会话过期误判为断网。
 * - 重连后的 sync 不回放通知失败，需用 `/notify-logs?ok=false&from=<断开时刻>` 补齐。
 */
import type { NotificationResult } from "../api/types.ts";
import { parseFrame, FRAME_TYPES, type LiveFrame } from "./reducer.ts";
import type { LiveStore } from "./store.ts";

/** 退避序列（秒）：1、2、4、8、16，之后固定 30。 */
export const BACKOFF_SECONDS = [1, 2, 4, 8, 16, 30] as const;

/**
 * 补齐起点相对“检测到断线”时刻的提前量。服务端心跳是 SSE 注释行，EventSource 不会派发给页面，
 * 前端只能在连接真正报错时才知道断线，可能晚于实际断流（经代理、TCP 超时）；另需容忍浏览器与
 * 服务端的时钟偏差。提前两个心跳周期（2×15 秒）以免漏掉记录，重复部分按日志 id 去重。
 */
export const BACKFILL_MARGIN_MS = 30_000;

/** 补齐只取最新一页；更早的记录在通知发送记录页查看。 */
export const BACKFILL_LIMIT = 200;

export function backoffDelayMs(attempt: number): number {
  const index = Math.min(Math.max(attempt, 1), BACKOFF_SECONDS.length) - 1;
  return BACKOFF_SECONDS[index] * 1000;
}

/** EventSource 的最小接口，便于测试注入。 */
export interface StreamSource {
  addEventListener(type: string, listener: (event: Event) => void): void;
  close(): void;
}

export type SessionProbeResult = "ok" | "unauthorized" | "unknown";

export interface LiveConnectionOptions {
  store: LiveStore;
  /** 默认 `new EventSource(url)`；同源请求自动携带 Cookie。 */
  createSource?: (url: string) => StreamSource;
  url?: string;
  /** 探测会话；网络等非 401 失败返回 unknown，继续按退避重连。 */
  probeSession: () => Promise<SessionProbeResult>;
  /** 拉取 `from` 之后的最终失败通知（ok=false）。 */
  fetchNotifyFailures: (from: string) => Promise<readonly NotificationResult[]>;
  /** 会话已失效：由应用层清空会话与实时数据并转到登录页。 */
  onUnauthorized: () => void;
  now?: () => number;
}

/**
 * 管理单条实时连接的生命周期。登录后 start，登出或会话失效时 stop。
 * 每次连接对应一个 generation，旧连接、旧探测与旧定时器的回调在 generation 变化后一律忽略。
 */
export class LiveConnection {
  private readonly options: Required<LiveConnectionOptions>;
  private source: StreamSource | null = null;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private running = false;
  private generation = 0;
  /** 登录周期：stop 后递增，丢弃上一周期未完成的补齐结果。 */
  private epoch = 0;

  constructor(options: LiveConnectionOptions) {
    // 逐项回退默认值：调用方显式传入 undefined 时不能覆盖默认实现。
    this.options = {
      ...options,
      url: options.url ?? "/api/v1/stream",
      now: options.now ?? Date.now,
      createSource: options.createSource ?? ((url) => new EventSource(url)),
    };
  }

  get isRunning(): boolean {
    return this.running;
  }

  start(): void {
    if (this.running) return;
    this.running = true;
    this.connect();
  }

  /** 停止并回到 idle；实时数据由调用方决定是否 reset。 */
  stop(): void {
    if (!this.running) return;
    this.running = false;
    this.generation += 1;
    this.epoch += 1;
    this.clearTimer();
    this.closeSource();
    this.options.store.setConnection({
      phase: "idle",
      disconnectedAt: null,
      attempt: 0,
      retryAt: null,
    });
  }

  /** 横幅「立即重连」：跳过剩余等待。已连接或正在连接时不做任何事。 */
  reconnectNow(): void {
    if (!this.running || this.source) return;
    this.connect();
  }

  private connect(): void {
    this.clearTimer();
    this.closeSource();
    const generation = ++this.generation;
    const { store, url, createSource } = this.options;
    store.setConnection({ phase: "connecting", retryAt: null });

    let source: StreamSource;
    try {
      source = createSource(url);
    } catch (err) {
      // 构造失败（URL 非法等）同样走退避，不中断应用。
      console.error("实时连接创建失败", err);
      this.handleFailure(generation);
      return;
    }
    this.source = source;

    source.addEventListener("open", () => {
      if (generation !== this.generation) return;
      store.setConnection({ phase: "open", retryAt: null });
    });
    source.addEventListener("error", () => {
      if (generation !== this.generation) return;
      this.closeSource();
      this.handleFailure(generation);
    });
    for (const type of FRAME_TYPES) {
      source.addEventListener(type, (event) => {
        if (generation !== this.generation) return;
        const data = (event as MessageEvent<unknown>).data;
        const frame = typeof data === "string" ? parseFrame(type, data) : null;
        if (!frame) {
          // 无法解析的帧只记录诊断，不中断连接；后续 sync 会纠正状态。
          console.warn(`实时帧解析失败：${type}`);
          return;
        }
        this.handleFrame(frame);
      });
    }
  }

  private handleFrame(frame: LiveFrame): void {
    const { store } = this.options;
    store.dispatch(frame);
    if (frame.type !== "sync") return;
    const { disconnectedAt } = store.getSnapshot().connection;
    store.setConnection({ phase: "open", attempt: 0, retryAt: null, disconnectedAt: null });
    if (disconnectedAt) void this.backfill(disconnectedAt);
  }

  private handleFailure(generation: number): void {
    const { store, now, probeSession, onUnauthorized } = this.options;
    const current = store.getSnapshot().connection;
    store.markUnsynced();
    store.setConnection({
      phase: "offline",
      attempt: current.attempt + 1,
      disconnectedAt: current.disconnectedAt ?? new Date(now()).toISOString(),
      retryAt: null,
    });

    probeSession().then(
      (result) => {
        if (generation !== this.generation || !this.running) return;
        if (result === "unauthorized") {
          this.stop();
          onUnauthorized();
          return;
        }
        this.scheduleRetry(generation);
      },
      () => {
        if (generation !== this.generation || !this.running) return;
        this.scheduleRetry(generation);
      },
    );
  }

  private scheduleRetry(generation: number): void {
    const { store, now } = this.options;
    const delay = backoffDelayMs(store.getSnapshot().connection.attempt);
    store.setConnection({ retryAt: now() + delay });
    this.timer = setTimeout(() => {
      this.timer = null;
      if (generation !== this.generation || !this.running) return;
      this.connect();
    }, delay);
  }

  private async backfill(disconnectedAt: string): Promise<void> {
    const epoch = this.epoch;
    const { store, fetchNotifyFailures } = this.options;
    const from = new Date(Date.parse(disconnectedAt) - BACKFILL_MARGIN_MS).toISOString();
    try {
      const items = await fetchNotifyFailures(from);
      if (epoch !== this.epoch) return;
      store.mergeBackfill(items);
    } catch (err) {
      if (epoch !== this.epoch) return;
      console.warn("断线期间的通知失败记录补齐失败", err);
      store.reportBackfillFailed();
    }
  }

  private closeSource(): void {
    if (!this.source) return;
    this.source.close();
    this.source = null;
  }

  private clearTimer(): void {
    if (this.timer === null) return;
    clearTimeout(this.timer);
    this.timer = null;
  }
}
