import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import {
  buildCreateBody,
  buildEnabledBody,
  buildUpdateBody,
  createChannel,
  deleteChannel,
  listChannels,
  listNotifyLogs,
  listTemplates,
  parseHeaders,
  resetTemplate,
  saveTemplate,
  testChannel,
  updateChannel,
  type Channel,
  type ChannelDraft,
} from "./notifications.ts";
import { draftFromChannel, emptyDraft } from "../pages/notifications/channelDraft.ts";

const telegram: Channel = {
  id: 1,
  name: "值班群",
  type: "telegram",
  config: {
    chat_ids: ["-100123", "@ups_oncall"],
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
  headers_set: true,
  signing_key_set: true,
  token_set: false,
  proxy_url_set: false,
};

function draftOf(channel: Channel, patch: Partial<ChannelDraft> = {}): ChannelDraft {
  return { ...draftFromChannel(channel), ...patch };
}

describe("buildUpdateBody：只提交修改的顶层字段", () => {
  it("未做任何修改时为空对象（凭据输入框永不预填）", () => {
    expect(buildUpdateBody(telegram, draftOf(telegram))).toEqual({});
    expect(buildUpdateBody(webhook, draftOf(webhook))).toEqual({});
  });

  it("只改名称时只有 name，不夹带 config 与凭据", () => {
    expect(buildUpdateBody(telegram, draftOf(telegram, { name: " 新名称 " }))).toEqual({
      name: "新名称",
    });
  });

  it("留空的凭据一律省略（空串、空 Header）", () => {
    const body = buildUpdateBody(webhook, draftOf(webhook, { name: "x", headersText: "  \n" }));
    expect(body).toEqual({ name: "x" });
    expect(body).not.toHaveProperty("url");
    expect(body).not.toHaveProperty("headers");
    expect(body).not.toHaveProperty("signing_key");
    expect(body).not.toHaveProperty("clear_secrets");
  });

  it("轮换凭据：只提交填写了新值的字段", () => {
    const body = buildUpdateBody(telegram, draftOf(telegram, { token: "123:NEW" }));
    expect(body).toEqual({ token: "123:NEW" });
  });

  it("配置有改动时提交完整 config（整体替换）", () => {
    const body = buildUpdateBody(telegram, draftOf(telegram, { chatIds: ["-100123"] }));
    expect(body).toEqual({
      config: { chat_ids: ["-100123"], format: "html", api_base: "https://api.telegram.org" },
    });
  });

  it("Webhook：清空正文模板后 config 不再含 body_template", () => {
    const withTemplate: Channel = {
      ...webhook,
      config: { method: "POST", format: "json", body_template: '{"a":"{{event.type}}"}' },
    };
    const body = buildUpdateBody(withTemplate, draftOf(withTemplate, { bodyTemplate: "" }));
    expect(body).toEqual({ config: { method: "POST", format: "json" } });
  });

  it("Webhook：Header 文本解析为对象后提交", () => {
    const body = buildUpdateBody(
      webhook,
      draftOf(webhook, { headersText: "X-API-Key: abc\nX-Env: prod" }),
    );
    expect(body).toEqual({ headers: { "X-API-Key": "abc", "X-Env": "prod" } });
  });

  it("清除凭据走 clear_secrets，且不与新值同时出现", () => {
    const body = buildUpdateBody(telegram, draftOf(telegram, { clearSecrets: ["proxy_url"] }));
    expect(body).toEqual({ clear_secrets: ["proxy_url"] });
    expect(body).not.toHaveProperty("proxy_url");
  });

  it("同一字段既有新值又要求清除属于逻辑错误，直接抛错", () => {
    expect(() =>
      buildUpdateBody(
        telegram,
        draftOf(telegram, { proxyUrl: "http://proxy:3128", clearSecrets: ["proxy_url"] }),
      ),
    ).toThrow(/不能同时设置新值和清除/);
  });

  it("切换类型：提交 type 与新类型的完整 config 和必需凭据，不带 clear_secrets", () => {
    const draft = draftOf(telegram, {
      type: "webhook",
      url: "https://example.test/hook",
      method: "PUT",
      // 切换类型后旧类型的清除标记不再适用，即使残留也不应提交。
      clearSecrets: ["proxy_url"],
    });
    const body = buildUpdateBody(telegram, draft);
    expect(body).toEqual({
      type: "webhook",
      config: { method: "PUT", format: "json" },
      url: "https://example.test/hook",
    });
    expect(body).not.toHaveProperty("clear_secrets");
  });

  it("Header 文本无效时拒绝构造", () => {
    expect(() => buildUpdateBody(webhook, draftOf(webhook, { headersText: "没有冒号" }))).toThrow();
  });

  it("启停开关单独提交", () => {
    expect(buildEnabledBody(false)).toEqual({ enabled: false });
  });
});

describe("buildCreateBody", () => {
  it("Telegram：必需字段 + 省略留空的代理", () => {
    const draft: ChannelDraft = {
      ...emptyDraft("telegram"),
      name: " 值班群 ",
      token: " 123:ABC ",
      chatIds: ["-100123"],
      telegramFormat: "html",
    };
    expect(buildCreateBody(draft)).toEqual({
      name: "值班群",
      type: "telegram",
      enabled: true,
      config: { chat_ids: ["-100123"], format: "html", api_base: "https://api.telegram.org" },
      token: "123:ABC",
    });
  });

  it("Webhook：格式固定 json，正文模板可选，空凭据省略", () => {
    const draft: ChannelDraft = {
      ...emptyDraft("webhook"),
      name: "运维",
      url: "https://example.test/hook?k=1",
      enabled: false,
    };
    const body = buildCreateBody(draft);
    expect(body).toEqual({
      name: "运维",
      type: "webhook",
      enabled: false,
      config: { method: "POST", format: "json" },
      url: "https://example.test/hook?k=1",
    });
  });
});

describe("parseHeaders", () => {
  it("解析多行、忽略空行，值保留冒号", () => {
    expect(parseHeaders("A: 1\n\nB: x:y\n")).toEqual({
      headers: { A: "1", B: "x:y" },
      error: null,
    });
  });

  it("拒绝禁用名、重复名、非法名称与缺少冒号，错误信息不回显值", () => {
    expect(parseHeaders("Host: a").error).toMatch(/不能设置 Host/);
    expect(parseHeaders("A: 1\na: 2").error).toMatch(/重复/);
    expect(parseHeaders("bad name: 1").error).toMatch(/非法字符/);
    expect(parseHeaders("secret-value").error).toMatch(/第 1 行/);
    expect(parseHeaders("secret-value").error).not.toContain("secret-value");
    expect(parseHeaders("X-ups-warden-signature: 1").error).toBeTruthy();
  });
});

describe("请求函数", () => {
  const calls: { url: string; init: RequestInit }[] = [];
  let reply: (url: URL, init: RequestInit) => Response;

  beforeEach(() => {
    calls.length = 0;
    reply = () => new Response("{}", { status: 200 });
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
        const raw =
          typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
        calls.push({ url: raw, init });
        return reply(new URL(raw, "http://localhost"), init);
      }),
    );
  });
  afterEach(() => vi.unstubAllGlobals());

  const bodyOf = (index = 0) => JSON.parse(calls[index].init.body as string);

  it("删除渠道：DELETE 且请求体为 {confirm:true}", async () => {
    await deleteChannel(7);
    expect(calls[0].url).toBe("/api/v1/channels/7");
    expect(calls[0].init.method).toBe("DELETE");
    expect(bodyOf()).toEqual({ confirm: true });
  });

  it("测试消息：POST 空对象", async () => {
    reply = () => new Response('{"accepted":true,"channel_id":3}', { status: 202 });
    await expect(testChannel(3)).resolves.toEqual({ accepted: true, channel_id: 3 });
    expect(calls[0].url).toBe("/api/v1/channels/3/test");
    expect(calls[0].init.method).toBe("POST");
    expect(bodyOf()).toEqual({});
  });

  it("新建用 POST、编辑用 PUT，响应补齐 chat_ids", async () => {
    reply = () =>
      new Response(JSON.stringify({ ...webhook, config: { method: "POST", format: "json" } }), {
        status: 201,
      });
    const created = await createChannel({ name: "x" });
    expect(calls[0].init.method).toBe("POST");
    expect(created.config.chat_ids).toEqual([]);
    await updateChannel(2, { name: "y" });
    expect(calls[1].url).toBe("/api/v1/channels/2");
    expect(calls[1].init.method).toBe("PUT");
    expect(bodyOf(1)).toEqual({ name: "y" });
  });

  it("渠道列表逐页读取", async () => {
    reply = (url) =>
      url.searchParams.has("before")
        ? new Response(JSON.stringify({ items: [{ ...webhook, id: 1 }], next_before: 0 }))
        : new Response(JSON.stringify({ items: [{ ...webhook, id: 2 }], next_before: 2 }));
    const items = await listChannels();
    expect(items.map((c) => c.id)).toEqual([2, 1]);
    expect(calls[0].url).toBe("/api/v1/channels?limit=200");
    expect(calls[1].url).toBe("/api/v1/channels?limit=200&before=2");
  });

  it("模板：保存提交 body；恢复默认提交 reset:true 且不带 body", async () => {
    await saveTemplate("mains_lost", "html", "<b>{{device.name}}</b>");
    expect(calls[0].init.method).toBe("PUT");
    expect(bodyOf(0)).toEqual({
      event_type: "mains_lost",
      format: "html",
      body: "<b>{{device.name}}</b>",
    });
    await resetTemplate("mains_lost", "html");
    expect(bodyOf(1)).toEqual({ event_type: "mains_lost", format: "html", reset: true });
    expect(bodyOf(1)).not.toHaveProperty("body");
  });

  it("模板列表 items 为 null 时按空列表处理", async () => {
    reply = () => new Response('{"items":null}');
    await expect(listTemplates()).resolves.toEqual([]);
  });

  it("发送记录：筛选参数进入 query，空值不发送", async () => {
    reply = () => new Response('{"items":[],"next_before":0}');
    await listNotifyLogs({ limit: 50, channel_id: 3, ok: false, from: "2026-10-27T00:00:00Z" });
    const url = new URL(calls[0].url, "http://localhost");
    expect(url.pathname).toBe("/api/v1/notify-logs");
    expect(Object.fromEntries(url.searchParams)).toEqual({
      limit: "50",
      channel_id: "3",
      ok: "false",
      from: "2026-10-27T00:00:00Z",
    });
  });
});
