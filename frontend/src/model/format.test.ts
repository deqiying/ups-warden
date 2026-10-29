import { describe, expect, it, vi } from "vite-plus/test";
import type { Field, Quality } from "../api/types.ts";
import {
  clockText,
  durationText,
  fieldText,
  formatField,
  formatNumber,
  goodNumber,
  NA_TEXT,
} from "./format.ts";
import { REASON_LABELS } from "./labels.ts";

function field(overrides: Partial<Field> = {}): Field {
  return { num: 0, bool: false, str: "", quality: "good", reason: "", ...overrides };
}

describe("formatNumber", () => {
  it("按单位取小数位：Hz 一位、% 取整、V 至多一位", () => {
    expect(formatNumber(50, "Hz")).toBe("50.0");
    expect(formatNumber(89.6, "%")).toBe("90");
    expect(formatNumber(220.44, "V")).toBe("220.4");
    expect(formatNumber(220, "V")).toBe("220");
    expect(formatNumber(1.234)).toBe("1.23");
    expect(formatNumber(12345, "W")).toBe("12345");
  });

  it("不显示 -0", () => {
    expect(formatNumber(-0, "V")).toBe("0");
  });
});

describe("durationText", () => {
  it("与后端 durationText 一致：省略为 0 的高位单位，秒总是保留", () => {
    expect(durationText(0)).toBe("0 秒");
    expect(durationText(750)).toBe("12 分 30 秒");
    expect(durationText(3600)).toBe("1 小时 0 秒");
    expect(durationText(90061)).toBe("1 天 1 小时 1 分 1 秒");
    expect(durationText(-5)).toBe("0 秒");
    expect(durationText(59.9)).toBe("59 秒");
  });

  it("分钟精度四舍五入且不显示秒", () => {
    expect(durationText(4889, "minute")).toBe("1 小时 21 分");
    expect(durationText(4890, "minute")).toBe("1 小时 22 分");
    expect(durationText(29, "minute")).toBe("不足 1 分");
    expect(durationText(30, "minute")).toBe("1 分");
  });
});

describe("clockText", () => {
  it("分:秒，超过一小时显示时:分:秒", () => {
    expect(clockText(0)).toBe("00:00");
    expect(clockText(613)).toBe("10:13");
    expect(clockText(3723)).toBe("1:02:03");
    expect(clockText(-1)).toBe("00:00");
  });
});

describe("formatField", () => {
  it("good 数值带单位，按相实例键使用基础字段规格", () => {
    expect(formatField("battery_capacity_pct", field({ num: 89 }))).toEqual({
      kind: "value",
      text: "89",
      unit: "%",
      stale: false,
      estimated: false,
    });
    expect(formatField("input_voltage[2]", field({ num: 220.44 }))).toMatchObject({
      kind: "value",
      text: "220.4",
      unit: "V",
    });
  });

  it("good 的 0 是真实读数（omitempty 补齐后），照常显示", () => {
    expect(fieldText("output_load_pct[1]", field({ num: 0 }))).toBe("0 %");
    expect(fieldText("on_battery_elapsed", field({ num: 0 }))).toBe("0 秒");
    expect(fieldText("mains_ok", field({ bool: false }))).toBe("否");
  });

  it.each(
    Object.keys(REASON_LABELS).flatMap((reason) =>
      (["invalid", "unsupported"] as const).map((quality) => [quality, reason] as const),
    ),
  )("%s/%s 显示「—」与原因，绝不显示 0", (quality, reason) => {
    const display = formatField("battery_capacity_pct", field({ quality, reason, num: 0 }));
    expect(display).toMatchObject({ kind: "na", text: NA_TEXT, reasonCode: reason, quality });
    expect(display.kind === "na" && display.reason).toBe(REASON_LABELS[reason]);
    expect(fieldText("battery_capacity_pct", field({ quality, reason }))).toBe(NA_TEXT);
  });

  it("快照缺少该键按 Profile 未映射显示", () => {
    expect(formatField("input_voltage[3]", undefined)).toEqual({
      kind: "na",
      text: NA_TEXT,
      reason: "Profile 未映射",
      reasonCode: "not_mapped",
      quality: "missing",
    });
  });

  it("派生时长在非电池供电时说明业务原因", () => {
    const offBattery = field({ quality: "unsupported", reason: "not_mapped" });
    expect(formatField("on_battery_elapsed", offBattery)).toMatchObject({
      kind: "na",
      reason: "未在电池供电",
    });
    expect(formatField("runtime_estimate", offBattery)).toMatchObject({
      kind: "na",
      reason: "仅电池供电时估算",
    });
    expect(
      formatField("runtime_estimate", field({ quality: "invalid", reason: "estimating" })),
    ).toMatchObject({ kind: "na", reason: "估算中" });
  });

  it("未知原因码回退为质量文案", () => {
    expect(
      formatField("temperature", field({ quality: "invalid", reason: "x_new" })),
    ).toMatchObject({ kind: "na", reason: "无效（x_new）" });
    expect(formatField("temperature", field({ quality: "unsupported" }))).toMatchObject({
      kind: "na",
      reason: "不支持",
    });
  });

  it("stale 保留最后读数并置灰，不改写数值", () => {
    expect(
      formatField(
        "battery_capacity_pct",
        field({ num: 76, quality: "stale", reason: "comm_stale" }),
      ),
    ).toMatchObject({ kind: "value", text: "76", stale: true });
    expect(formatField("battery_capacity_pct", field({ num: 76 }), { stale: true })).toMatchObject({
      kind: "value",
      text: "76",
      stale: true,
    });
  });

  it("剩余时间：估算值显示“约”与分钟精度，设备上报值完整显示", () => {
    expect(
      formatField("runtime_estimate", field({ num: 4860 }), { runtimeSource: "estimated" }),
    ).toEqual({ kind: "value", text: "约 1 小时 21 分", unit: "", stale: false, estimated: true });
    expect(
      formatField("runtime_estimate", field({ num: 4860 }), { runtimeSource: "native" }),
    ).toMatchObject({ text: "1 小时 21 分 0 秒", estimated: false });
    // 只有 runtime_estimate 受 runtime_source 影响。
    expect(
      formatField("native_runtime_remaining", field({ num: 600 }), { runtimeSource: "estimated" }),
    ).toMatchObject({ text: "10 分 0 秒", estimated: false });
  });

  it("枚举显示中文，未知值原样显示，空值不伪装成有值", () => {
    expect(fieldText("ups_mode", field({ str: "battery" }))).toBe("电池模式");
    expect(fieldText("ups_mode", field({ str: "future_mode" }))).toBe("future_mode");
    expect(formatField("ups_mode", field({ str: "" }))).toMatchObject({
      kind: "na",
      reasonCode: "empty",
      reason: "无数据",
    });
  });

  it("文本与布尔字段", () => {
    expect(fieldText("model", field({ str: "C3KS" }))).toBe("C3KS");
    expect(formatField("model", field())).toMatchObject({ kind: "na", reasonCode: "empty" });
    expect(fieldText("low_battery", field({ bool: true }))).toBe("是");
  });

  it("目录中没有的键按文本处理并提示同步目录", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    expect(fieldText("brand_new_field", field({ str: "x" }))).toBe("x");
    expect(fieldText("brand_new_field", field({ num: 3 }))).toBe(NA_TEXT);
    expect(warn).toHaveBeenCalledTimes(1);
    warn.mockRestore();
  });
});

describe("goodNumber", () => {
  it.each<[Quality, number | null]>([
    ["good", 42],
    ["stale", null],
    ["invalid", null],
    ["unsupported", null],
  ])("%s → %s", (quality, expected) => {
    expect(goodNumber(field({ num: 42, quality }))).toBe(expected);
  });

  it("缺失字段返回 null", () => {
    expect(goodNumber(undefined)).toBeNull();
  });
});
