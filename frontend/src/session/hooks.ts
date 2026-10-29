import { createContext, useContext, useSyncExternalStore } from "react";
import type { SessionSnapshot, SessionStore } from "./store.ts";

export interface SessionContextValue {
  store: SessionStore;
  /** 重新探测会话（启动或错误态“重试”）。 */
  probe: () => Promise<void>;
  /** 登出；失败时抛出 ApiError 且保留会话（见 AppServices.logout）。 */
  logout: () => Promise<void>;
}

export const SessionContext = createContext<SessionContextValue | null>(null);

function useSessionContext(): SessionContextValue {
  const value = useContext(SessionContext);
  if (!value) throw new Error("useSession 必须在 SessionContext.Provider 内使用");
  return value;
}

export function useSession(): SessionSnapshot {
  const { store } = useSessionContext();
  return useSyncExternalStore(store.subscribe, store.getSnapshot, store.getSnapshot);
}

export function useSessionActions(): SessionContextValue {
  return useSessionContext();
}
