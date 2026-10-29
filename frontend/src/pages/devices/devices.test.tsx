import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { AppProviders } from "../../App.tsx";
import { createAppServices } from "../../app/services.ts";
import type { Device, ProfileSummary, Source } from "../../api/types.ts";
import { FakeEventSource, T0, sessionResponse, settings } from "../../test/fixtures.ts";
import { DevicesPage } from "./DevicesPage.tsx";

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const DEVICE: Device = {
  id: 1,
  name: "机房 UPS-A",
  note: "",
  location: "1F 机房",
  tags: ["核心"],
  enabled: true,
  config: settings().device_defaults,
};

const SOURCE: Source = {
  id: 7,
  device_id: 1,
  protocol: "ayi9-json",
  host: "127.0.0.1",
  port: 23900,
  params: {},
  profile_key: "ayi9-json-generic",
  profile_version: 1,
  poll_ms: 3000,
  timeout_ms: 1000,
  retries: 0,
  enabled: true,
  password_set: true,
  community_set: false,
  status: { last_ok_at: T0, latency_ms: 12, failures: 0, error_code: "" },
};

const PROFILES: ProfileSummary[] = [
  { key: "ayi9-json-generic", name: "AYI9", protocol: "ayi9-json", version: 1, builtin: true },
  { key: "et1k-modbus", name: "ET1K Modbus", protocol: "modbus", version: 1, builtin: true },
  { key: "rfc1628-generic", name: "RFC 1628", protocol: "snmp", version: 1, builtin: true },
];

interface Call {
  method: string;
  path: string;
  body: unknown;
}

let calls: Call[];
let routes: Record<string, (method: string, body: unknown) => Response>;

beforeEach(() => {
  FakeEventSource.reset();
  calls = [];
  routes = {
    "/api/v1/auth/session": () => json(200, sessionResponse()),
    "/api/v1/settings": () => json(200, settings()),
    "/api/v1/devices": () => json(200, { items: [DEVICE], next_before: 0 }),
    "/api/v1/devices/1/sources": () => json(200, { items: [SOURCE], next_before: 0 }),
    "/api/v1/profiles": () => json(200, { items: PROFILES }),
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      const url = new URL(raw, "http://localhost");
      const method = (init?.method ?? "GET").toUpperCase();
      const body = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
      calls.push({ method, path: url.pathname, body });
      const handler = routes[url.pathname];
      return handler
        ? handler(method, body)
        : json(404, { code: "not_found", message: url.pathname });
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function CurrentLocation() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname + location.search}</output>;
}

function renderPage(path = "/devices") {
  const services = createAppServices({ createSource: (url) => new FakeEventSource(url) });
  render(
    <AppProviders services={services}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/devices" element={<DevicesPage />} />
          <Route path="/devices/new" element={<DevicesPage />} />
          <Route path="/devices/:id" element={<p>详情页</p>} />
        </Routes>
        <CurrentLocation />
      </MemoryRouter>
    </AppProviders>,
  );
  return services;
}

const writes = (method: string, path: string) =>
  calls.filter((c) => c.method === method && c.path === path);

describe("设备列表（F3-1）", () => {
  it("显示数据源摘要并通过 PUT /enabled 停用", async () => {
    let enabled = true;
    routes["/api/v1/devices/1/enabled"] = (_m, body) => {
      enabled = (body as { enabled: boolean }).enabled;
      return json(200, { ...DEVICE, enabled });
    };
    routes["/api/v1/devices"] = () =>
      json(200, { items: [{ ...DEVICE, enabled }], next_before: 0 });
    const user = userEvent.setup();
    renderPage();

    expect(await screen.findByText("ayi9-json-generic v1")).toBeTruthy();
    expect(screen.getByText(/AYI9 JSON/)).toBeTruthy();
    await user.click(screen.getByRole("switch", { name: "启用「机房 UPS-A」" }));
    await waitFor(() => expect(writes("PUT", "/api/v1/devices/1/enabled")).toHaveLength(1));
    expect(writes("PUT", "/api/v1/devices/1/enabled")[0].body).toEqual({ enabled: false });
    expect(await screen.findByText("已停用", { selector: ".pill" })).toBeTruthy();
  });

  it("删除：取消不发请求，确认后提交 confirm:true；409 时可改为停用", async () => {
    routes["/api/v1/devices/1"] = () =>
      json(409, { code: "resource_conflict", message: "资源被引用" });
    routes["/api/v1/devices/1/enabled"] = () => json(200, { ...DEVICE, enabled: false });
    const user = userEvent.setup();
    renderPage();

    const more = await screen.findByRole("button", { name: "「机房 UPS-A」的更多操作" });
    await user.click(more);
    await user.click(screen.getByRole("menuitem", { name: /删除/ }));
    const dialog = screen.getByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(writes("DELETE", "/api/v1/devices/1")).toHaveLength(0);

    await user.click(more);
    await user.click(screen.getByRole("menuitem", { name: /删除/ }));
    await user.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "删除" }));
    await waitFor(() => expect(writes("DELETE", "/api/v1/devices/1")).toHaveLength(1));
    expect(writes("DELETE", "/api/v1/devices/1")[0].body).toEqual({ confirm: true });

    await user.click(await screen.findByRole("button", { name: "改为停用" }));
    await waitFor(() => expect(writes("PUT", "/api/v1/devices/1/enabled")).toHaveLength(1));
    expect(writes("PUT", "/api/v1/devices/1/enabled")[0].body).toEqual({ enabled: false });
  });
});

describe("添加设备向导（F3-2）", () => {
  it("AYI9 密码错误时显示“设备密码错误（103）”", async () => {
    routes["/api/v1/sources/test"] = () =>
      json(502, { code: "source_password_wrong", message: "设备密码错误（103）" });
    const user = userEvent.setup();
    renderPage("/devices/new");

    await user.type(await screen.findByLabelText("名称"), "门卫室 UPS");
    await user.click(screen.getByRole("radio", { name: /AYI9 云监控卡 JSON API/ }));
    await user.click(screen.getByRole("button", { name: "下一步" }));
    await user.type(await screen.findByLabelText("主机"), "127.0.0.1");
    await user.type(screen.getByLabelText("密码"), "wrong");
    await user.click(screen.getByRole("button", { name: "测试连接" }));

    expect(await screen.findByText("设备密码错误（103）")).toBeTruthy();
    const body = writes("POST", "/api/v1/sources/test")[0].body as Record<string, unknown>;
    expect(body).toMatchObject({
      protocol: "ayi9-json",
      host: "127.0.0.1",
      port: 2390,
      password: "wrong",
      profile_key: "ayi9-json-generic",
    });
    expect(body).not.toHaveProperty("poll_ms");
    expect(screen.getByRole("button", { name: "下一步" })).toHaveProperty("disabled", true);
  });

  it("数据源保存失败时保留已创建的设备，可重试，不自动删除（Q-C2）", async () => {
    routes["/api/v1/sources/test"] = () =>
      json(200, {
        latency_ms: 21,
        device_info: {
          model: "ET1K",
          manufacturer: "",
          firmware: "",
          agent_version: "",
          identity: "",
        },
        snapshot: {
          fields: {
            battery_capacity_pct: { num: 100, quality: "good" },
            runtime_estimate: { quality: "invalid", reason: "placeholder" },
          },
        },
        recommendations: [{ key: "rfc1628-generic", score: 10, reason: "协议通用 Profile" }],
        raw: { protocol: "snmp", at: T0, snmp: null, modbus: null, ayi9: null, errors: null },
        trace: null,
      });
    routes["/api/v1/devices"] = (method) =>
      method === "POST"
        ? json(201, { ...DEVICE, id: 5, name: "门卫室 UPS", tags: [] })
        : json(200, { items: [DEVICE], next_before: 0 });
    let sourceAttempts = 0;
    routes["/api/v1/devices/5/sources"] = () => {
      sourceAttempts += 1;
      return sourceAttempts === 1
        ? json(422, { code: "validation_failed", message: "配置校验失败" })
        : json(201, { ...SOURCE, id: 8, device_id: 5, protocol: "snmp" });
    };
    const user = userEvent.setup();
    renderPage("/devices/new");

    await user.type(await screen.findByLabelText("名称"), "门卫室 UPS");
    await user.click(screen.getByRole("button", { name: "下一步" }));
    await user.type(await screen.findByLabelText("主机"), "127.0.0.1");
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    // 预览中的非 good 字段显示「—」，不显示 0。
    const preview = (await screen.findByText("剩余时间")).closest(".metric");
    expect(preview?.textContent).toContain("—");
    await user.click(screen.getByRole("button", { name: "使用 rfc1628-generic" }));
    const dialog = await screen.findByRole("dialog");
    await user.click(await within(dialog).findByRole("button", { name: "添加设备" }));

    expect(await screen.findByText("设备「门卫室 UPS」已创建，但数据源保存失败")).toBeTruthy();
    expect(writes("POST", "/api/v1/devices")[0].body).toMatchObject({
      name: "门卫室 UPS",
      config: settings().device_defaults,
    });
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);

    await user.click(screen.getByRole("button", { name: "重试保存数据源" }));
    await waitFor(() => expect(screen.getByTestId("location").textContent).toBe("/devices/5"));
    expect(writes("POST", "/api/v1/devices")).toHaveLength(1);
    expect(writes("POST", "/api/v1/devices/5/sources")).toHaveLength(2);
  });
});

describe("编辑设备（F3-3）", () => {
  it("凭据只写：不预填，新值只在填写时提交，清除走 clear_secrets", async () => {
    routes["/api/v1/sources/7"] = (_m, body) => json(200, { ...SOURCE, ...(body as object) });
    const user = userEvent.setup();
    renderPage("/devices?edit=1");

    await user.click(await screen.findByRole("button", { name: "数据源" }));
    const password = (await screen.findByLabelText("密码")) as HTMLInputElement;
    expect(password.value).toBe("");
    expect(screen.getByText(/已设置 · 留空表示保留原值/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "保存" })).toHaveProperty("disabled", true);

    await user.click(screen.getByLabelText("清除已保存的密码"));
    expect(password.disabled).toBe(true);
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(writes("PATCH", "/api/v1/sources/7")).toHaveLength(1));
    expect(writes("PATCH", "/api/v1/sources/7")[0].body).toEqual({ clear_secrets: ["password"] });
    expect(writes("PATCH", "/api/v1/devices/1")).toHaveLength(0);
  });
});
