import { describe, expect, it, vi } from "vite-plus/test";

import {
  ApiError,
  buildQuery,
  createApiClient,
  isApiError,
  isConfigSavedRuntimeError,
  parseRetryAfter,
} from "./client.ts";
import type { ApiHooks, RequestOptions } from "./client.ts";

interface Setup {
  fetchMock: ReturnType<typeof vi.fn<typeof fetch>>;
  hooks: ApiHooks & {
    getCsrfToken: ReturnType<typeof vi.fn<() => string | null>>;
    onUnauthorized: ReturnType<typeof vi.fn<(err: ApiError) => void>>;
    onConfigSavedRuntimeError: ReturnType<typeof vi.fn<(err: ApiError) => void>>;
  };
  request: <T = unknown>(path: string, options?: RequestOptions) => Promise<T>;
  lastCall: () => { url: string; init: RequestInit; headers: Record<string, string> };
}

/** 构造带假 fetch 与钩子的客户端；`respond` 每次调用都返回新的 Response（Response 只能读取一次）。 */
function setup(
  respond: () => Response | Promise<Response>,
  csrf: string | null = "csrf-abc",
  nowMs = 0,
): Setup {
  const fetchMock = vi.fn<typeof fetch>(async () => respond());
  const hooks = {
    getCsrfToken: vi.fn<() => string | null>(() => csrf),
    onUnauthorized: vi.fn<(err: ApiError) => void>(),
    onConfigSavedRuntimeError: vi.fn<(err: ApiError) => void>(),
  };
  const client = createApiClient(hooks, fetchMock, "/api/v1", () => nowMs);
  return {
    fetchMock,
    hooks,
    request: (path, options) => client.request(path, options),
    lastCall: () => {
      const call = fetchMock.mock.calls.at(-1);
      if (!call) throw new Error("fetch 未被调用");
      const init = call[1] ?? {};
      return {
        url: typeof call[0] === "string" ? call[0] : "",
        init,
        headers: (init.headers ?? {}) as Record<string, string>,
      };
    },
  };
}

const json = (body: unknown, init?: ResponseInit) => new Response(JSON.stringify(body), init);

/** 捕获 Promise 的拒绝值，便于对错误对象做多项断言。 */
async function rejection(promise: Promise<unknown>): Promise<unknown> {
  try {
    await promise;
  } catch (err) {
    return err;
  }
  throw new Error("预期 Promise 被拒绝，但它成功了");
}

describe("buildQuery", () => {
  it("无参数时返回空串", () => {
    expect(buildQuery(undefined)).toBe("");
    expect(buildQuery({})).toBe("");
  });

  it("跳过 null、undefined 与空串", () => {
    expect(buildQuery({ a: null, b: undefined, c: "", d: "x" })).toBe("?d=x");
    expect(buildQuery({ a: null, b: undefined, c: "" })).toBe("");
  });

  it("布尔与数字转为字符串，0 与 false 不会被跳过", () => {
    expect(buildQuery({ limit: 50, offset: 0, active: false, on: true })).toBe(
      "?limit=50&offset=0&active=false&on=true",
    );
  });

  it("对特殊字符做 URL 编码", () => {
    expect(buildQuery({ q: "a b&c" })).toBe("?q=a+b%26c");
  });
});

describe("parseRetryAfter", () => {
  it("纯数字按秒解析", () => {
    expect(parseRetryAfter("900", 0)).toBe(900);
    expect(parseRetryAfter(" 30 ", 0)).toBe(30);
    expect(parseRetryAfter("0", 0)).toBe(0);
  });

  it("HTTP 日期按注入的当前时间换算为剩余秒数（向上取整）", () => {
    const now = Date.parse("2026-10-27T06:00:00Z");
    expect(parseRetryAfter("Tue, 27 Oct 2026 06:15:00 GMT", now)).toBe(900);
    expect(parseRetryAfter("Tue, 27 Oct 2026 06:00:00 GMT", now - 500)).toBe(1);
  });

  it("日期已过去时夹取为 0", () => {
    const now = Date.parse("2026-10-27T07:00:00Z");
    expect(parseRetryAfter("Tue, 27 Oct 2026 06:00:00 GMT", now)).toBe(0);
  });

  it("空值与无法解析的内容返回 null", () => {
    expect(parseRetryAfter(null, 0)).toBeNull();
    expect(parseRetryAfter("", 0)).toBeNull();
    expect(parseRetryAfter("soon", 0)).toBeNull();
  });

  // 负数不是合法的 Retry-After；Date.parse("-5") 在 V8 中会得到有效日期，必须先排除。
  it("负数与其他无字母的非整数值返回 null", () => {
    expect(parseRetryAfter("-5", 0)).toBeNull();
    expect(parseRetryAfter("1.5", 0)).toBeNull();
  });
});

describe("ApiError", () => {
  it("message 为中文文案，serverMessage 保留原文，details 透传", () => {
    const err = new ApiError({
      status: 409,
      code: "resource_conflict",
      serverMessage: "name already used",
      details: { field: "name" },
    });
    expect(err.name).toBe("ApiError");
    expect(err.message).toBe("资源被引用、名称冲突或存在约束冲突");
    expect(err.serverMessage).toBe("name already used");
    expect(err.details).toEqual({ field: "name" });
    expect(err.retryAfterSeconds).toBeNull();
    expect(isApiError(err)).toBe(true);
    expect(isApiError(new Error("x"))).toBe(false);
  });

  it("未提供可选项时使用默认值", () => {
    const err = new ApiError({ status: 500, code: "internal_error" });
    expect(err.serverMessage).toBe("");
    expect(err.details).toEqual({});
  });
});

describe("createApiClient 请求构造", () => {
  it("GET 不带 X-CSRF-Token，也不读取 token", async () => {
    const s = setup(() => json({ ok: true }));
    await s.request("/devices");
    const { headers, init, url } = s.lastCall();
    expect(url).toBe("/api/v1/devices");
    expect(init.method).toBe("GET");
    expect(headers["X-CSRF-Token"]).toBeUndefined();
    expect(headers.Accept).toBe("application/json");
    expect(s.hooks.getCsrfToken).not.toHaveBeenCalled();
  });

  it.each(["POST", "PUT", "PATCH", "DELETE"] as const)("%s 携带 CSRF token", async (method) => {
    const s = setup(() => json({}));
    await s.request("/x", { method });
    expect(s.lastCall().init.method).toBe(method);
    expect(s.lastCall().headers["X-CSRF-Token"]).toBe("csrf-abc");
  });

  it("token 为 null 时不带 X-CSRF-Token", async () => {
    const s = setup(() => json({}), null);
    await s.request("/x", { method: "POST" });
    expect("X-CSRF-Token" in s.lastCall().headers).toBe(false);
  });

  it("有 body 时设置 Content-Type 并序列化为 JSON", async () => {
    const s = setup(() => json({}));
    await s.request("/x", { method: "POST", body: { a: 1, b: "中" } });
    const { headers, init } = s.lastCall();
    expect(headers["Content-Type"]).toBe("application/json");
    expect(init.body).toBe('{"a":1,"b":"中"}');
  });

  it("无 body 时不设置 Content-Type", async () => {
    const s = setup(() => json({}));
    await s.request("/x", { method: "POST" });
    const { headers, init } = s.lastCall();
    expect("Content-Type" in headers).toBe(false);
    expect(init.body).toBeUndefined();
  });

  it("body 为 falsy 值（0、空串、false、null）仍会发送", async () => {
    const s = setup(() => json({}));
    await s.request("/x", { method: "PUT", body: null });
    expect(s.lastCall().init.body).toBe("null");
    await s.request("/x", { method: "PUT", body: false });
    expect(s.lastCall().init.body).toBe("false");
  });

  it("credentials 为 same-origin，并透传 AbortSignal", async () => {
    const s = setup(() => json({}));
    const controller = new AbortController();
    await s.request("/x", { signal: controller.signal });
    expect(s.lastCall().init.credentials).toBe("same-origin");
    expect(s.lastCall().init.signal).toBe(controller.signal);
  });

  it("查询参数拼接在路径之后", async () => {
    const s = setup(() => json({}));
    await s.request("/events", { query: { limit: 20, cursor: "", active: true, type: null } });
    expect(s.lastCall().url).toBe("/api/v1/events?limit=20&active=true");
  });

  it("可自定义 base", async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => json({}));
    const client = createApiClient(
      { getCsrfToken: () => null, onUnauthorized: vi.fn(), onConfigSavedRuntimeError: vi.fn() },
      fetchMock,
      "/custom",
    );
    await client.request("/ping");
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/custom/ping");
  });
});

describe("createApiClient 响应处理", () => {
  it("2xx 返回解析后的 JSON", async () => {
    const s = setup(() => json({ items: [1, 2] }));
    await expect(s.request("/x")).resolves.toEqual({ items: [1, 2] });
  });

  it("空响应体（204 或 200 空串）返回 null", async () => {
    const s204 = setup(() => new Response(null, { status: 204 }));
    await expect(s204.request("/x", { method: "DELETE" })).resolves.toBeNull();
    const s200 = setup(() => new Response("", { status: 200 }));
    await expect(s200.request("/x")).resolves.toBeNull();
  });

  it("200 但响应体不是 JSON → invalid_response", async () => {
    const s = setup(() => new Response("<html>oops</html>", { status: 200 }));
    const err = await rejection(s.request("/x"));
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({
      status: 200,
      code: "invalid_response",
      message: "服务器响应格式无效",
    });
  });

  it("错误响应为 {code,message,details} 结构时透传", async () => {
    const s = setup(() =>
      json(
        { code: "validation_failed", message: "倒计时需在 0～3600 秒", details: { field: "x" } },
        { status: 400 },
      ),
    );
    const err = await rejection(s.request("/x", { method: "POST" }));
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({
      status: 400,
      code: "validation_failed",
      serverMessage: "倒计时需在 0～3600 秒",
      message: "倒计时需在 0～3600 秒",
      details: { field: "x" },
    });
  });

  it("已知 code 的 message 使用映射文案，serverMessage 仍为原文", async () => {
    const s = setup(() =>
      json({ code: "device_not_found", message: "no such device" }, { status: 404 }),
    );
    const err = await rejection(s.request("/x"));
    expect(err).toMatchObject({
      message: "设备不存在",
      serverMessage: "no such device",
      details: {},
    });
  });

  it("message 非字符串、details 非对象时回退为空值", async () => {
    const s = setup(() =>
      json({ code: "internal_error", message: 5, details: "oops" }, { status: 500 }),
    );
    const err = await rejection(s.request("/x"));
    expect(err).toMatchObject({
      serverMessage: "",
      details: {},
      message: "操作失败，请检查服务日志",
    });
  });

  it("错误响应不是 {code} 结构 → http_<status>", async () => {
    const s = setup(() => json({ error: "boom" }, { status: 500 }));
    const err = await rejection(s.request("/x"));
    expect(err).toMatchObject({
      status: 500,
      code: "http_500",
      serverMessage: "",
      message: "请求失败（HTTP 500）",
    });
  });

  it("错误响应体不是 JSON 或为空时同样为 http_<status>", async () => {
    const html = setup(() => new Response("<html>bad gateway</html>", { status: 502 }));
    expect(await rejection(html.request("/x"))).toMatchObject({ code: "http_502" });
    const empty = setup(() => new Response(null, { status: 504 }));
    expect(await rejection(empty.request("/x"))).toMatchObject({ code: "http_504" });
  });

  it("code 不是字符串时视为非结构化错误", async () => {
    const s = setup(() => json({ code: 123 }, { status: 400 }));
    expect(await rejection(s.request("/x"))).toMatchObject({ code: "http_400" });
  });
});

describe("401 处理", () => {
  const unauthorized = () =>
    json({ code: "unauthorized", message: "login required" }, { status: 401 });

  it("默认触发 onUnauthorized 并抛出 ApiError", async () => {
    const s = setup(unauthorized);
    const err = await rejection(s.request("/me"));
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 401, code: "unauthorized", message: "请先登录" });
    expect(s.hooks.onUnauthorized).toHaveBeenCalledTimes(1);
    expect(s.hooks.onUnauthorized).toHaveBeenCalledWith(err);
  });

  it("handleUnauthorized:false 时不触发钩子，仍抛 ApiError", async () => {
    const s = setup(unauthorized);
    const err = await rejection(s.request("/me", { handleUnauthorized: false }));
    expect(err).toMatchObject({ status: 401, code: "unauthorized" });
    expect(s.hooks.onUnauthorized).not.toHaveBeenCalled();
  });

  it("非结构化的 401 响应也触发钩子", async () => {
    const s = setup(() => new Response("", { status: 401 }));
    const err = await rejection(s.request("/me"));
    expect(err).toMatchObject({ status: 401, code: "http_401" });
    expect(s.hooks.onUnauthorized).toHaveBeenCalledTimes(1);
  });

  it("非 401 错误不触发 onUnauthorized", async () => {
    const s = setup(() => json({ code: "forbidden" }, { status: 403 }));
    await rejection(s.request("/x"));
    expect(s.hooks.onUnauthorized).not.toHaveBeenCalled();
  });
});

describe("429 Retry-After", () => {
  it("秒数形式", async () => {
    const s = setup(() =>
      json({ code: "rate_limited" }, { status: 429, headers: { "Retry-After": "900" } }),
    );
    const err = await rejection(s.request("/login", { method: "POST" }));
    expect(err).toMatchObject({ status: 429, code: "rate_limited", retryAfterSeconds: 900 });
  });

  it("HTTP 日期形式使用注入的 now 计算", async () => {
    const now = Date.parse("2026-10-27T06:00:00Z");
    const s = setup(
      () =>
        json(
          { code: "rate_limited" },
          { status: 429, headers: { "Retry-After": "Tue, 27 Oct 2026 06:01:30 GMT" } },
        ),
      "t",
      now,
    );
    const err = await rejection(s.request("/login", { method: "POST" }));
    expect(err).toMatchObject({ retryAfterSeconds: 90 });
  });

  it("无法解析或缺失时为 null", async () => {
    const bad = setup(() =>
      json({ code: "rate_limited" }, { status: 429, headers: { "Retry-After": "later" } }),
    );
    expect(await rejection(bad.request("/x"))).toMatchObject({ retryAfterSeconds: null });
    const missing = setup(() => json({ code: "rate_limited" }, { status: 429 }));
    expect(await rejection(missing.request("/x"))).toMatchObject({ retryAfterSeconds: null });
  });

  it("非结构化的 429 响应同样解析 Retry-After", async () => {
    const s = setup(() => new Response("", { status: 429, headers: { "Retry-After": "12" } }));
    expect(await rejection(s.request("/x"))).toMatchObject({
      code: "http_429",
      retryAfterSeconds: 12,
    });
  });
});

describe("503 configuration_saved_runtime_error", () => {
  it("触发 onConfigSavedRuntimeError，isConfigSavedRuntimeError 为 true", async () => {
    const s = setup(() =>
      json(
        { code: "configuration_saved_runtime_error", message: "restore failed" },
        { status: 503 },
      ),
    );
    const err = await rejection(s.request("/settings", { method: "PUT", body: {} }));
    expect(isConfigSavedRuntimeError(err)).toBe(true);
    expect(s.hooks.onConfigSavedRuntimeError).toHaveBeenCalledTimes(1);
    expect(s.hooks.onConfigSavedRuntimeError).toHaveBeenCalledWith(err);
    expect(s.hooks.onUnauthorized).not.toHaveBeenCalled();
  });

  it("其他 503 code 不触发", async () => {
    const s = setup(() => json({ code: "plan_unavailable" }, { status: 503 }));
    const err = await rejection(s.request("/x"));
    expect(isConfigSavedRuntimeError(err)).toBe(false);
    expect(s.hooks.onConfigSavedRuntimeError).not.toHaveBeenCalled();
  });

  it("同 code 但状态码不是 503 时不触发", async () => {
    const s = setup(() => json({ code: "configuration_saved_runtime_error" }, { status: 500 }));
    const err = await rejection(s.request("/x"));
    expect(isConfigSavedRuntimeError(err)).toBe(false);
    expect(s.hooks.onConfigSavedRuntimeError).not.toHaveBeenCalled();
  });

  it("isConfigSavedRuntimeError 对非 ApiError 返回 false", () => {
    expect(isConfigSavedRuntimeError(new Error("x"))).toBe(false);
    expect(isConfigSavedRuntimeError(null)).toBe(false);
  });
});

describe("fetch 失败", () => {
  it("TypeError → ApiError status 0 / network_error", async () => {
    const s = setup(() => {
      throw new TypeError("Failed to fetch");
    });
    const err = await rejection(s.request("/x"));
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({
      status: 0,
      code: "network_error",
      message: "无法连接服务器，请检查网络或服务状态",
    });
    expect(s.hooks.onUnauthorized).not.toHaveBeenCalled();
  });

  it("AbortError 原样抛出，不是 ApiError", async () => {
    const abort = new DOMException("x", "AbortError");
    const s = setup(() => {
      throw abort;
    });
    const err = await rejection(s.request("/x"));
    expect(err).toBe(abort);
    expect(err).not.toBeInstanceOf(ApiError);
  });

  it("其他 DOMException（非 AbortError）视为网络失败", async () => {
    const s = setup(() => {
      throw new DOMException("x", "NetworkError");
    });
    expect(await rejection(s.request("/x"))).toMatchObject({ status: 0, code: "network_error" });
  });
});
