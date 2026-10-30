import { describe, expect, it } from "vite-plus/test";
import type { AuditEntry } from "../../api/audit.ts";
import { ApiError } from "../../api/client.ts";
import {
  auditKindLabel,
  auditResultView,
  auditTargetText,
  groupAuditByDay,
  parseSection,
  retentionPosition,
  settingsFailureText,
  timeZoneOptions,
  userFailureText,
  utcOffsetLabel,
} from "./model.ts";

const entry = (overrides: Partial<AuditEntry> = {}): AuditEntry => ({
  id: 1,
  at: "2026-10-27T06:00:00Z",
  user_id: 1,
  ip: "192.0.2.24",
  kind: "login",
  target: "user:1",
  diff: {},
  result: "success",
  ...overrides,
});

describe("parseSection", () => {
  it("只接受六个分区", () => {
    for (const key of ["general", "retention", "defaults", "security", "account", "audit"]) {
      expect(parseSection(key)).toBe(key);
    }
    expect(parseSection(undefined)).toBeNull();
    expect(parseSection("guard")).toBeNull();
    expect(parseSection("constructor")).toBeNull();
  });
});

describe("userFailureText", () => {
  const err = (status: number, code: string, message = "后端原文") =>
    new ApiError({ status, code, serverMessage: message });

  it("删除：422 不能删除自己，409 不能删除最后一位管理员（不用后端的误导文案）", () => {
    expect(userFailureText("delete", err(422, "validation_failed", "用户名或密码不符合要求"))).toBe(
      "不能删除当前登录的账户",
    );
    expect(userFailureText("delete", err(409, "user_conflict"))).toBe("不能删除最后一位管理员");
  });

  it("创建 / 改名：409 为用户名已存在", () => {
    expect(userFailureText("create", err(409, "user_conflict"))).toBe("用户名已存在");
    expect(userFailureText("rename", err(409, "user_conflict"))).toBe("用户名已存在");
  });

  it("422 按操作给出规则提示", () => {
    expect(userFailureText("password", err(422, "validation_failed"))).toContain("至少 8 个字符");
    expect(userFailureText("create", err(422, "validation_failed"))).toContain("用户名");
  });

  it("404 用户不存在；其余回退到统一错误文案", () => {
    expect(userFailureText("delete", err(404, "user_not_found"))).toContain("不存在");
    expect(userFailureText("create", err(0, "network_error"))).toContain("无法连接服务器");
    expect(userFailureText("create", new Error("boom"))).toBe("boom");
  });
});

describe("settingsFailureText", () => {
  it("503 configuration_saved_runtime_error 已由全局提示，返回 null", () => {
    expect(
      settingsFailureText(new ApiError({ status: 503, code: "configuration_saved_runtime_error" })),
    ).toBeNull();
  });

  it("422 validation_failed 显示后端给出的具体原因", () => {
    expect(
      settingsFailureText(
        new ApiError({ status: 422, code: "validation_failed", serverMessage: "明细保留期越界" }),
      ),
    ).toBe("明细保留期越界");
  });
});

describe("审计展示", () => {
  it("操作类型有中文名，未收录的显示原始 kind", () => {
    expect(auditKindLabel("settings.update")).toBe("修改系统设置");
    expect(auditKindLabel("future.kind")).toBe("future.kind");
    expect(auditKindLabel("constructor")).toBe("constructor");
  });

  it("结果的文案与色调", () => {
    expect(auditResultView("success")).toStrictEqual({ label: "成功", tone: "green" });
    expect(auditResultView("failure")).toStrictEqual({ label: "失败", tone: "red" });
    expect(auditResultView("requested").label).toBe("已请求");
    expect(auditResultView("accepted").label).toBe("已受理");
    expect(auditResultView("weird")).toStrictEqual({ label: "weird", tone: "gray" });
  });

  it("对象文本：用户 id 解析为用户名，失败登录显示尝试的用户名", () => {
    const names = (id: number) => (id === 1 ? "admin" : undefined);
    expect(auditTargetText(entry({ target: "user:1" }), names)).toBe("管理员「admin」");
    expect(auditTargetText(entry({ target: "user:9" }), names)).toBe("管理员 #9");
    expect(auditTargetText(entry({ target: "user:ghost", result: "failure" }), names)).toBe(
      "用户「ghost」",
    );
    // 尝试登录的用户名恰好全是数字，仍按用户名显示。
    expect(auditTargetText(entry({ target: "user:123", result: "failure" }), names)).toBe(
      "用户「123」",
    );
    expect(auditTargetText(entry({ kind: "rule.update", target: "5" }), names)).toBe("规则 #5");
    expect(auditTargetText(entry({ kind: "settings.update", target: "settings" }), names)).toBe(
      "系统设置",
    );
    expect(auditTargetText(entry({ kind: "profile.update", target: "ayi9" }), names)).toBe(
      "Profile「ayi9」",
    );
    expect(auditTargetText(entry({ kind: "plan_run.cancel", target: "8" }), names)).toBe("运行 #8");
    expect(auditTargetText(entry({ kind: "unknown.kind", target: "" }), names)).toBe("—");
  });

  it("按时区内的日期分组，标题带今天 / 昨天", () => {
    // 2026-10-27 22:00 UTC = 北京时间 10-28 06:00。
    const now = Date.parse("2026-10-27T22:30:00Z");
    const groups = groupAuditByDay(
      [
        entry({ id: 4, at: "2026-10-27T22:10:00Z" }),
        entry({ id: 3, at: "2026-10-27T16:30:00Z" }),
        entry({ id: 2, at: "2026-10-27T10:00:00Z" }),
        entry({ id: 1, at: "2026-10-20T10:00:00Z" }),
      ],
      "Asia/Shanghai",
      now,
    );
    expect(groups.map((g) => g.items.map((i) => i.id))).toStrictEqual([[4, 3], [2], [1]]);
    // 北京时间：10-28 06:10 与 10-28 00:30 同属“今天”。
    expect(groups[0].label).toMatch(/^今天 · 10月28日/);
    expect(groups[1].label).toMatch(/^昨天 · 10月27日/);
    expect(groups[2].label).toMatch(/^10月20日/);
  });
});

describe("时区选项", () => {
  it("包含 UTC 与当前已保存的时区，并带 UTC 偏移", () => {
    const options = timeZoneOptions("Asia/Shanghai");
    const values = options.map((o) => o.value);
    expect(values).toContain("UTC");
    expect(values).toContain("Asia/Shanghai");
    expect(options.find((o) => o.value === "Asia/Shanghai")?.label).toBe(
      "Asia/Shanghai（UTC+08:00）",
    );
    expect(options.find((o) => o.value === "UTC")?.label).toBe("UTC（UTC+00:00）");
  });

  it("运行环境不认识的已保存时区也保留为选项（不悄悄换成别的值）", () => {
    const options = timeZoneOptions("Mars/Olympus");
    expect(options.find((o) => o.value === "Mars/Olympus")?.label).toBe("Mars/Olympus");
    expect(utcOffsetLabel("Mars/Olympus")).toBe("");
  });
});

describe("retentionPosition", () => {
  it("对数刻度：1 天为 0，3650 天为 100，7 天约 23.72%", () => {
    expect(retentionPosition(1)).toBe(0);
    expect(retentionPosition(3650)).toBeCloseTo(100, 6);
    expect(retentionPosition(7)).toBeCloseTo(23.72, 1);
    expect(retentionPosition(90)).toBeCloseTo(54.86, 1);
    expect(retentionPosition(Number.NaN)).toBe(0);
  });
});
