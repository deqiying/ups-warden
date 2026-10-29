import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vite-plus/test";
import { Ring } from "./Ring.tsx";

function dash(container: HTMLElement) {
  return container.querySelector("circle.bar")?.getAttribute("stroke-dasharray");
}

describe("Ring", () => {
  it.each([
    [0.5, "50 100"],
    [0, "0 100"],
    [1, "100 100"],
    [1.7, "100 100"],
    [-0.3, "0 100"],
    [Number.NaN, "0 100"],
  ])("fraction=%s → dasharray %s", (fraction, expected) => {
    const { container } = render(<Ring fraction={fraction} />);
    expect(dash(container)).toBe(expected);
  });

  it("默认尺寸 64、描边 5，半径与 viewBox 对应", () => {
    const { container } = render(<Ring fraction={0.3} />);
    const svg = container.querySelector("svg.ring");
    expect(svg?.getAttribute("viewBox")).toBe("0 0 64 64");
    const bar = container.querySelector("circle.bar");
    expect(bar?.getAttribute("r")).toBe("29.5");
    expect(bar?.getAttribute("cx")).toBe("32");
    expect(bar?.getAttribute("pathLength")).toBe("100");
    expect(container.querySelector("circle.track")).not.toBeNull();
  });

  it("颜色写入 bar 的 style.stroke，默认 accent", () => {
    const first = render(<Ring fraction={0.3} />);
    expect((first.container.querySelector("circle.bar") as SVGElement).style.stroke).toBe(
      "var(--accent)",
    );
    first.unmount();
    const second = render(<Ring fraction={0.3} color="var(--orange)" />);
    expect((second.container.querySelector("circle.bar") as SVGElement).style.stroke).toBe(
      "var(--orange)",
    );
  });

  it("自定义 size / stroke，并渲染中心内容", () => {
    const { container } = render(
      <Ring fraction={0.3} size={100} stroke={10}>
        <b>47</b>
      </Ring>,
    );
    expect(container.querySelector("svg")?.getAttribute("viewBox")).toBe("0 0 100 100");
    expect(container.querySelector("circle.bar")?.getAttribute("r")).toBe("45");
    expect(container.querySelector(".ring-center")?.textContent).toBe("47");
  });

  it("有 label 时为 img，否则对读屏隐藏", () => {
    const labelled = render(<Ring fraction={0.3} label="剩余 47 秒" />);
    expect(screen.getByRole("img", { name: "剩余 47 秒" })).toBeTruthy();
    labelled.unmount();
    const { container } = render(<Ring fraction={0.3} />);
    expect(container.firstElementChild?.getAttribute("aria-hidden")).toBe("true");
    expect(container.firstElementChild?.getAttribute("role")).toBeNull();
  });
});
