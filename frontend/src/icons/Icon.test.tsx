import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vite-plus/test";
import { Icon, IconSprite } from "./Icon.tsx";
import { ICON_PATHS } from "./icons.ts";

describe("Icon", () => {
  it("迁移全部 69 个图标并生成 symbol", () => {
    expect(Object.keys(ICON_PATHS)).toHaveLength(69);
    const { container } = render(<IconSprite />);
    expect(container.querySelectorAll("symbol")).toHaveLength(69);
    expect(container.querySelector("symbol#i-battery")).not.toBeNull();
  });

  it("无 label 时作为装饰对辅助技术隐藏", () => {
    const { container } = render(<Icon name="bolt" />);
    const svg = container.querySelector("svg");
    expect(svg?.getAttribute("aria-hidden")).toBe("true");
    expect(svg?.querySelector("use")?.getAttribute("href")).toBe("#i-bolt");
  });

  it("有 label 时以 img 角色暴露", () => {
    render(<Icon name="alert" label="严重" />);
    expect(screen.getByRole("img", { name: "严重" })).toBeTruthy();
  });
});
