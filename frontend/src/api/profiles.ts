import { api } from "./client.ts";
import { confirmBody } from "./patch.ts";
import type { ProfileDetail, ProfileSummary, ProfileValidation } from "./types.ts";

/** 全部 Profile（内置 + 自定义），按 key 排序一次返回，无分页。 */
export async function listProfiles(signal?: AbortSignal): Promise<ProfileSummary[]> {
  const res = await api.request<{ items: ProfileSummary[] | null }>("/profiles", { signal });
  return res.items ?? [];
}

export function getProfile(key: string, signal?: AbortSignal): Promise<ProfileDetail> {
  return api.request<ProfileDetail>(`/profiles/${encodeURIComponent(key)}`, { signal });
}

/** 只校验不保存；失败为 422 profile_invalid，details 可能含 line / path。 */
export function validateProfile(content: string): Promise<ProfileValidation> {
  return api.request<ProfileValidation>("/profiles/validate", {
    method: "POST",
    body: { content },
  });
}

/** 导入自定义 Profile；内置 key 返回 409 builtin_readonly，key 已存在返回 409 resource_conflict。 */
export function createProfile(content: string): Promise<ProfileDetail> {
  return api.request<ProfileDetail>("/profiles", { method: "POST", body: { content } });
}

/** 复制为新的自定义 Profile（version 重置为 1）。 */
export function copyProfile(
  key: string,
  target: { key: string; name: string },
): Promise<ProfileDetail> {
  return api.request<ProfileDetail>(`/profiles/${encodeURIComponent(key)}/copy`, {
    method: "POST",
    body: target,
  });
}

/** 确认删除自定义 Profile；被数据源引用时 409，内置为 409 builtin_readonly。 */
export function deleteProfile(key: string): Promise<{ ok: boolean }> {
  return api.request(`/profiles/${encodeURIComponent(key)}`, {
    method: "DELETE",
    body: confirmBody(),
  });
}

/**
 * 导出地址（YAML 附件下载）。GET 请求只需会话 Cookie，不需要 CSRF，
 * 因此直接用 `<a href download>` 交给浏览器下载，不经过 JSON 客户端。
 */
export function profileExportUrl(key: string): string {
  return `/api/v1/profiles/${encodeURIComponent(key)}/export`;
}

/** Profile 校验错误的定位信息（后端只返回受限的 line 与 path，不回显输入值）。 */
export interface ProfileErrorLocation {
  line: number | null;
  path: string | null;
}

export function profileErrorLocation(
  details: Readonly<Record<string, unknown>>,
): ProfileErrorLocation {
  const line = typeof details.line === "number" && details.line > 0 ? details.line : null;
  const path = typeof details.path === "string" && details.path !== "$" ? details.path : null;
  return { line, path };
}
