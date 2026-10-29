import { render, screen, waitFor, within, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { AppProviders } from "../../App.tsx";
import { createAppServices } from "../../app/services.ts";
import type { Channel, NotifyTemplate } from "../../api/notifications.ts";
import { FakeEventSource, notifyResult, sessionResponse, settings } from "../../test/fixtures.ts";
import { NotificationsPage } from "./NotificationsPage.tsx";
import { DEVICE_EVENT_TYPES, OTHER_EVENT_TYPES } from "./templateModel.ts";

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

const telegram: Channel = {
  id: 1,
  name: "值班群",
  type: "telegram",
  config: {
    chat_ids: ["-100198765432", "@ups_oncall"],
    format: "html",
    api_base: "https://api.telegram.org",
  },
  enabled: true,
  url_set: false,
  headers_set: false,
  signing_key_set: false,
  token_set: true,
  proxy_url_set: true,
};
const webhook: Channel = {
  id: 2,
  name: "运维平台",
  type: "webhook",
  config: { method: "POST", format: "json" },
  enabled: true,
  url_set: true,
  headers_set: false,
  signing_key_set: true,
  token_set: false,
  proxy_url_set: false,
};

/** 72 项模板：24 种事件 × 3 种格式；可覆盖个别项。 */
function templates(overrides: Record<string, Partial<NotifyTemplate>> = {}): NotifyTemplate[] {
  const out: NotifyTemplate[] = [];
  for (const event of [...DEVICE_EVENT_TYPES, ...OTHER_EVENT_TYPES]) {
    for (const format of ["json", "html", "text"] as const) {
      out.push({
        event_type: event,
        format,
        body: `${event} · {{device.name}}`,
        customized: false,
        ...overrides[`${event}:${format}`],
      });
    }
  }
  return out;
}

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
    "GET /api/v1/channels": () => json(200, { items: [telegram, webhook], next_before: 0 }),
    "GET /api/v1/notify-templates": () => json(200, { items: templates() }),
    "GET /api/v1/notify-logs": () => json(200, { items: [], next_before: 0 }),
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

function renderPage(path = "/notifications") {
  const services = createAppServices({ createSource: (url) => new FakeEventSource(url) });
  render(
    <AppProviders services={services}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/notifications" element={<NotificationsPage />} />
        </Routes>
        <CurrentLocation />
      </MemoryRouter>
    </AppProviders>,
  );
  return services;
}

const saveButton = () => screen.getByRole("button", { name: "保存" });

// ───────────────────────── 渠道 ─────────────────────────

describe("渠道：凭据只写", () => {
  it("显示“已设置 / 未设置”，凭据输入框永不预填，页面不出现任何凭据值", async () => {
    renderPage();
    const token = await screen.findByLabelText("Bot Token");
    expect((token as HTMLInputElement).value).toBe("");
    expect((token as HTMLInputElement).type).toBe("password");
    expect((screen.getByLabelText("HTTP 代理") as HTMLInputElement).value).toBe("");
    // token_set / proxy_url_set 均为 true；Bot Token 行与 HTTP 代理行各有一个“已设置”。
    expect(screen.getAllByText("已设置")).toHaveLength(2);
    // 没有变更时保存按钮禁用（不会发出空 PUT）。
    expect((saveButton() as HTMLButtonElement).disabled).toBe(true);
    // 已保存的 Chat ID 是非敏感配置，可以显示。
    expect(screen.getByText("-100198765432")).toBeTruthy();
  });

  it("编辑只提交修改的字段：只改名称时 PUT 只有 name", async () => {
    const user = userEvent.setup();
    handlers["PUT /api/v1/channels/1"] = () => json(200, { ...telegram, name: "新名称" });
    renderPage();
    const name = await screen.findByLabelText("名称");
    await user.clear(name);
    await user.type(name, "新名称");
    await user.click(saveButton());

    await waitFor(() => expect(callsTo("PUT", "/api/v1/channels/1")).toHaveLength(1));
    expect(callsTo("PUT", "/api/v1/channels/1")[0].body).toEqual({ name: "新名称" });
    expect(await screen.findByText("渠道已保存")).toBeTruthy();
  });

  it("留空凭据不提交；填写新 Token 只提交 token", async () => {
    const user = userEvent.setup();
    handlers["PUT /api/v1/channels/1"] = () => json(200, telegram);
    renderPage();
    await user.type(await screen.findByLabelText("Bot Token"), "123:NEW");
    await user.click(saveButton());
    await waitFor(() => expect(callsTo("PUT", "/api/v1/channels/1")).toHaveLength(1));
    expect(callsTo("PUT", "/api/v1/channels/1")[0].body).toEqual({ token: "123:NEW" });
  });

  it("清除凭据走 clear_secrets，输入框禁用，撤销后恢复", async () => {
    const user = userEvent.setup();
    handlers["PUT /api/v1/channels/1"] = () => json(200, { ...telegram, proxy_url_set: false });
    renderPage();
    const proxy = (await screen.findByLabelText("HTTP 代理")) as HTMLInputElement;
    await user.click(screen.getByRole("button", { name: "清除HTTP 代理" }));
    expect(proxy.disabled).toBe(true);
    expect(screen.getByText(/保存时将清除/, { selector: "span.ink-orange" })).toBeTruthy();

    // 撤销后回到未修改状态。
    await user.click(screen.getByRole("button", { name: "撤销清除HTTP 代理" }));
    expect(proxy.disabled).toBe(false);
    expect((saveButton() as HTMLButtonElement).disabled).toBe(true);

    await user.click(screen.getByRole("button", { name: "清除HTTP 代理" }));
    await user.click(saveButton());
    await waitFor(() => expect(callsTo("PUT", "/api/v1/channels/1")).toHaveLength(1));
    expect(callsTo("PUT", "/api/v1/channels/1")[0].body).toEqual({ clear_secrets: ["proxy_url"] });
  });

  it("必需凭据（Bot Token）没有“清除”入口", async () => {
    renderPage();
    await screen.findByLabelText("Bot Token");
    expect(screen.queryByRole("button", { name: "清除Bot Token" })).toBeNull();
  });
});

describe("渠道：类型切换与新建", () => {
  it("切换类型提示会清除旧配置与凭据，并要求新类型必需字段", async () => {
    const user = userEvent.setup();
    handlers["PUT /api/v1/channels/1"] = () => json(200, { ...webhook, id: 1 });
    renderPage();
    await screen.findByLabelText("Bot Token");
    const type = screen.getByRole("group", { name: "渠道类型" });
    await user.click(within(type).getByRole("button", { name: "Webhook" }));

    expect(screen.getByText(/保存后会清除原 Telegram/)).toBeTruthy();
    // 新类型的必需字段 URL 视为“未设置”，缺少时不能保存。
    await user.click(saveButton());
    expect(await screen.findByText("请填写接收地址")).toBeTruthy();
    expect(callsTo("PUT", "/api/v1/channels/1")).toHaveLength(0);

    await user.type(screen.getByLabelText("接收地址"), "https://example.test/hook");
    await user.click(saveButton());
    await waitFor(() => expect(callsTo("PUT", "/api/v1/channels/1")).toHaveLength(1));
    expect(callsTo("PUT", "/api/v1/channels/1")[0].body).toEqual({
      type: "webhook",
      config: { method: "POST", format: "json" },
      url: "https://example.test/hook",
    });
  });

  it("新建 Telegram：Chat ID 用 TokenInput，提交必需字段并选中新渠道", async () => {
    const user = userEvent.setup();
    handlers["POST /api/v1/channels"] = () =>
      json(201, {
        ...telegram,
        id: 9,
        name: "新群",
        config: { ...telegram.config, chat_ids: ["-100123"] },
      });
    renderPage("/notifications?tab=channels&id=new");

    await user.type(await screen.findByLabelText("名称"), "新群");
    await user.type(screen.getByLabelText("Bot Token"), "123:ABC");
    const chat = screen.getByRole("group", { name: "Chat ID" });
    await user.click(within(chat).getByRole("button", { name: "添加" }));
    await user.type(within(chat).getByRole("textbox"), "-100123{Enter}");
    await user.click(screen.getByRole("button", { name: "创建渠道" }));

    await waitFor(() => expect(callsTo("POST", "/api/v1/channels")).toHaveLength(1));
    expect(callsTo("POST", "/api/v1/channels")[0].body).toEqual({
      name: "新群",
      type: "telegram",
      enabled: true,
      config: { chat_ids: ["-100123"], format: "text", api_base: "https://api.telegram.org" },
      token: "123:ABC",
    });
    await waitFor(() => expect(location()).toBe("/notifications?tab=channels&id=9"));
  });

  it("Chat ID 非法时就地提示，不加入列表", async () => {
    const user = userEvent.setup();
    renderPage("/notifications?tab=channels&id=new");
    const chat = await screen.findByRole("group", { name: "Chat ID" });
    await user.click(within(chat).getByRole("button", { name: "添加" }));
    await user.type(within(chat).getByRole("textbox"), "abc{Enter}");
    expect(await within(chat).findByRole("alert")).toBeTruthy();
    expect(within(chat).queryByRole("button", { name: /移除/ })).toBeNull();
  });
});

describe("渠道：启停与删除", () => {
  it("启停开关只提交 enabled", async () => {
    const user = userEvent.setup();
    handlers["PUT /api/v1/channels/1"] = () => json(200, { ...telegram, enabled: false });
    renderPage();
    await user.click(await screen.findByRole("switch", { name: "启用" }));
    await waitFor(() => expect(callsTo("PUT", "/api/v1/channels/1")).toHaveLength(1));
    expect(callsTo("PUT", "/api/v1/channels/1")[0].body).toEqual({ enabled: false });
  });

  it("取消删除不发送任何请求；确认后才发送 {confirm:true}", async () => {
    const user = userEvent.setup();
    handlers["DELETE /api/v1/channels/1"] = () => json(200, { ok: true });
    renderPage();
    await user.click(await screen.findByRole("button", { name: "删除渠道" }));

    let dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(callsTo("DELETE", "/api/v1/channels/1")).toHaveLength(0);

    await user.click(screen.getByRole("button", { name: "删除渠道" }));
    dialog = await screen.findByRole("alertdialog");
    expect(callsTo("DELETE", "/api/v1/channels/1")).toHaveLength(0);
    await user.click(within(dialog).getByRole("button", { name: "删除渠道" }));

    await waitFor(() => expect(callsTo("DELETE", "/api/v1/channels/1")).toHaveLength(1));
    expect(callsTo("DELETE", "/api/v1/channels/1")[0].body).toEqual({ confirm: true });
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
  });

  it("删除返回 409 时提示存在发送记录引用，可改为停用", async () => {
    const user = userEvent.setup();
    handlers["DELETE /api/v1/channels/1"] = () => apiError(409, "resource_conflict");
    handlers["PUT /api/v1/channels/1"] = () => json(200, { ...telegram, enabled: false });
    renderPage();
    await user.click(await screen.findByRole("button", { name: "删除渠道" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "删除渠道" }));

    expect(await within(dialog).findByText(/存在发送记录引用，可改为停用/)).toBeTruthy();
    // 对话框不关闭，且给出“改为停用”。
    await user.click(within(dialog).getByRole("button", { name: "改为停用" }));
    await waitFor(() => expect(callsTo("PUT", "/api/v1/channels/1")).toHaveLength(1));
    expect(callsTo("PUT", "/api/v1/channels/1")[0].body).toEqual({ enabled: false });
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
  });
});

describe("渠道：503 configuration_saved_runtime_error", () => {
  it("保存成功但运行恢复失败：不回滚为旧值，重新读取并显示服务端最新配置", async () => {
    const user = userEvent.setup();
    let listCalls = 0;
    handlers["GET /api/v1/channels"] = () => {
      listCalls++;
      // 第一次读取为旧值；保存后（配置已写入）读取到新名称。
      const name = listCalls === 1 ? "值班群" : "已保存的新名称";
      return json(200, { items: [{ ...telegram, name }], next_before: 0 });
    };
    handlers["PUT /api/v1/channels/1"] = () =>
      apiError(503, "configuration_saved_runtime_error", "配置已保存，但通知渠道恢复失败");
    renderPage();

    const name = await screen.findByLabelText("名称");
    await user.clear(name);
    await user.type(name, "已保存的新名称");
    await user.click(saveButton());

    expect(await screen.findByText(/新配置已写入，相关服务未能按新配置恢复运行/)).toBeTruthy();
    expect(listCalls).toBeGreaterThanOrEqual(2);
    const refreshed = (await screen.findByLabelText("名称")) as HTMLInputElement;
    expect(refreshed.value).toBe("已保存的新名称");
    // 视为已保存：保存按钮回到禁用，而不是保留“未保存”状态诱导重复提交。
    expect((saveButton() as HTMLButtonElement).disabled).toBe(true);
    expect(screen.queryByText("渠道已保存")).toBeNull();
  });
});

describe("测试消息", () => {
  it("202 显示“已加入发送队列”（而不是已送达），并可跳转到该渠道的发送记录", async () => {
    const user = userEvent.setup();
    handlers["POST /api/v1/channels/1/test"] = () => json(202, { accepted: true, channel_id: 1 });
    renderPage();
    await user.click(await screen.findByRole("button", { name: "发送测试" }));

    expect(await screen.findByText("测试消息已加入发送队列")).toBeTruthy();
    expect(callsTo("POST", "/api/v1/channels/1/test")[0].body).toEqual({});
    expect(screen.queryByText(/已送达/)).toBeNull();

    await user.click(screen.getByRole("button", { name: "查看发送记录" }));
    await waitFor(() => expect(location()).toBe("/notifications?tab=logs&channel=1"));
  });

  it.each([
    [409, "channel_disabled", "请先启用渠道再发送测试消息"],
    [429, "notification_queue_full", "渠道发送队列已满，请稍后重试"],
    [503, "notification_unavailable", "通知渠道不可用或正在变更"],
  ])("%i %s 显示对应文案，不显示“已加入发送队列”", async (status, code, text) => {
    const user = userEvent.setup();
    handlers["POST /api/v1/channels/1/test"] = () => apiError(status, code);
    renderPage();
    await user.click(await screen.findByRole("button", { name: "发送测试" }));
    expect(await screen.findByText("测试消息未发送")).toBeTruthy();
    expect(screen.getByText(new RegExp(text))).toBeTruthy();
    expect(screen.queryByText("测试消息已加入发送队列")).toBeNull();
  });

  it("停用的渠道不能发送测试", async () => {
    handlers["GET /api/v1/channels"] = () =>
      json(200, { items: [{ ...telegram, enabled: false }], next_before: 0 });
    renderPage();
    const button = await screen.findByRole("button", { name: "发送测试" });
    expect((button as HTMLButtonElement).disabled).toBe(true);
  });
});

describe("渠道列表", () => {
  it("加载失败显示错误文案与重试入口", async () => {
    const user = userEvent.setup();
    handlers["GET /api/v1/channels"] = () => apiError(429, "rate_limited");
    renderPage();
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText(/请求过于频繁/)).toBeTruthy();

    handlers["GET /api/v1/channels"] = () => json(200, { items: [telegram], next_before: 0 });
    await user.click(within(alert).getByRole("button", { name: "重试" }));
    expect(await screen.findByLabelText("Bot Token")).toBeTruthy();
  });

  it("没有渠道时引导添加", async () => {
    handlers["GET /api/v1/channels"] = () => json(200, { items: [], next_before: 0 });
    renderPage();
    expect(await screen.findByText("还没有通知渠道", { selector: ".t-headline" })).toBeTruthy();
  });
});

// ───────────────────────── 模板 ─────────────────────────

const editor = () => screen.getByLabelText("模板内容") as HTMLTextAreaElement;
const preview = () => screen.getByLabelText("模板预览（示例）");

describe("模板", () => {
  it("列出 72 项模板对应的 24 种事件，customized 显示“已自定义”", async () => {
    handlers["GET /api/v1/notify-templates"] = () =>
      json(200, {
        items: templates({
          "mains_lost:html": { customized: true },
          "mains_lost:text": { customized: true },
          "mains_lost:json": { customized: true },
          "battery_low:html": { customized: true },
        }),
      });
    renderPage("/notifications?tab=templates");
    expect(
      await screen.findByText("共 72 个模板：每种事件各有纯文本、HTML、JSON 三种格式。"),
    ).toBeTruthy();
    expect(screen.getAllByText(/^[a-z_]+$/, { selector: ".tpl-key" })).toHaveLength(24);
    // 选中的 mains_lost 模板在编辑器标题处也有“已自定义”徽标。
    expect(screen.getAllByText("已自定义").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("1 种格式已自定义")).toBeTruthy();
  });

  it("选中的事件与格式写入 query", async () => {
    const user = userEvent.setup();
    renderPage("/notifications?tab=templates");
    await screen.findByLabelText("模板内容");
    await user.click(screen.getByRole("button", { name: /市电恢复/ }));
    await waitFor(() => expect(location()).toContain("event=mains_restored"));
    await user.click(
      within(screen.getByRole("group", { name: "模板格式" })).getByRole("button", { name: "HTML" }),
    );
    await waitFor(() => expect(location()).toContain("format=html"));
    expect(editor().value).toBe("mains_restored · {{device.name}}");
  });

  it("预览标注“示例”，非 good 与未知变量渲染为「—」而不是 0", async () => {
    handlers["GET /api/v1/notify-templates"] = () =>
      json(200, {
        items: templates({
          "mains_lost:text": {
            body: "电量：{{battery_capacity_pct}}%\n输入电压：{{input_voltage[1]}} V\n位置：{{device.location}}",
          },
        }),
      });
    renderPage("/notifications?tab=templates&event=mains_lost&format=text");
    await screen.findByLabelText("模板内容");
    expect(screen.getByText("示例", { selector: ".badge" })).toBeTruthy();
    const text = preview().textContent ?? "";
    expect(text).toContain("电量：89%");
    expect(text).toContain("输入电压：— V");
    expect(text).toContain("位置：—");
    expect(text).not.toMatch(/输入电压：0/);
    expect(text).not.toMatch(/位置：0/);
  });

  it("点按变量 chip 插入到光标处，并实时更新预览", async () => {
    const user = userEvent.setup();
    renderPage("/notifications?tab=templates&event=mains_lost&format=text");
    await screen.findByLabelText("模板内容");
    fireEvent.change(editor(), { target: { value: "时间：" } });
    editor().setSelectionRange(3, 3);
    await user.click(screen.getByRole("button", { name: "插入变量 time" }));
    expect(editor().value).toBe("时间：{{time}}");
    expect(preview().textContent).toContain("2026-10-27T14:11:12+08:00");
  });

  it("本地扫描高亮并列出未知变量与行号（保存仍以服务端为准）", async () => {
    renderPage("/notifications?tab=templates&event=mains_lost&format=text");
    await screen.findByLabelText("模板内容");
    fireEvent.change(editor(), {
      target: { value: "第一行 {{device.name}}\n位置：{{ device.location }}\n{{foo.bar}}" },
    });
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText(/第 2 行/)).toBeTruthy();
    expect(within(alert).getByText(/占位符内不能有空白/)).toBeTruthy();
    expect(within(alert).getByText(/第 3 行/)).toBeTruthy();
    expect(within(alert).getByText(/foo\.bar 不是可用变量/)).toBeTruthy();
  });

  it("保存提交 {event_type, format, body}；422 时提示被拒绝并保留未知变量定位", async () => {
    const user = userEvent.setup();
    handlers["PUT /api/v1/notify-templates"] = () =>
      apiError(422, "validation_failed", "模板校验失败");
    renderPage("/notifications?tab=templates&event=mains_lost&format=html");
    await screen.findByLabelText("模板内容");
    fireEvent.change(editor(), { target: { value: "<b>{{device.location}}</b>" } });
    await user.click(saveButton());

    await waitFor(() => expect(callsTo("PUT", "/api/v1/notify-templates")).toHaveLength(1));
    expect(callsTo("PUT", "/api/v1/notify-templates")[0].body).toEqual({
      event_type: "mains_lost",
      format: "html",
      body: "<b>{{device.location}}</b>",
    });
    expect(await screen.findByText("保存被拒绝")).toBeTruthy();
    expect(screen.getByText(/模板校验失败/)).toBeTruthy();
    expect(screen.getByText(/device\.location 不是可用变量/)).toBeTruthy();
  });

  it("保存成功后提示并重新读取", async () => {
    const user = userEvent.setup();
    handlers["PUT /api/v1/notify-templates"] = (call) =>
      json(200, { ...(call.body as object), customized: true });
    renderPage("/notifications?tab=templates&event=mains_lost&format=text");
    await screen.findByLabelText("模板内容");
    fireEvent.change(editor(), { target: { value: "新内容 {{device.name}}" } });
    await user.click(saveButton());
    expect(await screen.findByText("模板已保存")).toBeTruthy();
    await waitFor(() =>
      expect(callsTo("GET", "/api/v1/notify-templates").length).toBeGreaterThanOrEqual(2),
    );
  });

  it("恢复默认需确认：取消不发请求，确认后提交 reset:true", async () => {
    const user = userEvent.setup();
    handlers["GET /api/v1/notify-templates"] = () =>
      json(200, { items: templates({ "mains_lost:text": { customized: true, body: "自定义" } }) });
    handlers["PUT /api/v1/notify-templates"] = () =>
      json(200, {
        event_type: "mains_lost",
        format: "text",
        body: "市电中断\n设备：{{device.name}}",
        customized: false,
      });
    renderPage("/notifications?tab=templates&event=mains_lost&format=text");
    await screen.findByLabelText("模板内容");
    expect(editor().value).toBe("自定义");

    await user.click(screen.getByRole("button", { name: "恢复默认模板" }));
    let dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(callsTo("PUT", "/api/v1/notify-templates")).toHaveLength(0);

    await user.click(screen.getByRole("button", { name: "恢复默认模板" }));
    dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "恢复默认" }));
    await waitFor(() => expect(callsTo("PUT", "/api/v1/notify-templates")).toHaveLength(1));
    expect(callsTo("PUT", "/api/v1/notify-templates")[0].body).toEqual({
      event_type: "mains_lost",
      format: "text",
      reset: true,
    });
    await waitFor(() => expect(editor().value).toBe("市电中断\n设备：{{device.name}}"));
  });

  it("未自定义的模板不能恢复默认", async () => {
    renderPage("/notifications?tab=templates");
    await screen.findByLabelText("模板内容");
    expect(
      (screen.getByRole("button", { name: "恢复默认模板" }) as HTMLButtonElement).disabled,
    ).toBe(true);
  });

  it("加载失败显示错误文案与重试入口", async () => {
    handlers["GET /api/v1/notify-templates"] = () => apiError(429, "rate_limited");
    renderPage("/notifications?tab=templates");
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByRole("button", { name: "重试" })).toBeTruthy();
  });
});

// ───────────────────────── 发送记录 ─────────────────────────

const LOG_PAGE_1 = [
  notifyResult({
    id: 30,
    channel_id: 1,
    event_id: 5,
    ok: false,
    error: "HTTP 502",
    attempts: 4,
    duration_ms: 14_900,
    target: "-100198765432",
    summary: "市电中断 · 机房 UPS-A",
    template_fallback: true,
  }),
  notifyResult({
    id: 29,
    channel_id: 2,
    event_id: null,
    ok: true,
    error: "",
    attempts: 1,
    duration_ms: 87,
    target: "webhook",
    summary: "UPS Warden 测试消息",
  }),
];
const LOG_PAGE_2 = [
  notifyResult({ id: 20, channel_id: 1, event_id: 4, ok: true, error: "", summary: "更早的一条" }),
];

describe("发送记录", () => {
  beforeEach(() => {
    handlers["GET /api/v1/notify-logs"] = (call) =>
      call.search.has("before")
        ? json(200, { items: LOG_PAGE_2, next_before: 0 })
        : json(200, { items: LOG_PAGE_1, next_before: 29 });
  });

  it("显示 target、摘要、尝试、耗时、错误与模板回退；事件列按 Q-C7 显示，没有“重试中”", async () => {
    renderPage("/notifications?tab=logs");
    const table = await screen.findByRole("table");
    await within(table).findByText("市电中断 · 机房 UPS-A");

    const rows = within(table).getAllByRole("row");
    const failed = within(rows[1]);
    expect(failed.getByText("失败 · HTTP 502")).toBeTruthy();
    expect(failed.getByText("事件 #5")).toBeTruthy();
    expect(failed.getByText("值班群")).toBeTruthy();
    expect(failed.getByText("-100198765432")).toBeTruthy();
    expect(failed.getByText("4")).toBeTruthy();
    expect(failed.getByText("14.9 s")).toBeTruthy();
    expect(failed.getByText("默认模板")).toBeTruthy();

    const ok = within(rows[2]);
    expect(ok.getByText("成功")).toBeTruthy();
    expect(ok.getByText("无关联事件")).toBeTruthy();
    expect(ok.getByText("87 ms")).toBeTruthy();

    expect(screen.queryByText(/重试中/)).toBeNull();
    expect(screen.queryByText(/共 \d+ 条/)).toBeNull();
  });

  it("默认请求带 limit=50 与时间范围；筛选写入 query 并重新请求", async () => {
    const user = userEvent.setup();
    renderPage("/notifications?tab=logs");
    await screen.findByRole("table");
    const first = callsTo("GET", "/api/v1/notify-logs")[0].search;
    expect(first.get("limit")).toBe("50");
    expect(first.get("from")).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/);
    expect(first.has("ok")).toBe(false);
    expect(first.has("channel_id")).toBe(false);

    await user.click(
      within(screen.getByRole("group", { name: "结果筛选" })).getByRole("button", { name: "失败" }),
    );
    await waitFor(() => expect(location()).toBe("/notifications?tab=logs&result=failed"));
    await waitFor(() => {
      const last = callsTo("GET", "/api/v1/notify-logs").at(-1)!.search;
      expect(last.get("ok")).toBe("false");
    });

    await user.selectOptions(screen.getByLabelText("渠道"), "2");
    await waitFor(() => expect(location()).toBe("/notifications?tab=logs&result=failed&channel=2"));
    await waitFor(() => {
      const last = callsTo("GET", "/api/v1/notify-logs").at(-1)!.search;
      expect(last.get("channel_id")).toBe("2");
      expect(last.get("ok")).toBe("false");
    });

    await user.selectOptions(screen.getByLabelText("时间范围"), "all");
    await waitFor(() => expect(location()).toContain("range=all"));
    await waitFor(() => {
      const last = callsTo("GET", "/api/v1/notify-logs").at(-1)!.search;
      expect(last.has("from")).toBe(false);
    });

    await user.click(screen.getByRole("button", { name: "清除筛选" }));
    await waitFor(() => expect(location()).toBe("/notifications?tab=logs"));
  });

  it("query 中的筛选进入请求（成功 + 渠道）", async () => {
    renderPage("/notifications?tab=logs&result=ok&channel=1&range=24h");
    await screen.findByRole("table");
    const first = callsTo("GET", "/api/v1/notify-logs")[0].search;
    expect(first.get("ok")).toBe("true");
    expect(first.get("channel_id")).toBe("1");
    expect(first.has("from")).toBe(true);
  });

  it("游标“加载更早的记录”按 next_before 翻页，翻到底后提示没有更多", async () => {
    const user = userEvent.setup();
    renderPage("/notifications?tab=logs");
    const more = await screen.findByRole("button", { name: "加载更早的记录" });
    await user.click(more);
    await waitFor(() => expect(callsTo("GET", "/api/v1/notify-logs")).toHaveLength(2));
    expect(callsTo("GET", "/api/v1/notify-logs")[1].search.get("before")).toBe("29");
    expect(await screen.findByText("更早的一条")).toBeTruthy();
    expect(await screen.findByText("没有更多匹配的记录")).toBeTruthy();
  });

  it("加载失败显示错误文案与重试入口", async () => {
    const user = userEvent.setup();
    handlers["GET /api/v1/notify-logs"] = () => apiError(400, "invalid_filter");
    renderPage("/notifications?tab=logs");
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("筛选条件无效")).toBeTruthy();
    handlers["GET /api/v1/notify-logs"] = () => json(200, { items: LOG_PAGE_1, next_before: 0 });
    await user.click(within(alert).getByRole("button", { name: "重试" }));
    expect(await screen.findByRole("table")).toBeTruthy();
  });

  it("没有记录时显示空态", async () => {
    handlers["GET /api/v1/notify-logs"] = () => json(200, { items: [], next_before: 0 });
    renderPage("/notifications?tab=logs&result=failed");
    expect(await screen.findByText("没有匹配的发送记录")).toBeTruthy();
    expect(screen.getAllByRole("button", { name: "清除筛选" })).toHaveLength(2);
  });
});

describe("页面骨架", () => {
  it("tab 写入 query，默认是渠道", async () => {
    const user = userEvent.setup();
    renderPage("/notifications");
    await screen.findByLabelText("Bot Token");
    const tabs = screen.getByRole("group", { name: "通知视图" });
    await user.click(within(tabs).getByRole("button", { name: "模板" }));
    await waitFor(() => expect(location()).toBe("/notifications?tab=templates"));
    await user.click(within(tabs).getByRole("button", { name: "发送记录" }));
    await waitFor(() => expect(location()).toBe("/notifications?tab=logs"));
  });
});
