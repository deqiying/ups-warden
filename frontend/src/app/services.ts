/*
 * 应用级单例：会话、实时存储、SSE 连接与查询缓存。由 main.tsx 创建一次后注入 <App>，
 * 测试可创建独立实例。
 */
import { QueryClient } from "@tanstack/react-query";
import { getSession, getSetupStatus, logout as requestLogout } from "../api/auth.ts";
import { isApiError, setApiHooks } from "../api/client.ts";
import { listNotifyLogs } from "../api/notifications.ts";
import { BACKFILL_LIMIT, LiveConnection, type StreamSource } from "../live/connection.ts";
import { LiveStore } from "../live/store.ts";
import { SessionStore, type SignedOutReason } from "../session/store.ts";

export interface AppServices {
  session: SessionStore;
  live: LiveStore;
  connection: LiveConnection;
  queryClient: QueryClient;
  /**
   * 启动流程（F1-1）：先探测会话；返回 401 时再查 `GET /setup/status`，决定进入初始化页还是登录页。
   * 并发调用共享同一次探测。
   */
  probe: () => Promise<void>;
  /** 全局会话失效（任意请求 401、SSE 重连探测 401）。 */
  handleUnauthorized: () => void;
  /**
   * 登出：`POST /auth/logout` 成功（或服务端已认为未登录，返回 401）后清空本地会话与实时数据。
   * 其他失败原样抛出且保留本地会话——服务端会话仍然有效，不能假装已经退出。
   */
  logout: () => Promise<void>;
}

export interface AppServicesOptions {
  createSource?: (url: string) => StreamSource;
}

/** 4xx 是确定性结果，重试没有意义；网络错误与 5xx 最多重试 2 次。 */
function shouldRetry(failureCount: number, error: unknown): boolean {
  if (isApiError(error) && error.status >= 400 && error.status < 500) return false;
  return failureCount < 2;
}

const PROBE_FAILED = "无法连接到服务，请检查网络后重试";

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
  const clearLocal = (reason: SignedOutReason) => {
    connection.stop();
    live.reset();
    queryClient.clear();
    session.signOut(reason);
  };

  const handleUnauthorized = () => {
    // 已经是未登录（例如多个并发请求同时 401）时只处理一次。
    if (session.getSnapshot().status === "anonymous") return;
    clearLocal("expired");
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

  const runProbe = async () => {
    try {
      session.signIn(await getSession());
      return;
    } catch (err) {
      if (!(isApiError(err) && err.status === 401)) {
        session.setError(isApiError(err) ? err.message : PROBE_FAILED);
        return;
      }
    }
    // 未登录：区分“尚未初始化”与“需要登录”。查询失败同样显示可重试的错误，不猜测进入哪一页。
    try {
      const status = await getSetupStatus();
      session.signOut(null, status.setup_required);
    } catch (err) {
      session.setError(isApiError(err) ? err.message : PROBE_FAILED);
    }
  };

  let inflight: Promise<void> | null = null;
  const probe = () => {
    if (inflight) return inflight;
    session.setProbing();
    inflight = runProbe().finally(() => {
      inflight = null;
    });
    return inflight;
  };

  const logout = async () => {
    try {
      await requestLogout();
    } catch (err) {
      // 401：会话已过期或已被撤销，服务端同样认为未登录，按已退出处理。
      if (!(isApiError(err) && err.status === 401)) throw err;
    }
    clearLocal("logout");
  };

  setApiHooks({
    getCsrfToken: () => session.csrfToken,
    onUnauthorized: handleUnauthorized,
  });

  return { session, live, connection, queryClient, probe, handleUnauthorized, logout };
}
