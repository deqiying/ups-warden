import { fetchAllPages, MAX_PAGE_LIMIT } from "./devices.ts";
import { api } from "./client.ts";
import { confirmBody } from "./patch.ts";
import type { Page, User } from "./types.ts";

/*
 * 管理员账户 API（docs/reference/monitoring-api.md「初始化、登录与用户」）。
 * P0 所有账户都是 admin。用户名 1～64 个 ASCII 字母/数字/./_/-，后端统一转小写；
 * 密码至少 8 个 Unicode 字符、最多 1024 字节（校验见 pages/auth/credentials.ts）。
 * 密码只出现在请求体中，不进入响应、审计、URL 或日志。
 */

/** 全部管理员（按 id 降序）；账户数量很小，一次取全。 */
export function listUsers(signal?: AbortSignal): Promise<User[]> {
  return fetchAllPages((before) =>
    api.request<Page<User>>("/users", { query: { limit: MAX_PAGE_LIMIT, before }, signal }),
  );
}

/** 201；409 user_conflict：用户名已存在；422 validation_failed：用户名或密码不符合要求。 */
export function createUser(username: string, password: string): Promise<User> {
  return api.request<User>("/users", { method: "POST", body: { username, password } });
}

/** 200；409 user_conflict：用户名已存在；404 user_not_found。 */
export function renameUser(id: number, username: string): Promise<User> {
  return api.request<User>(`/users/${id}`, { method: "PATCH", body: { username } });
}

/**
 * 请求体 `{confirm:true}`，用户在确认框确认后才调用。
 * 422：不能删除自己；409 user_conflict：不能删除最后一位管理员；404 user_not_found。
 */
export function deleteUser(id: number): Promise<{ ok: boolean }> {
  return api.request<{ ok: boolean }>(`/users/${id}`, { method: "DELETE", body: confirmBody() });
}

/**
 * 修改密码（不要求旧密码）。修改自己的密码会撤销自己的其他会话，当前会话保留；
 * 修改他人的密码会撤销对方的全部会话。
 */
export function changePassword(id: number, password: string): Promise<{ ok: boolean }> {
  return api.request<{ ok: boolean }>(`/users/${id}/password`, {
    method: "PUT",
    body: { password },
  });
}
