import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { AppProviders } from "../../App.tsx";
import { createAppServices } from "../../app/services.ts";
import type { Action } from "../../api/actions.ts";
import type { Rule, RuleTemplate } from "../../api/rules.ts";
import type { Device } from "../../api/types.ts";
import {
  FakeEventSource,
  T0,
  sessionResponse,
  settings,
  wireAction,
  wirePlan,
} from "../../test/fixtures.ts";
import { RulesPage } from "./RulesPage.tsx";

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
  location: "",
  tags: ["核心"],
  enabled: true,
  config: settings().device_defaults,
};

function eventRule(overrides: Partial<Rule> = {}): Rule {
  return {
    id: 1,
    name: "停电与恢复通知",
    enabled: true,
    dry_run: true,
    scope: { type: "all" },
    trigger: { type: "event", events: ["mains_lost"] },
    actions: [{ action_id: 10 }],
    recovery_actions: [],
    cooldown_s: 60,
    ...overrides,
  };
}

function conditionRule(): Rule {
  return {
    id: 3,
    name: "低电量关机",
    enabled: true,
    dry_run: false,
    scope: { type: "tags", tags: ["核心"] },
    trigger: {
      type: "condition",
      any_of: [{ all_of: [{ field: "battery_capacity_pct", op: "lte", value: 40, for_s: 10 }] }],
    },
    actions: [{ action_id: 11 }],
    recovery_actions: [],
    cooldown_s: 0,
  };
}

const templates: RuleTemplate[] = [
  {
    key: "mains_lost",
    suggested_action_type: "notify",
    rule: eventRule({ id: 0, name: "停电通知", enabled: false, actions: [] }),
  },
  {
    key: "shutdown",
    suggested_action_type: "plan",
    rule: {
      ...conditionRule(),
      id: 0,
      name: "电池模式过久或电量低 → 关机计划",
      enabled: false,
      dry_run: true,
      scope: { type: "all" },
      actions: [],
    },
  },
];

let rules: Rule[];
let actions: Action[];
let handlers: Record<string, Handler>;
let calls: Call[];

const callsTo = (method: string, path: string) =>
  calls.filter((c) => c.method === method && c.path === path);

beforeEach(() => {
  FakeEventSource.reset();
  calls = [];
  rules = [eventRule(), conditionRule()];
  actions = [
    wireAction({ id: 10, name: "通知值班群", type: "notify", config: { channel_ids: [] } }),
    wireAction({ id: 11, name: "启动机房关机", type: "plan", config: { plan_id: 1 } }),
  ];
  handlers = {
    "GET /api/v1/auth/session": () => json(200, sessionResponse()),
    "GET /api/v1/setup/status": () => json(200, { setup_required: false }),
    "GET /api/v1/settings": () => json(200, settings()),
    "GET /api/v1/rules": () => page(rules),
    "GET /api/v1/rules/templates": () => json(200, { items: templates }),
    "GET /api/v1/rule-logs": () => page([]),
    "GET /api/v1/actions": () => page(actions),
    "GET /api/v1/hosts": () => page([]),
    "GET /api/v1/channels": () => page([]),
    "GET /api/v1/plans": () => page([wirePlan()]),
    "GET /api/v1/devices": () => page([device]),
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
});

function CurrentLocation() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname + location.search}</output>;
}
const location = () => screen.getByTestId("location").textContent ?? "";

function renderRules(path: string) {
  const services = createAppServices({ createSource: (url) => new FakeEventSource(url) });
  render(
    <AppProviders services={services}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/rules" element={<RulesPage />} />
          <Route path="/rules/:id" element={<RulesPage />} />
        </Routes>
        <CurrentLocation />
      </MemoryRouter>
    </AppProviders>,
  );
  return services;
}

const saveResponse = (rule: Rule, warnings: string[] = []) => json(200, { ...rule, warnings });

// ───────────────────────── 列表 ─────────────────────────

describe("列表", () => {
  it("/rules 重定向到第一个规则；列表显示触发方式、范围与执行方式", async () => {
    renderRules("/rules");
    await waitFor(() => expect(location()).toBe("/rules/1"));
    const list = await screen.findByLabelText("规则");
    expect(within(list).getByText(/事件 · 所有设备/)).toBeTruthy();
    expect(within(list).getByText(/条件 · 标签 「核心」/)).toBeTruthy();
    expect(within(list).getByText("Dry-run")).toBeTruthy();
    expect(within(list).getByText("真实执行")).toBeTruthy();
  });

  it("没有规则时显示空态与模板入口", async () => {
    rules = [];
    renderRules("/rules");
    expect(await screen.findByText("还没有规则")).toBeTruthy();
  });
});

// ───────────────────────── dry-run 确认 ─────────────────────────

describe("关闭 dry-run", () => {
  it("关闭前弹确认框；取消后保持 dry-run 且不发请求", async () => {
    const user = userEvent.setup();
    renderRules("/rules/1");
    const dry = await screen.findByRole("switch", { name: "Dry-run" });
    await screen.findByText("通知值班群");
    await user.click(dry);
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/发送通知「通知值班群」/)).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "保持 dry-run" }));
    expect(dry.getAttribute("aria-checked")).toBe("true");
    expect(screen.getByRole("button", { name: "保存" }).hasAttribute("disabled")).toBe(true);
    expect(callsTo("PUT", "/api/v1/rules/1")).toHaveLength(0);
  });

  it("确认后保存只提交 dry_run:false；重新开启不需确认", async () => {
    const user = userEvent.setup();
    handlers["PUT /api/v1/rules/1"] = () => saveResponse(eventRule({ dry_run: false }));
    renderRules("/rules/1");
    const dry = await screen.findByRole("switch", { name: "Dry-run" });
    await user.click(dry);
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", { name: "真实执行" }),
    );
    expect(dry.getAttribute("aria-checked")).toBe("false");
    expect(screen.getByText(/重新开启 dry-run 不需要确认/)).toBeTruthy();

    // 重新开启再关闭：开启不弹框。
    await user.click(dry);
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(dry.getAttribute("aria-checked")).toBe("true");
    await user.click(dry);
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", { name: "真实执行" }),
    );

    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(callsTo("PUT", "/api/v1/rules/1")).toHaveLength(1));
    expect(callsTo("PUT", "/api/v1/rules/1")[0].body).toEqual({ dry_run: false });
  });

  it("条件规则同样需要确认（Q-D6）", async () => {
    const user = userEvent.setup();
    rules = [{ ...conditionRule(), dry_run: true }];
    renderRules("/rules/3");
    await user.click(await screen.findByRole("switch", { name: "Dry-run" }));
    expect(await screen.findByRole("alertdialog")).toBeTruthy();
  });
});

// ───────────────────────── 新建与编辑 ─────────────────────────

describe("新建与编辑", () => {
  it("新建事件规则：默认 dry-run，提交数值类型与显式 dry_run，创建后跳到规则", async () => {
    const user = userEvent.setup();
    handlers["POST /api/v1/rules"] = (call) =>
      json(201, { ...(call.body as object), id: 9, warnings: [] });
    renderRules("/rules/new");
    expect(
      (await screen.findByRole("switch", { name: "Dry-run" })).getAttribute("aria-checked"),
    ).toBe("true");
    await user.type(screen.getByLabelText("规则名称"), "停电通知");
    await user.click(screen.getByRole("button", { name: /市电中断/ }));
    await user.selectOptions(await screen.findByLabelText("添加命中动作"), "10");

    // 校验未通过时不提交：先清空事件。
    await user.click(screen.getByRole("button", { name: /市电中断/ }));
    await user.click(screen.getByRole("button", { name: "创建规则" }));
    expect(await screen.findByText("请至少选择 1 个事件")).toBeTruthy();
    expect(callsTo("POST", "/api/v1/rules")).toHaveLength(0);

    await user.click(screen.getByRole("button", { name: /市电中断/ }));
    await user.click(screen.getByRole("button", { name: "创建规则" }));
    await waitFor(() => expect(callsTo("POST", "/api/v1/rules")).toHaveLength(1));
    expect(callsTo("POST", "/api/v1/rules")[0].body).toEqual({
      name: "停电通知",
      enabled: true,
      dry_run: true,
      scope: { type: "all" },
      trigger: { type: "event", events: ["mains_lost"] },
      actions: [{ action_id: 10 }],
      recovery_actions: [],
      cooldown_s: 0,
    });
    await waitFor(() => expect(location()).toBe("/rules/9"));
  });

  it("修改条件值：整体提交 trigger，数值为 number；显示保存 warnings", async () => {
    const user = userEvent.setup();
    rules = [conditionRule()];
    handlers["PUT /api/v1/rules/3"] = (call) =>
      saveResponse({ ...conditionRule(), ...(call.body as object) }, [
        "runtime_estimate 可能来自估算",
      ]);
    renderRules("/rules/3");
    const value = await screen.findByLabelText("条件组 1 第 1 项取值");
    await user.clear(value);
    await user.type(value, "30");
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(callsTo("PUT", "/api/v1/rules/3")).toHaveLength(1));
    expect(callsTo("PUT", "/api/v1/rules/3")[0].body).toEqual({
      trigger: {
        type: "condition",
        any_of: [{ all_of: [{ field: "battery_capacity_pct", op: "lte", value: 30, for_s: 10 }] }],
      },
    });
    expect(await screen.findByText(/runtime_estimate 可能来自估算/)).toBeTruthy();
  });

  it("切换为布尔字段时运算符只剩等于 / 不等于", async () => {
    const user = userEvent.setup();
    rules = [conditionRule()];
    renderRules("/rules/3");
    const field = await screen.findByLabelText("条件组 1 第 1 项字段");
    await user.selectOptions(field, "low_battery");
    const op = screen.getByLabelText("条件组 1 第 1 项运算符") as HTMLSelectElement;
    expect([...op.options].map((o) => o.value)).toEqual(["eq", "ne"]);
    expect(op.value).toBe("eq");
  });

  it("保存失败（422）显示服务端原因并保留编辑", async () => {
    const user = userEvent.setup();
    handlers["PUT /api/v1/rules/1"] = () => apiError(422, "validation_failed", "配置校验失败");
    renderRules("/rules/1");
    const name = await screen.findByLabelText("规则名称");
    await user.type(name, "（新）");
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByText("未能保存")).toBeTruthy();
    expect((name as HTMLInputElement).value).toBe("停电与恢复通知（新）");
  });
});

// ───────────────────────── 删除 ─────────────────────────

describe("删除", () => {
  it("确认后提交 {confirm:true}；取消不发请求", async () => {
    const user = userEvent.setup();
    handlers["DELETE /api/v1/rules/1"] = () => json(200, { ok: true });
    renderRules("/rules/1");
    await user.click(await screen.findByRole("button", { name: "删除规则" }));
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", { name: "取消" }),
    );
    expect(callsTo("DELETE", "/api/v1/rules/1")).toHaveLength(0);

    await user.click(screen.getByRole("button", { name: "删除规则" }));
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", { name: "删除规则" }),
    );
    await waitFor(() => expect(callsTo("DELETE", "/api/v1/rules/1")).toHaveLength(1));
    expect(callsTo("DELETE", "/api/v1/rules/1")[0].body).toEqual({ confirm: true });
  });

  it("有评估日志时 409，可改为停用", async () => {
    const user = userEvent.setup();
    handlers["DELETE /api/v1/rules/1"] = () => apiError(409, "resource_conflict");
    handlers["PUT /api/v1/rules/1"] = () => saveResponse(eventRule({ enabled: false }));
    renderRules("/rules/1");
    await user.click(await screen.findByRole("button", { name: "删除规则" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "删除规则" }));
    expect(await within(dialog).findByText(/已有评估日志/)).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "改为停用" }));
    await waitFor(() => expect(callsTo("PUT", "/api/v1/rules/1")).toHaveLength(1));
    expect(callsTo("PUT", "/api/v1/rules/1")[0].body).toEqual({ enabled: false });
  });
});

// ───────────────────────── 模板 ─────────────────────────

describe("从模板创建", () => {
  it("关机模板要求显式选择 plan 动作，默认停用且 dry-run", async () => {
    const user = userEvent.setup();
    handlers["POST /api/v1/rules"] = (call) =>
      json(201, { ...(call.body as object), id: 12, warnings: [] });
    renderRules("/rules/1");
    await user.click(await screen.findByRole("button", { name: "从模板创建" }));
    const sheet = await screen.findByRole("dialog");
    await user.click(await within(sheet).findByText("电池模式过久或电量低 → 关机计划"));
    await user.click(within(sheet).getByRole("button", { name: "下一步" }));
    await waitFor(() => expect(location()).toBe("/rules/new?template=shutdown"));

    expect((await screen.findByLabelText("规则名称")) as HTMLInputElement).toHaveProperty(
      "value",
      "电池模式过久或电量低 → 关机计划",
    );
    expect(screen.getByRole("switch", { name: "启用" }).getAttribute("aria-checked")).toBe("false");
    expect(screen.getByRole("switch", { name: "Dry-run" }).getAttribute("aria-checked")).toBe(
      "true",
    );

    // 只选通知动作不满足关机模板要求。
    await user.selectOptions(await screen.findByLabelText("添加命中动作"), "10");
    await user.click(screen.getByRole("button", { name: "创建规则" }));
    expect(await screen.findByText(/关机模板需要选择一个/)).toBeTruthy();
    expect(callsTo("POST", "/api/v1/rules")).toHaveLength(0);

    await user.selectOptions(screen.getByLabelText("添加命中动作"), "11");
    await user.click(screen.getByRole("button", { name: "创建规则" }));
    await waitFor(() => expect(callsTo("POST", "/api/v1/rules")).toHaveLength(1));
    const body = callsTo("POST", "/api/v1/rules")[0].body as Record<string, unknown>;
    expect(body.enabled).toBe(false);
    expect(body.dry_run).toBe(true);
    expect(body.actions).toEqual([{ action_id: 10 }, { action_id: 11 }]);
  });
});

// ───────────────────────── 评估日志 ─────────────────────────

describe("评估日志", () => {
  it("显示全部 kind 文案、最近触发与执行记录链接", async () => {
    handlers["GET /api/v1/rule-logs"] = (call) => {
      expect(call.search.get("rule_id")).toBe("1");
      return page([
        {
          id: 5,
          rule_id: 1,
          device_id: 1,
          at: T0,
          kind: "fired",
          reason: "mains_lost",
          values: { battery_capacity_pct: { quality: "good", num: 89 } },
          action_run_ids: [311],
        },
        {
          id: 4,
          rule_id: 1,
          device_id: 1,
          at: T0,
          kind: "quality_blocked",
          reason: "battery_capacity_pct:stale:comm_stale",
          values: {},
          action_run_ids: [],
        },
        {
          id: 3,
          rule_id: 1,
          device_id: 1,
          at: T0,
          kind: "dispatch_failed",
          reason: "mains_lost;action_not_accepted",
          values: null,
          action_run_ids: null,
        },
      ]);
    };
    renderRules("/rules/1");
    const logs = await screen.findByRole("region", { name: "评估日志" });
    expect(await within(logs).findByText("已触发")).toBeTruthy();
    expect(within(logs).getByText("质量不足")).toBeTruthy();
    expect(within(logs).getByText("派发失败")).toBeTruthy();
    expect(within(logs).getByText("事件「市电中断」；动作未被接受")).toBeTruthy();
    expect(within(logs).getByRole("link", { name: "#311" }).getAttribute("href")).toBe(
      "/hosts-actions?tab=runs&open=311",
    );
    expect(await screen.findByText(/最近触发/)).toBeTruthy();
  });
});
