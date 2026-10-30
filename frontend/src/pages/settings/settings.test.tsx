import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { AppProviders } from "../../App.tsx";
import { createAppServices } from "../../app/services.ts";
import type { AuditEntry } from "../../api/audit.ts";
import type { Settings, User } from "../../api/types.ts";
import { FakeEventSource, T0, sessionResponse, settings } from "../../test/fixtures.ts";
import { SettingsPage } from "./SettingsPage.tsx";
import { THEME_STORAGE_KEY } from "./theme.ts";

// ───────────────────────── 假后端 ─────────────────────────

interface Call {
  method: string;
  path: string;
  search: URLSearchParams;
  body: unknown;
}
type Handler = (call: Call) => Response;

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
const apiError = (status: number, code: string, message = code) => json(status, { code, message });
const page = <T,>(items: T[], nextBefore = 0) => json(200, { items, next_before: nextBefore });

const user = (id: number, username: string): User => ({
  id,
  username,
  role: "admin",
  created_at: T0,
  last_login_at: id === 1 ? T0 : null,
});

const audit = (overrides: Partial<AuditEntry> & { id: number }): AuditEntry => ({
  at: T0,
  user_id: 1,
  ip: "192.0.2.10",
  kind: "login",
  target: "user:1",
  diff: null,
  result: "success",
  ...overrides,
});

/** 当前服务端设置；PATCH 把请求体合并进去并原样返回，和真实后端一致。 */
let current: Settings;
let users: User[];
let handlers: Record<string, Handler>;
let calls: Call[];

const callsTo = (method: string, path: string) =>
  calls.filter((c) => c.method === method && c.path === path);
/** 所有写请求（排除 GET），用于断言“没有发出任何请求”。 */
const writes = () => calls.filter((c) => c.method !== "GET");

beforeEach(() => {
  FakeEventSource.reset();
  window.localStorage.clear();
  calls = [];
  current = settings();
  users = [user(2, "bob"), user(1, "admin")];
  handlers = {
    "GET /api/v1/auth/session": () => json(200, sessionResponse()),
    "GET /api/v1/setup/status": () => json(200, { setup_required: false }),
    "GET /api/v1/settings": () => json(200, current),
    "PATCH /api/v1/settings": (call) => {
      current = { ...current, ...(call.body as Partial<Settings>) };
      return json(200, current);
    },
    "GET /api/v1/users": () => page(users),
    "GET /api/v1/devices": () => page([]),
    "POST /api/v1/auth/logout": () => json(200, { ok: true }),
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      const url = new URL(raw, "http://localhost");
      const call: Call = {
        method: init.method ?? "GET",
        path: url.pathname,
        search: url.searchParams,
        body: typeof init.body === "string" ? JSON.parse(init.body) : undefined,
      };
      calls.push(call);
      const handler = handlers[`${call.method} ${call.path}`];
      if (handler) return handler(call);
      return apiError(404, "not_found", call.path);
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.clear();
  document.documentElement.removeAttribute("data-theme");
});

function CurrentLocation() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname}</output>;
}
const location = () => screen.getByTestId("location").textContent ?? "";

function renderSettings(path: string) {
  const services = createAppServices({ createSource: (url) => new FakeEventSource(url) });
  render(
    <AppProviders services={services}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="/settings/:section" element={<SettingsPage />} />
        </Routes>
        <CurrentLocation />
      </MemoryRouter>
    </AppProviders>,
  );
  return services;
}

type User_ = ReturnType<typeof userEvent.setup>;

/** 在 Stepper 中输入数值并失焦提交。 */
async function setStepper(u: User_, label: string, value: number) {
  const input = await screen.findByLabelText(label);
  await u.clear(input);
  await u.type(input, String(value));
  await u.tab();
}

// ───────────────────────── 路由 ─────────────────────────

describe("路由与分区", () => {
  it("/settings 重定向到 /settings/general；未知分区同样", async () => {
    renderSettings("/settings");
    await waitFor(() => expect(location()).toBe("/settings/general"));
    expect(await screen.findByLabelText("时区")).toBeTruthy();
  });

  it("未知分区重定向到通用", async () => {
    renderSettings("/settings/nope");
    await waitFor(() => expect(location()).toBe("/settings/general"));
  });

  it("点击侧栏分区切换路由，并显示管理员数量", async () => {
    const u = userEvent.setup();
    renderSettings("/settings/general");
    const nav = await screen.findByRole("navigation", { name: "设置分类" });
    await u.click(within(nav).getByText("安全"));
    await waitFor(() => expect(location()).toBe("/settings/security"));
    expect(await within(nav).findByText("2")).toBeTruthy();
  });
});

// ───────────────────────── F8-1 通用 ─────────────────────────

describe("通用", () => {
  it("时区即时保存，PATCH 只含 timezone", async () => {
    const u = userEvent.setup();
    renderSettings("/settings/general");
    await u.selectOptions(await screen.findByLabelText("时区"), "UTC");
    await waitFor(() => expect(callsTo("PATCH", "/api/v1/settings")).toHaveLength(1));
    expect(callsTo("PATCH", "/api/v1/settings")[0].body).toStrictEqual({ timezone: "UTC" });
  });

  it("语言即时保存，PATCH 只含 language", async () => {
    const u = userEvent.setup();
    renderSettings("/settings/general");
    await u.click(await screen.findByRole("button", { name: "English" }));
    await waitFor(() => expect(callsTo("PATCH", "/api/v1/settings")).toHaveLength(1));
    expect(callsTo("PATCH", "/api/v1/settings")[0].body).toStrictEqual({ language: "en-US" });
  });

  it("保存成功后重新读取设置（使全站时区随之变化）", async () => {
    const u = userEvent.setup();
    renderSettings("/settings/general");
    await u.selectOptions(await screen.findByLabelText("时区"), "UTC");
    await waitFor(() => expect(callsTo("GET", "/api/v1/settings").length).toBeGreaterThan(1));
  });

  it("主题只写 localStorage 与 <html data-theme>，不发任何写请求", async () => {
    const u = userEvent.setup();
    renderSettings("/settings/general");
    await u.click(await screen.findByRole("button", { name: "深色" }));
    expect(window.localStorage.getItem(THEME_STORAGE_KEY)).toBe("dark");
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark");

    await u.click(screen.getByRole("button", { name: "跟随系统" }));
    expect(window.localStorage.getItem(THEME_STORAGE_KEY)).toBeNull();
    expect(document.documentElement.hasAttribute("data-theme")).toBe(false);
    expect(writes()).toHaveLength(0);
  });

  it("422 显示保存失败；503 configuration_saved_runtime_error 不显示页内错误", async () => {
    const u = userEvent.setup();
    handlers["PATCH /api/v1/settings"] = () => apiError(422, "validation_failed", "时区无效");
    renderSettings("/settings/general");
    await u.selectOptions(await screen.findByLabelText("时区"), "UTC");
    expect(await screen.findByText("保存失败")).toBeTruthy();
  });

  it("503 configuration_saved_runtime_error：配置已保存，页内不再弹“保存失败”", async () => {
    const u = userEvent.setup();
    handlers["PATCH /api/v1/settings"] = () => apiError(503, "configuration_saved_runtime_error");
    renderSettings("/settings/general");
    await u.selectOptions(await screen.findByLabelText("时区"), "UTC");
    await waitFor(() => expect(callsTo("PATCH", "/api/v1/settings")).toHaveLength(1));
    // 等待请求结束后的重新渲染。
    await waitFor(() => expect(callsTo("GET", "/api/v1/settings").length).toBeGreaterThan(1));
    expect(screen.queryByText("保存失败")).toBeNull();
  });
});

// ───────────────────────── F8-2 数据保留 ─────────────────────────

describe("数据保留", () => {
  const patches = () => callsTo("PATCH", "/api/v1/settings");

  it("聚合少于明细：显示校验错误并禁用保存", async () => {
    const u = userEvent.setup();
    renderSettings("/settings/retention");
    await setStepper(u, "分钟聚合保留天数", 5);
    expect(await screen.findByText(/分钟聚合保留期不能少于明细样本保留期（7 天）/)).toBeTruthy();
    expect((screen.getByRole("button", { name: "保存" }) as HTMLButtonElement).disabled).toBe(true);
    expect(writes()).toHaveLength(0);
  });

  it("明细超过 90 天被限制在上限", async () => {
    const u = userEvent.setup();
    renderSettings("/settings/retention");
    await setStepper(u, "明细样本保留天数", 400);
    expect((screen.getByLabelText("明细样本保留天数") as HTMLInputElement).value).toBe("90");
  });

  it("延长保留期无需确认，PATCH 只含被修改的字段", async () => {
    const u = userEvent.setup();
    renderSettings("/settings/retention");
    await setStepper(u, "明细样本保留天数", 14);
    await u.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(patches()).toHaveLength(1));
    expect(patches()[0].body).toStrictEqual({ detail_retention_days: 14 });
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("缩短保留期先确认：取消后不发请求，确认后只提交变更字段", async () => {
    const u = userEvent.setup();
    renderSettings("/settings/retention");
    await setStepper(u, "日志与记录保留天数", 30);
    await u.click(screen.getByRole("button", { name: "保存" }));

    let dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/下一轮每小时清理时被删除/)).toBeTruthy();
    expect(within(dialog).getByText(/日志与记录：90 天 → 30 天/)).toBeTruthy();
    await u.click(within(dialog).getByRole("button", { name: "取消" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(writes()).toHaveLength(0);

    await u.click(screen.getByRole("button", { name: "保存" }));
    dialog = await screen.findByRole("alertdialog");
    await u.click(within(dialog).getByRole("button", { name: "缩短并保存" }));
    await waitFor(() => expect(patches()).toHaveLength(1));
    expect(patches()[0].body).toStrictEqual({ log_retention_days: 30 });
  });

  it("还原丢弃草稿，不发请求", async () => {
    const u = userEvent.setup();
    renderSettings("/settings/retention");
    await setStepper(u, "日志与记录保留天数", 30);
    await u.click(screen.getByRole("button", { name: "还原" }));
    expect((screen.getByLabelText("日志与记录保留天数") as HTMLInputElement).value).toBe("90");
    expect(writes()).toHaveLength(0);
  });
});

// ───────────────────────── F8-2 设备默认值 ─────────────────────────

describe("设备默认值", () => {
  it("整体提交 device_defaults，并说明只影响新建的设备", async () => {
    const u = userEvent.setup();
    renderSettings("/settings/defaults");
    expect(await screen.findByText(/这些值只用于之后新建的设备/)).toBeTruthy();
    await setStepper(u, "低电量阈值（%）", 40);
    await u.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(callsTo("PATCH", "/api/v1/settings")).toHaveLength(1));
    expect(callsTo("PATCH", "/api/v1/settings")[0].body).toStrictEqual({
      device_defaults: { ...settings().device_defaults, battery_low_pct: 40 },
    });
  });

  it("恢复内置默认值只改草稿，保存前不发请求", async () => {
    const u = userEvent.setup();
    renderSettings("/settings/defaults");
    await u.click(await screen.findByRole("button", { name: "恢复内置默认值" }));
    expect(writes()).toHaveLength(0);
    expect((screen.getByLabelText("输入电压阈值（V）") as HTMLInputElement).value).toBe("50");
  });
});

// ───────────────────────── F8-3 安全 ─────────────────────────

describe("安全", () => {
  const guardSwitch = () => screen.findByRole("switch", { name: "禁止 HTTP 动作访问 UPS 数据源" });
  const patches = () => callsTo("PATCH", "/api/v1/settings");

  it("关闭护栏：取消不发请求；确认后带 confirm_disable_guard:true", async () => {
    const u = userEvent.setup();
    renderSettings("/settings/security");
    await u.click(await guardSwitch());
    let dialog = await screen.findByRole("alertdialog");
    await u.click(within(dialog).getByRole("button", { name: "保持开启" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(writes()).toHaveLength(0);
    expect((await guardSwitch()).getAttribute("aria-checked")).toBe("true");

    await u.click(await guardSwitch());
    dialog = await screen.findByRole("alertdialog");
    await u.click(within(dialog).getByRole("button", { name: "关闭护栏" }));
    await waitFor(() => expect(patches()).toHaveLength(1));
    expect(patches()[0].body).toStrictEqual({
      http_action_guard_ups_hosts: false,
      confirm_disable_guard: true,
    });
    await waitFor(async () =>
      expect((await guardSwitch()).getAttribute("aria-checked")).toBe("false"),
    );
    expect(await screen.findByText(/护栏已关闭/)).toBeTruthy();
  });

  it("开启护栏无需确认，请求体不带确认字段", async () => {
    const u = userEvent.setup();
    current = settings({ http_action_guard_ups_hosts: false });
    renderSettings("/settings/security");
    await u.click(await guardSwitch());
    await waitFor(() => expect(patches()).toHaveLength(1));
    expect(patches()[0].body).toStrictEqual({ http_action_guard_ups_hosts: true });
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("显示会话到期时间（所选时区），退出登录调用 POST /auth/logout", async () => {
    const u = userEvent.setup();
    renderSettings("/settings/security");
    // 2026-10-28T06:05:00Z = 北京时间 14:05。
    expect(await screen.findByText("10月28日 14:05 到期")).toBeTruthy();
    await u.click(screen.getByRole("button", { name: "退出登录" }));
    await waitFor(() => expect(callsTo("POST", "/api/v1/auth/logout")).toHaveLength(1));
  });
});

// ───────────────────────── F8-4 管理员 ─────────────────────────

describe("管理员", () => {
  const dialogOf = () => screen.findByRole("alertdialog");

  it("按创建先后列出管理员，自己的删除按钮禁用", async () => {
    renderSettings("/settings/account");
    const del = await screen.findByRole("button", { name: "删除 admin" });
    expect((del as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "删除 bob" }) as HTMLButtonElement).disabled).toBe(
      false,
    );
    expect(screen.getByText(/从未登录/)).toBeTruthy();
  });

  it("删除管理员：请求体带 confirm:true，成功后刷新列表", async () => {
    const u = userEvent.setup();
    handlers["DELETE /api/v1/users/2"] = () => {
      users = users.filter((x) => x.id !== 2);
      return json(200, { ok: true });
    };
    renderSettings("/settings/account");
    await u.click(await screen.findByRole("button", { name: "删除 bob" }));
    const dialog = await dialogOf();
    await u.click(within(dialog).getByRole("button", { name: "删除" }));
    await waitFor(() => expect(callsTo("DELETE", "/api/v1/users/2")).toHaveLength(1));
    expect(callsTo("DELETE", "/api/v1/users/2")[0].body).toStrictEqual({ confirm: true });
    await waitFor(() => expect(screen.queryByRole("button", { name: "删除 bob" })).toBeNull());
  });

  it("取消删除不发请求", async () => {
    const u = userEvent.setup();
    renderSettings("/settings/account");
    await u.click(await screen.findByRole("button", { name: "删除 bob" }));
    await u.click(within(await dialogOf()).getByRole("button", { name: "取消" }));
    expect(writes()).toHaveLength(0);
  });

  it("422（不能删除自己）显示对应文案，对话框保持打开", async () => {
    const u = userEvent.setup();
    handlers["DELETE /api/v1/users/2"] = () =>
      apiError(422, "validation_failed", "用户名或密码不符合要求");
    renderSettings("/settings/account");
    await u.click(await screen.findByRole("button", { name: "删除 bob" }));
    const dialog = await dialogOf();
    await u.click(within(dialog).getByRole("button", { name: "删除" }));
    expect(await within(dialog).findByText("不能删除当前登录的账户")).toBeTruthy();
    expect(within(dialog).queryByText(/用户名或密码不符合要求/)).toBeNull();
  });

  it("409（最后一位管理员）显示对应文案", async () => {
    const u = userEvent.setup();
    handlers["DELETE /api/v1/users/2"] = () => apiError(409, "user_conflict", "last admin");
    renderSettings("/settings/account");
    await u.click(await screen.findByRole("button", { name: "删除 bob" }));
    const dialog = await dialogOf();
    await u.click(within(dialog).getByRole("button", { name: "删除" }));
    expect(await within(dialog).findByText("不能删除最后一位管理员")).toBeTruthy();
  });

  it("添加管理员：用户名转小写，密码校验通过后才可提交", async () => {
    const u = userEvent.setup();
    handlers["POST /api/v1/users"] = (call) =>
      json(201, user(3, (call.body as { username: string }).username));
    renderSettings("/settings/account");
    await u.click(await screen.findByRole("button", { name: "添加管理员" }));
    const submit = await screen.findByRole("button", { name: "添加" });
    expect((submit as HTMLButtonElement).disabled).toBe(true);

    const name = screen.getByLabelText("用户名") as HTMLInputElement;
    expect(name.value).toBe("");
    await u.type(name, "Eve");
    expect(name.value).toBe("eve");
    await u.type(screen.getByLabelText("新密码"), "correct-horse");
    await u.type(screen.getByLabelText("确认密码"), "correct-horsX");
    expect((submit as HTMLButtonElement).disabled).toBe(true);

    await u.clear(screen.getByLabelText("确认密码"));
    await u.type(screen.getByLabelText("确认密码"), "correct-horse");
    expect((submit as HTMLButtonElement).disabled).toBe(false);
    await u.click(submit);
    await waitFor(() => expect(callsTo("POST", "/api/v1/users")).toHaveLength(1));
    expect(callsTo("POST", "/api/v1/users")[0].body).toStrictEqual({
      username: "eve",
      password: "correct-horse",
    });
  });

  it("改名：PATCH 只含 username；409 显示“用户名已存在”", async () => {
    const u = userEvent.setup();
    handlers["PATCH /api/v1/users/2"] = () => apiError(409, "user_conflict");
    renderSettings("/settings/account");
    await u.click(await screen.findByRole("button", { name: "修改 bob 的用户名" }));
    const name = await screen.findByLabelText("用户名");
    await u.clear(name);
    await u.type(name, "admin");
    await u.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByText("用户名已存在")).toBeTruthy();
    expect(callsTo("PATCH", "/api/v1/users/2")[0].body).toStrictEqual({ username: "admin" });
  });

  it("修改他人密码：PUT /users/{id}/password，输入框不预填", async () => {
    const u = userEvent.setup();
    handlers["PUT /api/v1/users/2/password"] = () => json(200, { ok: true });
    renderSettings("/settings/account");
    await u.click(await screen.findByRole("button", { name: "修改 bob 的密码" }));
    const password = (await screen.findByLabelText("新密码")) as HTMLInputElement;
    expect(password.value).toBe("");
    expect(screen.getByText(/所有已登录的会话会立即失效/)).toBeTruthy();
    await u.type(password, "another-secret");
    await u.type(screen.getByLabelText("确认密码"), "another-secret");
    await u.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(callsTo("PUT", "/api/v1/users/2/password")).toHaveLength(1));
    expect(callsTo("PUT", "/api/v1/users/2/password")[0].body).toStrictEqual({
      password: "another-secret",
    });
  });

  it("修改自己的密码：说明会撤销其他会话", async () => {
    const u = userEvent.setup();
    renderSettings("/settings/account");
    await u.click(await screen.findByRole("button", { name: "修改 admin 的密码" }));
    expect(await screen.findByText(/会撤销你在其他设备上的登录会话/)).toBeTruthy();
  });
});

// ───────────────────────── F8-5 审计日志 ─────────────────────────

describe("审计日志", () => {
  const sensitiveDiff = {
    "device_defaults.battery_low_pct": { before: 30, after: 40 },
    "notify.password": "已修改",
    // 即使响应意外带有明文，敏感字段名也只显示“已修改”。
    "ssh.secret": { before: "old-secret-value", after: "new-secret-value" },
  };

  beforeEach(() => {
    handlers["GET /api/v1/audit"] = (call) => {
      if (call.search.get("before") === "2") {
        return page([audit({ id: 1, kind: "logout", target: "user:1" })]);
      }
      return page(
        [
          audit({ id: 3, kind: "settings.update", target: "settings", diff: sensitiveDiff }),
          audit({
            id: 2,
            user_id: null,
            kind: "login",
            target: "user:ghost",
            result: "failure",
          }),
        ],
        2,
      );
    };
  });

  it("首页不带 before；“加载更早的记录”用 next_before 作游标，加载完后不再显示按钮", async () => {
    const u = userEvent.setup();
    renderSettings("/settings/audit");
    expect(await screen.findByText("修改系统设置")).toBeTruthy();
    const first = callsTo("GET", "/api/v1/audit")[0];
    expect(first.search.get("limit")).toBe("50");
    expect(first.search.has("before")).toBe(false);
    // 契约没有 total，界面不显示总数。
    expect(screen.queryByText(/共\s*\d+\s*条/)).toBeNull();

    await u.click(screen.getByRole("button", { name: "加载更早的记录" }));
    expect(await screen.findByText("退出登录", { selector: "button" })).toBeTruthy();
    expect(callsTo("GET", "/api/v1/audit")[1].search.get("before")).toBe("2");
    expect(await screen.findByText("没有更早的记录")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "加载更早的记录" })).toBeNull();
  });

  it("失败登录显示尝试的用户名与“失败”", async () => {
    renderSettings("/settings/audit");
    expect(await screen.findByText("用户「ghost」")).toBeTruthy();
    expect(screen.getByText("失败")).toBeTruthy();
  });

  it("展开 diff：敏感字段只显示“已修改”，不出现任何明文", async () => {
    const u = userEvent.setup();
    renderSettings("/settings/audit");
    await u.click(await screen.findByRole("button", { name: "修改系统设置，展开差异" }));
    const diff = await screen.findByRole("group", { name: "字段差异" });
    expect(within(diff).getByText("device_defaults.battery_low_pct")).toBeTruthy();
    expect(within(diff).getByText("30")).toBeTruthy();
    expect(within(diff).getByText("40")).toBeTruthy();
    expect(within(diff).getAllByText("已修改")).toHaveLength(2);
    expect(document.body.textContent).not.toContain("old-secret-value");
    expect(document.body.textContent).not.toContain("new-secret-value");
  });
});
