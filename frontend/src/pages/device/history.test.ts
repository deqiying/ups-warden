import { describe, expect, it } from "vite-plus/test";
import { normalizeEventView } from "../../api/normalize.ts";
import type { HistoryResponse } from "../../api/types.ts";
import { wireEventView } from "../../test/fixtures.ts";
import {
  buildSeries,
  groupByUnit,
  historyFieldOptions,
  niceScale,
  outageIntervals,
  sanitizeHistoryFields,
  seriesStats,
} from "./history.ts";

const T = (s: number) => new Date(Date.UTC(2026, 9, 27, 6, 0, s)).toISOString();
const v = (avg: number, count = 1) => ({ min: avg, max: avg, avg, count });

describe("history", () => {
  it("字段实例只含 History 字段，按相字段按相数展开", () => {
    const single = historyFieldOptions({});
    expect(single).toContain("battery_capacity_pct");
    expect(single).toContain("output_load_pct[1]");
    expect(single).not.toContain("output_load_pct[2]");
    // 非 History 字段不可选。
    expect(single).not.toContain("battery_status");
    expect(single).not.toContain("output_load_pct");
    const three = historyFieldOptions({
      input_phases: { num: 3, bool: false, str: "", quality: "good", reason: "" },
    });
    expect(three).toContain("input_voltage[3]");
    expect(three).not.toContain("output_voltage[2]");
  });

  it("缺失采样处断开：超过期望步长 2.5 倍的间隔、或某点缺少该字段", () => {
    const response: HistoryResponse = {
      resolution: "raw",
      points: [
        { at: T(0), fields: { battery_capacity_pct: v(100) } },
        { at: T(3), fields: { battery_capacity_pct: v(99) } },
        { at: T(6), fields: { battery_capacity_pct: v(98) } },
        // 30 秒空档（通讯中断）。
        { at: T(36), fields: { battery_capacity_pct: v(95) } },
        { at: T(39), fields: { battery_capacity_pct: v(94) } },
        { at: T(42), fields: { battery_voltage: v(25) } },
        { at: T(45), fields: { battery_capacity_pct: v(93) } },
      ],
    };
    const [series] = buildSeries(response, ["battery_capacity_pct"], 60_000);
    expect(series.segments.map((s) => s.map((p) => p.avg))).toEqual([
      [100, 99, 98],
      [95, 94],
      [93],
    ]);
    expect(series.unit).toBe("%");
  });

  it("统计按 count 加权平均", () => {
    const response: HistoryResponse = {
      resolution: "1m",
      points: [
        { at: T(0), fields: { temperature: { min: 30, max: 32, avg: 31, count: 3 } } },
        { at: T(60), fields: { temperature: { min: 28, max: 30, avg: 29, count: 1 } } },
      ],
    };
    const [series] = buildSeries(response, ["temperature"], 3600_000);
    expect(seriesStats(series)).toEqual({ min: 28, max: 32, avg: 30.5 });
  });

  it("刻度：百分比固定 0～100；其他单位取整齐步长", () => {
    expect(niceScale(20, 90, "%").ticks).toEqual([0, 25, 50, 75, 100]);
    const volts = niceScale(218.4, 231.2, "V");
    expect(volts.min).toBeLessThanOrEqual(218.4);
    expect(volts.max).toBeGreaterThanOrEqual(231.2);
    expect(volts.ticks.length).toBeGreaterThanOrEqual(3);
    expect(niceScale(5, 5, "V").max).toBeGreaterThan(5);
  });

  it("停电区间：cleared_at、其后的 mains_restored、或进行中取当前时刻，并裁剪到查询范围", () => {
    const from = Date.parse(T(0));
    const to = Date.parse(T(600));
    const lost = [
      wireEventView({ id: 1, started_at: T(-120), cleared_at: T(60), active: false }),
      wireEventView({ id: 2, started_at: T(200), cleared_at: null, active: false }),
      wireEventView({ id: 3, started_at: T(500), cleared_at: null, active: true }),
    ].map(normalizeEventView);
    const restored = [wireEventView({ id: 9, type: "mains_restored", started_at: T(260) })].map(
      normalizeEventView,
    );
    expect(outageIntervals(lost, restored, from, to, Date.parse(T(550)))).toEqual([
      { start: from, end: Date.parse(T(60)) },
      { start: Date.parse(T(200)), end: Date.parse(T(260)) },
      { start: Date.parse(T(500)), end: Date.parse(T(550)) },
    ]);
  });

  it("按单位分组，URL 字段参数只保留可选实例并去重", () => {
    const response: HistoryResponse = { resolution: "raw", points: [] };
    const groups = groupByUnit(
      buildSeries(response, ["battery_capacity_pct", "battery_voltage", "output_load_pct[1]"], 1),
    );
    expect(groups.map((g) => g.map((s) => s.key))).toEqual([
      ["battery_capacity_pct", "output_load_pct[1]"],
      ["battery_voltage"],
    ]);
    const options = historyFieldOptions({});
    expect(
      sanitizeHistoryFields(
        ["battery_voltage", "battery_voltage", "nope", "battery_status"],
        options,
      ),
    ).toEqual(["battery_voltage"]);
  });
});
