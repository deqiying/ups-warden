/*
 * 会话状态：只保存在内存中（frontend-plan 3.3）。CSRF token 来自登录响应或 `GET /auth/session`，
 * 不写入 localStorage / sessionStorage；刷新页面后重新探测会话。
 */
import type { SessionResponse, User } from "../api/types.ts";

export type SessionStatus =
  /** 尚未探测（启动中）。 */
  | "unknown"
  | "authenticated"
  | "anonymous"
  /** 探测失败（网络或服务端错误），可重试；不当作未登录。 */
  | "error";

/** 回到未登录的原因：expired 为服务端返回 401（会话过期或被撤销），logout 为用户主动登出。 */
export type SignedOutReason = "expired" | "logout" | null;

export interface SessionSnapshot {
  status: SessionStatus;
  user: User | null;
  csrfToken: string | null;
  /** 空闲与绝对期限中较早者（UTC），探测或登录时刻的值。 */
  expiresAt: string | null;
  signedOutReason: SignedOutReason;
  /** status=error 时的界面文案。 */
  error: string | null;
}

const initial: SessionSnapshot = {
  status: "unknown",
  user: null,
  csrfToken: null,
  expiresAt: null,
  signedOutReason: null,
  error: null,
};

export class SessionStore {
  private snapshot: SessionSnapshot = initial;
  private readonly listeners = new Set<() => void>();

  getSnapshot = (): SessionSnapshot => this.snapshot;

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  get csrfToken(): string | null {
    return this.snapshot.csrfToken;
  }

  /** 登录成功或会话探测成功。 */
  signIn(session: SessionResponse): void {
    this.set({
      status: "authenticated",
      user: session.user,
      csrfToken: session.csrf_token,
      expiresAt: session.expires_at,
      signedOutReason: null,
      error: null,
    });
  }

  /** 清空会话（登出或 401）；凭据只在内存中，清空即失效。 */
  signOut(reason: SignedOutReason): void {
    this.set({ ...initial, status: "anonymous", signedOutReason: reason });
  }

  setProbing(): void {
    this.set({ ...this.snapshot, status: "unknown", error: null });
  }

  setError(message: string): void {
    this.set({ ...initial, status: "error", error: message });
  }

  private set(next: SessionSnapshot): void {
    this.snapshot = next;
    for (const listener of this.listeners) listener();
  }
}
