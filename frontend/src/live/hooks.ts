import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  useSyncExternalStore,
} from "react";
import type { LiveConnection } from "./connection.ts";
import type { ConnectionState, LiveSnapshot, LiveStore } from "./store.ts";
import { isLiveStale } from "./store.ts";

export interface LiveContextValue {
  store: LiveStore;
  /** 测试或未登录场景可为 null；此时「立即重连」不可用。 */
  connection: LiveConnection | null;
}

export const LiveContext = createContext<LiveContextValue | null>(null);

function useLiveContext(): LiveContextValue {
  const value = useContext(LiveContext);
  if (!value) throw new Error("useLive* 必须在 <LiveProvider> 内使用");
  return value;
}

export function useLiveStore(): LiveStore {
  return useLiveContext().store;
}

/** 实时连接实例（外壳负责启动与停止）；测试中可为 null。 */
export function useLiveContextConnection(): LiveConnection | null {
  return useLiveContext().connection;
}

type Selector<T> = (snapshot: LiveSnapshot) => T;

/**
 * 选择结果缓存（思路同 use-sync-external-store/with-selector）：快照与选择器都未变时直接复用；
 * 任一变化时重新计算，若 isEqual 判定与上次相等则沿用旧引用。
 * 放在 hook 外部，缓存跨渲染保留，内联选择器也不会让 getSnapshot 每次返回新值。
 */
function createSelection<T>() {
  let last: { snapshot: LiveSnapshot; selector: Selector<T>; value: T } | null = null;
  return (snapshot: LiveSnapshot, selector: Selector<T>, isEqual: (a: T, b: T) => boolean): T => {
    if (last && last.snapshot === snapshot && last.selector === selector) return last.value;
    const next = selector(snapshot);
    const value = last && isEqual(last.value, next) ? last.value : next;
    last = { snapshot, selector, value };
    return value;
  };
}

/**
 * 订阅实时快照的一部分。快照变化时若 `isEqual` 判定选择结果相等则沿用旧引用，
 * 避免无关帧导致重渲染。选择器可以内联书写（可依赖 props）。
 */
export function useLive<T>(selector: Selector<T>, isEqual: (a: T, b: T) => boolean = Object.is): T {
  const store = useLiveStore();
  // 每个 hook 实例一份缓存，生命周期与组件相同。
  const [select] = useState(() => createSelection<T>());
  const getSelection = () => select(store.getSnapshot(), selector, isEqual);
  return useSyncExternalStore(store.subscribe, getSelection, getSelection);
}

/** 数组逐项引用相等；用于返回过滤结果的选择器。 */
export function shallowArrayEqual<T>(a: readonly T[], b: readonly T[]): boolean {
  if (a === b) return true;
  if (a.length !== b.length) return false;
  return a.every((item, i) => Object.is(item, b[i]));
}

export function useConnection(): ConnectionState {
  return useLive((s) => s.connection);
}

/** 实时数值是否应按过期显示（断线、重连中、重连后未 sync）。 */
export function useLiveStale(): boolean {
  return useLive(isLiveStale);
}

/** 横幅「立即重连」。 */
export function useReconnect(): () => void {
  const { connection } = useLiveContext();
  return useMemo(() => () => connection?.reconnectNow(), [connection]);
}

/**
 * 本地时钟，每 `intervalMs` 刷新一次。用于倒计时与“x 秒后重试”；
 * 只用于显示本地推算的剩余时间，不外推任何设备实时值。
 */
export function useNow(intervalMs = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(timer);
  }, [intervalMs]);
  return now;
}
