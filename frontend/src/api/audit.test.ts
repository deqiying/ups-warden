import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { auditDiffRows, isSensitiveField, listAudit } from "./audit.ts";

describe("auditDiffRows", () => {
  it("普通字段显示 before / after", () => {
    const rows = auditDiffRows({
      cooldown_s: { before: 300, after: 60 },
      dry_run: { before: true, after: false },
    });
    expect(rows).toStrictEqual([
      { field: "cooldown_s", masked: false, before: "300", after: "60" },
      { field: "dry_run", masked: false, before: "true", after: "false" },
    ]);
  });

  it("后端已标记为“已修改”的敏感字段只显示已修改", () => {
    expect(auditDiffRows({ password: "已修改" })).toStrictEqual([
      { field: "password", masked: true },
    ]);
  });

  it("纵深防御：字段名敏感时，即使响应里意外带有明文也不显示", () => {
    const rows = auditDiffRows({
      password: { before: "old-secret", after: "new-secret" },
      "config.headers.Authorization": { before: "Bearer a", after: "Bearer b" },
      bot_token: { before: "t1", after: "t2" },
      community: "public",
    });
    expect(rows.every((r) => r.masked)).toBe(true);
    expect(JSON.stringify(rows)).not.toMatch(/secret|Bearer|t1|t2|public/);
  });

  it("创建 / 删除：缺失的一侧显示为空；过长的值被截断", () => {
    const [created] = auditDiffRows({ username: { before: null, after: "ops" } });
    expect(created).toStrictEqual({ field: "username", masked: false, before: null, after: "ops" });
    const [long] = auditDiffRows({ note: { before: "a".repeat(500), after: "b" } });
    expect(long.masked === false && long.before?.length).toBe(201);
  });

  it("非对象 diff 视为无差异", () => {
    expect(auditDiffRows(null)).toStrictEqual([]);
    expect(auditDiffRows("x")).toStrictEqual([]);
    expect(auditDiffRows([])).toStrictEqual([]);
    expect(auditDiffRows({})).toStrictEqual([]);
  });
});

describe("isSensitiveField", () => {
  it("与后端规则一致：忽略大小写、下划线与连字符", () => {
    for (const key of [
      "password",
      "new_pass",
      "Api-Key",
      "CSRF",
      "secret",
      "ssh.private_key",
      "SNMP_Community",
      "cookie",
      "auth.credentials",
    ]) {
      expect(isSensitiveField(key)).toBe(true);
    }
    for (const key of ["name", "cooldown_s", "http_action_guard_ups_hosts", "timezone"]) {
      expect(isSensitiveField(key)).toBe(false);
    }
  });
});

describe("listAudit", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("limit / before 作为查询参数；缺省的 before 不发送", async () => {
    const urls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        urls.push(
          typeof input === "string" ? input : input instanceof URL ? input.href : input.url,
        );
        return new Response(JSON.stringify({ items: null, next_before: 0 }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }),
    );
    const page = await listAudit({ limit: 50 });
    await listAudit({ limit: 50, before: 120 });
    expect(urls).toStrictEqual(["/api/v1/audit?limit=50", "/api/v1/audit?limit=50&before=120"]);
    expect(page).toStrictEqual({ items: [], next_before: 0 });
  });
});
