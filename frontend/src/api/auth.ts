import { api } from "./client.ts";
import type { SessionResponse, SetupStatus, User } from "./types.ts";

/**
 * 探测当前会话。401 由调用方处理（返回登录或初始化），因此不触发全局会话失效；
 * 其余错误照常抛出，由调用方显示可重试的错误态。
 */
export function getSession(signal?: AbortSignal): Promise<SessionResponse> {
  return api.request<SessionResponse>("/auth/session", { signal, handleUnauthorized: false });
}

/** 是否仍需首次初始化；不会返回 setup token。 */
export function getSetupStatus(signal?: AbortSignal): Promise<SetupStatus> {
  return api.request<SetupStatus>("/setup/status", { signal, handleUnauthorized: false });
}

export interface SetupRequest {
  setup_token: string;
  username: string;
  password: string;
}

/**
 * 创建首个管理员。成功只返回 User、不建立会话，之后需正常登录。
 * 失败：403 invalid_setup_token、409 setup_closed（已有管理员）、422 validation_failed。
 */
export function setup(body: SetupRequest): Promise<User> {
  return api.request<User>("/setup", { method: "POST", body, handleUnauthorized: false });
}

/**
 * 登录。失败：401 invalid_credentials（不区分用户名不存在与密码错误）、
 * 429 rate_limited（带 Retry-After，按用户名与来源 IP 分别计数）。
 */
export function login(username: string, password: string): Promise<SessionResponse> {
  return api.request<SessionResponse>("/auth/login", {
    method: "POST",
    body: { username, password },
    handleUnauthorized: false,
  });
}

export function logout(): Promise<{ ok: true }> {
  return api.request<{ ok: true }>("/auth/logout", { method: "POST", handleUnauthorized: false });
}
