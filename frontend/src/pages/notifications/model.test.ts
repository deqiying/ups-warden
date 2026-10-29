import { describe, expect, it } from "vite-plus/test";
import { emptyDraft, validateChatId, validateDraft } from "./channelDraft.ts";
import {
  DEFAULT_LOG_FILTERS,
  durationMsText,
  eventColumnText,
  isDefaultLogFilters,
  logQuery,
  logRangeFrom,
  parseLogFilters,
  withLogFilters,
} from "./logs.ts";
import {
  TEMPLATE_VARIABLES,
  renderPreview,
  sampleValue,
  scanTemplate,
  templateIssues,
  variableTitle,
} from "./templateModel.ts";

describe("templateModel", () => {
  it("变量目录含基础变量、按相字段 [1]~[3] 与 Duration 的 _text", () => {
    expect(TEMPLATE_VARIABLES).toContain("device.name");
    expect(TEMPLATE_VARIABLES).toContain("input_voltage[3]");
    expect(TEMPLATE_VARIABLES).not.toContain("input_voltage[4]");
    expect(TEMPLATE_VARIABLES).toContain("runtime_estimate_text");
  });

  it("scanTemplate 识别未知变量并给出行号", () => {
    const segments = scanTemplate("第一行\n电量 {{nope_field}}", "text");
    const issues = templateIssues(segments);
    expect(issues).toEqual([
      { line: 2, text: "{{nope_field}}", reason: "nope_field 不是可用变量" },
    ]);
  });

  it("scanTemplate：缺少结束符、孤立 }} 与空占位符都是问题；JSON 嵌套 }} 合法", () => {
    expect(templateIssues(scanTemplate("a {{device.name", "text"))[0]?.reason).toBe(
      "缺少结束的 }}",
    );
    expect(templateIssues(scanTemplate("a }} b", "text"))[0]?.reason).toBe("多余的 }}");
    expect(templateIssues(scanTemplate("{{}}", "text"))[0]?.reason).toBe("占位符不能为空");
    expect(templateIssues(scanTemplate('{"a":{"b":"{{device.name}}"}}', "json"))).toEqual([]);
  });

  it("非 good 字段与未知变量渲染为「—」，绝不是 0", () => {
    const { text } = renderPreview(
      "U={{input_voltage[1]}} X={{nope_field}} 电量={{battery_capacity_pct}}",
      "text",
      "mains_lost",
    );
    expect(text).toBe("U=— X=— 电量=89");
    expect(text).not.toMatch(/=0(\s|$)/);
  });

  it("时长 _text 用中文时长，非 good 的时长为「—」", () => {
    expect(sampleValue("runtime_estimate_text", "mains_lost")).toBe("1 小时 21 分 0 秒");
    expect(sampleValue("battery_runtime_text", "mains_lost")).toBe("—");
  });

  it("detail 仅关机计划事件有内容", () => {
    expect(sampleValue("detail", "mains_lost")).toBe("");
    expect(sampleValue("detail", "plan_started")).toContain("计划");
  });

  it("HTML 转义变量值；JSON 转义字符串并校验合法性", () => {
    // 事件类型示例值无特殊字符，用 device.name 之外的路径验证转义：模板里的字面量保持不变。
    expect(renderPreview("<b>{{device.name}}</b>", "html", "mains_lost").text).toBe(
      "<b>机房 UPS-A</b>",
    );
    const ok = renderPreview(
      '{"d":"{{device.name}}","v":"{{input_voltage[1]}}"}',
      "json",
      "mains_lost",
    );
    expect(ok.jsonValid).toBe(true);
    expect(ok.text).toContain("—");
    const bad = renderPreview('{"d":"{{device.name}}"', "json", "mains_lost");
    expect(bad.jsonValid).toBe(false);
  });

  it("格式错误的占位符保留原文并进入 issues", () => {
    const preview = renderPreview("a {{ device.name }} b", "text", "mains_lost");
    expect(preview.text).toBe("a {{ device.name }} b");
    expect(preview.issues).toHaveLength(1);
  });

  it("variableTitle 对基础变量与统一字段都不触发未知字段", () => {
    expect(variableTitle("device.name")).toBe("设备名称");
    expect(variableTitle("input_voltage[1]")).toMatch(/L1$/);
    expect(variableTitle("runtime_estimate_text")).toMatch(/中文时长/);
  });
});

describe("logs 筛选", () => {
  it("默认值不写入 query，非法值回落默认", () => {
    expect(parseLogFilters(new URLSearchParams("result=zzz&range=1y&channel=-3"))).toEqual(
      DEFAULT_LOG_FILTERS,
    );
    const next = withLogFilters(new URLSearchParams("tab=logs"), { range: "7d", result: "all" });
    expect(next.toString()).toBe("tab=logs");
  });

  it("补丁写入 query 并保留 tab 等其他参数", () => {
    const next = withLogFilters(new URLSearchParams("tab=logs"), { channel: 4, result: "failed" });
    expect(next.get("tab")).toBe("logs");
    expect(next.get("channel")).toBe("4");
    expect(next.get("result")).toBe("failed");
    expect(parseLogFilters(next)).toEqual({ channel: 4, result: "failed", range: "7d" });
    expect(isDefaultLogFilters(parseLogFilters(next))).toBe(false);
  });

  it("logQuery：全部不传 ok；all 范围不传 from；from 为秒精度 UTC", () => {
    const now = Date.parse("2026-10-29T08:00:00.123Z");
    expect(logQuery(DEFAULT_LOG_FILTERS, now)).toEqual({
      channel_id: undefined,
      ok: undefined,
      from: "2026-10-22T08:00:00Z",
    });
    expect(logQuery({ channel: 2, result: "ok", range: "all" }, now)).toEqual({
      channel_id: 2,
      ok: true,
      from: undefined,
    });
    expect(logQuery({ channel: null, result: "failed", range: "24h" }, now).ok).toBe(false);
    expect(logRangeFrom("30d", now)).toBe("2026-09-29T08:00:00Z");
  });

  it("耗时与事件列文案（Q-C7）", () => {
    expect(durationMsText(250)).toBe("250 ms");
    expect(durationMsText(1500)).toBe("1.5 s");
    expect(durationMsText(-1)).toBe("—");
    expect(eventColumnText(null)).toBe("无关联事件");
    expect(eventColumnText(42)).toBe("事件 #42");
  });
});

describe("channelDraft 校验", () => {
  it("Chat ID：整数、负数群组、@username，拒绝 0 与越界", () => {
    expect(validateChatId("123456")).toBeNull();
    expect(validateChatId("-1001234567890")).toBeNull();
    expect(validateChatId("@ups_alerts")).toBeNull();
    expect(validateChatId("0")).not.toBeNull();
    expect(validateChatId("@abc")).not.toBeNull();
    expect(validateChatId("9223372036854775808")).not.toBeNull();
    expect(validateChatId("abc")).not.toBeNull();
  });

  it("新建 Telegram 必须有 Token 与 Chat ID；Webhook 必须有 URL", () => {
    const telegram = validateDraft({ ...emptyDraft("telegram"), name: "群" });
    expect(Object.keys(telegram).sort()).toEqual(["chatIds", "token"]);
    const webhook = validateDraft({ ...emptyDraft("webhook"), name: "钩子" });
    expect(Object.keys(webhook)).toEqual(["url"]);
    expect(
      validateDraft({ ...emptyDraft("webhook"), name: "", url: "https://x.test/h" }).name,
    ).toBe("请填写渠道名称");
  });

  it("Webhook URL 不允许账号密码，也不回显", () => {
    const errors = validateDraft({
      ...emptyDraft("webhook"),
      name: "钩子",
      url: "https://user:pw@x.test/h",
    });
    expect(errors.url).toBe("不能包含账号密码");
    expect(JSON.stringify(errors)).not.toContain("pw");
  });
});
