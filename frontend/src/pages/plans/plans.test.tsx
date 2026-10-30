import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { AppProviders } from "../../App.tsx";
import { createAppServices } from "../../app/services.ts";
import type { Action } from "../../api/actions.ts";
import type { Plan } from "../../api/plans.ts";
import type { Device, WirePlanRun } from "../../api/types.ts";
import {
  FakeEventSource,
  T0,
  sessionResponse,
  settings,
  wireAction,
  wireDevice,
  wireHost,
  wirePlan,
  wirePlanRun,
  wirePlanStep,
  wireSync,
} from "../../test/fixtures.ts";
import { OverviewPage } from "../overview/OverviewPage.tsx";
import { PlansPage } from "./PlansPage.tsx";

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
const page = <T,>(items: T[]) => json(200, { items, next_before: 0 });

const device: Device = {
  id: 1,
  name: "机房 UPS-A",
  note: "",
  location: "1F 机房",
  tags: [],
  enabled: true,
  config: settings().device_defaults,
};

let plans: Plan[];
let actions: Action[];
/** GET /plan-runs 列表（按 id 倒序）；详情按 id 从中查找。 */
let planRuns: WirePlanRun[];
let handlers: Record<string, Handler>;
let calls: Call[];

const callsTo = (method: string, path: string) =>
  calls.filter((c) => c.method === method && c.path === path);

beforeEach(() => {
  FakeEventSource.reset();
  calls = [];
  plans = [wirePlan()];
  actions = [
    wireAction({ id: 1, name: "关闭 NAS" }),
    wireAction({ id: 2, name: "关闭本机", config: { host_id: 1, command: "poweroff" } }),
  ];
  planRuns = [];
  handlers = {
    "GET /api/v1/auth/session": () => json(200, sessionResponse()),
    "GET /api/v1/setup/status": () => json(200, { setup_required: false }),
    "GET /api/v1/settings": () => json(200, settings()),
    "GET /api/v1/plans": () => page(plans),
    "GET /api/v1/actions": () => page(actions),
    "GET /api/v1/hosts": () => page([wireHost()]),
    "GET /api/v1/channels": () => page([]),
    "GET /api/v1/devices": () => page([device]),
    "GET /api/v1/plan-runs": () => page(planRuns),
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
      const detail = /^\/api\/v1\/plan-runs\/(\d+)$/.exec(call.path);
      if (call.method === "GET" && detail) {
        const run = planRuns.find((r) => r.id === Number(detail[1]));
        return run ? json(200, run) : apiError(404, "not_found");
      }
      if (call.method === "GET" && /^\/api\/v1\/devices\/\d+\/sources$/.test(call.path)) {
        return page([]);
      }
      return apiError(404, "not_found", call.path);
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
const location = () => screen.getByTestId("location").textContent ?? "";

function renderPlans(path: string) {
  const services = createAppServices({ createSource: (url) => new FakeEventSource(url) });
  render(
    <AppProviders services={services}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/plans" element={<PlansPage />} />
          <Route path="/plans/:id" element={<PlansPage />} />
        </Routes>
        <CurrentLocation />
      </MemoryRouter>
    </AppProviders>,
  );
  return services;
}

function renderOverview() {
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

type Services = ReturnType<typeof createAppServices>;
const sync = (services: Services, data: ReturnType<typeof wireSync>) =>
  act(() => services.live.dispatch({ type: "sync", data }));

/** 活动运行：默认倒计时中、关联计划 #1。 */
const activeRun = (overrides: Partial<WirePlanRun> = {}) =>
  wirePlanRun({
    id: 5,
    plan_id: 1,
    plan_name: "机房关机",
    status: "countdown",
    steps: [wirePlanStep({ action_id: 1 })],
    ...overrides,
  });

// ───────────────────────── 页面骨架 ─────────────────────────

describe("页面骨架", () => {
  it("/plans 有计划时重定向到第一个计划", async () => {
    renderPlans("/plans");
    await waitFor(() => expect(location()).toBe("/plans/1"));
    expect(await screen.findByLabelText("计划名称")).toBeTruthy();
  });

  it("/plans 没有计划时显示空态与新建入口", async () => {
    plans = [];
    renderPlans("/plans");
    expect(await screen.findByText("还没有关机计划")).toBeTruthy();
    expect(location()).toBe("/plans");
  });

  it("列表显示阶段 / 步骤统计与活动运行徽标；当前运行出现在视图切换上", async () => {
    const user = userEvent.setup();
    const services = renderPlans("/plans/1");
    const list = await screen.findByLabelText("关机计划");
    expect(within(list).getByText(/2 个阶段 · 2 个步骤 · 仅手动/)).toBeTruthy();

    sync(services, wireSync({ plan_runs: [activeRun({ dry_run: true })] }));
    expect(await within(list).findByText(/Dry-run · 倒计时/)).toBeTruthy();
    const tabs = screen.getByRole("group", { name: "计划视图" });
    await user.click(within(tabs).getByRole("button", { name: "当前运行 #5" }));
    await waitFor(() => expect(location()).toBe("/plans/1?tab=run"));
  });

  it("运行记录表按计划过滤；点击运行写入 ?tab=run&run=", async () => {
    const user = userEvent.setup();
    planRuns = [wirePlanRun({ id: 3, plan_id: 1, status: "completed", dry_run: false })];
    renderPlans("/plans/1");
    await user.click(await screen.findByRole("button", { name: "#3" }));
    expect(callsTo("GET", "/api/v1/plan-runs")[0].search.get("plan_id")).toBe("1");
    await waitFor(() => expect(location()).toBe("/plans/1?tab=run&run=3"));
  });
});

// ───────────────────────── 手动执行 ─────────────────────────

describe("手动执行计划", () => {
  const runPath = "/api/v1/plans/1/run";
  const accepted = () => json(202, { accepted: true, run_id: 7 });

  async function openDialog(user: ReturnType<typeof userEvent.setup>) {
    await user.click(await screen.findByRole("button", { name: "手动执行…" }));
    const dialog = await screen.findByRole("alertdialog");
    return {
      dialog,
      input: within(dialog).getByLabelText("输入计划名称「机房关机」以确认") as HTMLInputElement,
    };
  }
  /** 真实执行模式下，分段控件与确认按钮都叫“真实执行”；取分段控件之外的那个。 */
  const realConfirm = (dialog: HTMLElement) => {
    const group = within(dialog).getByRole("group", { name: "执行方式" });
    return within(dialog)
      .getAllByRole("button", { name: "真实执行" })
      .find((b) => !group.contains(b)) as HTMLButtonElement;
  };

  beforeEach(() => {
    planRuns = [wirePlanRun({ id: 7, plan_id: 1, status: "completed", dry_run: true })];
  });

  it("名称不一致时确认按钮保持禁用，且不发请求", async () => {
    const user = userEvent.setup();
    handlers[`POST ${runPath}`] = accepted;
    renderPlans("/plans/1");
    const { dialog, input } = await openDialog(user);
    const confirm = within(dialog).getByRole("button", {
      name: "执行 dry-run",
    }) as HTMLButtonElement;
    expect(input.value).toBe("");
    expect(confirm.disabled).toBe(true);

    await user.type(input, "机房");
    expect(confirm.disabled).toBe(true);
    await user.type(input, "关机X");
    expect(confirm.disabled).toBe(true);
    await user.click(confirm);
    expect(callsTo("POST", runPath)).toHaveLength(0);
  });

  it("名称完全一致：默认 dry-run，请求体 {confirm_name, dry_run:true}，不含 device_id", async () => {
    const user = userEvent.setup();
    handlers[`POST ${runPath}`] = accepted;
    renderPlans("/plans/1");
    const { dialog, input } = await openDialog(user);
    await user.type(input, "机房关机");
    const confirm = within(dialog).getByRole("button", {
      name: "执行 dry-run",
    }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(false);
    await user.click(confirm);

    await waitFor(() => expect(callsTo("POST", runPath)).toHaveLength(1));
    const body = callsTo("POST", runPath)[0].body;
    expect(body).toStrictEqual({ confirm_name: "机房关机", dry_run: true });
    expect(body).not.toHaveProperty("device_id");
    // 202 只表示受理：对话框关闭并切到该运行的视图。
    expect(await screen.findByText("已提交 dry-run")).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    await waitFor(() => expect(location()).toBe("/plans/1?tab=run&run=7"));
  });

  it("名称前后带空格不会被 trim：按钮保持禁用", async () => {
    const user = userEvent.setup();
    handlers[`POST ${runPath}`] = accepted;
    renderPlans("/plans/1");
    const { dialog, input } = await openDialog(user);
    const confirm = within(dialog).getByRole("button", {
      name: "执行 dry-run",
    }) as HTMLButtonElement;

    await user.type(input, " 机房关机 ");
    expect(input.value).toBe(" 机房关机 ");
    expect(confirm.disabled).toBe(true);
    await user.clear(input);
    await user.type(input, "机房关机 ");
    expect(confirm.disabled).toBe(true);
    await user.clear(input);
    await user.type(input, " 机房关机");
    expect(confirm.disabled).toBe(true);
    expect(callsTo("POST", runPath)).toHaveLength(0);
  });

  it("切换到真实执行：按钮文案变为“真实执行”，请求体 dry_run:false", async () => {
    const user = userEvent.setup();
    handlers[`POST ${runPath}`] = accepted;
    renderPlans("/plans/1");
    const { dialog, input } = await openDialog(user);
    await user.type(input, "机房关机");

    const group = within(dialog).getByRole("group", { name: "执行方式" });
    await user.click(within(group).getByRole("button", { name: "真实执行" }));
    expect(within(dialog).queryByRole("button", { name: "执行 dry-run" })).toBeNull();
    const confirm = realConfirm(dialog);
    expect(confirm.disabled).toBe(false);
    // 真实执行的说明包含阶段与步骤数量。
    expect(within(dialog).getByText(/按 2 个阶段依次执行 2 个步骤/)).toBeTruthy();
    await user.click(confirm);

    await waitFor(() => expect(callsTo("POST", runPath)).toHaveLength(1));
    expect(callsTo("POST", runPath)[0].body).toStrictEqual({
      confirm_name: "机房关机",
      dry_run: false,
    });
    expect(await screen.findByText("已提交执行")).toBeTruthy();
  });

  it("关联设备后请求体带 device_id（设备需有实时快照才可选）", async () => {
    const user = userEvent.setup();
    handlers[`POST ${runPath}`] = accepted;
    const services = renderPlans("/plans/1");
    sync(services, wireSync({ devices: [wireDevice({ device_id: 1 })] }));
    const { dialog, input } = await openDialog(user);
    await user.type(input, "机房关机");
    await user.selectOptions(within(dialog).getByLabelText("关联设备（可选）"), "1");
    await user.click(within(dialog).getByRole("button", { name: "执行 dry-run" }));

    await waitFor(() => expect(callsTo("POST", runPath)).toHaveLength(1));
    expect(callsTo("POST", runPath)[0].body).toStrictEqual({
      confirm_name: "机房关机",
      dry_run: true,
      device_id: 1,
    });
  });

  it("409 plan_run_active：对话框内显示错误且不关闭，不跳转", async () => {
    const user = userEvent.setup();
    handlers[`POST ${runPath}`] = () => apiError(409, "plan_run_active");
    renderPlans("/plans/1");
    const { dialog, input } = await openDialog(user);
    await user.type(input, "机房关机");
    await user.click(within(dialog).getByRole("button", { name: "执行 dry-run" }));

    const alert = await within(dialog).findByRole("alert");
    expect(alert.textContent).toMatch(/已有倒计时或执行中的运行/);
    expect(screen.getByRole("alertdialog")).toBeTruthy();
    expect(location()).toBe("/plans/1");
    expect(screen.queryByText("已提交 dry-run")).toBeNull();
  });
});

// ───────────────────────── 取消运行 ─────────────────────────

describe("取消运行：计划页「当前运行」", () => {
  const cancelPath = "/api/v1/plan-runs/5/cancel";

  async function openCancel(user: ReturnType<typeof userEvent.setup>, run = activeRun()) {
    const services = renderPlans("/plans/1?tab=run");
    sync(services, wireSync({ plan_runs: [run] }));
    await user.click(await screen.findByRole("button", { name: "取消运行…" }));
    return screen.findByRole("alertdialog", { name: /取消「机房关机」运行 #5/ });
  }

  it("点“继续运行”不发请求；确认后 POST cancel，请求体 {confirm:true}", async () => {
    const user = userEvent.setup();
    handlers[`POST ${cancelPath}`] = () => json(202, { accepted: true });
    let dialog = await openCancel(user);

    await user.click(within(dialog).getByRole("button", { name: "继续运行" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(callsTo("POST", cancelPath)).toHaveLength(0);

    await user.click(screen.getByRole("button", { name: "取消运行…" }));
    dialog = await screen.findByRole("alertdialog");
    expect(callsTo("POST", cancelPath)).toHaveLength(0);
    await user.click(within(dialog).getByRole("button", { name: "取消运行" }));

    await waitFor(() => expect(callsTo("POST", cancelPath)).toHaveLength(1));
    expect(callsTo("POST", cancelPath)[0].body).toStrictEqual({ confirm: true });
    expect(await screen.findByText("已提交取消")).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
  });

  it("409 plan_run_finished：提示运行已结束并关闭对话框，不显示错误", async () => {
    const user = userEvent.setup();
    handlers[`POST ${cancelPath}`] = () => apiError(409, "plan_run_finished");
    const dialog = await openCancel(user);
    await user.click(within(dialog).getByRole("button", { name: "取消运行" }));

    expect(await screen.findByText("运行已结束")).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(screen.queryByRole("alert")).toBeNull();
    expect(callsTo("POST", cancelPath)).toHaveLength(1);
  });

  it("其他失败（503）：对话框保持打开并显示错误，可再次尝试", async () => {
    const user = userEvent.setup();
    handlers[`POST ${cancelPath}`] = () => apiError(503, "plan_unavailable");
    const dialog = await openCancel(user);
    await user.click(within(dialog).getByRole("button", { name: "取消运行" }));

    expect(await within(dialog).findByRole("alert")).toBeTruthy();
    expect(screen.getByRole("alertdialog")).toBeTruthy();
    expect(
      (within(dialog).getByRole("button", { name: "取消运行" }) as HTMLButtonElement).disabled,
    ).toBe(false);
  });

  it("执行中的运行入口文案为“取消未开始的步骤…”", async () => {
    const services = renderPlans("/plans/1?tab=run");
    sync(services, wireSync({ plan_runs: [activeRun({ status: "running", current_stage: 1 })] }));
    expect(await screen.findByRole("button", { name: "取消未开始的步骤…" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "取消运行…" })).toBeNull();
  });
});

describe("取消运行：总览停电主卡", () => {
  const cancelPath = "/api/v1/plan-runs/5/cancel";

  function outageSync() {
    return wireSync({
      devices: [
        wireDevice({
          device_id: 1,
          power_state: "on_battery",
          power_state_raw: "on_battery",
          on_battery_since: T0,
          outage_cycle_id: 12,
        }),
      ],
      plan_runs: [activeRun({ device_id: 1, outage_cycle_id: 12 })],
    });
  }

  async function openCancel(user: ReturnType<typeof userEvent.setup>) {
    const services = renderOverview();
    sync(services, outageSync());
    const outage = screen.getByRole("region", { name: "停电中" });
    await user.click(within(outage).getByRole("button", { name: "取消运行…" }));
    return screen.findByRole("alertdialog");
  }

  it("点“继续运行”不发请求；确认后 POST cancel，请求体 {confirm:true}", async () => {
    const user = userEvent.setup();
    handlers[`POST ${cancelPath}`] = () => json(202, { accepted: true });
    let dialog = await openCancel(user);

    await user.click(within(dialog).getByRole("button", { name: "继续运行" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(callsTo("POST", cancelPath)).toHaveLength(0);

    const outage = screen.getByRole("region", { name: "停电中" });
    await user.click(within(outage).getByRole("button", { name: "取消运行…" }));
    dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "取消运行" }));

    await waitFor(() => expect(callsTo("POST", cancelPath)).toHaveLength(1));
    expect(callsTo("POST", cancelPath)[0].body).toStrictEqual({ confirm: true });
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
  });

  it("409 plan_run_finished：关闭对话框，不报错", async () => {
    const user = userEvent.setup();
    handlers[`POST ${cancelPath}`] = () => apiError(409, "plan_run_finished");
    const dialog = await openCancel(user);
    await user.click(within(dialog).getByRole("button", { name: "取消运行" }));

    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(screen.queryByRole("alert")).toBeNull();
    expect(callsTo("POST", cancelPath)).toHaveLength(1);
  });
});

// ───────────────────────── 删除计划 ─────────────────────────

describe("删除计划", () => {
  it("取消不发 DELETE；确认后 DELETE 请求体为 {confirm:true}，随后回到列表", async () => {
    const user = userEvent.setup();
    handlers["DELETE /api/v1/plans/1"] = () => {
      plans = [];
      return json(200, { ok: true });
    };
    renderPlans("/plans/1");
    await user.click(await screen.findByRole("button", { name: "删除" }));

    let dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(callsTo("DELETE", "/api/v1/plans/1")).toHaveLength(0);

    await user.click(screen.getByRole("button", { name: "删除" }));
    dialog = await screen.findByRole("alertdialog");
    expect(callsTo("DELETE", "/api/v1/plans/1")).toHaveLength(0);
    await user.click(within(dialog).getByRole("button", { name: "删除计划" }));

    await waitFor(() => expect(callsTo("DELETE", "/api/v1/plans/1")).toHaveLength(1));
    expect(callsTo("DELETE", "/api/v1/plans/1")[0].body).toStrictEqual({ confirm: true });
    await waitFor(() => expect(location()).toBe("/plans"));
    expect(await screen.findByText("还没有关机计划")).toBeTruthy();
  });

  it("409 resource_conflict：对话框内说明原因且不关闭", async () => {
    const user = userEvent.setup();
    handlers["DELETE /api/v1/plans/1"] = () => apiError(409, "resource_conflict");
    renderPlans("/plans/1");
    await user.click(await screen.findByRole("button", { name: "删除" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "删除计划" }));

    expect(await within(dialog).findByText(/已有运行记录/)).toBeTruthy();
    expect(screen.getByRole("alertdialog")).toBeTruthy();
  });

  it("被 plan 动作引用时删除按钮禁用，点击不会打开对话框", async () => {
    const user = userEvent.setup();
    actions = [
      ...actions,
      wireAction({ id: 9, name: "启动机房关机", type: "plan", config: { plan_id: 1 } }),
    ];
    renderPlans("/plans/1");
    const button = (await screen.findByRole("button", { name: "删除" })) as HTMLButtonElement;
    await waitFor(() => expect(button.disabled).toBe(true));
    expect(button.title).toMatch(/被 plan 动作引用/);
    await user.click(button);
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(screen.getByText(/被 「启动机房关机」 动作引用/)).toBeTruthy();
  });
});

// ───────────────────────── 编辑保存 ─────────────────────────

describe("编辑与新建计划", () => {
  it("未修改时保存按钮禁用", async () => {
    renderPlans("/plans/1");
    const save = (await screen.findByRole("button", { name: "保存" })) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    expect((screen.getByRole("button", { name: "还原" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("只修改倒计时：PUT 请求体只含 countdown_s", async () => {
    const user = userEvent.setup();
    handlers["PUT /api/v1/plans/1"] = () => json(200, { ...plans[0], countdown_s: 120 });
    renderPlans("/plans/1");
    const countdown = await screen.findByLabelText("倒计时（秒）");
    await user.clear(countdown);
    await user.type(countdown, "120{Enter}");

    const save = screen.getByRole("button", { name: "保存" }) as HTMLButtonElement;
    expect(save.disabled).toBe(false);
    await user.click(save);

    await waitFor(() => expect(callsTo("PUT", "/api/v1/plans/1")).toHaveLength(1));
    expect(callsTo("PUT", "/api/v1/plans/1")[0].body).toStrictEqual({ countdown_s: 120 });
    expect(await screen.findByText("计划已保存")).toBeTruthy();
  });

  it("还原：丢弃修改后保存按钮重新禁用", async () => {
    const user = userEvent.setup();
    renderPlans("/plans/1");
    const name = await screen.findByLabelText("计划名称");
    await user.type(name, "X");
    const save = screen.getByRole("button", { name: "保存" }) as HTMLButtonElement;
    expect(save.disabled).toBe(false);
    await user.click(screen.getByRole("button", { name: "还原" }));
    expect((screen.getByLabelText("计划名称") as HTMLInputElement).value).toBe("机房关机");
    expect(save.disabled).toBe(true);
  });

  it("新建：阶段没有步骤时点保存不发请求并显示校验错误", async () => {
    const user = userEvent.setup();
    handlers["POST /api/v1/plans"] = () => json(201, { ...wirePlan(), id: 9 });
    renderPlans("/plans/new");
    await user.type(await screen.findByLabelText("计划名称"), "新计划");
    await user.click(screen.getByRole("button", { name: "创建计划" }));

    expect(await screen.findByText("每个阶段至少需要 1 个步骤")).toBeTruthy();
    expect(screen.getByText(/还有未通过校验的项目/)).toBeTruthy();
    expect(callsTo("POST", "/api/v1/plans")).toHaveLength(0);
    expect(location()).toBe("/plans/new");
  });

  it("新建：名称为空时同样不发请求", async () => {
    const user = userEvent.setup();
    renderPlans("/plans/new");
    await user.click(await screen.findByRole("button", { name: "创建计划" }));
    expect(await screen.findByText("请填写计划名称")).toBeTruthy();
    expect(callsTo("POST", "/api/v1/plans")).toHaveLength(0);
  });

  it("新建：补全步骤后 POST 完整请求体，并跳转到新计划", async () => {
    const user = userEvent.setup();
    handlers["POST /api/v1/plans"] = () => {
      const created = wirePlan({ id: 9, name: "新计划" });
      plans = [...plans, created];
      return json(201, created);
    };
    renderPlans("/plans/new");
    await user.type(await screen.findByLabelText("计划名称"), "新计划");
    await user.selectOptions(await screen.findByLabelText("为阶段 1添加步骤"), "1");
    await user.click(screen.getByRole("button", { name: "创建计划" }));

    await waitFor(() => expect(callsTo("POST", "/api/v1/plans")).toHaveLength(1));
    expect(callsTo("POST", "/api/v1/plans")[0].body).toStrictEqual({
      name: "新计划",
      countdown_s: 60,
      on_mains_restored: "cancel_pending",
      on_restart: "resume",
      notify_channel_ids: [],
      stages: [{ delay_s: 0, step_timeout_s: 30, offline_wait_s: 0, steps: [{ action_id: 1 }] }],
    });
    await waitFor(() => expect(location()).toBe("/plans/9"));
  });

  it("服务端 422 的原因显示在“未能保存”横幅中", async () => {
    const user = userEvent.setup();
    handlers["PUT /api/v1/plans/1"] = () => apiError(422, "validation_failed", "渠道已停用");
    renderPlans("/plans/1");
    const name = await screen.findByLabelText("计划名称");
    await user.type(name, "2");
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByText("未能保存")).toBeTruthy();
  });
});

// ───────────────────────── 运行视图 ─────────────────────────

describe("运行视图", () => {
  const finishedRun = () =>
    wirePlanRun({
      id: 3,
      plan_id: 1,
      plan_name: "机房关机",
      dry_run: false,
      status: "completed_with_errors",
      current_stage: 2,
      ended_at: "2026-10-27T06:25:00Z",
      steps: [
        wirePlanStep({
          stage: 1,
          step: 1,
          action_id: 1,
          status: "sent",
          started_at: T0,
          ended_at: T0,
          result: { action_run_id: 11, action_status: "sent", offline_timeout: true },
        }),
        wirePlanStep({
          stage: 1,
          step: 2,
          action_id: 2,
          status: "unknown",
          started_at: T0,
          result: { action_run_id: 12 },
        }),
        wirePlanStep({
          stage: 2,
          step: 1,
          action_id: 3,
          status: "failed",
          started_at: T0,
          ended_at: T0,
          result: { error: "ssh: connection refused" },
        }),
      ],
    });

  beforeEach(() => {
    planRuns = [finishedRun()];
    actions = [
      wireAction({ id: 1, name: "关闭 NAS" }),
      wireAction({ id: 2, name: "关闭 ESXi" }),
      wireAction({ id: 3, name: "关闭备份机" }),
    ];
  });

  it("sent 且 offline_timeout 显示“等待离线超时”；unknown 显示“未知”", async () => {
    renderPlans("/plans/1?tab=run&run=3");
    expect(await screen.findByText("等待离线超时")).toBeTruthy();
    expect(screen.getByText("未知 · 未重放")).toBeTruthy();
    expect(screen.getByText("失败")).toBeTruthy();
    // 超时的 sent 步骤不能显示成普通的“已发送”。
    expect(screen.queryByText("已发送", { selector: ".pill, .pill *" })).toBeNull();
    // 错误原因来自步骤结果，汇总提示问题步骤数。
    expect(screen.getByText("ssh: connection refused")).toBeTruthy();
    expect(screen.getByText(/3 个步骤失败、结果未知或等待离线超时/)).toBeTruthy();
  });

  it("有 action_run_id 的步骤带“执行记录 #id”链接；没有的不带", async () => {
    renderPlans("/plans/1?tab=run&run=3");
    const link11 = await screen.findByRole("link", { name: "执行记录 #11" });
    expect(link11.getAttribute("href")).toBe("/hosts-actions?tab=runs&action=1&open=11");
    const link12 = screen.getByRole("link", { name: "执行记录 #12" });
    expect(link12.getAttribute("href")).toBe("/hosts-actions?tab=runs&action=2&open=12");
    // 第三个步骤没有 action_run_id。
    expect(screen.getAllByRole("link", { name: /^执行记录 #/ })).toHaveLength(2);
  });

  it("按阶段分组显示，已结束的运行没有取消入口", async () => {
    renderPlans("/plans/1?tab=run&run=3");
    const stage1 = await screen.findByRole("region", { name: "阶段 1" });
    const stage2 = screen.getByRole("region", { name: "阶段 2" });
    expect(within(stage1).getByText("关闭 NAS")).toBeTruthy();
    expect(within(stage1).getByText("关闭 ESXi")).toBeTruthy();
    expect(within(stage2).getByText("关闭备份机")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /取消/ })).toBeNull();
  });

  it("未指定 run 且无活动运行时显示该计划最近一次运行", async () => {
    renderPlans("/plans/1?tab=run");
    expect(await screen.findByRole("region", { name: "运行 #3" })).toBeTruthy();
    const latest = callsTo("GET", "/api/v1/plan-runs").find((c) => c.search.get("limit") === "1");
    expect(latest?.search.get("plan_id")).toBe("1");
  });

  it("?run= 指向其他计划的运行时不显示其内容", async () => {
    planRuns = [{ ...finishedRun(), plan_id: 2 }];
    renderPlans("/plans/1?tab=run&run=3");
    expect(await screen.findByText("运行不属于此计划")).toBeTruthy();
    expect(screen.queryByRole("region", { name: "运行 #3" })).toBeNull();
  });

  it("活动运行由实时存储驱动；?run= 指向其他运行时提示可查看当前运行", async () => {
    const user = userEvent.setup();
    const services = renderPlans("/plans/1?tab=run&run=3");
    await screen.findByRole("region", { name: "运行 #3" });
    sync(services, wireSync({ plan_runs: [activeRun({ status: "running", current_stage: 1 })] }));

    expect(await screen.findByText(/计划当前有进行中的运行 #5/)).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "查看当前运行" }));
    expect(await screen.findByRole("region", { name: "运行 #5" })).toBeTruthy();
    await waitFor(() => expect(location()).toBe("/plans/1?tab=run"));
  });
});
