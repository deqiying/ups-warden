import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vite-plus/test";
import { Meter } from "./Meter.tsx";

describe("Meter", () => {
  it.each([
    [0.924, "92", "92.4%"],
    [0, "0", "0%"],
    [1, "100", "100%"],
    [2.5, "100", "100%"],
    [-1, "0", "0%"],
    [Number.NaN, "0", "0%"],
  ])("fraction=%s → valuenow %s、宽度 %s", (fraction, now, width) => {
    const { container } = render(<Meter fraction={fraction} label="负载" />);
    const meter = screen.getByRole("meter", { name: "负载" });
    expect(meter.getAttribute("aria-valuenow")).toBe(now);
    expect((container.querySelector(".meter > span") as HTMLElement).style.width).toBe(width);
  });

  it("暴露 0～100 范围，颜色写入 --c", () => {
    const { container } = render(
      <Meter fraction={0.5} label="负载 L1" color="var(--orange)" className="wide" />,
    );
    const meter = screen.getByRole("meter");
    expect(meter.getAttribute("aria-valuemin")).toBe("0");
    expect(meter.getAttribute("aria-valuemax")).toBe("100");
    expect(meter.className).toBe("meter wide");
    expect(
      (container.querySelector(".meter > span") as HTMLElement).style.getPropertyValue("--c"),
    ).toBe("var(--orange)");
  });
});
