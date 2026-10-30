import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { AppProviders } from "../../App.tsx";
import { createAppServices } from "../../app/services.ts";
import type { Action } from "../../api/actions.ts";
import type { Host } from "../../api/hosts.ts";
import {
  FakeEventSource,
  sessionResponse,
  settings,
  wireAction,
  wireActionRun,
  wireHost,
  wirePlan,
} from "../../test/fixtures.ts";
import { HostsActionsPage } from "./HostsActionsPage.tsx";

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

const passwordHost: Host = wireHost();
const keyHost: Host = wireHost({
  id: 2,
  name: "ESXi-01",
  address: "esxi-01.lan",
  kind: "esxi",
  conn: { port: 22, username: "root", auth_method: "private_key", sudo: false },
  hostkey_fingerprint: "SHA256:confirmed-esxi",
  password_set: false,
  private_key_set: true,
  passphrase_set: true,
});
const sshAction: Action = wireAction();
const httpAction: Action = wireAction({
  id: 2,
  name: "通知平台",
  type: "http",
  config: { method: "POST", expected_status: [200] },
  url_set: true,
  headers_set: true,
});
const planAction: Action = wireAction({
  id: 3,
  name: "启动机房关机",
  type: "plan",
  config: { plan_id: 1 },
});

let handlers: Record<string, Handler>;
let calls: Call[];

const callsTo = (method: string, path: string) =>
  calls.filter((c) => c.method === method && c.path === path);

beforeEach(() => {
  FakeEventSource.reset();
  calls = [];
  handlers = {
    "GET /api/v1/auth/session": () => json(200, sessionResponse()),
    "GET /api/v1/setup/status": () => json(200, { setup_required: false }),
    "GET /api/v1/settings": () => json(200, settings()),
    "GET /api/v1/hosts": () => page([passwordHost, keyHost]),
    "GET /api/v1/actions": () => page([sshAction, httpAction, planAction]),
    "GET /api/v1/plans": () => page([wirePlan()]),
    "GET /api/v1/channels": () => page([]),
    "GET /api/v1/devices": () => page([]),
    "GET /api/v1/scripts": () => json(200, { items: [] }),
    "GET /api/v1/action-runs": () => page([wireActionRun()]),
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
      return handler ? handler(call) : apiError(404, "not_found", call.path);
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

function renderPage(path: string) {
  const services = createAppServices({ createSource: (url) => new FakeEventSource(url) });
  render(
    <AppProviders services={services}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/hosts-actions" element={<HostsActionsPage />} />
        </Routes>
        <CurrentLocation />
      </MemoryRouter>
    </AppProviders>,
  );
  return services;
}

// ───────────────────────── 页面骨架 ─────────────────────────

describe("页面骨架", () => {
  it("默认显示受控主机；切换视图写入 ?tab=", async () => {
    const user = userEvent.setup();
    renderPage("/hosts-actions");
    expect(await screen.findByText("NAS-01")).toBeTruthy();

    const tabs = screen.getByRole("group", { name: "主机与动作视图" });
    await user.click(within(tabs).getByRole("button", { name: "动作" }));
    await waitFor(() => expect(location()).toBe("/hosts-actions?tab=actions"));
    expect(await screen.findByText("关闭 NAS")).toBeTruthy();

    await user.click(within(tabs).getByRole("button", { name: "执行记录" }));
    await waitFor(() => expect(location()).toBe("/hosts-actions?tab=runs"));
  });

  it("主机列表区分指纹已确认 / 未确认，未确认主机提供“获取指纹”入口", async () => {
    renderPage("/hosts-actions?tab=hosts");
    await screen.findByText("NAS-01");
    expect(screen.getByText("指纹未确认")).toBeTruthy();
    expect(screen.getByText("指纹已确认")).toBeTruthy();
    expect(screen.getAllByRole("button", { name: "获取指纹" })).toHaveLength(1);
  });

  it("plan 动作没有手动执行入口，只提供前往计划；?run= 指向 plan 动作时不打开执行浮层", async () => {
    renderPage("/hosts-actions?tab=actions&run=3");
    await screen.findByText("启动机房关机");
    expect(screen.getByRole("link", { name: "前往计划" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "运行动作「启动机房关机」" })).toBeNull();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.getByRole("button", { name: "运行动作「关闭 NAS」" })).toBeTruthy();
  });
});

// ───────────────────────── 手动执行动作 ─────────────────────────

describe("手动执行动作", () => {
  const runPath = "/api/v1/actions/1/run";
  const accepted = () => json(202, { accepted: true, run_id: 7 });

  it("默认 dry-run：请求体为 {confirm:true, dry_run:true}，不弹二次确认", async () => {
    const user = userEvent.setup();
    handlers[`POST ${runPath}`] = accepted;
    renderPage("/hosts-actions?tab=actions&run=1");

    const submit = await screen.findByRole("button", { name: "运行 dry-run" });
    expect(screen.queryByRole("button", { name: "真实执行…" })).toBeNull();
    await user.click(submit);

    await waitFor(() => expect(callsTo("POST", runPath)).toHaveLength(1));
    expect(callsTo("POST", runPath)[0].body).toStrictEqual({ confirm: true, dry_run: true });
    expect(screen.queryByRole("alertdialog")).toBeNull();
    // 202 只表示已受理：提示“受理不代表成功”，并关闭浮层。
    expect(await screen.findByText("已提交 dry-run")).toBeTruthy();
    await waitFor(() => expect(location()).not.toContain("run=1"));
  });

  it("切到真实执行需二次确认：取消不发请求，确认后请求体 dry_run:false", async () => {
    const user = userEvent.setup();
    handlers[`POST ${runPath}`] = accepted;
    renderPage("/hosts-actions?tab=actions&run=1");
    await screen.findByRole("button", { name: "运行 dry-run" });

    const mode = screen.getByRole("group", { name: "运行方式" });
    await user.click(within(mode).getByRole("button", { name: "真实执行" }));
    // 真实执行模式下 dry-run 按钮被替换，点按只打开确认框，不发请求。
    expect(screen.queryByRole("button", { name: "运行 dry-run" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "真实执行…" }));

    let dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/命令：poweroff/)).toBeTruthy();
    expect(callsTo("POST", runPath)).toHaveLength(0);
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(callsTo("POST", runPath)).toHaveLength(0);

    await user.click(screen.getByRole("button", { name: "真实执行…" }));
    dialog = await screen.findByRole("alertdialog");
    expect(callsTo("POST", runPath)).toHaveLength(0);
    await user.click(within(dialog).getByRole("button", { name: "真实执行" }));

    await waitFor(() => expect(callsTo("POST", runPath)).toHaveLength(1));
    expect(callsTo("POST", runPath)[0].body).toStrictEqual({ confirm: true, dry_run: false });
    expect(await screen.findByText("已提交执行")).toBeTruthy();
  });

  it("切回 dry-run 后再提交仍是 dry_run:true（模式不会残留真实执行）", async () => {
    const user = userEvent.setup();
    handlers[`POST ${runPath}`] = accepted;
    renderPage("/hosts-actions?tab=actions&run=1");
    await screen.findByRole("button", { name: "运行 dry-run" });

    const mode = screen.getByRole("group", { name: "运行方式" });
    await user.click(within(mode).getByRole("button", { name: "真实执行" }));
    await user.click(within(mode).getByRole("button", { name: "Dry-run 预演" }));
    await user.click(await screen.findByRole("button", { name: "运行 dry-run" }));

    await waitFor(() => expect(callsTo("POST", runPath)).toHaveLength(1));
    expect(callsTo("POST", runPath)[0].body).toStrictEqual({ confirm: true, dry_run: true });
  });

  it("被拒绝时留在浮层并显示失败原因，不显示“已提交”", async () => {
    const user = userEvent.setup();
    handlers[`POST ${runPath}`] = () => apiError(503, "action_unavailable");
    renderPage("/hosts-actions?tab=actions&run=1");
    await user.click(await screen.findByRole("button", { name: "运行 dry-run" }));

    expect(await screen.findByText("未能提交")).toBeTruthy();
    expect(screen.queryByText("已提交 dry-run")).toBeNull();
    expect(location()).toContain("run=1");
  });

  it("SSH 动作所属主机指纹未确认时提示真实执行会被拒绝", async () => {
    renderPage("/hosts-actions?tab=actions&run=1");
    expect(await screen.findByText("主机指纹未确认")).toBeTruthy();
  });
});

// ───────────────────────── 删除 ─────────────────────────

describe("删除主机", () => {
  it("取消不发 DELETE；确认后 DELETE 请求体为 {confirm:true}", async () => {
    const user = userEvent.setup();
    handlers["DELETE /api/v1/hosts/1"] = () => json(200, { ok: true });
    renderPage("/hosts-actions?tab=hosts&host=1");
    await user.click(await screen.findByRole("button", { name: "删除主机" }));

    let dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(callsTo("DELETE", "/api/v1/hosts/1")).toHaveLength(0);

    await user.click(screen.getByRole("button", { name: "删除主机" }));
    dialog = await screen.findByRole("alertdialog");
    expect(callsTo("DELETE", "/api/v1/hosts/1")).toHaveLength(0);
    await user.click(within(dialog).getByRole("button", { name: "删除主机" }));

    await waitFor(() => expect(callsTo("DELETE", "/api/v1/hosts/1")).toHaveLength(1));
    expect(callsTo("DELETE", "/api/v1/hosts/1")[0].body).toStrictEqual({ confirm: true });
    await waitFor(() => expect(location()).not.toContain("host=1"));
  });

  it("被动作引用返回 409 时提示原因，对话框不关闭", async () => {
    const user = userEvent.setup();
    handlers["DELETE /api/v1/hosts/1"] = () => apiError(409, "resource_conflict");
    renderPage("/hosts-actions?tab=hosts&host=1");
    await user.click(await screen.findByRole("button", { name: "删除主机" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "删除主机" }));

    expect(await within(dialog).findByText(/主机被 SSH 动作引用/)).toBeTruthy();
    expect(screen.getByRole("alertdialog")).toBeTruthy();
    expect(location()).toContain("host=1");
  });
});

describe("删除动作", () => {
  it("取消不发 DELETE；确认后 DELETE 请求体为 {confirm:true}", async () => {
    const user = userEvent.setup();
    handlers["DELETE /api/v1/actions/1"] = () => json(200, { ok: true });
    renderPage("/hosts-actions?tab=actions&edit=1");
    await user.click(await screen.findByRole("button", { name: "删除动作" }));

    let dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(callsTo("DELETE", "/api/v1/actions/1")).toHaveLength(0);

    await user.click(screen.getByRole("button", { name: "删除动作" }));
    dialog = await screen.findByRole("alertdialog");
    expect(callsTo("DELETE", "/api/v1/actions/1")).toHaveLength(0);
    await user.click(within(dialog).getByRole("button", { name: "删除动作" }));

    await waitFor(() => expect(callsTo("DELETE", "/api/v1/actions/1")).toHaveLength(1));
    expect(callsTo("DELETE", "/api/v1/actions/1")[0].body).toStrictEqual({ confirm: true });
    await waitFor(() => expect(location()).not.toContain("edit=1"));
  });
});

// ───────────────────────── 凭据只写 ─────────────────────────

describe("编辑主机：凭据只写", () => {
  it("密码主机：密码输入框不预填，显示“已设置”，未修改时保存按钮禁用", async () => {
    renderPage("/hosts-actions?tab=hosts&host=1");
    const password = (await screen.findByLabelText("密码")) as HTMLInputElement;
    expect(password.value).toBe("");
    expect(password.type).toBe("password");
    expect(password.placeholder).toBe("留空保留当前值");
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getAllByText("已设置")).toHaveLength(1);
    // 必需凭据（密码）没有“清除”入口。
    expect(screen.queryByRole("button", { name: "清除密码" })).toBeNull();
    expect((screen.getByRole("button", { name: "保存" }) as HTMLButtonElement).disabled).toBe(true);
    // 页面任何位置都不出现凭据值。
    expect(document.body.textContent).not.toMatch(/password"|private_key"/);
  });

  it("私钥主机：私钥与口令输入框均为空，各显示“已设置”", async () => {
    renderPage("/hosts-actions?tab=hosts&host=2");
    const key = (await screen.findByLabelText("私钥")) as HTMLTextAreaElement;
    const passphrase = screen.getByLabelText("私钥口令") as HTMLInputElement;
    expect(key.value).toBe("");
    expect(passphrase.value).toBe("");
    expect(within(screen.getByRole("dialog")).getAllByText("已设置")).toHaveLength(2);
    // 只有口令可以清除。
    expect(screen.getByRole("button", { name: "清除私钥口令" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "清除私钥" })).toBeNull();
  });

  it("只改名称：PUT 只含 name，不含任何凭据字段", async () => {
    const user = userEvent.setup();
    handlers["PUT /api/v1/hosts/1"] = () => json(200, { ...passwordHost, name: "NAS-02" });
    renderPage("/hosts-actions?tab=hosts&host=1");
    const name = await screen.findByLabelText("名称");
    await user.clear(name);
    await user.type(name, "NAS-02");
    await user.click(screen.getByRole("button", { name: "保存" }));

    await waitFor(() => expect(callsTo("PUT", "/api/v1/hosts/1")).toHaveLength(1));
    expect(callsTo("PUT", "/api/v1/hosts/1")[0].body).toStrictEqual({ name: "NAS-02" });
    expect(await screen.findByText("主机已保存")).toBeTruthy();
  });

  it('清除私钥口令：PUT 只含 clear_secrets:["passphrase"]', async () => {
    const user = userEvent.setup();
    handlers["PUT /api/v1/hosts/2"] = () => json(200, { ...keyHost, passphrase_set: false });
    renderPage("/hosts-actions?tab=hosts&host=2");
    await user.click(await screen.findByRole("button", { name: "清除私钥口令" }));
    await user.click(screen.getByRole("button", { name: "保存" }));

    await waitFor(() => expect(callsTo("PUT", "/api/v1/hosts/2")).toHaveLength(1));
    expect(callsTo("PUT", "/api/v1/hosts/2")[0].body).toStrictEqual({
      clear_secrets: ["passphrase"],
    });
  });

  it("修改已确认主机的地址时提示指纹将被清除", async () => {
    const user = userEvent.setup();
    renderPage("/hosts-actions?tab=hosts&host=2");
    const address = await screen.findByLabelText("地址");
    expect(screen.queryByText(/已确认的指纹会被清除/)).toBeNull();
    await user.type(address, "x");
    expect(await screen.findByText(/已确认的指纹会被清除/)).toBeTruthy();
  });
});

describe("编辑动作：HTTP 凭据只写", () => {
  it("HTTP 动作的地址 / Headers / 正文模板输入框不预填，仅改名称时 PUT 只含 name", async () => {
    const user = userEvent.setup();
    handlers["PUT /api/v1/actions/2"] = () => json(200, { ...httpAction, name: "通知平台2" });
    renderPage("/hosts-actions?tab=actions&edit=2");
    const name = await screen.findByLabelText("名称");
    for (const label of ["请求地址", "Headers", "正文模板"]) {
      expect((screen.getByLabelText(label) as HTMLInputElement).value).toBe("");
    }
    // url_set / headers_set 为 true，body_template_set 为 false：两项“已设置”。
    expect(within(screen.getByRole("dialog")).getAllByText("已设置")).toHaveLength(2);
    await user.clear(name);
    await user.type(name, "通知平台2");
    await user.click(screen.getByRole("button", { name: "保存" }));

    await waitFor(() => expect(callsTo("PUT", "/api/v1/actions/2")).toHaveLength(1));
    expect(callsTo("PUT", "/api/v1/actions/2")[0].body).toStrictEqual({ name: "通知平台2" });
  });
});

// ───────────────────────── 指纹确认 ─────────────────────────

describe("指纹确认", () => {
  const FP = "SHA256:abcDEF123+/xyz";

  it("获取不带凭据；展示指纹后点按“信任此指纹”才发送 {confirm:true, fingerprint}", async () => {
    const user = userEvent.setup();
    handlers["POST /api/v1/hosts/1/hostkey/fetch"] = () =>
      json(200, { fingerprint: FP, expires_at: "2026-10-27T06:25:58Z" });
    handlers["POST /api/v1/hosts/1/hostkey/confirm"] = () =>
      json(200, { ...passwordHost, hostkey_fingerprint: FP });
    renderPage("/hosts-actions?tab=hosts&fp=1");

    const sheet = await screen.findByRole("dialog", { name: "确认主机指纹" });
    // 获取之前没有候选指纹，也没有“信任”按钮。
    expect(within(sheet).queryByRole("button", { name: "信任此指纹" })).toBeNull();
    await user.click(within(sheet).getByRole("button", { name: "获取指纹" }));

    expect((await within(sheet).findByLabelText("本次获取的指纹")).textContent).toBe(FP);
    expect(callsTo("POST", "/api/v1/hosts/1/hostkey/fetch")[0].body).toStrictEqual({});
    expect(within(sheet).getByText(/内有效/)).toBeTruthy();
    // 获取后、人工核对前不发送确认。
    expect(callsTo("POST", "/api/v1/hosts/1/hostkey/confirm")).toHaveLength(0);

    await user.click(within(sheet).getByRole("button", { name: "信任此指纹" }));
    await waitFor(() => expect(callsTo("POST", "/api/v1/hosts/1/hostkey/confirm")).toHaveLength(1));
    expect(callsTo("POST", "/api/v1/hosts/1/hostkey/confirm")[0].body).toStrictEqual({
      confirm: true,
      fingerprint: FP,
    });
    expect(await screen.findByText(/现在可以认证与执行命令/)).toBeTruthy();
    await waitFor(() => expect(location()).not.toContain("fp=1"));
  });

  it("确认被拒绝（409）时保留候选指纹并显示错误，不关闭浮层", async () => {
    const user = userEvent.setup();
    handlers["POST /api/v1/hosts/1/hostkey/fetch"] = () =>
      json(200, { fingerprint: FP, expires_at: "2026-10-27T06:25:58Z" });
    handlers["POST /api/v1/hosts/1/hostkey/confirm"] = () => apiError(409, "hostkey_conflict");
    renderPage("/hosts-actions?tab=hosts&fp=1");

    const sheet = await screen.findByRole("dialog", { name: "确认主机指纹" });
    await user.click(within(sheet).getByRole("button", { name: "获取指纹" }));
    await within(sheet).findByLabelText("本次获取的指纹");
    await user.click(within(sheet).getByRole("button", { name: "信任此指纹" }));

    expect(await within(sheet).findByRole("alert")).toBeTruthy();
    expect(within(sheet).getByLabelText("本次获取的指纹").textContent).toBe(FP);
    expect(location()).toContain("fp=1");
  });

  it("获取失败时显示错误，不出现“信任此指纹”", async () => {
    const user = userEvent.setup();
    handlers["POST /api/v1/hosts/1/hostkey/fetch"] = () => apiError(422, "host_operation_failed");
    renderPage("/hosts-actions?tab=hosts&fp=1");

    const sheet = await screen.findByRole("dialog", { name: "确认主机指纹" });
    await user.click(within(sheet).getByRole("button", { name: "获取指纹" }));
    expect(await within(sheet).findByRole("alert")).toBeTruthy();
    expect(within(sheet).queryByRole("button", { name: "信任此指纹" })).toBeNull();
  });
});
