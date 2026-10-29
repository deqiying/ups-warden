import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vite-plus/test";
import type { Field } from "../api/types.ts";
import { FieldValue, Metric } from "./Metric.tsx";

function field(overrides: Partial<Field> = {}): Field {
  return { num: 0, bool: false, str: "", quality: "good", reason: "", ...overrides };
}

describe("FieldValue", () => {
  it("good 数值与单位", () => {
    const { container } = render(
      <FieldValue fieldKey="battery_capacity_pct" field={field({ num: 89 })} />,
    );
    expect(container.textContent).toBe("89%");
    expect(container.querySelector("small")?.textContent).toBe("%");
    expect(container.querySelector(".is-stale")).toBeNull();
  });

  it("非 good 显示「—」与原因，不渲染数值 0", () => {
    const { container } = render(
      <FieldValue
        fieldKey="battery_capacity_pct"
        field={field({ quality: "invalid", reason: "placeholder", num: 0 })}
      />,
    );
    const trigger = screen.getByRole("button", { name: "电池容量无数据" });
    expect(trigger.textContent).toContain("—");
    expect(screen.getByRole("tooltip", { hidden: true }).textContent).toBe("占位值");
    expect(container.textContent).not.toMatch(/\d/);
  });

  it("快照缺少该键按「—」加 Profile 未映射显示", () => {
    render(<FieldValue fieldKey="input_voltage[3]" field={undefined} />);
    expect(screen.getByRole("button", { name: "输入电压 L3无数据" })).toBeTruthy();
    expect(screen.getByRole("tooltip", { hidden: true }).textContent).toBe("Profile 未映射");
  });

  it("inline 模式把原因写在值下方", () => {
    const { container } = render(
      <FieldValue
        fieldKey="runtime_estimate"
        field={field({ quality: "unsupported", reason: "not_mapped" })}
        reasonMode="inline"
        className="v"
      />,
    );
    const na = container.querySelector(".na.v");
    expect(na?.textContent).toBe("—仅电池供电时估算");
    expect(na?.querySelector(".reason")?.textContent).toBe("仅电池供电时估算");
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("stale 字段或实时数据过期时保留读数并置灰", () => {
    const { container, rerender } = render(
      <FieldValue
        fieldKey="battery_capacity_pct"
        field={field({ num: 76, quality: "stale", reason: "comm_stale" })}
      />,
    );
    expect(container.querySelector(".is-stale")?.textContent).toBe("76%");
    rerender(<FieldValue fieldKey="battery_capacity_pct" field={field({ num: 76 })} stale />);
    expect(container.querySelector(".is-stale")?.textContent).toBe("76%");
  });

  it("估算的剩余时间显示“约”与「估算」标记", () => {
    const { container } = render(
      <FieldValue
        fieldKey="runtime_estimate"
        field={field({ num: 4860 })}
        runtimeSource="estimated"
      />,
    );
    expect(container.textContent).toBe("约 1 小时 21 分 估算");
    expect(container.querySelector(".badge")?.textContent).toBe("估算");
  });
});

describe("Metric", () => {
  it("默认用字段目录的中文名作为标签", () => {
    const { container } = render(
      <Metric fieldKey="input_freq" field={field({ num: 50 })}>
        <span className="extra">附加</span>
      </Metric>,
    );
    const metric = container.querySelector(".metric");
    expect(metric?.querySelector(".metric-label")?.textContent).toBe("输入频率");
    expect(metric?.querySelector(".metric-value")?.textContent).toBe("50.0Hz");
    expect(metric?.querySelector(".extra")?.textContent).toBe("附加");
  });

  it("自定义标签；非 good 时数值位置仍是「—」", () => {
    const { container } = render(
      <Metric
        label="负载"
        fieldKey="output_load_pct[1]"
        field={field({ quality: "unsupported", reason: "phase_inactive" })}
      />,
    );
    expect(container.querySelector(".metric-label")?.textContent).toBe("负载");
    const value = container.querySelector(".metric-value");
    expect(value?.classList.contains("na")).toBe(true);
    expect(value?.textContent).toContain("—");
    expect(screen.getByRole("tooltip", { hidden: true }).textContent).toBe("该相未启用");
  });
});
