import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { AppProviders } from "../../App.tsx";
import { createAppServices } from "../../app/services.ts";
import type { Device } from "../../api/types.ts";
import {
  FakeEventSource,
  T0,
  sessionResponse,
  settings,
  wireDevice,
  wireEventView,
  wirePlanRun,
  wireSync,
} from "../../test/fixtures.ts";
import { OverviewPage } from "./OverviewPage.tsx";

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function device(id: number, name: string, location: string): Device {
  return {
    id,
    name,
    note: "",
    location,
    tags: [],
    enabled: true,
    config: settings().device_defaults,
  };
}

beforeEach(() => {
  FakeEventSource.reset();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      const { pathname } = new URL(raw, "http://localhost");
      if (pathname === "/api/v1/auth/session") return json(200, sessionResponse());
      if (pathname === "/api/v1/settings") return json(200, settings());
      if (pathname === "/api/v1/devices")
        return json(200, {
          items: [device(1, "机房 UPS-A", "1F 机房"), device(2, "监控室 UPS", "2F 监控室")],
          next_before: 0,
        });
      if (/^\/api\/v1\/devices\/\d+\/sources$/.test(pathname))
        return json(200, { items: [], next_before: 0 });
      return json(404, { code: "not_found", message: pathname });
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function renderPage() {
  const services = createAppServices({ createSource: (url) => new FakeEventSource(url) });
  render(
    <AppProviders services={services}>
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route index element={<OverviewPage />} />
        </Routes>
      </MemoryRouter>
    </AppProviders>,
  );
  return services;
}

/** 设备 1 停电（电池供电，输入电压无效）并有倒计时中的计划运行；设备 2 市电正常。 */
function outageSync() {
  return wireSync({
    devices: [
      wireDevice({
        device_id: 1,
        power_state: "on_battery",
        power_state_raw: "on_battery",
        on_battery_since: T0,
        outage_cycle_id: 12,
        fields: {
          battery_capacity_pct: { num: 89, quality: "good" },
          "output_load_pct[1]": { num: 27, quality: "good" },
          "input_voltage[1]": { quality: "invalid", reason: "invalid_value" },
          "output_voltage[1]": { num: 220, quality: "good" },
        },
      }),
      wireDevice({ device_id: 2 }),
    ],
    alerts: [wireEventView({ id: 5, device_id: 1, type: "mains_lost", severity: "critical" })],
    plan_runs: [wirePlanRun({ device_id: 1, plan_name: "机房关机" })],
  });
}

describe("总览（F2-1）", () => {
  it("未同步时显示加载态，已同步但没有设备时显示空态", () => {
    const services = renderPage();
    expect(screen.getByText("正在同步实时数据…")).toBeTruthy();

    act(() => services.live.dispatch({ type: "sync", data: wireSync() }));
    expect(screen.getByText("还没有运行中的设备")).toBeTruthy();
    expect(screen.getAllByRole("link", { name: "添加设备" })[0].getAttribute("href")).toBe(
      "/devices/new",
    );
  });

  it("停电主卡显示停电周期与关联的计划运行", async () => {
    const services = renderPage();
    act(() => services.live.dispatch({ type: "sync", data: outageSync() }));

    const outage = screen.getByRole("region", { name: "停电中" });
    expect(within(outage).getByText(/停电周期 #12/)).toBeTruthy();
    expect(await within(outage).findByRole("link", { name: "机房 UPS-A" })).toBeTruthy();
    expect(within(outage).getByText(/关机计划「机房关机」/)).toBeTruthy();
    expect(within(outage).getByRole("link", { name: "查看进度" }).getAttribute("href")).toBe(
      "/plans/1?tab=run",
    );
    // 关联到停电设备的运行只出现在主卡内，不再单独列出。
    expect(screen.queryByRole("region", { name: "关机计划「机房关机」" })).toBeNull();
    // 取消运行属于 F6，总览不提供。
    expect(screen.queryByRole("button", { name: /取消/ })).toBeNull();
  });

  it("设备卡中非 good 字段显示「—」而不是 0", async () => {
    const services = renderPage();
    act(() => services.live.dispatch({ type: "sync", data: outageSync() }));

    // 设备名称来自 GET /devices，等它加载后再定位设备卡。
    await screen.findAllByText("监控室 UPS");
    const card = screen
      .getAllByRole("article")
      .find((a) => within(a).queryByText("机房 UPS-A") !== null);
    expect(card).toBeTruthy();
    const inout = within(card!).getAllByText("输入 / 输出")[0].nextElementSibling;
    expect(inout?.textContent).toContain("—");
    expect(inout?.textContent).not.toMatch(/(^|\D)0 V/);
    expect(inout?.textContent).toContain("220");
  });

  it("筛选「电池」只显示电池供电的设备", async () => {
    const services = renderPage();
    act(() => services.live.dispatch({ type: "sync", data: outageSync() }));
    await screen.findAllByText("监控室 UPS");
    expect(screen.getAllByRole("article")).toHaveLength(2);

    const filter = screen.getByRole("group", { name: "设备筛选" });
    await userEvent.click(within(filter).getByRole("button", { name: /电池/ }));
    const cards = screen.getAllByRole("article");
    expect(cards).toHaveLength(1);
    expect(within(cards[0]).getByText("机房 UPS-A")).toBeTruthy();
  });

  it("活动告警行链接到告警页", async () => {
    const services = renderPage();
    act(() => services.live.dispatch({ type: "sync", data: outageSync() }));
    const section = screen.getByRole("region", { name: "活动告警" });
    const row = await within(section).findByRole("link", { name: /市电中断/ });
    expect(row.getAttribute("href")).toBe("/alerts");
  });
});
