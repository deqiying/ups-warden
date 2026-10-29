import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";

import { FIELD_CATALOG, fieldLabel, lookupField, phaseKeys, splitFieldKey } from "./fields.ts";

function spec(key: string) {
  const found = FIELD_CATALOG.find((s) => s.key === key);
  if (!found) throw new Error(`目录中缺少字段 ${key}`);
  return found;
}

describe("FIELD_CATALOG", () => {
  it("条目数与 backend/internal/model/catalog.go 一致（49）", () => {
    expect(FIELD_CATALOG.length).toBe(49);
  });

  it("key 唯一且按字典序升序", () => {
    const keys = FIELD_CATALOG.map((s) => s.key);
    expect(new Set(keys).size).toBe(keys.length);
    expect(keys).toEqual([...keys].sort((a, b) => (a < b ? -1 : a > b ? 1 : 0)));
  });

  it("每个条目都有不等于 key 的中文 label", () => {
    for (const s of FIELD_CATALOG) {
      expect(s.label, s.key).not.toBe(s.key);
      expect(s.label, s.key).toMatch(/[\u4e00-\u9fff]/);
    }
  });

  it("enum 类型条目都有取值，非 enum 条目取值为空", () => {
    for (const s of FIELD_CATALOG) {
      if (s.kind === "enum") expect(s.enum.length, s.key).toBeGreaterThan(0);
      else expect(s.enum, s.key).toEqual([]);
    }
  });

  it("功率单位：input_power 为 W，output_power / bypass_power 为 VA", () => {
    expect(spec("input_power").unit).toBe("W");
    expect(spec("output_power").unit).toBe("VA");
    expect(spec("bypass_power").unit).toBe("VA");
  });

  it.each(["input", "output", "bypass"])(
    "%s 的电压/电流/功率按相，频率不按相但进入历史",
    (group) => {
      for (const suffix of ["voltage", "current", "power"]) {
        expect(spec(`${group}_${suffix}`).phased, `${group}_${suffix}`).toBe(true);
      }
      const freq = spec(`${group}_freq`);
      expect(freq.phased).toBe(false);
      expect(freq.history).toBe(true);
      expect(freq.unit).toBe("Hz");
    },
  );

  it("output_load_pct 按相且单位为 %", () => {
    const s = spec("output_load_pct");
    expect(s.phased).toBe(true);
    expect(s.unit).toBe("%");
  });

  it("temperature 单位为 °C", () => {
    expect(spec("temperature").unit).toBe("°C");
  });

  it("on_battery_elapsed / runtime_estimate 为派生的 duration", () => {
    for (const key of ["on_battery_elapsed", "runtime_estimate"]) {
      expect(spec(key).kind, key).toBe("duration");
      expect(spec(key).derived, key).toBe(true);
    }
  });

  it("self_test_result 为 text", () => {
    expect(spec("self_test_result").kind).toBe("text");
  });

  it("power_state 的 enum 取值", () => {
    expect(spec("power_state").enum).toEqual([
      "online",
      "on_battery",
      "bypass",
      "fault",
      "shutdown",
      "unknown",
    ]);
  });

  it("只有状态引擎派生字段 derived 为 true", () => {
    const derived = FIELD_CATALOG.filter((s) => s.derived).map((s) => s.key);
    expect(derived).toEqual([
      "comm_state",
      "on_battery_elapsed",
      "power_state",
      "runtime_estimate",
      "runtime_source",
    ]);
  });
});

describe("splitFieldKey", () => {
  it("合法相键", () => {
    expect(splitFieldKey("input_voltage[2]")).toEqual({ base: "input_voltage", phase: 2 });
    expect(splitFieldKey("output_power[1]")).toEqual({ base: "output_power", phase: 1 });
    expect(splitFieldKey("bypass_current[3]")).toEqual({ base: "bypass_current", phase: 3 });
  });

  it("相号越界或格式非法 → null", () => {
    expect(splitFieldKey("input_voltage[4]")).toBeNull();
    expect(splitFieldKey("input_voltage[0]")).toBeNull();
    expect(splitFieldKey("a]")).toBeNull();
    expect(splitFieldKey("input_voltage[")).toBeNull();
    expect(splitFieldKey("input_voltage[12]")).toBeNull();
    expect(splitFieldKey("input_voltage[a]")).toBeNull();
    expect(splitFieldKey("[1]")).toBeNull();
  });

  it("无方括号 → phase 0", () => {
    expect(splitFieldKey("input_voltage")).toEqual({ base: "input_voltage", phase: 0 });
    expect(splitFieldKey("")).toEqual({ base: "", phase: 0 });
  });
});

describe("lookupField", () => {
  let warn: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    warn = vi.spyOn(console, "warn").mockImplementation(() => {});
  });

  afterEach(() => {
    warn.mockRestore();
  });

  it("基础键返回规格，且不警告", () => {
    expect(lookupField("input_voltage")).toBe(spec("input_voltage"));
    expect(lookupField("battery_voltage")).toBe(spec("battery_voltage"));
    expect(warn).not.toHaveBeenCalled();
  });

  it("按相字段的合法相键返回规格", () => {
    expect(lookupField("input_voltage[2]")).toBe(spec("input_voltage"));
    expect(lookupField("output_load_pct[3]")).toBe(spec("output_load_pct"));
    expect(warn).not.toHaveBeenCalled();
  });

  it("非按相字段带相号 → undefined", () => {
    expect(lookupField("battery_voltage[1]")).toBeUndefined();
    expect(lookupField("input_freq[1]")).toBeUndefined();
  });

  it("相号越界 → undefined", () => {
    expect(lookupField("input_voltage[4]")).toBeUndefined();
    expect(lookupField("input_voltage[0]")).toBeUndefined();
  });

  it("未知键 → undefined，同一 key 只警告一次", () => {
    expect(lookupField("no_such_field_a")).toBeUndefined();
    expect(lookupField("no_such_field_a")).toBeUndefined();
    expect(warn.mock.calls.length).toBeLessThanOrEqual(1);
    // 开发模式（vitest 默认 DEV=true）下应恰好警告一次。
    if (import.meta.env.DEV) {
      expect(warn).toHaveBeenCalledTimes(1);
      expect(String(warn.mock.calls[0]?.[0])).toContain("no_such_field_a");
    }
  });

  it("不同的未知键各自警告一次", () => {
    lookupField("no_such_field_b");
    lookupField("no_such_field_c");
    lookupField("no_such_field_b");
    if (import.meta.env.DEV) expect(warn).toHaveBeenCalledTimes(2);
    else expect(warn).not.toHaveBeenCalled();
  });
});

describe("fieldLabel", () => {
  beforeEach(() => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("基础键返回中文名", () => {
    expect(fieldLabel("input_voltage")).toBe("输入电压");
    expect(fieldLabel("temperature")).toBe("温度");
  });

  it("按相实例追加相号", () => {
    expect(fieldLabel("input_voltage[2]")).toBe("输入电压 L2");
    expect(fieldLabel("output_power[1]")).toBe("输出功率 L1");
  });

  it("未知键原样返回", () => {
    expect(fieldLabel("no_such_field_label")).toBe("no_such_field_label");
    expect(fieldLabel("battery_voltage[1]")).toBe("battery_voltage[1]");
  });
});

describe("phaseKeys", () => {
  it("生成 [1]～[N] 实例键", () => {
    expect(phaseKeys("input_voltage", 3)).toEqual([
      "input_voltage[1]",
      "input_voltage[2]",
      "input_voltage[3]",
    ]);
    expect(phaseKeys("input_voltage", 1)).toEqual(["input_voltage[1]"]);
  });

  it("phases 越界时夹取到 1～3", () => {
    expect(phaseKeys("input_voltage", 0)).toEqual(["input_voltage[1]"]);
    expect(phaseKeys("input_voltage", -2)).toEqual(["input_voltage[1]"]);
    expect(phaseKeys("input_voltage", 5)).toHaveLength(3);
    expect(phaseKeys("input_voltage", 5).at(-1)).toBe("input_voltage[3]");
  });

  it("小数向零取整", () => {
    expect(phaseKeys("x", 2.9)).toEqual(["x[1]", "x[2]"]);
  });
});
