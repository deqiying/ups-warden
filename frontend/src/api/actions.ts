import { fetchAllPages, MAX_PAGE_LIMIT } from "./devices.ts";
import { api } from "./client.ts";
import { parseHeaders } from "./notifications.ts";
import { confirmBody, secretFields } from "./patch.ts";
import type { Page } from "./types.ts";

/*
 * 动作 API（docs/reference/rules-actions-hosts-api.md「动作」）。
 * - PUT 省略的字段保留；提供 `config` 即整体替换；改变 `type` 会清空旧类型配置与凭据；
 * - HTTP 的 url / headers / body_template 只写不读，响应只有 `*_set`，清除走 `clear_secrets`；
 * - 手动执行总是显式提交 `dry_run`：**未传 dry_run 的手动动作按真实执行处理**。
 */

export type ActionType = "notify" | "http" | "script" | "ssh" | "plan";

export const HTTP_METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"] as const;
export type HttpMethod = (typeof HTTP_METHODS)[number];

/** 各类型配置；后端 omitempty，字段是否出现取决于类型。 */
export interface ActionConfig {
  channel_ids?: number[];
  host_id?: number;
  method?: HttpMethod;
  expected_status?: number[];
  script?: string;
  args?: string[];
  command?: string;
  shutdown?: boolean;
  plan_id?: number;
}

export interface Action {
  id: number;
  name: string;
  type: ActionType;
  timeout_s: number;
  config: ActionConfig;
  url_set: boolean;
  headers_set: boolean;
  body_template_set: boolean;
}

export type ActionSecretKey = "url" | "headers" | "body_template";

export const DEFAULT_ACTION_TIMEOUT_S = 30;

/** 后端 action.headerName 之外额外禁止的 Header（大小写不敏感）。 */
export const ACTION_FORBIDDEN_HEADERS: ReadonlySet<string> = new Set([
  "host",
  "proxy-authorization",
  "connection",
  "transfer-encoding",
  "content-length",
]);

/** 补齐可能缺省的配置字段（omitempty），页面不用处理 undefined。 */
export function normalizeAction(wire: Action): Action {
  return { ...wire, config: { ...(wire.config ?? {}) } };
}

/** 动作表单草稿；HTTP 凭据输入永不预填，空串表示“保留”。 */
export interface ActionDraft {
  name: string;
  type: ActionType;
  timeoutS: number;
  channelIds: number[];
  method: HttpMethod;
  /** 期望状态码（200～599）；空表示默认 2xx。 */
  expectedStatus: number[];
  script: string;
  args: string[];
  hostId: number | null;
  command: string;
  shutdown: boolean;
  planId: number | null;
  url: string;
  /** 每行一个 `Name: value`。 */
  headersText: string;
  bodyTemplate: string;
  clearSecrets: ActionSecretKey[];
}

export function emptyActionDraft(type: ActionType = "ssh"): ActionDraft {
  return {
    name: "",
    type,
    timeoutS: DEFAULT_ACTION_TIMEOUT_S,
    channelIds: [],
    method: "POST",
    expectedStatus: [],
    script: "",
    args: [],
    hostId: null,
    command: "",
    shutdown: false,
    planId: null,
    url: "",
    headersText: "",
    bodyTemplate: "",
    clearSecrets: [],
  };
}

export function draftFromAction(action: Action): ActionDraft {
  const c = action.config;
  return {
    ...emptyActionDraft(action.type),
    name: action.name,
    timeoutS: action.timeout_s,
    channelIds: [...(c.channel_ids ?? [])],
    method: c.method ?? "POST",
    expectedStatus: [...(c.expected_status ?? [])],
    script: c.script ?? "",
    args: [...(c.args ?? [])],
    hostId: c.host_id ?? null,
    command: c.command ?? "",
    shutdown: c.shutdown === true,
    planId: c.plan_id ?? null,
  };
}

/** 本类型的完整配置对象；按“提供 config 即整体替换”的语义，只含该类型的键。 */
export function actionConfigOf(draft: ActionDraft): ActionConfig {
  switch (draft.type) {
    case "notify":
      return { channel_ids: [...draft.channelIds] };
    case "http":
      return { method: draft.method, expected_status: [...draft.expectedStatus] };
    case "script":
      return { script: draft.script.trim(), args: [...draft.args] };
    case "ssh":
      return { host_id: draft.hostId ?? 0, command: draft.command, shutdown: draft.shutdown };
    case "plan":
      return { plan_id: draft.planId ?? 0 };
  }
}

/** 补默认值后的规范形式，用于判断配置是否被修改。 */
function canonicalConfig(type: ActionType, c: ActionConfig): ActionConfig {
  switch (type) {
    case "notify":
      return { channel_ids: c.channel_ids ?? [] };
    case "http":
      return { method: c.method ?? "POST", expected_status: c.expected_status ?? [] };
    case "script":
      return { script: c.script ?? "", args: c.args ?? [] };
    case "ssh":
      return { host_id: c.host_id ?? 0, command: c.command ?? "", shutdown: c.shutdown === true };
    case "plan":
      return { plan_id: c.plan_id ?? 0 };
  }
}

/** HTTP 凭据；Header 文本无效时抛错（由表单校验先给出具体提示）。 */
function secretValuesOf(draft: ActionDraft): Record<string, unknown> {
  if (draft.type !== "http") return {};
  const { headers, error } = parseHeaders(draft.headersText, ACTION_FORBIDDEN_HEADERS);
  if (error) throw new Error(`Headers 无效：${error}`);
  return { url: draft.url.trim(), headers, body_template: draft.bodyTemplate };
}

export function buildActionCreateBody(draft: ActionDraft): Record<string, unknown> {
  return {
    name: draft.name.trim(),
    type: draft.type,
    timeout_s: draft.timeoutS,
    config: actionConfigOf(draft),
    ...secretFields(secretValuesOf(draft)),
  };
}

/**
 * 编辑（PUT）请求体：只含修改过的字段。配置有改动时提交完整 config；
 * 切换类型时后端会清空旧配置与凭据，因此提交新类型的完整 config 与必需凭据，`clear_secrets` 不再适用。
 */
export function buildActionUpdateBody(
  original: Action,
  draft: ActionDraft,
): Record<string, unknown> {
  const body: Record<string, unknown> = {};
  const name = draft.name.trim();
  if (name !== original.name) body.name = name;
  const typeChanged = draft.type !== original.type;
  if (typeChanged) body.type = draft.type;
  if (draft.timeoutS !== original.timeout_s) body.timeout_s = draft.timeoutS;

  const config = actionConfigOf(draft);
  const configChanged =
    JSON.stringify(canonicalConfig(draft.type, config)) !==
    JSON.stringify(canonicalConfig(original.type, original.config));
  if (typeChanged || configChanged) body.config = config;

  // URL 是 HTTP 动作的必需凭据，不允许清除。
  const clear =
    typeChanged || draft.type !== "http" ? [] : draft.clearSecrets.filter((key) => key !== "url");
  Object.assign(body, secretFields(secretValuesOf(draft), clear));
  return body;
}

/** 手动执行请求体：`confirm:true` 与显式 `dry_run` 总是存在，`device_id` 可选。 */
export function buildActionRunBody(options: { dryRun: boolean; deviceId: number | null }): {
  confirm: true;
  dry_run: boolean;
  device_id?: number;
} {
  return {
    ...confirmBody(),
    dry_run: options.dryRun,
    ...(options.deviceId !== null ? { device_id: options.deviceId } : {}),
  };
}

// ───────────────────────── 执行记录 ─────────────────────────

/** 执行来源；字段 omitempty。type 已知取值 manual / rule / plan，未知值按原文展示。 */
export interface ActionTrigger {
  type: string;
  rule_id?: number;
  event_id?: number;
  user_id?: number;
  plan_run_id?: number;
  recovery?: boolean;
}

export interface ActionRun {
  id: number;
  action_id: number;
  trigger: ActionTrigger;
  dry_run: boolean;
  /** 见 ACTION_RUN_STATUS_LABELS；未知值原样显示。 */
  status: string;
  started_at: string;
  ended_at: string | null;
  exit_code: number | null;
  /** 各最多前 4 KiB，已按已知凭据脱敏。 */
  stdout: string;
  stderr: string;
  error: string;
  /** 未结束时为 0。 */
  duration_ms: number;
}

export interface ActionRunQuery {
  action_id?: number;
  before?: number;
  limit?: number;
}

// ───────────────────────── 请求 ─────────────────────────

export function listActions(signal?: AbortSignal): Promise<Action[]> {
  return fetchAllPages((before) =>
    api.request<Page<Action>>("/actions", { query: { limit: MAX_PAGE_LIMIT, before }, signal }),
  ).then((items) => items.map(normalizeAction));
}

/** 保存被 HTTP 护栏拒绝时与其他校验失败一样返回 422 validation_failed（不含具体原因）。 */
export function createAction(body: Record<string, unknown>): Promise<Action> {
  return api.request<Action>("/actions", { method: "POST", body }).then(normalizeAction);
}

/** 执行中的动作修改返回 409 action_busy。 */
export function updateAction(id: number, body: Record<string, unknown>): Promise<Action> {
  return api.request<Action>(`/actions/${id}`, { method: "PUT", body }).then(normalizeAction);
}

/** 请求体 `{confirm:true}`；被规则或计划引用为 409 resource_conflict，执行中为 409 action_busy。 */
export function deleteAction(id: number): Promise<{ ok: boolean }> {
  return api.request<{ ok: boolean }>(`/actions/${id}`, { method: "DELETE", body: confirmBody() });
}

/**
 * 手动执行：202 只表示已受理（同一动作重入记为 skipped_concurrent，仍是 202）。
 * 409 snapshot_unavailable；422 plan_confirmation_required（plan 动作）；503 action_unavailable。
 */
export function runAction(
  id: number,
  body: ReturnType<typeof buildActionRunBody>,
): Promise<{ accepted: boolean; run_id: number }> {
  return api.request<{ accepted: boolean; run_id: number }>(`/actions/${id}/run`, {
    method: "POST",
    body,
  });
}

export function listActionRuns(
  query: ActionRunQuery,
  signal?: AbortSignal,
): Promise<Page<ActionRun>> {
  return api.request<Page<ActionRun>>("/action-runs", { query: { ...query }, signal });
}

/** 服务端脚本目录内可选的相对路径（已排序）；目录不存在时为空。 */
export async function listScripts(signal?: AbortSignal): Promise<string[]> {
  const res = await api.request<{ items: string[] | null }>("/scripts", { signal });
  return res.items ?? [];
}
