import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { AppProviders, AppRoutes } from "./App.tsx";
import { createAppServices } from "./app/services.ts";
import {
  FakeEventSource,
  sessionResponse,
  settings,
  wireDevice,
  wireSync,
} from "./test/fixtures.ts";

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const unauthorized = () => json(401, { code: "unauthorized", message: "unauthorized" });

/** 按路径返回的模拟后端；用例可替换单个处理函数。 */
let routes: Record<string, () => Response | Promise<Response>>;

beforeEach(() => {
  FakeEventSource.reset();
  routes = {
    "/api/v1/auth/session": () => json(200, sessionResponse()),
    "/api/v1/setup/status": () => json(200, { setup_required: false }),
    "/api/v1/settings": () => json(200, settings()),
    "/api/v1/notify-logs": () => json(200, { items: [], next_before: null }),
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      const { pathname } = new URL(raw, "http://localhost");
      const handler = routes[pathname];
      return handler ? handler() : json(404, { code: "not_found", message: pathname });
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function CurrentLocation() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname}</output>;
}

function renderApp(path: string) {
  const services = createAppServices({ createSource: (url) => new FakeEventSource(url) });
  render(
    <AppProviders services={services}>
      <MemoryRouter initialEntries={[path]}>
        <AppRoutes />
        <CurrentLocation />
      </MemoryRouter>
    </AppProviders>,
  );
  return services;
}

function currentPath(): string {
  return screen.getByTestId("location").textContent ?? "";
}

describe("App 路由与会话守卫", () => {
  it("未登录访问业务页面时转到登录页，不建立实时连接", async () => {
    routes["/api/v1/auth/session"] = unauthorized;
    renderApp("/devices?filter=on_battery");
    expect(await screen.findByRole("heading", { name: "登录 UPS Warden" })).toBeTruthy();
    expect(currentPath()).toBe("/login");
    expect(screen.queryByText(/会话已过期/)).toBeNull();
    expect(FakeEventSource.instances).toHaveLength(0);
  });

  it("已登录显示外壳、导航与当前用户，并建立唯一实时连接", async () => {
    const services = renderApp("/devices");
    expect(await screen.findByRole("heading", { level: 1, name: "设备" })).toBeTruthy();
    expect(document.title).toBe("设备 · UPS Warden");

    const nav = screen.getByRole("navigation", { name: "主导航" });
    expect(within(nav).getByRole("link", { name: "设备" }).getAttribute("aria-current")).toBe(
      "page",
    );
    expect(screen.getByText("admin")).toBeTruthy();
    // expires_at 06:05Z 按设置时区 Asia/Shanghai 显示。
    expect(await screen.findByText("管理员 · 会话至 10月28日 14:05")).toBeTruthy();

    expect(FakeEventSource.instances).toHaveLength(1);
    const source = FakeEventSource.latest();
    expect(source.url).toBe("/api/v1/stream");
    expect(screen.getByText("正在同步…")).toBeTruthy();
    act(() => {
      source.open();
      source.send("sync", wireSync({ devices: [wireDevice()] }));
    });
    expect(screen.getByText("1 台设备运行中")).toBeTruthy();
    expect(services.live.getSnapshot().connection.phase).toBe("open");
  });

  it("未知路由显示 404", async () => {
    renderApp("/no-such-page");
    expect(await screen.findByText("找不到这个页面")).toBeTruthy();
    expect(screen.getByRole("link", { name: "返回总览" }).getAttribute("href")).toBe("/");
  });

  it("任意请求 401：清空会话与实时数据，转到登录页并说明会话已过期", async () => {
    routes["/api/v1/settings"] = unauthorized;
    const services = renderApp("/alerts");
    expect(await screen.findByText(/会话已过期或已在其他地方退出/)).toBeTruthy();
    expect(currentPath()).toBe("/login");
    expect(services.session.getSnapshot().status).toBe("anonymous");
    expect(services.session.csrfToken).toBeNull();
    expect(FakeEventSource.latest().closed).toBe(true);
    expect(services.live.getSnapshot().connection.phase).toBe("idle");
  });

  it("实时连接断开后探测到会话失效同样转到登录页", async () => {
    const services = renderApp("/");
    expect(await screen.findByRole("heading", { level: 1, name: "总览" })).toBeTruthy();
    routes["/api/v1/auth/session"] = unauthorized;
    act(() => FakeEventSource.latest().fail());
    expect(await screen.findByText(/会话已过期或已在其他地方退出/)).toBeTruthy();
    expect(services.connection.isRunning).toBe(false);
  });

  it("会话探测网络失败显示可重试的错误，而不是当作未登录", async () => {
    const user = userEvent.setup();
    routes["/api/v1/auth/session"] = () => Promise.reject(new TypeError("Failed to fetch"));
    renderApp("/");
    expect(await screen.findByText("无法连接到 UPS Warden")).toBeTruthy();
    expect(currentPath()).toBe("/");

    routes["/api/v1/auth/session"] = () => json(200, sessionResponse());
    await user.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByRole("heading", { level: 1, name: "总览" })).toBeTruthy();
  });

  it("已登录访问登录页时回到首页", async () => {
    renderApp("/login");
    await waitFor(() => expect(currentPath()).toBe("/"));
    expect(await screen.findByRole("heading", { level: 1, name: "总览" })).toBeTruthy();
  });
});
