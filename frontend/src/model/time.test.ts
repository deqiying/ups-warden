import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";

import {
  formatDateHeading,
  formatDateTime,
  formatFullDateTime,
  formatTime,
  resolveTimeZone,
} from "./time.ts";

// 2026-10-27 是星期二；Asia/Shanghai 为 UTC+8（无夏令时）。
const ISO = "2026-10-27T06:20:58Z";
const SH = "Asia/Shanghai";

describe("时间格式化（Asia/Shanghai）", () => {
  it("formatTime", () => {
    expect(formatTime(ISO, SH)).toBe("14:20:58");
  });

  it("formatDateTime", () => {
    expect(formatDateTime(ISO, SH)).toBe("10月27日 14:20");
  });

  it("formatFullDateTime", () => {
    expect(formatFullDateTime(ISO, SH)).toBe("2026-10-27 14:20:58");
  });

  it("formatDateHeading 含日期与星期", () => {
    const text = formatDateHeading(ISO, SH);
    expect(text).toContain("10月27日");
    expect(text).toContain("星期二");
  });

  it("跨日换算：UTC 18:30 在上海为次日 02:30", () => {
    expect(formatFullDateTime("2026-10-27T18:30:00Z", SH)).toBe("2026-10-28 02:30:00");
    expect(formatDateTime("2026-10-27T18:30:00Z", SH)).toBe("10月28日 02:30");
  });

  it("午夜按 h23 显示为 00 而不是 24", () => {
    expect(formatTime("2026-10-27T16:00:00Z", SH)).toBe("00:00:00");
  });

  it("月日去掉前导零", () => {
    expect(formatDateTime("2026-01-05T00:00:00Z", "UTC")).toBe("1月5日 00:00");
    expect(formatDateHeading("2026-01-05T00:00:00Z", "UTC")).toContain("1月5日");
  });

  it("接受 Date 与毫秒时间戳", () => {
    expect(formatTime(new Date(ISO), SH)).toBe("14:20:58");
    expect(formatTime(Date.parse(ISO), SH)).toBe("14:20:58");
    expect(formatDateHeading(new Date(ISO), SH)).toContain("星期二");
  });
});

describe("时间格式化（UTC）", () => {
  it("对应 06:20:58", () => {
    expect(formatTime(ISO, "UTC")).toBe("06:20:58");
    expect(formatDateTime(ISO, "UTC")).toBe("10月27日 06:20");
    expect(formatFullDateTime(ISO, "UTC")).toBe("2026-10-27 06:20:58");
    expect(formatDateHeading(ISO, "UTC")).toContain("10月27日");
  });
});

describe("无效输入", () => {
  it.each([null, undefined, "not-a-date", ""])("格式化 %j → 破折号", (value) => {
    // 空串被 new Date("") 解析为 Invalid Date。
    expect(formatTime(value, SH)).toBe("—");
    expect(formatDateTime(value, SH)).toBe("—");
    expect(formatFullDateTime(value, SH)).toBe("—");
  });

  it("formatDateHeading 对非法日期返回破折号", () => {
    expect(formatDateHeading("not-a-date", SH)).toBe("—");
    expect(formatDateHeading(new Date(Number.NaN), SH)).toBe("—");
  });
});

describe("时区回退", () => {
  beforeEach(() => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("无效时区回退为浏览器时区，格式化仍然成功", () => {
    expect(formatFullDateTime(ISO, "Bad/Zone_fmt")).toMatch(/^2026-10-2[67] \d{2}:\d{2}:\d{2}$/);
  });

  it("未指定时区时使用运行环境时区，仍能格式化", () => {
    expect(formatTime(ISO)).toMatch(/^\d{2}:\d{2}:\d{2}$/);
  });
});

describe("resolveTimeZone", () => {
  let warn: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    warn = vi.spyOn(console, "warn").mockImplementation(() => {});
  });

  afterEach(() => {
    warn.mockRestore();
  });

  it("合法时区原样返回", () => {
    expect(resolveTimeZone("Asia/Shanghai")).toBe("Asia/Shanghai");
    expect(resolveTimeZone("UTC")).toBe("UTC");
    expect(warn).not.toHaveBeenCalled();
  });

  it("空值返回 undefined，且不警告", () => {
    expect(resolveTimeZone(undefined)).toBeUndefined();
    expect(resolveTimeZone(null)).toBeUndefined();
    expect(resolveTimeZone("")).toBeUndefined();
    expect(warn).not.toHaveBeenCalled();
  });

  it("非法时区返回 undefined 并只警告一次", () => {
    expect(resolveTimeZone("Not/AZone")).toBeUndefined();
    expect(resolveTimeZone("Not/AZone")).toBeUndefined();
    expect(warn).toHaveBeenCalledTimes(1);
    expect(String(warn.mock.calls[0]?.[0])).toContain("Not/AZone");
  });
});
