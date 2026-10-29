import { useConnection, useLiveStale, useNow } from "../../live/hooks.ts";

/**
 * 监控页的时钟：`now` 每秒刷新；`refMs` 是计算“已放电”等计时的参考时刻——
 * 实时连接断开（或重连后未同步）时停在断开时刻，不把断流期间当作连续观测。
 */
export function useLiveClock(): { now: number; refMs: number; stale: boolean } {
  const now = useNow(1000);
  const stale = useLiveStale();
  const { disconnectedAt } = useConnection();
  const frozen = stale && disconnectedAt ? Date.parse(disconnectedAt) : NaN;
  return { now, refMs: Number.isNaN(frozen) ? now : frozen, stale };
}
