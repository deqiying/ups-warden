/*
 * 渠道表单草稿：初始值、前端预校验与展示辅助。校验只是尽早提示，规则镜像后端 notify.ValidateConfig，
 * 最终以服务端 422 为准。凭据输入永不预填：草稿里的凭据字段初始都是空串（空串 = 保留）。
 */
import {
  DEFAULT_API_BASE,
  REQUIRED_SECRETS,
  parseHeaders,
  type Channel,
  type ChannelDraft,
  type ChannelType,
  type SecretKey,
} from "../../api/notifications.ts";

export const CHANNEL_TYPE_LABELS: Readonly<Record<ChannelType, string>> = {
  telegram: "Telegram",
  webhook: "Webhook",
};

export function emptyDraft(type: ChannelType = "telegram"): ChannelDraft {
  return {
    name: "",
    type,
    enabled: true,
    chatIds: [],
    telegramFormat: "text",
    apiBase: DEFAULT_API_BASE,
    method: "POST",
    bodyTemplate: "",
    token: "",
    proxyUrl: "",
    url: "",
    headersText: "",
    signingKey: "",
    clearSecrets: [],
  };
}

/** 从服务端渠道生成草稿；只带非敏感配置，凭据字段全部为空。 */
export function draftFromChannel(channel: Channel): ChannelDraft {
  const config = channel.config;
  return {
    ...emptyDraft(channel.type),
    name: channel.name,
    enabled: channel.enabled,
    chatIds: [...(config.chat_ids ?? [])],
    telegramFormat: config.format === "html" ? "html" : "text",
    apiBase: config.api_base || DEFAULT_API_BASE,
    method: config.method === "PUT" ? "PUT" : "POST",
    bodyTemplate: config.body_template ?? "",
  };
}

/** 渠道某项凭据当前是否已设置（来自 `*_set`）。 */
export function isSecretSet(channel: Channel | undefined, key: SecretKey): boolean {
  if (!channel) return false;
  switch (key) {
    case "url":
      return channel.url_set;
    case "headers":
      return channel.headers_set;
    case "signing_key":
      return channel.signing_key_set;
    case "token":
      return channel.token_set;
    case "proxy_url":
      return channel.proxy_url_set;
  }
}

export type DraftErrors = Partial<Record<string, string>>;

const CHAT_ID_USERNAME = /^@[A-Za-z][A-Za-z0-9_]{4,31}$/;
const TOKEN_CHARS = /^[A-Za-z0-9:_-]+$/;

/** Chat ID：非零 int64 字符串（群组为负数）或 @username；用于 TokenInput 的校验。 */
export function validateChatId(value: string): string | null {
  if (value.startsWith("@")) {
    return CHAT_ID_USERNAME.test(value)
      ? null
      : "用户名应为 @ 加 5～32 位字母、数字或下划线，且以字母开头";
  }
  if (!/^-?[1-9]\d{0,18}$/.test(value)) return "Chat ID 应为非零整数（群组为负数）或 @用户名";
  try {
    const id = BigInt(value);
    const limit = 2n ** 63n;
    if (id >= limit || id < -limit) return "Chat ID 超出范围";
  } catch {
    return "Chat ID 应为非零整数（群组为负数）或 @用户名";
  }
  return null;
}

/** 校验 http(s) 地址；`allowQuery` 仅 Webhook URL 允许，代理允许 userinfo。 */
function validateUrl(raw: string, role: "webhook" | "base" | "proxy"): string | null {
  if (raw.length > 8192) return "地址过长";
  let parsed: URL;
  try {
    parsed = new URL(raw);
  } catch {
    return "不是有效的地址";
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:")
    return "仅支持 http:// 或 https://";
  if (raw.includes("#")) return "不能包含 #片段";
  if (role !== "proxy" && (parsed.username || parsed.password)) return "不能包含账号密码";
  if (role !== "webhook" && (parsed.search || raw.includes("?"))) return "不能包含 query";
  if (role === "proxy" && parsed.pathname !== "/" && parsed.pathname !== "")
    return "代理地址不能带路径";
  return null;
}

/**
 * 预校验。`original` 为已保存渠道（新建时 undefined）：
 * 新建或切换类型时，必需凭据（Token / URL）必须提供；其余情况留空表示保留。
 */
export function validateDraft(draft: ChannelDraft, original?: Channel): DraftErrors {
  const errors: DraftErrors = {};
  const name = draft.name.trim();
  if (!name) errors.name = "请填写渠道名称";
  else if (new TextEncoder().encode(name).length > 128) errors.name = "名称最多 128 字节";

  const needRequired = !original || original.type !== draft.type;
  if (draft.type === "telegram") {
    if (needRequired && !draft.token.trim()) errors.token = "请填写 Bot Token";
    else if (draft.token.trim() && !TOKEN_CHARS.test(draft.token.trim())) {
      errors.token = "Token 只能包含字母、数字、冒号、下划线和连字符";
    }
    if (draft.chatIds.length === 0) errors.chatIds = "至少添加 1 个 Chat ID";
    else if (draft.chatIds.length > 32) errors.chatIds = "最多 32 个 Chat ID";
    const base = draft.apiBase.trim();
    if (base) {
      const message = validateUrl(base, "base");
      if (message) errors.apiBase = message;
    }
    const proxy = draft.proxyUrl.trim();
    if (proxy) {
      const message = validateUrl(proxy, "proxy");
      if (message) errors.proxyUrl = message;
    }
  } else {
    const url = draft.url.trim();
    if (needRequired && !url) errors.url = "请填写接收地址";
    else if (url) {
      const message = validateUrl(url, "webhook");
      if (message) errors.url = message;
    }
    const headers = parseHeaders(draft.headersText);
    if (headers.error) errors.headers = headers.error;
    if (draft.signingKey.length > 4096) errors.signingKey = "签名密钥过长";
  }
  return errors;
}

/** 是否允许在界面上“清除”该凭据：必需凭据不能清除。 */
export function canClear(key: SecretKey): boolean {
  return !REQUIRED_SECRETS.includes(key);
}

/** 列表副标题：“Telegram · 2 个 Chat ID” / “Webhook · 签名已设置”。 */
export function channelSubtitle(channel: Channel): string {
  if (channel.type === "telegram") {
    return `Telegram · ${channel.config.chat_ids?.length ?? 0} 个 Chat ID`;
  }
  return `Webhook · ${channel.config.method || "POST"} · ${channel.signing_key_set ? "签名已设置" : "未签名"}`;
}
