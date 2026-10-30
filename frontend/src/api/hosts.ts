import { fetchAllPages, MAX_PAGE_LIMIT } from "./devices.ts";
import { api } from "./client.ts";
import { confirmBody, secretFields } from "./patch.ts";
import type { Page } from "./types.ts";

/*
 * 受控主机 API（docs/reference/rules-actions-hosts-api.md「受控主机」）。
 * - 只支持 SSH；凭据（password / private_key / passphrase）只写不读，响应只有 `*_set`；
 * - PUT 为部分更新：`conn` 内只替换提供的子字段，`online_check` 提供即整体替换；
 * - 切换 `auth_method` 由后端清空旧凭据；修改地址或 SSH 端口会清除已确认的指纹。
 */

export type HostKind = "linux" | "windows" | "nas" | "esxi" | "macos" | "other";
export type HostAuthMethod = "password" | "private_key";
export type OnlineCheckType = "tcp" | "icmp";

export interface HostConn {
  port: number;
  username: string;
  auth_method: HostAuthMethod;
  /** 普通动作添加 `sudo -n --`，要求目标主机配置 NOPASSWD；测试命令不使用 sudo。 */
  sudo: boolean;
}

export interface OnlineCheck {
  type: OnlineCheckType;
  /** 仅 TCP 有意义；ICMP 时后端忽略。 */
  port: number;
}

export interface Host {
  id: number;
  name: string;
  address: string;
  kind: HostKind;
  conn: HostConn;
  /** 已确认的 SHA256 指纹；空串表示未确认（不能认证或执行命令）。 */
  hostkey_fingerprint: string;
  online_check: OnlineCheck;
  password_set: boolean;
  private_key_set: boolean;
  passphrase_set: boolean;
}

export type HostSecretKey = "password" | "private_key" | "passphrase";

/** 创建时的后端默认值（hosts.go）。 */
export const DEFAULT_SSH_PORT = 22;

/** 主机表单草稿；凭据输入永不预填，空串表示“保留”。 */
export interface HostDraft {
  name: string;
  address: string;
  kind: HostKind;
  port: number;
  username: string;
  authMethod: HostAuthMethod;
  sudo: boolean;
  onlineType: OnlineCheckType;
  onlinePort: number;
  password: string;
  privateKey: string;
  passphrase: string;
  /** 要求清除的凭据；与同名新值互斥。 */
  clearSecrets: HostSecretKey[];
}

export function emptyHostDraft(): HostDraft {
  return {
    name: "",
    address: "",
    kind: "linux",
    port: DEFAULT_SSH_PORT,
    username: "",
    authMethod: "password",
    sudo: false,
    onlineType: "tcp",
    onlinePort: DEFAULT_SSH_PORT,
    password: "",
    privateKey: "",
    passphrase: "",
    clearSecrets: [],
  };
}

export function draftFromHost(host: Host): HostDraft {
  return {
    name: host.name,
    address: host.address,
    kind: host.kind,
    port: host.conn.port,
    username: host.conn.username,
    authMethod: host.conn.auth_method,
    sudo: host.conn.sudo,
    onlineType: host.online_check.type,
    onlinePort: host.online_check.port || host.conn.port,
    password: "",
    privateKey: "",
    passphrase: "",
    clearSecrets: [],
  };
}

function onlineCheckOf(draft: HostDraft): OnlineCheck {
  return { type: draft.onlineType, port: draft.onlineType === "tcp" ? draft.onlinePort : 0 };
}

/** 本认证方式下可提交的凭据；另一种方式的凭据输入不提交。 */
function secretValuesOf(draft: HostDraft): Record<HostSecretKey, string> {
  return draft.authMethod === "password"
    ? { password: draft.password, private_key: "", passphrase: "" }
    : { password: "", private_key: draft.privateKey, passphrase: draft.passphrase };
}

export function buildHostCreateBody(draft: HostDraft): Record<string, unknown> {
  return {
    name: draft.name.trim(),
    address: draft.address.trim(),
    kind: draft.kind,
    conn: {
      port: draft.port,
      username: draft.username.trim(),
      auth_method: draft.authMethod,
      sudo: draft.sudo,
    },
    online_check: onlineCheckOf(draft),
    ...secretFields(secretValuesOf(draft)),
  };
}

/**
 * 编辑（PUT）请求体：只含修改过的字段；`conn` 只带修改过的子字段。
 * 切换认证方式时后端会清空全部旧凭据，因此必须同时提交新方式的凭据，`clear_secrets` 不再适用。
 * 返回空对象表示没有修改。
 */
export function buildHostUpdateBody(original: Host, draft: HostDraft): Record<string, unknown> {
  const body: Record<string, unknown> = {};
  const name = draft.name.trim();
  const address = draft.address.trim();
  if (name !== original.name) body.name = name;
  if (address !== original.address) body.address = address;
  if (draft.kind !== original.kind) body.kind = draft.kind;

  const conn: Record<string, unknown> = {};
  const username = draft.username.trim();
  if (draft.port !== original.conn.port) conn.port = draft.port;
  if (username !== original.conn.username) conn.username = username;
  const authChanged = draft.authMethod !== original.conn.auth_method;
  if (authChanged) conn.auth_method = draft.authMethod;
  if (draft.sudo !== original.conn.sudo) conn.sudo = draft.sudo;
  if (Object.keys(conn).length > 0) body.conn = conn;

  const online = onlineCheckOf(draft);
  const originalOnline = {
    type: original.online_check.type,
    port: original.online_check.type === "tcp" ? original.online_check.port : 0,
  };
  if (online.type !== originalOnline.type || online.port !== originalOnline.port) {
    body.online_check = online;
  }

  // 密码与私钥是各自认证方式的必需凭据，只有私钥口令可以清除。
  const clear =
    authChanged || draft.authMethod !== "private_key"
      ? []
      : draft.clearSecrets.filter((key) => key === "passphrase");
  Object.assign(body, secretFields(secretValuesOf(draft), clear));
  return body;
}

/** 地址或 SSH 端口变化会让后端清除已确认的指纹。 */
export function hostKeyWillReset(original: Host, draft: HostDraft): boolean {
  return (
    original.hostkey_fingerprint !== "" &&
    (draft.address.trim() !== original.address || draft.port !== original.conn.port)
  );
}

// ───────────────────────── 请求 ─────────────────────────

export function listHosts(signal?: AbortSignal): Promise<Host[]> {
  return fetchAllPages((before) =>
    api.request<Page<Host>>("/hosts", { query: { limit: MAX_PAGE_LIMIT, before }, signal }),
  );
}

export function createHost(body: Record<string, unknown>): Promise<Host> {
  return api.request<Host>("/hosts", { method: "POST", body });
}

export function updateHost(id: number, body: Record<string, unknown>): Promise<Host> {
  return api.request<Host>(`/hosts/${id}`, { method: "PUT", body });
}

/** 请求体 `{confirm:true}`；被 SSH 动作或计划运行记录引用时 409 resource_conflict。 */
export function deleteHost(id: number): Promise<{ ok: boolean }> {
  return api.request<{ ok: boolean }>(`/hosts/${id}`, { method: "DELETE", body: confirmBody() });
}

export interface FetchedHostKey {
  fingerprint: string;
  /** 服务端候选有效期（5 分钟）；界面按本地获取时刻计算，避免时钟偏差。 */
  expires_at: string;
}

/** 只取主机公钥，不发送用户名或凭据；候选指纹只存服务端内存 5 分钟。 */
export function fetchHostKey(id: number): Promise<FetchedHostKey> {
  return api.request<FetchedHostKey>(`/hosts/${id}/hostkey/fetch`, { method: "POST", body: {} });
}

/** 用户人工核对后才调用；伪造、过期、重复确认或地址变化均返回 409。 */
export function confirmHostKey(id: number, fingerprint: string): Promise<Host> {
  return api.request<Host>(`/hosts/${id}/hostkey/confirm`, {
    method: "POST",
    body: { confirm: true, fingerprint },
  });
}

/** 指纹校验 + SSH 认证，不打开命令会话。 */
export function testHostConnection(id: number): Promise<{ ok: boolean }> {
  return api.request<{ ok: boolean }>(`/hosts/${id}/test-connection`, { method: "POST", body: {} });
}

export interface HostCommandResult {
  status: string;
  exit_code?: number;
  stdout: string;
  stderr: string;
  duration_ms: number;
}

/** 固定执行 `echo ups-warden-ok`（不使用 sudo）；用户确认后才调用。非零退出返回 422 host_operation_failed。 */
export function testHostCommand(id: number): Promise<HostCommandResult> {
  return api.request<HostCommandResult>(`/hosts/${id}/test-command`, {
    method: "POST",
    body: confirmBody(),
  });
}

/** 单次 TCP / ICMP 检测（3 秒）；ICMP 权限不足为 422 host_operation_failed，message 含处理建议。 */
export function checkHostOnline(id: number): Promise<{ online: boolean }> {
  return api.request<{ online: boolean }>(`/hosts/${id}/online-check`, {
    method: "POST",
    body: {},
  });
}
