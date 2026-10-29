/*
 * 应用级单例：会话、实时存储、SSE 连接与查询缓存。由 main.tsx 创建一次后注入 <App>，
 * 测试可创建独立实例。
 */
import { QueryClient } from "@tanstack/react-query";
import { getSession } from "../api/auth.ts";
import { isApiError, setApiHooks } from "../api/client.ts";
import { listNotifyLogs } from "../api/notifications.ts";
import { BACKFILL_LIMIT, LiveConnection, type StreamSource } from "../live/connection.ts";
import { LiveStore } from "../live/store.ts";
import { SessionStore } from "../session/store.ts";

export interface AppServices {
  session: SessionStore;
  live: LiveStore;
  connection: LiveConnection;
  queryClient: QueryClient;
  /** 探测会话；并发调用共享同一次请求。 */
  probe: () => Promise<void>;
  /** 全局会话失效（任意请求 401、SSE 重连探测 401）。 */
  handleUnauthorized: () => void;
}

export interface AppServicesOptions {
  createSource?: (url: string) => StreamSource;
}

/** 4xx 是确定性结果，重试没有意义；网络错误与 5xx 最多重试 2 次。 */
function shouldRetry(failureCount: number, error: unknown): boolean {
  if (isApiError(error) && error.status >= 400 && error.status < 500) return false;
  return failureCount < 2;
}

export function createAppServices(options: AppServicesOptions = {}): AppServices {
  const session = new SessionStore();
  const live = new LiveStore();
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: shouldRetry, refetchOnWindowFocus: false },
      mutations: { retry: false },
    },
  });

  // 闭包内引用下方声明的 connection：只会在连接创建后被调用。
  const handleUnauthorized = () => {
    // 已经是未登录（例如多个并发请求同时 401）时只处理一次。
    if (session.getSnapshot().status === "anonymous") return;
    connection.stop();
    live.reset();
    queryClient.clear();
    session.signOut("expired");
  };

  const connection = new LiveConnection({
    store: live,
    createSource: options.createSource,
    probeSession: async () => {
      try {
        // 探测成功时顺便刷新会话期限显示。
        session.signIn(await getSession());
        return "ok";
      } catch (err) {
        return isApiError(err) && err.status === 401 ? "unauthorized" : "unknown";
      }
    },
    fetchNotifyFailures: async (from) =>
      (await listNotifyLogs({ ok: false, from, limit: BACKFILL_LIMIT })).items,
    onUnauthorized: handleUnauthorized,
  });

  let inflight: Promise<void> | null = null;
  const probe = () => {
    if (inflight) return inflight;
    session.setProbing();
    inflight = getSession()
      .then(
        (resp) => session.signIn(resp),
        (err: unknown) => {
          if (isApiError(err) && err.status === 401) session.signOut(null);
          else session.setError(isApiError(err) ? err.message : "无法连接到服务，请检查网络后重试");
        },
      )
      .finally(() => {
        inflight = null;
      });
    return inflight;
  };

  setApiHooks({
    getCsrfToken: () => session.csrfToken,
    onUnauthorized: handleUnauthorized,
  });

  return { session, live, connection, queryClient, probe, handleUnauthorized };
}
