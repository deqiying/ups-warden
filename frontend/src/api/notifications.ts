import { fetchAllPages, MAX_PAGE_LIMIT } from "./devices.ts";
import { api } from "./client.ts";
import { confirmBody, secretFields } from "./patch.ts";
import type { NotificationResult, Page } from "./types.ts";

/*
 * 通知 API（docs/reference/notifications-api.md）：渠道、测试消息、模板与发送记录。
 * 凭据（url / headers / signing_key / token / proxy_url）只写不读：响应只有 `*_set`，
 * 请求体里留空省略，清除走显式 `clear_secrets`，且同一字段不能同时设置和清除。
 */

// ───────────────────────── 查询键 ─────────────────────────

/** 渠道与模板的查询键前缀；发送记录沿用 `queryKeys.notifyLogs`（hooks.ts）。 */
export const channelKeys = { all: ["channels"] as const };
export const templateKeys = { all: ["notify-templates"] as const };

// ───────────────────────── 渠道类型 ─────────────────────────

export type ChannelType = "telegram" | "webhook";

/** 可明文保存的渠道配置；字段是否出现取决于渠道类型（后端 omitempty）。 */
export interface ChannelConfig {
  /** Webhook：POST / PUT。 */
  method?: string;
  /** Webhook：可选的正文模板，省略时使用对应事件的全局模板。 */
  body_template?: string;
  /** Telegram：自建反向代理地址。 */
  api_base?: string;
  /** Telegram：1～32 个 Chat ID。 */
  chat_ids?: string[];
  /** Telegram：text / html；Webhook 固定 json。 */
  format?: string;
}

export interface Channel {
  id: number;
  name: string;
  type: ChannelType;
  config: ChannelConfig;
  enabled: boolean;
  url_set: boolean;
  headers_set: boolean;
  signing_key_set: boolean;
  token_set: boolean;
  proxy_url_set: boolean;
}

export type SecretKey = "url" | "headers" | "signing_key" | "token" | "proxy_url";

/** 各渠道类型拥有的凭据字段。 */
export const SECRET_KEYS_BY_TYPE: Readonly<Record<ChannelType, readonly SecretKey[]>> = {
  telegram: ["token", "proxy_url"],
  webhook: ["url", "headers", "signing_key"],
};

/** 必需凭据：后端拒绝清除（即使渠道已停用）。 */
export const REQUIRED_SECRETS: readonly SecretKey[] = ["token", "url"];

export const DEFAULT_API_BASE = "https://api.telegram.org";

/** 补齐可能缺省的配置字段，保证页面不用处理 undefined。 */
export function normalizeChannel(wire: Channel): Channel {
  return { ...wire, config: { ...wire.config, chat_ids: wire.config?.chat_ids ?? [] } };
}

// ───────────────────────── 渠道请求体构造 ─────────────────────────

/** 渠道表单草稿；凭据输入永不预填，空串表示“保留”。 */
export interface ChannelDraft {
  name: string;
  type: ChannelType;
  enabled: boolean;
  chatIds: string[];
  telegramFormat: "text" | "html";
  apiBase: string;
  method: "POST" | "PUT";
  bodyTemplate: string;
  token: string;
  proxyUrl: string;
  url: string;
  /** 每行一个 `Name: value`。 */
  headersText: string;
  signingKey: string;
  /** 要求清除的凭据；与同名新值互斥。 */
  clearSecrets: SecretKey[];
}

// Header 规则镜像后端 validHeaders：token 字符、禁用名、大小写不敏感去重。
const HEADER_NAME = /^[A-Za-z0-9!#$%&'*+\-.^_`|~]+$/;
const FORBIDDEN_HEADERS: ReadonlySet<string> = new Set([
  "host",
  "content-length",
  "transfer-encoding",
  "x-ups-warden-timestamp",
  "x-ups-warden-signature",
]);

/** 与后端一致：除 Tab 外的控制字符（含 DEL）都拒绝。 */
function hasControlChar(value: string): boolean {
  for (const char of value) {
    const code = char.codePointAt(0) ?? 0;
    if ((code < 32 && code !== 9) || code === 127) return true;
  }
  return false;
}

/**
 * 解析 Header 文本；错误信息只含行号与规则，不回显值（值可能是凭据）。
 * `forbidden` 为小写的禁用名集合，默认是 Webhook 渠道的规则；HTTP 动作传入自己的集合。
 */
export function parseHeaders(
  text: string,
  forbidden: ReadonlySet<string> = FORBIDDEN_HEADERS,
): {
  headers: Record<string, string>;
  error: string | null;
} {
  const headers: Record<string, string> = {};
  const seen = new Set<string>();
  const lines = text.split(/\r?\n/);
  for (const [index, raw] of lines.entries()) {
    if (raw.trim() === "") continue;
    const at = `第 ${index + 1} 行`;
    const colon = raw.indexOf(":");
    if (colon <= 0) return { headers: {}, error: `${at}：格式应为「名称: 值」` };
    const name = raw.slice(0, colon).trim();
    const value = raw.slice(colon + 1).trim();
    if (!HEADER_NAME.test(name)) return { headers: {}, error: `${at}：Header 名称含非法字符` };
    const key = name.toLowerCase();
    if (forbidden.has(key)) return { headers: {}, error: `${at}：不能设置 ${name}` };
    if (seen.has(key)) return { headers: {}, error: `${at}：Header 名称重复（不区分大小写）` };
    if (hasControlChar(value)) return { headers: {}, error: `${at}：值含控制字符` };
    seen.add(key);
    headers[name] = value;
  }
  return { headers, error: null };
}

/** 本类型的配置对象；按后端“提供 config 即整体替换”的语义，必须是完整对象。 */
export function configOf(draft: ChannelDraft): ChannelConfig {
  if (draft.type === "telegram") {
    const apiBase = draft.apiBase.trim();
    return {
      chat_ids: [...draft.chatIds],
      format: draft.telegramFormat,
      ...(apiBase ? { api_base: apiBase } : {}),
    };
  }
  return {
    method: draft.method,
    format: "json",
    ...(draft.bodyTemplate.trim() ? { body_template: draft.bodyTemplate } : {}),
  };
}

/** 补默认值后的规范形式，用于判断配置是否被修改（后端读出的配置已补默认值）。 */
function canonicalConfig(type: ChannelType, config: ChannelConfig): ChannelConfig {
  if (type === "telegram") {
    return {
      chat_ids: config.chat_ids ?? [],
      format: config.format || "text",
      api_base: config.api_base || DEFAULT_API_BASE,
    };
  }
  return {
    method: config.method || "POST",
    format: "json",
    body_template: config.body_template ?? "",
  };
}

function secretValuesOf(draft: ChannelDraft): Record<string, unknown> {
  if (draft.type === "telegram") {
    return { token: draft.token.trim(), proxy_url: draft.proxyUrl.trim() };
  }
  const { headers, error } = parseHeaders(draft.headersText);
  if (error) throw new Error(`Headers 无效：${error}`);
  return { url: draft.url.trim(), headers, signing_key: draft.signingKey };
}

/** 新建渠道请求体：凭据留空的字段省略。 */
export function buildCreateBody(draft: ChannelDraft): Record<string, unknown> {
  return {
    name: draft.name.trim(),
    type: draft.type,
    enabled: draft.enabled,
    config: configOf(draft),
    // 新建没有旧值可清除，不提交 clear_secrets。
    ...secretFields(secretValuesOf(draft)),
  };
}

/**
 * 编辑（PUT）请求体：只含修改过的顶层字段。
 * - 配置有改动时提交完整 config（整体替换）；
 * - 凭据留空省略；清除走 `clear_secrets`；
 * - 切换类型：后端会清空旧类型的配置与凭据，因此必须同时提交新类型的 config 与必需凭据，`clear_secrets` 不再适用。
 * 返回空对象表示没有任何修改。
 */
export function buildUpdateBody(original: Channel, draft: ChannelDraft): Record<string, unknown> {
  const body: Record<string, unknown> = {};
  const name = draft.name.trim();
  if (name !== original.name) body.name = name;

  const typeChanged = draft.type !== original.type;
  if (typeChanged) body.type = draft.type;

  const config = configOf(draft);
  const configChanged =
    JSON.stringify(canonicalConfig(draft.type, config)) !==
    JSON.stringify(canonicalConfig(original.type, original.config));
  if (typeChanged || configChanged) body.config = config;

  const clear = typeChanged
    ? []
    : draft.clearSecrets.filter((key) => SECRET_KEYS_BY_TYPE[draft.type].includes(key));
  Object.assign(body, secretFields(secretValuesOf(draft), clear));
  return body;
}

/** 启停开关：单独提交，不夹带表单里未保存的修改。 */
export function buildEnabledBody(enabled: boolean): { enabled: boolean } {
  return { enabled };
}

// ───────────────────────── 渠道请求 ─────────────────────────

/** 全部渠道（含停用），按 id 降序。 */
export function listChannels(signal?: AbortSignal): Promise<Channel[]> {
  return fetchAllPages((before) =>
    api.request<Page<Channel>>("/channels", { query: { limit: MAX_PAGE_LIMIT, before }, signal }),
  ).then((items) => items.map(normalizeChannel));
}

/** 201；503 configuration_saved_runtime_error 表示已保存但运行恢复失败，不是回滚。 */
export function createChannel(body: Record<string, unknown>): Promise<Channel> {
  return api.request<Channel>("/channels", { method: "POST", body }).then(normalizeChannel);
}

export function updateChannel(id: number, body: Record<string, unknown>): Promise<Channel> {
  return api.request<Channel>(`/channels/${id}`, { method: "PUT", body }).then(normalizeChannel);
}

/** 请求体 `{confirm:true}`；调用方必须在用户完成确认对话框后才调用。409：存在引用，可改为停用。 */
export function deleteChannel(id: number): Promise<{ ok: boolean }> {
  return api.request<{ ok: boolean }>(`/channels/${id}`, { method: "DELETE", body: confirmBody() });
}

export interface TestAccepted {
  accepted: boolean;
  channel_id: number;
}

/**
 * 测试消息，请求体 `{}`。202 只表示已入队，不代表送达；
 * 409 channel_disabled、429 notification_queue_full、503 notification_unavailable。
 */
export function testChannel(id: number): Promise<TestAccepted> {
  return api.request<TestAccepted>(`/channels/${id}/test`, { method: "POST", body: {} });
}

// ───────────────────────── 模板 ─────────────────────────

export type TemplateFormat = "json" | "html" | "text";

export interface NotifyTemplate {
  event_type: string;
  format: TemplateFormat;
  body: string;
  /** 是否有覆盖默认模板的自定义内容。 */
  customized: boolean;
}

/** 全部模板：24 种事件 × 3 种格式 = 72 项。 */
export async function listTemplates(signal?: AbortSignal): Promise<NotifyTemplate[]> {
  const res = await api.request<{ items: NotifyTemplate[] | null }>("/notify-templates", {
    signal,
  });
  return res.items ?? [];
}

/** 写入一项模板；未知变量、语法或 JSON 无效时 422 validation_failed（无 details）。 */
export function saveTemplate(
  eventType: string,
  format: TemplateFormat,
  body: string,
): Promise<NotifyTemplate> {
  return api.request<NotifyTemplate>("/notify-templates", {
    method: "PUT",
    body: { event_type: eventType, format, body },
  });
}

/** 恢复默认：`reset:true`，不能同时提供 body。 */
export function resetTemplate(eventType: string, format: TemplateFormat): Promise<NotifyTemplate> {
  return api.request<NotifyTemplate>("/notify-templates", {
    method: "PUT",
    body: { event_type: eventType, format, reset: true },
  });
}

// ───────────────────────── 发送记录 ─────────────────────────

export interface NotifyLogQuery {
  before?: number;
  limit?: number;
  channel_id?: number;
  event_id?: number;
  ok?: boolean;
  /** RFC3339；区间为 `from <= at < to`。 */
  from?: string;
  to?: string;
}

export function listNotifyLogs(
  query: NotifyLogQuery,
  signal?: AbortSignal,
): Promise<Page<NotificationResult>> {
  return api.request<Page<NotificationResult>>("/notify-logs", { query: { ...query }, signal });
}
