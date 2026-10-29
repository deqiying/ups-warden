import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { FakeEventSource, sessionResponse } from "../test/fixtures.ts";
import { createAppServices } from "./services.ts";

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const unauthorized = () => json(401, { code: "unauthorized", message: "unauthorized" });

interface RecordedCall {
  pathname: string;
  method: string;
  headers: Headers;
}

/** 按路径返回的模拟后端；用例可替换单个处理函数。 */
let routes: Record<string, () => Response | Promise<Response>>;
let calls: RecordedCall[];

function callsTo(pathname: string): RecordedCall[] {
  return calls.filter((c) => c.pathname === pathname);
}

function newServices() {
  return createAppServices({ createSource: (url) => new FakeEventSource(url) });
}

beforeEach(() => {
  FakeEventSource.reset();
  calls = [];
  routes = {
    "/api/v1/auth/session": () => json(200, sessionResponse()),
    "/api/v1/setup/status": () => json(200, { setup_required: false }),
    "/api/v1/auth/logout": () => json(200, { ok: true }),
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      const { pathname } = new URL(raw, "http://localhost");
      calls.push({
        pathname,
        method: init?.method ?? "GET",
        headers: new Headers(init?.headers),
      });
      const handler = routes[pathname];
      return handler ? handler() : json(404, { code: "not_found", message: pathname });
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("createAppServices.probe", () => {
  it("session 200 → authenticated，保存用户与 csrfToken", async () => {
    const services = newServices();
    await services.probe();

    const snap = services.session.getSnapshot();
    expect(snap.status).toBe("authenticated");
    expect(snap.user).toEqual(sessionResponse().user);
    expect(snap.csrfToken).toBe(sessionResponse().csrf_token);
    expect(callsTo("/api/v1/setup/status")).toHaveLength(0);
  });

  it("session 401 且 setup_required=true → anonymous + setupRequired，signedOutReason=null", async () => {
    routes["/api/v1/auth/session"] = unauthorized;
    routes["/api/v1/setup/status"] = () => json(200, { setup_required: true });
    const services = newServices();
    await services.probe();

    const snap = services.session.getSnapshot();
    expect(snap.status).toBe("anonymous");
    expect(snap.setupRequired).toBe(true);
    expect(snap.signedOutReason).toBeNull();
    expect(snap.csrfToken).toBeNull();
  });

  it("session 401 且 setup_required=false → anonymous + setupRequired=false", async () => {
    routes["/api/v1/auth/session"] = unauthorized;
    routes["/api/v1/setup/status"] = () => json(200, { setup_required: false });
    const services = newServices();
    await services.probe();

    const snap = services.session.getSnapshot();
    expect(snap.status).toBe("anonymous");
    expect(snap.setupRequired).toBe(false);
    expect(snap.signedOutReason).toBeNull();
  });

  it("session 401 而 setup/status 网络失败 → error", async () => {
    routes["/api/v1/auth/session"] = unauthorized;
    routes["/api/v1/setup/status"] = () => Promise.reject(new TypeError("Failed to fetch"));
    const services = newServices();
    await services.probe();

    const snap = services.session.getSnapshot();
    expect(snap.status).toBe("error");
    expect(snap.error).toBeTruthy();
    expect(callsTo("/api/v1/setup/status")).toHaveLength(1);
  });

  it("session 网络失败 → error，且不请求 setup/status", async () => {
    routes["/api/v1/auth/session"] = () => Promise.reject(new TypeError("Failed to fetch"));
    const services = newServices();
    await services.probe();

    const snap = services.session.getSnapshot();
    expect(snap.status).toBe("error");
    expect(snap.error).toBeTruthy();
    expect(callsTo("/api/v1/setup/status")).toHaveLength(0);
  });

  it("并发两次 probe 共享同一次探测，只发一次 session 请求", async () => {
    const services = newServices();
    const first = services.probe();
    const second = services.probe();
    expect(second).toBe(first);
    await Promise.all([first, second]);

    expect(callsTo("/api/v1/auth/session")).toHaveLength(1);
    expect(services.session.getSnapshot().status).toBe("authenticated");
  });

  it("探测结束后可再次发起新的 probe", async () => {
    const services = newServices();
    await services.probe();
    await services.probe();

    expect(callsTo("/api/v1/auth/session")).toHaveLength(2);
  });
});

describe("createAppServices.logout", () => {
  async function signedIn() {
    const services = newServices();
    await services.probe();
    expect(services.session.getSnapshot().status).toBe("authenticated");
    return services;
  }

  it("POST /auth/logout 带 X-CSRF-Token；成功后会话 anonymous、reason=logout、csrfToken 清空", async () => {
    const services = await signedIn();
    await services.logout();

    const [call] = callsTo("/api/v1/auth/logout");
    expect(callsTo("/api/v1/auth/logout")).toHaveLength(1);
    expect(call?.method).toBe("POST");
    expect(call?.headers.get("X-CSRF-Token")).toBe(sessionResponse().csrf_token);

    const snap = services.session.getSnapshot();
    expect(snap.status).toBe("anonymous");
    expect(snap.signedOutReason).toBe("logout");
    expect(snap.csrfToken).toBeNull();
    expect(snap.user).toBeNull();
  });

  it("logout 返回 401 也按已退出处理", async () => {
    const services = await signedIn();
    routes["/api/v1/auth/logout"] = unauthorized;
    await services.logout();

    const snap = services.session.getSnapshot();
    expect(snap.status).toBe("anonymous");
    expect(snap.signedOutReason).toBe("logout");
    expect(snap.csrfToken).toBeNull();
  });

  it("logout 返回 500 时抛出，会话仍为 authenticated", async () => {
    const services = await signedIn();
    routes["/api/v1/auth/logout"] = () =>
      json(500, { code: "internal_error", message: "internal error" });

    await expect(services.logout()).rejects.toMatchObject({ name: "ApiError", status: 500 });

    const snap = services.session.getSnapshot();
    expect(snap.status).toBe("authenticated");
    expect(snap.csrfToken).toBe(sessionResponse().csrf_token);
    expect(snap.signedOutReason).toBeNull();
  });
});
