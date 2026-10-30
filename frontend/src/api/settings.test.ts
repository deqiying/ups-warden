import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { settings } from "../test/fixtures.ts";
import {
  BUILTIN_DEVICE_DEFAULTS,
  buildDefaultsPatch,
  buildGeneralPatch,
  buildGuardPatch,
  buildRetentionPatch,
  retentionOf,
  shortenedRetention,
  updateSettings,
  validateRetention,
  type RetentionDraft,
} from "./settings.ts";

const base: RetentionDraft = {
  detail_retention_days: 7,
  aggregate_retention_days: 90,
  log_retention_days: 365,
};

describe("buildGeneralPatch", () => {
  it("每次只含一个字段", () => {
    expect(buildGeneralPatch("timezone", "Asia/Shanghai")).toStrictEqual({
      timezone: "Asia/Shanghai",
    });
    expect(buildGeneralPatch("language", "en-US")).toStrictEqual({ language: "en-US" });
  });
});

describe("validateRetention", () => {
  it("合法值无错误；边界值 1、90、3650 合法", () => {
    expect(validateRetention(base)).toStrictEqual({});
    expect(
      validateRetention({
        detail_retention_days: 1,
        aggregate_retention_days: 1,
        log_retention_days: 1,
      }),
    ).toStrictEqual({});
    expect(
      validateRetention({
        detail_retention_days: 90,
        aggregate_retention_days: 3650,
        log_retention_days: 3650,
      }),
    ).toStrictEqual({});
  });

  it("明细需在 1～90 天", () => {
    expect(validateRetention({ ...base, detail_retention_days: 0 })).toHaveProperty(
      "detail_retention_days",
    );
    expect(validateRetention({ ...base, detail_retention_days: 91 })).toHaveProperty(
      "detail_retention_days",
    );
    expect(validateRetention({ ...base, detail_retention_days: 1.5 })).toHaveProperty(
      "detail_retention_days",
    );
    expect(validateRetention({ ...base, detail_retention_days: Number.NaN })).toHaveProperty(
      "detail_retention_days",
    );
  });

  it("聚合不小于明细且不超过 3650 天", () => {
    const small = validateRetention({
      ...base,
      detail_retention_days: 30,
      aggregate_retention_days: 7,
    });
    expect(small.aggregate_retention_days).toContain("30 天");
    expect(validateRetention({ ...base, aggregate_retention_days: 3651 })).toHaveProperty(
      "aggregate_retention_days",
    );
    // 聚合恰好等于明细合法。
    expect(
      validateRetention({ ...base, detail_retention_days: 30, aggregate_retention_days: 30 }),
    ).toStrictEqual({});
  });

  it("日志需在 1～3650 天", () => {
    expect(validateRetention({ ...base, log_retention_days: 0 })).toHaveProperty(
      "log_retention_days",
    );
    expect(validateRetention({ ...base, log_retention_days: 3651 })).toHaveProperty(
      "log_retention_days",
    );
  });
});

describe("保留期修改", () => {
  it("buildRetentionPatch 只含修改过的字段", () => {
    expect(buildRetentionPatch(base, base)).toStrictEqual({});
    expect(buildRetentionPatch(base, { ...base, detail_retention_days: 14 })).toStrictEqual({
      detail_retention_days: 14,
    });
    expect(
      buildRetentionPatch(base, { ...base, aggregate_retention_days: 120, log_retention_days: 30 }),
    ).toStrictEqual({ aggregate_retention_days: 120, log_retention_days: 30 });
  });

  it("shortenedRetention 只列出缩短的字段，延长与不变不算", () => {
    expect(shortenedRetention(base, base)).toStrictEqual([]);
    expect(shortenedRetention(base, { ...base, detail_retention_days: 14 })).toStrictEqual([]);
    expect(
      shortenedRetention(base, {
        detail_retention_days: 3,
        aggregate_retention_days: 120,
        log_retention_days: 30,
      }),
    ).toStrictEqual(["detail_retention_days", "log_retention_days"]);
  });

  it("retentionOf 取自设置", () => {
    expect(retentionOf(settings({ detail_retention_days: 5 })).detail_retention_days).toBe(5);
  });
});

describe("buildDefaultsPatch", () => {
  it("device_defaults 整体提交（含未修改的字段）", () => {
    const config = { ...settings().device_defaults, outage_confirm_s: 20 };
    const patch = buildDefaultsPatch(config);
    expect(Object.keys(patch)).toStrictEqual(["device_defaults"]);
    expect(Object.keys(patch.device_defaults).sort()).toStrictEqual(Object.keys(config).sort());
    expect(patch.device_defaults.outage_confirm_s).toBe(20);
  });

  it("内置默认值与后端 DefaultConfig 一致", () => {
    expect(BUILTIN_DEVICE_DEFAULTS).toStrictEqual({
      outage_confirm_s: 10,
      recovery_confirm_s: 60,
      input_voltage_threshold: 50,
      comm_failures: 3,
      comm_lost_policy: "hold",
      assume_on_battery_after_s: 300,
      battery_low_pct: 30,
      battery_hysteresis_pct: 5,
      overload_pct: 90,
      load_hysteresis_pct: 5,
    });
  });
});

describe("buildGuardPatch", () => {
  it("关闭带 confirm_disable_guard:true", () => {
    expect(buildGuardPatch(false)).toStrictEqual({
      http_action_guard_ups_hosts: false,
      confirm_disable_guard: true,
    });
  });

  it("开启不带确认字段", () => {
    const patch = buildGuardPatch(true);
    expect(patch).toStrictEqual({ http_action_guard_ups_hosts: true });
    expect(patch).not.toHaveProperty("confirm_disable_guard");
  });
});

describe("updateSettings", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("PATCH /api/v1/settings，请求体原样为传入的单字段", async () => {
    const fetchMock = vi.fn(
      async (_input: RequestInfo | URL, _init?: RequestInit) =>
        new Response(JSON.stringify(settings({ timezone: "UTC" })), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const saved = await updateSettings(buildGeneralPatch("timezone", "UTC"));
    expect(saved.timezone).toBe("UTC");
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/v1/settings");
    expect(init?.method).toBe("PATCH");
    expect(JSON.parse(typeof init?.body === "string" ? init.body : "")).toStrictEqual({
      timezone: "UTC",
    });
  });
});
