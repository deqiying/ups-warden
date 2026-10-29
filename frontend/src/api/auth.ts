import { api } from "./client.ts";
import type { SessionResponse } from "./types.ts";

/**
 * 探测当前会话。401 由调用方处理（返回登录或初始化），因此不触发全局会话失效；
 * 其余错误照常抛出，由调用方显示可重试的错误态。
 */
export function getSession(signal?: AbortSignal): Promise<SessionResponse> {
  return api.request<SessionResponse>("/auth/session", { signal, handleUnauthorized: false });
}

export function logout(): Promise<{ ok: true }> {
  return api.request<{ ok: true }>("/auth/logout", { method: "POST", handleUnauthorized: false });
}
