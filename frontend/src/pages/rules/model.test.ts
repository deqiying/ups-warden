import { describe, expect, it } from "vite-plus/test";
import { normalizeField } from "../../api/normalize.ts";
import {
  conditionFieldOptions,
  conditionText,
  logReasonText,
  logValuesText,
  matchedDevices,
  templateDescription,
} from "./model.ts";
import { emptyRuleDraft, normalizeRule, type Rule } from "../../api/rules.ts";
import type { Device } from "../../api/types.ts";

const device = (id: number, tags: string[], enabled = true): Device =>
  ({ id, name: `UPS-${id}`, note: "", location: "", tags, enabled }) as Device;

describe("条件字段选项", () => {
  it("按相字段先列不带相号的实例，再列 L1～L3", () => {
    const values = conditionFieldOptions().map((o) => o.value);
    const i = values.indexOf("input_voltage");
    expect(values.slice(i, i + 4)).toEqual([
      "input_voltage",
      "input_voltage[1]",
      "input_voltage[2]",
      "input_voltage[3]",
    ]);
    expect(values).not.toContain("input_freq[1]");
    expect(values[0]).toBe("comm_state");
  });
});

describe("文案", () => {
  it("条件与模板描述", () => {
    expect(conditionText({ field: "power_state", op: "eq", value: "on_battery" })).toBe(
      "供电状态 等于 电池供电",
    );
    expect(conditionText({ field: "battery_capacity_pct", op: "lte", value: 40, for_s: 10 })).toBe(
      "电池容量 ≤ 40 %，持续 10 秒",
    );
    const tpl = {
      key: "mains_lost",
      suggested_action_type: "notify",
      rule: normalizeRule({
        trigger: { type: "event", events: ["mains_lost"] },
      } as unknown as Rule),
    };
    expect(templateDescription(tpl)).toBe("事件 「市电中断」");
  });

  it("日志依据：条件、事件、解除、冷却、质量与派发失败", () => {
    expect(logReasonText("fired", "condition_matched")).toBe("条件成立");
    expect(logReasonText("fired", "mains_lost")).toBe("事件「市电中断」");
    expect(logReasonText("recovered", "mains_lost_cleared")).toBe("事件「市电中断」已解除");
    expect(logReasonText("skipped_cooldown", "cooldown")).toMatch(/冷却/);
    expect(
      logReasonText(
        "quality_blocked",
        "battery_capacity_pct:stale:comm_stale;power_state:invalid:",
      ),
    ).toBe("电池容量 过期（通讯中断，显示旧值）；供电状态 无效");
    expect(logReasonText("dispatch_failed", "condition_matched;action_not_accepted")).toBe(
      "条件成立；动作未被接受",
    );
    expect(logReasonText("fired", "something_new")).toBe("something_new");
  });

  it("日志 values：非 good 字段显示质量，不显示为 0", () => {
    const text = logValuesText({
      battery_capacity_pct: normalizeField({ quality: "good", num: 38 }),
      input_voltage: normalizeField({ quality: "stale", num: 0 }),
    });
    expect(text).toBe("电池容量 38 % · 输入电压 过期");
  });
});

describe("matchedDevices", () => {
  const devices = [device(1, ["核心"]), device(2, ["边缘"]), device(3, ["核心"], false)];
  it("只计已启用设备；标签按任一匹配", () => {
    expect(matchedDevices(emptyRuleDraft(), devices).map((d) => d.id)).toEqual([1, 2]);
    expect(
      matchedDevices({ ...emptyRuleDraft(), scopeType: "tags", tags: ["核心"] }, devices).map(
        (d) => d.id,
      ),
    ).toEqual([1]);
    expect(
      matchedDevices({ ...emptyRuleDraft(), scopeType: "device", deviceIds: [2, 3] }, devices).map(
        (d) => d.id,
      ),
    ).toEqual([2]);
  });
});
