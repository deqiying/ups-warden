import { errorMessage } from "../model/errors.ts";

/** 统一的 API 错误：status 为 HTTP 状态（网络失败为 0），code 为后端稳定错误码。 */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  /** 后端原始 message（不含凭据）；界面文案见 `message`。 */
  readonly serverMessage: string;
  readonly details: Readonly<Record<string, unknown>>;
  /** 429 的 Retry-After（秒）；未提供或无法解析时为 null。 */
  readonly retryAfterSeconds: number | null;

  constructor(init: {
    status: number;
    code: string;
    serverMessage?: string;
    details?: Record<string, unknown>;
    retryAfterSeconds?: number | null;
  }) {
    super(errorMessage(init.code, init.serverMessage ?? "", init.status));
    this.name = "ApiError";
    this.status = init.status;
    this.code = init.code;
    this.serverMessage = init.serverMessage ?? "";
    this.details = init.details ?? {};
    this.retryAfterSeconds = init.retryAfterSeconds ?? null;
  }
}

export function isApiError(err: unknown): err is ApiError {
  return err instanceof ApiError;
}

/**
 * 503 / configuration_saved_runtime_error：配置**已经保存**，只是运行时恢复失败。
 * 不能当作回滚处理——调用方应重新读取配置，而不是恢复表单旧值。
 */
export function isConfigSavedRuntimeError(err: unknown): err is ApiError {
  return isApiError(err) && err.status === 503 && err.code === "configuration_saved_runtime_error";
}

/** 解析 Retry-After：秒数或 HTTP 日期；无法解析返回 null。 */
export function parseRetryAfter(header: string | null, nowMs: number): number | null {
  if (!header) return null;
  const trimmed = header.trim();
  if (/^\d+$/.test(trimmed)) return Number(trimmed);
  // HTTP-date 必含星期与月份名；纯数字以外的无字母值（如 "-5"）不交给 Date.parse 宽松解析。
  if (!/[a-z]/i.test(trimmed)) return null;
  const at = Date.parse(trimmed);
  if (Number.isNaN(at)) return null;
  return Math.max(0, Math.ceil((at - nowMs) / 1000));
}

export type QueryValue = string | number | boolean | null | undefined;

export interface RequestOptions {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  /** 值为 null / undefined / 空串的参数不发送。 */
  query?: Readonly<Record<string, QueryValue>>;
  /** JSON 请求体；undefined 表示无请求体。 */
  body?: unknown;
  signal?: AbortSignal;
  /**
   * 默认 true：401 触发全局会话失效（清空会话与实时存储并转到登录页）。
   * 登录、初始化与会话探测自己处理 401，需设为 false。
   */
  handleUnauthorized?: boolean;
}

export interface ApiHooks {
  /** CSRF token 只保存在内存中（会话存储）。 */
  getCsrfToken(): string | null;
  onUnauthorized(err: ApiError): void;
  onConfigSavedRuntimeError(err: ApiError): void;
}

export interface ApiClient {
  request<T>(path: string, options?: RequestOptions): Promise<T>;
}

export function buildQuery(query: Readonly<Record<string, QueryValue>> | undefined): string {
  if (!query) return "";
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value === null || value === undefined || value === "") continue;
    params.append(key, String(value));
  }
  const text = params.toString();
  return text ? `?${text}` : "";
}

function isWireError(
  value: unknown,
): value is { code: string; message?: unknown; details?: unknown } {
  return (
    typeof value === "object" &&
    value !== null &&
    typeof (value as { code?: unknown }).code === "string"
  );
}

export function createApiClient(
  hooks: ApiHooks,
  fetchImpl: typeof fetch = (...args) => fetch(...args),
  base = "/api/v1",
  now: () => number = Date.now,
): ApiClient {
  return {
    async request<T>(path: string, options: RequestOptions = {}): Promise<T> {
      const method = options.method ?? "GET";
      const headers: Record<string, string> = { Accept: "application/json" };
      let body: string | undefined;
      if (options.body !== undefined) {
        headers["Content-Type"] = "application/json";
        body = JSON.stringify(options.body);
      }
      if (method !== "GET") {
        const csrf = hooks.getCsrfToken();
        if (csrf) headers["X-CSRF-Token"] = csrf;
      }

      let res: Response;
      try {
        res = await fetchImpl(base + path + buildQuery(options.query), {
          method,
          headers,
          body,
          credentials: "same-origin",
          signal: options.signal,
        });
      } catch (err) {
        // 主动取消交给调用方（React Query 会忽略），其余视为网络失败。
        if (err instanceof DOMException && err.name === "AbortError") throw err;
        throw new ApiError({ status: 0, code: "network_error" });
      }

      const text = await res.text();
      let data: unknown = null;
      let parsed = true;
      if (text) {
        try {
          data = JSON.parse(text);
        } catch {
          parsed = false;
        }
      }

      if (res.ok) {
        if (!parsed) throw new ApiError({ status: res.status, code: "invalid_response" });
        return data as T;
      }

      const error = isWireError(data)
        ? new ApiError({
            status: res.status,
            code: data.code,
            serverMessage: typeof data.message === "string" ? data.message : "",
            details:
              typeof data.details === "object" && data.details !== null
                ? (data.details as Record<string, unknown>)
                : {},
            retryAfterSeconds: parseRetryAfter(res.headers.get("Retry-After"), now()),
          })
        : new ApiError({
            status: res.status,
            code: `http_${res.status}`,
            retryAfterSeconds: parseRetryAfter(res.headers.get("Retry-After"), now()),
          });

      if (res.status === 401 && options.handleUnauthorized !== false) hooks.onUnauthorized(error);
      if (isConfigSavedRuntimeError(error)) hooks.onConfigSavedRuntimeError(error);
      throw error;
    },
  };
}

// 默认实例的钩子由应用根组件在启动时注入（见 App.tsx）；未注入前的行为是只抛错、不做全局处理。
const hooks: ApiHooks = {
  getCsrfToken: () => null,
  onUnauthorized: () => {},
  onConfigSavedRuntimeError: () => {},
};

export function setApiHooks(next: Partial<ApiHooks>): void {
  Object.assign(hooks, next);
}

/** 应用共用的 API 客户端；页面通过 api/*.ts 中的请求函数调用，不直接 fetch。 */
export const api: ApiClient = createApiClient({
  getCsrfToken: () => hooks.getCsrfToken(),
  onUnauthorized: (err) => hooks.onUnauthorized(err),
  onConfigSavedRuntimeError: (err) => hooks.onConfigSavedRuntimeError(err),
});
