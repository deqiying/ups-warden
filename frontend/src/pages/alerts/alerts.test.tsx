import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { AppProviders } from "../../App.tsx";
import { createAppServices } from "../../app/services.ts";
import type { Device, WireEventView } from "../../api/types.ts";
import {
  FakeEventSource,
  T0,
  notifyResult,
  sessionResponse,
  settings,
  wireDevice,
  wireEventView,
  wirePlanRun,
  wireSync,
} from "../../test/fixtures.ts";
import { dayHeading, parseFilters, rangeFrom, withFilters } from "./helpers.ts";
import { AlertsPage } from "./AlertsPage.tsx";

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

/** 历史事件第一页（next_before=90 表示还有更早的事件）与第二页。 */
const PAGE_1: WireEventView[] = [
  wireEventView({
    id: 100,
    device_id: 1,
    type: "mains_lost",
    severity: "critical",
    active: true,
    summary: { battery_capacity_pct: { num: 100, quality: "good" } },
  }),
  wireEventView({
    id: 95,
    device_id: 2,
    type: "comm_lost",
    severity: "warning",
    started_at: "2026-10-27T06:00:00Z",
    cleared_at: "2026-10-27T06:01:00Z",
    active: false,
  }),
  wireEventView({
    id: 90,
    device_id: 1,
    type: "mains_restored",
    severity: "info",
    started_at: "2026-10-26T01:00:00Z",
    active: false,
  }),
];
const PAGE_2: WireEventView[] = [
  wireEventView({
    id: 80,
    device_id: 2,
    type: "overload",
    severity: "warning",
    started_at: "2026-10-25T01:00:00Z",
    cleared_at: "2026-10-25T01:10:00Z",
    active: false,
  }),
];

let routes: Record<string, (url: URL) => Response>;
/** 记录每次 GET /events 的查询参数。 */
let eventCalls: URLSearchParams[];

beforeEach(() => {
  FakeEventSource.reset();
  eventCalls = [];
  routes = {
    "/api/v1/auth/session": () => json(200, sessionResponse()),
    "/api/v1/setup/status": () => json(200, { setup_required: false }),
    "/api/v1/settings": () => json(200, settings()),
    "/api/v1/devices": () =>
      json(200, {
        items: [device(1, "机房 UPS-A", "1F 机房"), device(2, "监控室 UPS", "2F 监控室")],
        next_before: 0,
      }),
    "/api/v1/events": (url) => {
      eventCalls.push(url.searchParams);
      const paged = url.searchParams.has("before");
      return json(
        200,
        paged ? { items: PAGE_2, next_before: 0 } : { items: PAGE_1, next_before: 90 },
      );
    },
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      const url = new URL(raw, "http://localhost");
      const handler = routes[url.pathname];
      return handler ? handler(url) : json(404, { code: "not_found", message: url.pathname });
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

function renderPage(path = "/alerts") {
  const services = createAppServices({ createSource: (url) => new FakeEventSource(url) });
  render(
    <AppProviders services={services}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/alerts" element={<AlertsPage />} />
        </Routes>
        <CurrentLocation />
      </MemoryRouter>
    </AppProviders>,
  );
  return services;
}

const location = () => screen.getByTestId("location").textContent ?? "";
const lastEventCall = () => eventCalls[eventCalls.length - 1];

/** 1 个 critical（机房 UPS-A 停电，输入电压无效）+ 1 个 warning（监控室 UPS 通讯中断）。 */
function activeSync() {
  return wireSync({
    devices: [
      wireDevice({
        device_id: 1,
        power_state: "on_battery",
        power_state_raw: "on_battery",
        runtime_source: "estimated",
        fields: {
          battery_capacity_pct: { num: 89, quality: "good" },
          "output_load_pct[1]": { num: 27, quality: "good" },
          runtime_estimate: { num: 4860, quality: "good" },
          "input_voltage[1]": { quality: "invalid", reason: "invalid_value" },
        },
      }),
      wireDevice({ device_id: 2, comm_state: "lost", power_state: "online", last_ok_at: T0 }),
    ],
    alerts: [
      wireEventView({ id: 5, device_id: 1, type: "mains_lost", severity: "critical" }),
      wireEventView({ id: 4, device_id: 2, type: "comm_lost", severity: "warning" }),
    ],
  });
}

describe("活动告警", () => {
  it("主卡显示 critical，需要关注显示 warning，且没有确认按钮", async () => {
    const services = renderPage();
    act(() => services.live.dispatch({ type: "sync", data: activeSync() }));

    const hero = await screen.findByRole("region", { name: "严重告警" });
    expect(within(hero).getByText("市电中断")).toBeTruthy();
    expect(within(hero).getByText("严重")).toBeTruthy();
    const link = await within(hero).findByRole("link", { name: "机房 UPS-A" });
    expect(link.getAttribute("href")).toBe("/devices/1");
    expect(within(hero).getByText("已持续")).toBeTruthy();
    // 通知渠道链与“重试中”不在契约内（Q-C7 / Q-D4）。
    expect(screen.queryByText(/重试中/)).toBeNull();
    expect(screen.queryByText(/个渠道/)).toBeNull();

    expect(screen.getByRole("heading", { name: "需要关注" })).toBeTruthy();
    const cards = screen.getAllByRole("article");
    expect(cards).toHaveLength(1);
    const card = cards[0];
    expect(within(card).getByText("通讯中断")).toBeTruthy();
    expect(within(card).getByText("警告")).toBeTruthy();
    expect(await within(card).findByText("监控室 UPS")).toBeTruthy();
    // 通讯中断类显示最后成功采集、失联策略与保持的供电状态。
    expect(within(card).getByText("最后成功采集")).toBeTruthy();
    expect(within(card).getByText("保持（沿用最后确认的供电状态）")).toBeTruthy();
    expect(within(card).getByText("保持：市电供电")).toBeTruthy();
    expect(within(card).getByRole("link", { name: "查看设备" }).getAttribute("href")).toBe(
      "/devices/2",
    );

    expect(screen.queryByRole("button", { name: /确认|确认告警|ack/i })).toBeNull();
    expect(screen.getByText(/无需手动确认/)).toBeTruthy();
    // 页头视图切换带活动告警数。
    const switcher = screen.getByRole("group", { name: "页面视图" });
    expect(within(switcher).getByRole("button", { name: /活动告警/ }).textContent).toContain("2");
    // 活动视图不请求历史事件。
    expect(eventCalls).toHaveLength(0);
  });

  it("非 good 字段在主卡显示「—」而不是 0", async () => {
    const services = renderPage();
    act(() => services.live.dispatch({ type: "sync", data: activeSync() }));

    const hero = await screen.findByRole("region", { name: "严重告警" });
    const voltage = within(hero).getByText("输入电压").closest(".metric");
    expect(voltage?.textContent).toContain("—");
    expect(voltage?.textContent).not.toMatch(/\b0\b/);
    expect(within(hero).getByRole("button", { name: "输入电压 L1无数据" })).toBeTruthy();

    // good 字段正常显示，估算的剩余时间带「估算」标记。
    expect(within(hero).getByText("89")).toBeTruthy();
    const runtime = within(hero).getByText("剩余时间").closest(".metric");
    expect(runtime?.textContent).toContain("约 1 小时 21 分");
    expect(runtime?.textContent).toContain("估算");
  });

  it("实时断线时指标置灰", async () => {
    const services = renderPage();
    act(() => services.live.dispatch({ type: "sync", data: activeSync() }));
    const hero = await screen.findByRole("region", { name: "严重告警" });
    expect(within(hero).getByText("89").className).not.toContain("is-stale");

    act(() => services.live.setConnection({ phase: "offline", disconnectedAt: T0 }));
    expect(within(hero).getByText("89").className).toContain("is-stale");
  });

  it("没有 critical 时不显示主卡，全部进入需要关注", async () => {
    const services = renderPage();
    act(() =>
      services.live.dispatch({
        type: "sync",
        data: wireSync({
          devices: [wireDevice({ device_id: 2 })],
          alerts: [wireEventView({ id: 4, device_id: 2, type: "overload", severity: "warning" })],
        }),
      }),
    );
    expect(await screen.findByRole("heading", { name: "需要关注" })).toBeTruthy();
    expect(screen.queryByRole("region", { name: "严重告警" })).toBeNull();
    expect(screen.getAllByRole("article")).toHaveLength(1);
  });

  it("主卡下方显示该设备的关机计划倒计时并链接到运行详情", async () => {
    const services = renderPage();
    act(() =>
      services.live.dispatch({
        type: "sync",
        data: {
          ...activeSync(),
          plan_runs: [
            wirePlanRun({
              id: 7,
              plan_id: 3,
              device_id: 1,
              status: "countdown",
              countdown_until: new Date(Date.now() + 47_000).toISOString(),
            }),
          ],
        },
      }),
    );
    const hero = await screen.findByRole("region", { name: "严重告警" });
    expect(within(hero).getByText(/关机计划「机房关机」/)).toBeTruthy();
    expect(within(hero).getByText(/^\d{2}:\d{2}$/, { selector: "b" })).toBeTruthy();
    expect(within(hero).getByRole("link", { name: "查看运行" }).getAttribute("href")).toBe(
      "/plans/3?tab=run",
    );
  });

  it("已同步且无告警时显示「一切正常」", async () => {
    const services = renderPage();
    act(() =>
      services.live.dispatch({
        type: "sync",
        data: wireSync({ devices: [wireDevice({ device_id: 1 }), wireDevice({ device_id: 2 })] }),
      }),
    );
    expect(await screen.findByText("一切正常")).toBeTruthy();
    expect(screen.getByText(/2 台设备，没有活动告警/)).toBeTruthy();
    expect(screen.queryByRole("region", { name: "严重告警" })).toBeNull();

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "查看历史事件" }));
    expect(location()).toBe("/alerts?view=history");
  });

  it("尚未同步时显示加载态，而不是「一切正常」", () => {
    renderPage();
    expect(screen.getByText("正在同步实时数据…")).toBeTruthy();
    expect(screen.queryByText("一切正常")).toBeNull();
  });

  it("存在通知失败时显示警告条并链接到发送记录", async () => {
    const services = renderPage();
    act(() => {
      services.live.dispatch({ type: "sync", data: activeSync() });
      services.live.dispatch({ type: "notify_failed", data: notifyResult({ id: 11 }) });
    });
    expect(await screen.findByText("最近有 1 条通知发送失败")).toBeTruthy();
    expect(screen.getByRole("link", { name: "查看发送记录" }).getAttribute("href")).toBe(
      "/notifications?tab=logs",
    );
  });
});

describe("历史事件", () => {
  it("请求带 limit=50 与时间范围，按日分组显示，切换严重度后重新请求", async () => {
    const user = userEvent.setup();
    renderPage("/alerts?view=history");

    const table = await screen.findByRole("table");
    await within(table).findByText("监控室 UPS");
    expect(eventCalls).toHaveLength(1);
    const first = eventCalls[0];
    expect(first.get("limit")).toBe("50");
    expect(first.get("from")).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/);
    expect(first.has("before")).toBe(false);
    expect(first.has("severity")).toBe(false);

    // 按设置时区（Asia/Shanghai）分组：T0 为 14:20:58。
    expect(within(table).getByText(/10月27日 星期二/)).toBeTruthy();
    expect(within(table).getByText(/10月26日 星期一/)).toBeTruthy();
    expect(within(table).getByText("14:20:58")).toBeTruthy();
    // 活动事件显示「进行中」；有 cleared_at 的显示结束时间与持续；无 cleared_at 的非活动事件显示「—」。
    expect(within(table).getByText("进行中")).toBeTruthy();
    expect(within(table).getByText("14:01:00")).toBeTruthy();
    expect(within(table).getByText("1 分钟")).toBeTruthy();
    expect(within(table).getByText("容量 100%")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "清除筛选" })).toBeNull();

    const severity = screen.getByRole("group", { name: "严重度筛选" });
    await user.click(within(severity).getByRole("button", { name: "严重" }));
    await waitFor(() => expect(lastEventCall().get("severity")).toBe("critical"));
    expect(lastEventCall().get("limit")).toBe("50");
    expect(location()).toBe("/alerts?view=history&severity=critical");
    expect(screen.getByRole("button", { name: "清除筛选" })).toBeTruthy();
  });

  it("next_before 非 0 时「加载更早的事件」按游标翻页，翻到底后提示没有更多", async () => {
    const user = userEvent.setup();
    renderPage("/alerts?view=history&severity=critical");

    const more = await screen.findByRole("button", { name: "加载更早的事件" });
    expect(screen.queryByText("没有更多匹配的事件")).toBeNull();
    await user.click(more);

    await waitFor(() => expect(eventCalls).toHaveLength(2));
    expect(eventCalls[1].get("before")).toBe("90");
    expect(eventCalls[1].get("severity")).toBe("critical");
    expect(eventCalls[1].get("limit")).toBe("50");
    const table = screen.getByRole("table");
    expect(await within(table).findByText("过载")).toBeTruthy();
    expect(await screen.findByText("没有更多匹配的事件")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "加载更早的事件" })).toBeNull();
  });

  it("query 中的筛选条件进入请求，清除筛选后恢复默认", async () => {
    const user = userEvent.setup();
    renderPage("/alerts?view=history&device=1&type=mains_lost&severity=critical&range=all");

    await screen.findByRole("table");
    const first = eventCalls[0];
    expect(first.get("device_id")).toBe("1");
    expect(first.get("type")).toBe("mains_lost");
    expect(first.get("severity")).toBe("critical");
    // range=all 不传 from。
    expect(first.has("from")).toBe(false);

    await user.click(screen.getByRole("button", { name: "清除筛选" }));
    await waitFor(() => expect(location()).toBe("/alerts?view=history"));
    await waitFor(() => expect(lastEventCall().has("from")).toBe(true));
    const last = lastEventCall();
    expect(last.has("device_id")).toBe(false);
    expect(last.has("type")).toBe(false);
    expect(last.has("severity")).toBe(false);
    expect(screen.queryByRole("button", { name: "清除筛选" })).toBeNull();
  });

  it("加载失败时显示错误信息并可重试", async () => {
    const user = userEvent.setup();
    routes["/api/v1/events"] = () => json(400, { code: "invalid_filter", message: "bad filter" });
    renderPage("/alerts?view=history");

    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("筛选条件无效")).toBeTruthy();

    routes["/api/v1/events"] = (url) => {
      eventCalls.push(url.searchParams);
      return json(200, { items: PAGE_1, next_before: 0 });
    };
    await user.click(within(alert).getByRole("button", { name: "重试" }));
    expect(await screen.findByRole("table")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("没有匹配事件时显示空态", async () => {
    routes["/api/v1/events"] = () => json(200, { items: [], next_before: 0 });
    renderPage("/alerts?view=history&severity=info");
    expect(await screen.findByText("没有匹配的事件")).toBeTruthy();
    expect(screen.queryByRole("table")).toBeNull();
    // 空态与筛选条各有一个“清除筛选”入口。
    expect(screen.getAllByRole("button", { name: "清除筛选" })).toHaveLength(2);
  });

  it("页头切换写入 view 参数", async () => {
    const user = userEvent.setup();
    renderPage();
    const switcher = screen.getByRole("group", { name: "页面视图" });
    await user.click(within(switcher).getByRole("button", { name: "历史事件" }));
    expect(location()).toBe("/alerts?view=history");
    await user.click(within(switcher).getByRole("button", { name: /活动告警/ }));
    expect(location()).toBe("/alerts");
  });
});

describe("helpers", () => {
  it("parseFilters 忽略非法参数，withFilters 不写入默认值", () => {
    const filters = parseFilters(
      new URLSearchParams("device=abc&type=nope&severity=fatal&range=1y"),
    );
    expect(filters).toEqual({ device: null, type: null, severity: "all", range: "7d" });

    const next = withFilters(new URLSearchParams("view=history&severity=critical"), {
      severity: "all",
      device: 2,
      range: "7d",
    });
    expect(next.toString()).toBe("view=history&device=2");
  });

  it("rangeFrom 换算为 RFC3339 UTC，all 不传 from", () => {
    const now = Date.parse("2026-10-27T06:20:58Z");
    expect(rangeFrom("24h", now)).toBe("2026-10-26T06:20:58Z");
    expect(rangeFrom("7d", now)).toBe("2026-10-20T06:20:58Z");
    expect(rangeFrom("30d", now)).toBe("2026-09-27T06:20:58Z");
    expect(rangeFrom("all", now)).toBeUndefined();
  });

  it("dayHeading 为今天与昨天加前缀（按设置时区）", () => {
    const now = Date.parse("2026-10-27T06:20:58Z");
    expect(dayHeading("2026-10-27T01:00:00Z", now, "Asia/Shanghai")).toBe("今天 · 10月27日 星期二");
    expect(dayHeading("2026-10-26T01:00:00Z", now, "Asia/Shanghai")).toBe("昨天 · 10月26日 星期一");
    expect(dayHeading("2026-10-24T01:00:00Z", now, "Asia/Shanghai")).toBe("10月24日 星期六");
  });
});
