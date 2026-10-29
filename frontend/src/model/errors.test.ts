import { describe, expect, it } from "vite-plus/test";

import { ERROR_MESSAGES, errorMessage } from "./errors.ts";

describe("errorMessage", () => {
  it("已知 code 返回映射文案，忽略服务端 message", () => {
    expect(errorMessage("unauthorized", "login required", 401)).toBe("请先登录");
    expect(errorMessage("rate_limited", "", 429)).toBe("请求过于频繁，请稍后重试");
    expect(errorMessage("network_error", "", 0)).toBe("无法连接服务器，请检查网络或服务状态");
    expect(errorMessage("invalid_response", "", 200)).toBe("服务器响应格式无效");
  });

  it.each([
    ["user_not_found", "用户不存在"],
    ["device_not_found", "设备不存在"],
    ["source_not_found", "数据源不存在"],
    ["profile_not_found", "Profile 不存在"],
    ["channel_not_found", "通知渠道不存在"],
    ["rule_not_found", "规则不存在"],
    ["action_not_found", "动作不存在"],
    ["host_not_found", "主机不存在"],
    ["plan_not_found", "计划不存在"],
    ["plan_run_not_found", "运行记录不存在"],
  ])("*_not_found：%s → %s", (code, text) => {
    expect(errorMessage(code, "server says something", 404)).toBe(text);
  });

  it("通用 not_found 为“接口不存在”", () => {
    expect(errorMessage("not_found", "", 404)).toBe("接口不存在");
  });

  it.each(["validation_failed", "host_operation_failed", "profile_invalid"])(
    "%s 优先使用服务端 message",
    (code) => {
      expect(errorMessage(code, "倒计时需在 0～3600 秒", 400)).toBe("倒计时需在 0～3600 秒");
    },
  );

  it.each([
    ["validation_failed", "配置校验失败"],
    ["host_operation_failed", "主机操作失败"],
    ["profile_invalid", "Profile 无效"],
  ])("%s 服务端 message 为空时回退映射文案", (code, text) => {
    expect(errorMessage(code, "", 400)).toBe(text);
  });

  it("非 SERVER_DETAIL 的已知 code 不被服务端 message 覆盖", () => {
    expect(errorMessage("resource_conflict", "duplicate key", 409)).toBe(
      ERROR_MESSAGES.resource_conflict,
    );
  });

  it("未知 code 回退服务端 message", () => {
    expect(errorMessage("brand_new_code", "后端给的说明", 400)).toBe("后端给的说明");
  });

  it("未知 code 且无服务端 message 时按 status 给通用文案", () => {
    expect(errorMessage("http_500", "", 500)).toBe("请求失败（HTTP 500）");
    expect(errorMessage("whatever", "", 418)).toBe("请求失败（HTTP 418）");
  });

  it("status 为 0 时不附带 HTTP 状态", () => {
    expect(errorMessage("whatever", "", 0)).toBe("请求失败");
  });

  // ERROR_MESSAGES 是普通对象：`constructor` 等原型键不能被当作已知 code。
  it("不应把 Object 原型属性当作已知 code", () => {
    expect(errorMessage("constructor", "", 500)).toBe("请求失败（HTTP 500）");
  });
});

describe("ERROR_MESSAGES", () => {
  it("所有文案均为非空字符串", () => {
    for (const [code, text] of Object.entries(ERROR_MESSAGES)) {
      expect(typeof text, code).toBe("string");
      expect(text.length, code).toBeGreaterThan(0);
    }
  });
});
