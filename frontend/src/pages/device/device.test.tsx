import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { AppProviders } from "../../App.tsx";
import { createAppServices } from "../../app/services.ts";
import type { Device, Source } from "../../api/types.ts";
import {
  FakeEventSource,
  T0,
  sessionResponse,
  settings,
  wireDevice,
  wireSync,
} from "../../test/fixtures.ts";
import { DeviceDetailPage } from "./DeviceDetailPage.tsx";

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
  tags: [],
  enabled: true,
  config: settings().device_defaults,
};

const SOURCE: Source = {
  id: 7,
  device_id: 1,
  protocol: "snmp",
  host: "127.0.0.1",
  port: 16101,
  params: { version: "2c" },
  profile_key: "rfc1628-generic",
  profile_version: 1,
  poll_ms: 3000,
  timeout_ms: 1000,
  retries: 0,
  enabled: true,
  password_set: false,
  community_set: false,
  status: { last_ok_at: T0, latency_ms: 12, failures: 0, error_code: "" },
};

interface Call {
  method: string;
  url: URL;
}

let calls: Call[];
let routes: Record<string, (url: URL, method: string) => Response>;

beforeEach(() => {
  FakeEventSource.reset();
  calls = [];
  routes = {
    "/api/v1/auth/session": () => json(200, sessionResponse()),
    "/api/v1/settings": () => json(200, settings()),
    "/api/v1/devices/1": () => json(200, DEVICE),
    "/api/v1/devices/1/sources": () => json(200, { items: [SOURCE], next_before: 0 }),
    "/api/v1/devices/1/history": () =>
      json(200, {
        resolution: "raw",
        points: [
          {
            at: new Date(Date.now() - 60_000).toISOString(),
            fields: { battery_capacity_pct: { min: 90, max: 90, avg: 90, count: 1 } },
          },
          {
            at: new Date(Date.now() - 57_000).toISOString(),
            fields: { battery_capacity_pct: { min: 89, max: 89, avg: 89, count: 1 } },
          },
        ],
      }),
    "/api/v1/events": () => json(200, { items: [], next_before: 0 }),
    "/api/v1/sources/7/raw": () =>
      json(404, { code: "diagnostic_unavailable", message: "暂无诊断数据" }),
    "/api/v1/sources/7/test": () =>
      json(200, {
        latency_ms: 35,
        device_info: {
          model: "ET1K",
          manufacturer: "",
          firmware: "",
          agent_version: "",
          identity: "",
        },
        snapshot: null,
        recommendations: null,
        raw: { protocol: "snmp", at: T0, snmp: null, modbus: null, ayi9: null, errors: null },
        trace: null,
      }),
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      const url = new URL(raw, "http://localhost");
      const method = (init?.method ?? "GET").toUpperCase();
      calls.push({ method, url });
      const handler = routes[url.pathname];
      return handler
        ? handler(url, method)
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

function renderPage(path: string) {
  const services = createAppServices({ createSource: (url) => new FakeEventSource(url) });
  render(
    <AppProviders services={services}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/devices/:id" element={<DeviceDetailPage />} />
        </Routes>
        <CurrentLocation />
      </MemoryRouter>
    </AppProviders>,
  );
  return services;
}

const location = () => screen.getByTestId("location").textContent ?? "";
const callsTo = (pathname: string) => calls.filter((c) => c.url.pathname === pathname);

function liveSync(overrides: Parameters<typeof wireDevice>[0] = {}) {
  return wireSync({
    devices: [
      wireDevice({
        device_id: 1,
        fields: {
          battery_capacity_pct: { num: 89, quality: "good" },
          "output_load_pct[1]": { num: 27, quality: "good" },
          "input_voltage[1]": { quality: "invalid", reason: "invalid_value" },
          "output_voltage[1]": { num: 220, quality: "good" },
        },
        ...overrides,
      }),
    ],
  });
}

describe("设备详情（F2-2～F2-5）", () => {
  it("设备不存在（404）时显示空态", async () => {
    renderPage("/devices/9");
    expect(await screen.findByText("找不到这台设备")).toBeTruthy();
  });

  it("实时视图：概况中非 good 字段显示「—」", async () => {
    const services = renderPage("/devices/1");
    act(() => services.live.dispatch({ type: "sync", data: liveSync() }));

    expect(await screen.findByRole("heading", { name: "机房 UPS-A" })).toBeTruthy();
    const hero = screen.getByRole("region", { name: "设备概况" });
    const input = within(hero).getByText("输入").closest(".metric");
    expect(input?.textContent).toContain("—");
    expect(input?.textContent).not.toMatch(/(^|\D)0(\D|$)/);
    expect(within(hero).getByText("89")).toBeTruthy();
  });

  it("同步后没有该设备的运行快照时显示提示", async () => {
    const services = renderPage("/devices/1");
    act(() => services.live.dispatch({ type: "sync", data: wireSync() }));
    expect(await screen.findByText("暂无运行快照")).toBeTruthy();
    expect(screen.getByText("没有实时数据")).toBeTruthy();
  });

  it("通讯中断横幅的「测试连接」切换到数据源视图", async () => {
    const services = renderPage("/devices/1");
    act(() => services.live.dispatch({ type: "sync", data: liveSync({ comm_state: "lost" }) }));
    // 横幅说明最后成功采集时间，并声明下方数值为旧值。
    expect(await screen.findByText(/最后成功采集于.*已置灰/)).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "测试连接" }));
    expect(location()).toBe("/devices/1?tab=sources");
  });

  it("历史视图：按契约请求默认字段与 1 小时范围，停电事件分别查询", async () => {
    const services = renderPage("/devices/1?tab=history");
    act(() => services.live.dispatch({ type: "sync", data: liveSync() }));

    await waitFor(() => expect(callsTo("/api/v1/devices/1/history")).toHaveLength(1));
    const q = callsTo("/api/v1/devices/1/history")[0].url.searchParams;
    expect(q.get("fields")).toBe("battery_capacity_pct,output_load_pct[1]");
    expect(q.get("resolution")).toBe("auto");
    expect(q.get("max_points")).toBe("1000");
    const span = Date.parse(q.get("to")!) - Date.parse(q.get("from")!);
    expect(span).toBe(3600_000);

    await waitFor(() => expect(callsTo("/api/v1/events")).toHaveLength(2));
    const events = callsTo("/api/v1/events").map((c) => c.url.searchParams);
    const lost = events.find((p) => p.get("type") === "mains_lost")!;
    const restored = events.find((p) => p.get("type") === "mains_restored")!;
    // mains_lost 可能早于 from 开始，因此只限制 to。
    expect(lost.has("from")).toBe(false);
    expect(lost.get("to")).toBe(q.get("to"));
    expect(restored.get("from")).toBe(q.get("from"));
    expect(lost.get("device_id")).toBe("1");

    expect(await screen.findByText("历史曲线")).toBeTruthy();
    expect(screen.queryByText("该时间范围内没有历史数据")).toBeNull();
  });

  it("切换离开历史视图时清除 range 与 fields 参数", async () => {
    const services = renderPage("/devices/1?tab=history&range=6h&fields=battery_capacity_pct");
    act(() => services.live.dispatch({ type: "sync", data: liveSync() }));
    await waitFor(() => expect(callsTo("/api/v1/devices/1/history")).toHaveLength(1));
    const q = callsTo("/api/v1/devices/1/history")[0].url.searchParams;
    expect(q.get("fields")).toBe("battery_capacity_pct");
    expect(Date.parse(q.get("to")!) - Date.parse(q.get("from")!)).toBe(6 * 3600_000);

    const tabs = screen.getByRole("group", { name: "设备视图" });
    await userEvent.click(within(tabs).getByRole("button", { name: "事件" }));
    expect(location()).toBe("/devices/1?tab=events");
    expect(await screen.findByText("该设备还没有事件")).toBeTruthy();
  });

  it("数据源视图：/raw 404 显示空态，SNMP 未设 community 有安全提示，连接测试为只读 POST", async () => {
    const services = renderPage("/devices/1?tab=sources");
    act(() => services.live.dispatch({ type: "sync", data: liveSync() }));

    expect(await screen.findByText("暂无诊断数据")).toBeTruthy();
    expect(screen.queryByText("诊断数据加载失败")).toBeNull();
    expect(screen.getByText(/安全提示：未设置 Community/)).toBeTruthy();
    expect(screen.getByText("127.0.0.1:16101")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "只读连接测试" }));
    expect(await screen.findByText("连接测试成功")).toBeTruthy();
    const test = callsTo("/api/v1/sources/7/test");
    expect(test).toHaveLength(1);
    expect(test[0].method).toBe("POST");
  });

  it("连接测试失败时显示错误信息", async () => {
    routes["/api/v1/sources/7/test"] = () =>
      json(409, { code: "source_busy", message: "数据源正忙，请稍后重试" });
    const services = renderPage("/devices/1?tab=sources");
    act(() => services.live.dispatch({ type: "sync", data: liveSync() }));

    await userEvent.click(await screen.findByRole("button", { name: "只读连接测试" }));
    expect(await screen.findByText("连接测试未成功")).toBeTruthy();
  });
});
