import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { AppProviders, AppRoutes } from "../../App.tsx";
import { createAppServices } from "../../app/services.ts";
import { FakeEventSource, sessionResponse, settings } from "../../test/fixtures.ts";

function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });
}

const unauthorized = () => json(401, { code: "unauthorized", message: "unauthorized" });

interface Call {
  method: string;
  path: string;
  body: unknown;
  headers: Record<string, string>;
}

type Handler = (call: Call) => Response | Promise<Response>;

let routes: Record<string, Handler>;
let calls: Call[];

const PASSWORD = "correct-horse-1";
const TOKEN = "setup-token-abc";

beforeEach(() => {
  FakeEventSource.reset();
  calls = [];
  routes = {
    "GET /api/v1/auth/session": unauthorized,
    "GET /api/v1/setup/status": () => json(200, { setup_required: false }),
    "GET /api/v1/settings": () => json(200, settings()),
    "GET /api/v1/notify-logs": () => json(200, { items: [], next_before: 0 }),
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      const url = new URL(raw, "http://localhost");
      const method = init?.method ?? "GET";
      const call: Call = {
        method,
        path: url.pathname + url.search,
        body: typeof init?.body === "string" ? JSON.parse(init.body) : undefined,
        headers: (init?.headers ?? {}) as Record<string, string>,
      };
      calls.push(call);
      const handler = routes[`${method} ${url.pathname}`];
      return handler ? handler(call) : json(404, { code: "not_found", message: url.pathname });
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
  sessionStorage.clear();
});

function CurrentLocation() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname + location.search}</output>;
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

const currentPath = () => screen.getByTestId("location").textContent ?? "";
const callsTo = (method: string, path: string) =>
  calls.filter((c) => c.method === method && c.path.startsWith(path));

/** 凭据不进入 URL、浏览器存储或控制台（F1 完成标准）。 */
function expectNoLeak(secrets: string[], consoleSpies: ReturnType<typeof vi.spyOn>[]) {
  expect(localStorage.length).toBe(0);
  expect(sessionStorage.length).toBe(0);
  for (const secret of secrets) {
    expect(currentPath()).not.toContain(secret);
    for (const c of calls) expect(c.path).not.toContain(secret);
    for (const spy of consoleSpies) {
      for (const args of spy.mock.calls) expect(JSON.stringify(args)).not.toContain(secret);
    }
  }
}

function spyConsole() {
  return (["log", "info", "warn", "error", "debug"] as const).map((m) =>
    vi.spyOn(console, m).mockImplementation(() => {}),
  );
}

describe("启动分流（F1-1）", () => {
  it("未登录且尚未初始化：任意页面转到初始化页", async () => {
    routes["GET /api/v1/setup/status"] = () => json(200, { setup_required: true });
    renderApp("/devices");
    expect(await screen.findByRole("heading", { name: "欢迎使用 UPS Warden" })).toBeTruthy();
    expect(currentPath()).toBe("/setup");
    expect(FakeEventSource.instances).toHaveLength(0);
  });

  it("已初始化时访问 /setup 转到登录页", async () => {
    renderApp("/setup");
    expect(await screen.findByRole("heading", { name: "登录 UPS Warden" })).toBeTruthy();
    expect(currentPath()).toBe("/login");
  });

  it("尚未初始化时访问 /login 转到初始化页", async () => {
    routes["GET /api/v1/setup/status"] = () => json(200, { setup_required: true });
    renderApp("/login");
    expect(await screen.findByRole("heading", { name: "欢迎使用 UPS Warden" })).toBeTruthy();
    expect(currentPath()).toBe("/setup");
  });

  it("初始化状态查询失败显示可重试错误，不猜测进入哪一页", async () => {
    routes["GET /api/v1/setup/status"] = () => Promise.reject(new TypeError("Failed to fetch"));
    renderApp("/");
    expect(await screen.findByText("无法连接到 UPS Warden")).toBeTruthy();
    expect(screen.getByRole("button", { name: "重试" })).toBeTruthy();
  });
});

describe("初始化页（F1-2）", () => {
  beforeEach(() => {
    routes["GET /api/v1/setup/status"] = () => json(200, { setup_required: true });
  });

  async function fillSetup(user: ReturnType<typeof userEvent.setup>, overrides = {}) {
    const values = {
      token: TOKEN,
      username: "Admin",
      password: PASSWORD,
      confirm: PASSWORD,
      ...overrides,
    };
    await user.type(await screen.findByLabelText("初始化令牌"), values.token);
    await user.type(screen.getByLabelText("用户名"), values.username);
    await user.type(screen.getByLabelText("密码"), values.password);
    await user.type(screen.getByLabelText("确认密码"), values.confirm);
  }

  it("校验未通过时不能提交；用户名统一转小写", async () => {
    const user = userEvent.setup();
    renderApp("/setup");
    await fillSetup(user, { password: "short", confirm: "shorx" });
    expect((screen.getByLabelText("用户名") as HTMLInputElement).value).toBe("admin");
    const submit = screen.getByRole("button", { name: "创建账户" }) as HTMLButtonElement;
    expect(submit.disabled).toBe(true);

    const rules = screen.getByRole("list");
    expect(
      within(rules)
        .getByText(/密码至少 8 个字符/)
        .closest("li")?.className,
    ).toBe("bad");
    expect(
      within(rules)
        .getByText(/两次输入一致/)
        .closest("li")?.className,
    ).toBe("bad");

    await user.click(submit);
    expect(callsTo("POST", "/api/v1/setup")).toHaveLength(0);
  });

  it("用户名含非法字符时不能提交并给出提示", async () => {
    const user = userEvent.setup();
    renderApp("/setup");
    await fillSetup(user, { username: "ad min" });
    expect((screen.getByRole("button", { name: "创建账户" }) as HTMLButtonElement).disabled).toBe(
      true,
    );
    expect(screen.getByLabelText("用户名").getAttribute("aria-invalid")).toBe("true");
  });

  it("成功：提交令牌与凭据，转到登录页并预填用户名，密码不预填", async () => {
    const consoleSpies = spyConsole();
    routes["POST /api/v1/setup"] = () => json(201, sessionResponse().user);
    const user = userEvent.setup();
    renderApp("/setup");
    await fillSetup(user);
    await user.click(screen.getByRole("button", { name: "创建账户" }));

    expect(await screen.findByRole("heading", { name: "登录 UPS Warden" })).toBeTruthy();
    expect(currentPath()).toBe("/login");
    expect(callsTo("POST", "/api/v1/setup")[0].body).toEqual({
      setup_token: TOKEN,
      username: "admin",
      password: PASSWORD,
    });
    expect(screen.getByText(/管理员账户已创建/)).toBeTruthy();
    expect((screen.getByLabelText("用户名") as HTMLInputElement).value).toBe("admin");
    const password = screen.getByLabelText("密码") as HTMLInputElement;
    expect(password.value).toBe("");
    expect(document.activeElement).toBe(password);
    expectNoLeak([TOKEN, PASSWORD], consoleSpies);
  });

  it("令牌无效：显示错误并标记表单，停留在初始化页", async () => {
    routes["POST /api/v1/setup"] = () =>
      json(403, { code: "invalid_setup_token", message: "初始化 token 无效" });
    const user = userEvent.setup();
    renderApp("/setup");
    await fillSetup(user);
    await user.click(screen.getByRole("button", { name: "创建账户" }));

    expect((await screen.findByRole("alert")).textContent).toMatch(/初始化令牌无效/);
    expect(screen.getByRole("form", { name: "创建管理员账户" }).className).toContain("is-error");
    expect(currentPath()).toBe("/setup");
  });

  it("setup_closed：已由他人完成初始化，转到登录页", async () => {
    routes["POST /api/v1/setup"] = () =>
      json(409, { code: "setup_closed", message: "初始化已完成" });
    const user = userEvent.setup();
    renderApp("/setup");
    await fillSetup(user);
    await user.click(screen.getByRole("button", { name: "创建账户" }));

    expect(await screen.findByText(/初始化已经完成，请使用管理员账户登录/)).toBeTruthy();
    expect(currentPath()).toBe("/login");
  });
});

describe("登录页（F1-3）", () => {
  async function fillLogin(user: ReturnType<typeof userEvent.setup>, password = PASSWORD) {
    await user.type(await screen.findByLabelText("用户名"), "admin");
    await user.type(screen.getByLabelText("密码"), password);
  }

  it("成功：建立会话并回到原页面，凭据不泄露", async () => {
    const consoleSpies = spyConsole();
    routes["POST /api/v1/auth/login"] = () => json(200, sessionResponse());
    const user = userEvent.setup();
    const services = renderApp("/alerts?view=history");
    expect(await screen.findByRole("heading", { name: "登录 UPS Warden" })).toBeTruthy();
    await fillLogin(user);
    await user.click(screen.getByRole("button", { name: "登录" }));

    expect(await screen.findByRole("heading", { level: 1, name: "告警与事件" })).toBeTruthy();
    expect(currentPath()).toBe("/alerts?view=history");
    expect(callsTo("POST", "/api/v1/auth/login")[0].body).toEqual({
      username: "admin",
      password: PASSWORD,
    });
    expect(services.session.csrfToken).toBe(sessionResponse().csrf_token);
    expect(FakeEventSource.instances).toHaveLength(1);
    expectNoLeak([PASSWORD, sessionResponse().csrf_token], consoleSpies);
  });

  it("密码错误：清空密码、标记表单并说明锁定规则", async () => {
    routes["POST /api/v1/auth/login"] = () =>
      json(401, { code: "invalid_credentials", message: "用户名或密码错误，或会话已失效" });
    const user = userEvent.setup();
    renderApp("/login");
    await fillLogin(user, "wrong-password");
    await user.click(screen.getByRole("button", { name: "登录" }));

    expect((await screen.findByRole("alert")).textContent).toMatch(/连续失败 5 次/);
    const password = screen.getByLabelText("密码") as HTMLInputElement;
    expect(password.value).toBe("");
    expect(password.getAttribute("aria-invalid")).toBe("true");
    const form = screen.getByRole("form", { name: "登录" });
    expect(form.className).toContain("is-error");
    expect(form.className).toContain("is-shaking");
    expect((screen.getByLabelText("用户名") as HTMLInputElement).value).toBe("admin");
    expect(currentPath()).toBe("/login");

    // 重新输入即清除错误态。
    await user.type(password, "x");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("网络失败：显示错误并保留输入，便于重试", async () => {
    routes["POST /api/v1/auth/login"] = () => Promise.reject(new TypeError("Failed to fetch"));
    const user = userEvent.setup();
    renderApp("/login");
    await fillLogin(user);
    await user.click(screen.getByRole("button", { name: "登录" }));

    expect((await screen.findByRole("alert")).textContent).toMatch(/无法连接服务器/);
    expect((screen.getByLabelText("密码") as HTMLInputElement).value).toBe(PASSWORD);
  });

  it("429：按 Retry-After 倒计时，结束后回到表单并保留用户名", async () => {
    routes["POST /api/v1/auth/login"] = () =>
      json(429, { code: "rate_limited", message: "请求过于频繁" }, { "Retry-After": "1" });
    const user = userEvent.setup();
    renderApp("/login");
    await fillLogin(user);
    await user.click(screen.getByRole("button", { name: "登录" }));

    expect(await screen.findByRole("heading", { name: "暂时无法登录" })).toBeTruthy();
    expect(screen.getByRole("img", { name: "00:01 后可重试" })).toBeTruthy();
    expect(screen.queryByLabelText("密码")).toBeNull();

    expect(
      await screen.findByRole("heading", { name: "登录 UPS Warden" }, { timeout: 3000 }),
    ).toBeTruthy();
    expect((screen.getByLabelText("用户名") as HTMLInputElement).value).toBe("admin");
    expect((screen.getByLabelText("密码") as HTMLInputElement).value).toBe("");
  });

  it("429 缺少 Retry-After 时按 15 分钟倒计时", async () => {
    routes["POST /api/v1/auth/login"] = () =>
      json(429, { code: "rate_limited", message: "请求过于频繁" });
    const user = userEvent.setup();
    renderApp("/login");
    await fillLogin(user);
    await user.click(screen.getByRole("button", { name: "登录" }));
    expect(await screen.findByRole("img", { name: "15:00 后可重试" })).toBeTruthy();
  });

  it("返回地址只接受应用内路径", async () => {
    routes["GET /api/v1/auth/session"] = () => json(200, sessionResponse());
    const services = createAppServices({ createSource: (url) => new FakeEventSource(url) });
    render(
      <AppProviders services={services}>
        <MemoryRouter initialEntries={[{ pathname: "/login", state: { from: "//evil.example" } }]}>
          <AppRoutes />
          <CurrentLocation />
        </MemoryRouter>
      </AppProviders>,
    );
    await waitFor(() => expect(currentPath()).toBe("/"));
  });
});

describe("登出（F1-4）", () => {
  beforeEach(() => {
    routes["GET /api/v1/auth/session"] = () => json(200, sessionResponse());
  });

  it("用户菜单退出登录：带 CSRF 提交，清空会话与实时连接，转到登录页", async () => {
    routes["POST /api/v1/auth/logout"] = () => json(200, { ok: true });
    const user = userEvent.setup();
    const services = renderApp("/devices");
    expect(await screen.findByRole("heading", { level: 1, name: "设备" })).toBeTruthy();

    const trigger = screen.getByRole("button", { name: /admin/ });
    expect(trigger.getAttribute("aria-expanded")).toBe("false");
    await user.click(trigger);
    await user.click(screen.getByRole("menuitem", { name: "退出登录" }));

    expect(await screen.findByText("已退出登录。")).toBeTruthy();
    expect(currentPath()).toBe("/login");
    const [logout] = callsTo("POST", "/api/v1/auth/logout");
    expect(logout.headers["X-CSRF-Token"]).toBe(sessionResponse().csrf_token);
    expect(services.session.getSnapshot()).toMatchObject({
      status: "anonymous",
      csrfToken: null,
      user: null,
      signedOutReason: "logout",
    });
    expect(FakeEventSource.latest().closed).toBe(true);
  });

  it("Esc 关闭菜单并回焦到触发按钮，不发送请求", async () => {
    const user = userEvent.setup();
    renderApp("/");
    const trigger = await screen.findByRole("button", { name: /admin/ });
    await user.click(trigger);
    expect(screen.getByRole("menu")).toBeTruthy();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(trigger);
    expect(callsTo("POST", "/api/v1/auth/logout")).toHaveLength(0);
  });

  it("登出失败（非 401）：保留会话并提示", async () => {
    routes["POST /api/v1/auth/logout"] = () =>
      json(500, { code: "internal_error", message: "boom" });
    const user = userEvent.setup();
    const services = renderApp("/");
    await user.click(await screen.findByRole("button", { name: /admin/ }));
    await user.click(screen.getByRole("menuitem", { name: "退出登录" }));

    expect(await screen.findByText("退出登录失败")).toBeTruthy();
    expect(services.session.getSnapshot().status).toBe("authenticated");
    expect(currentPath()).toBe("/");
  });
});
