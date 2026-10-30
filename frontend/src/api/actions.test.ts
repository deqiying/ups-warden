import { describe, expect, it } from "vite-plus/test";
import { wireAction } from "../test/fixtures.ts";
import {
  buildActionCreateBody,
  buildActionRunBody,
  buildActionUpdateBody,
  draftFromAction,
  emptyActionDraft,
  normalizeAction,
  type Action,
  type ActionDraft,
} from "./actions.ts";

const httpAction = (): Action =>
  wireAction({
    id: 5,
    name: "Webhook",
    type: "http",
    timeout_s: 10,
    config: { method: "POST", expected_status: [200, 204] },
    url_set: true,
    headers_set: true,
    body_template_set: true,
  });

/** 以已保存动作为基线构造“未修改”的草稿，再叠加修改。 */
const draftOf = (action: Action, overrides: Partial<ActionDraft> = {}): ActionDraft => ({
  ...draftFromAction(action),
  ...overrides,
});

describe("buildActionRunBody", () => {
  it("总是含 confirm:true 与显式 dry_run（true）", () => {
    const body = buildActionRunBody({ dryRun: true, deviceId: null });
    expect(body).toStrictEqual({ confirm: true, dry_run: true });
  });

  it("dry_run=false 也必须显式存在（未传会被后端当作真实执行）", () => {
    const body = buildActionRunBody({ dryRun: false, deviceId: null });
    expect(body).toStrictEqual({ confirm: true, dry_run: false });
    expect(Object.keys(body)).toContain("dry_run");
  });

  it("deviceId 为 null 时不含 device_id，指定时提交", () => {
    expect(buildActionRunBody({ dryRun: true, deviceId: null })).not.toHaveProperty("device_id");
    expect(buildActionRunBody({ dryRun: false, deviceId: 3 })).toStrictEqual({
      confirm: true,
      dry_run: false,
      device_id: 3,
    });
  });
});

describe("normalizeAction", () => {
  it("缺省 config（omitempty）补为空对象，且不与入参共享引用", () => {
    const wire = { ...wireAction({ type: "plan" }), config: undefined } as unknown as Action;
    expect(normalizeAction(wire).config).toEqual({});

    const withConfig = wireAction();
    const normalized = normalizeAction(withConfig);
    expect(normalized.config).toEqual(withConfig.config);
    expect(normalized.config).not.toBe(withConfig.config);
  });
});

describe("draftFromAction", () => {
  it("HTTP 凭据输入永不预填，缺省配置补默认值", () => {
    const draft = draftFromAction(httpAction());
    expect(draft.url).toBe("");
    expect(draft.headersText).toBe("");
    expect(draft.bodyTemplate).toBe("");
    expect(draft.clearSecrets).toEqual([]);
    expect(draft.expectedStatus).toEqual([200, 204]);

    const bare = draftFromAction(wireAction({ type: "http", config: {} }));
    expect(bare.method).toBe("POST");
    expect(bare.expectedStatus).toEqual([]);
  });
});

describe("buildActionCreateBody", () => {
  it("HTTP：地址 trim，Header 文本解析为对象，正文模板原样提交", () => {
    const body = buildActionCreateBody({
      ...emptyActionDraft("http"),
      name: " 通知平台 ",
      url: " https://hook.example/x ",
      headersText: "Authorization: Bearer t\nX-Env: prod",
      bodyTemplate: '{"e":"{{event.type}}"}',
      expectedStatus: [200],
    });
    expect(body).toStrictEqual({
      name: "通知平台",
      type: "http",
      timeout_s: 30,
      config: { method: "POST", expected_status: [200] },
      url: "https://hook.example/x",
      headers: { Authorization: "Bearer t", "X-Env": "prod" },
      body_template: '{"e":"{{event.type}}"}',
    });
  });

  it("HTTP：Header / 正文留空则省略对应凭据字段", () => {
    const body = buildActionCreateBody({ ...emptyActionDraft("http"), name: "a", url: "http://x" });
    expect(body).not.toHaveProperty("headers");
    expect(body).not.toHaveProperty("body_template");
    expect(body).not.toHaveProperty("clear_secrets");
  });

  it("HTTP：Header 无效时抛错，且错误信息不回显 Header 值", () => {
    const build = () =>
      buildActionCreateBody({
        ...emptyActionDraft("http"),
        name: "a",
        url: "http://x",
        headersText: "Host: secret-value",
      });
    expect(build).toThrow(/Headers 无效/);
    expect(build).not.toThrow(/secret-value/);
  });

  it("非 HTTP 类型不含任何凭据字段，config 只含本类型的键", () => {
    const ssh = buildActionCreateBody({
      ...emptyActionDraft("ssh"),
      name: "关机",
      hostId: 2,
      command: "poweroff",
      shutdown: true,
      // 其他类型的残留输入不应提交。
      url: "http://stale",
      headersText: "A: b",
    });
    expect(ssh).toStrictEqual({
      name: "关机",
      type: "ssh",
      timeout_s: 30,
      config: { host_id: 2, command: "poweroff", shutdown: true },
    });

    expect(
      buildActionCreateBody({
        ...emptyActionDraft("script"),
        name: "s",
        script: " a/b.sh ",
        args: ["1"],
      }).config,
    ).toEqual({ script: "a/b.sh", args: ["1"] });
    expect(
      buildActionCreateBody({ ...emptyActionDraft("notify"), name: "n", channelIds: [1, 2] })
        .config,
    ).toEqual({ channel_ids: [1, 2] });
    expect(
      buildActionCreateBody({ ...emptyActionDraft("plan"), name: "p", planId: 4 }).config,
    ).toEqual({
      plan_id: 4,
    });
  });
});

describe("buildActionUpdateBody：只提交修改过的字段", () => {
  it("未修改时为空对象", () => {
    expect(buildActionUpdateBody(httpAction(), draftOf(httpAction()))).toStrictEqual({});
    expect(buildActionUpdateBody(wireAction(), draftOf(wireAction()))).toStrictEqual({});
  });

  it("HTTP 的 url / headers / body_template 是只写凭据：留空不提交", () => {
    const action = httpAction();
    const body = buildActionUpdateBody(action, draftOf(action, { name: "新名称", timeoutS: 20 }));
    expect(body).toStrictEqual({ name: "新名称", timeout_s: 20 });
    expect(body).not.toHaveProperty("url");
    expect(body).not.toHaveProperty("headers");
    expect(body).not.toHaveProperty("body_template");
  });

  it("只填新地址时只提交 url（trim）", () => {
    const action = httpAction();
    const body = buildActionUpdateBody(action, draftOf(action, { url: " https://new.example/ " }));
    expect(body).toStrictEqual({ url: "https://new.example/" });
  });

  it("HTTP 配置修改时整体提交 config", () => {
    const action = httpAction();
    const body = buildActionUpdateBody(action, draftOf(action, { method: "PUT" }));
    expect(body).toStrictEqual({ config: { method: "PUT", expected_status: [200, 204] } });
  });

  it("后端省略的默认配置（omitempty）不算修改", () => {
    const action = wireAction({ type: "http", config: {}, url_set: true });
    expect(buildActionUpdateBody(action, draftOf(action))).toStrictEqual({});
  });

  it("可清除 headers / body_template，但 url 是必需凭据，不能清除", () => {
    const action = httpAction();
    const body = buildActionUpdateBody(
      action,
      draftOf(action, { clearSecrets: ["url", "headers", "body_template"] }),
    );
    expect(body).toStrictEqual({ clear_secrets: ["headers", "body_template"] });

    const onlyUrl = buildActionUpdateBody(action, draftOf(action, { clearSecrets: ["url"] }));
    expect(onlyUrl).toStrictEqual({});
  });

  it("同一凭据既有新值又要求清除时抛错", () => {
    const action = httpAction();
    expect(() =>
      buildActionUpdateBody(
        action,
        draftOf(action, { bodyTemplate: "x", clearSecrets: ["body_template"] }),
      ),
    ).toThrow(/不能同时设置新值和清除/);
  });

  it("非 HTTP 动作忽略残留的 clear_secrets 与凭据输入", () => {
    const action = wireAction();
    const body = buildActionUpdateBody(
      action,
      draftOf(action, { clearSecrets: ["headers"], url: "http://stale", headersText: "A: b" }),
    );
    expect(body).toStrictEqual({});
  });

  it("切换类型：提交新类型的完整 config 与必需凭据，丢弃 clear_secrets", () => {
    const body = buildActionUpdateBody(
      wireAction(),
      draftOf(wireAction(), {
        type: "http",
        method: "POST",
        url: "https://hook.example/",
        clearSecrets: ["headers"],
      }),
    );
    expect(body).toStrictEqual({
      type: "http",
      config: { method: "POST", expected_status: [] },
      url: "https://hook.example/",
    });
  });

  it("SSH 配置任一字段修改时整体提交 config", () => {
    const action = wireAction();
    const body = buildActionUpdateBody(action, draftOf(action, { command: "shutdown -h now" }));
    expect(body).toStrictEqual({
      config: { host_id: 1, command: "shutdown -h now", shutdown: true },
    });
  });
});
