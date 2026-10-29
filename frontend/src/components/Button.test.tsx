import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vite-plus/test";
import { Button } from "./Button.tsx";

describe("Button", () => {
  it("默认 type=button、gray 变体", () => {
    render(<Button>保存</Button>);
    const button = screen.getByRole("button", { name: "保存" });
    expect(button.getAttribute("type")).toBe("button");
    expect(button.className).toBe("btn btn-gray");
  });

  it("允许显式覆盖 type", () => {
    render(<Button type="submit">提交</Button>);
    expect(screen.getByRole("button").getAttribute("type")).toBe("submit");
  });

  it.each([
    ["primary", "btn btn-primary"],
    ["tinted", "btn btn-tinted"],
    ["gray", "btn btn-gray"],
    ["plain", "btn btn-plain"],
    ["danger", "btn btn-danger"],
    ["danger-tinted", "btn btn-danger-tinted"],
  ] as const)("variant=%s 生成类名 %s", (variant, expected) => {
    render(<Button variant={variant}>x</Button>);
    expect(screen.getByRole("button").className).toBe(expected);
  });

  it("尺寸、块级、矩形与仅图标修饰类", () => {
    render(
      <Button variant="primary" size="sm" block rect iconOnly aria-label="刷新" icon="refresh" />,
    );
    const button = screen.getByRole("button", { name: "刷新" });
    expect(button.className).toBe("btn btn-primary btn-sm btn-icon btn-block btn-rect");
    expect(button.querySelector("svg.i")).not.toBeNull();
  });

  it("图标位于文字之前，并保留自定义 className", () => {
    render(
      <Button icon="plus" className="extra">
        新增
      </Button>,
    );
    const button = screen.getByRole("button", { name: "新增" });
    expect(button.firstElementChild?.tagName.toLowerCase()).toBe("svg");
    expect(button.classList.contains("extra")).toBe(true);
  });

  it("点击触发 onClick，禁用时不触发", async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();
    const { rerender } = render(<Button onClick={onClick}>go</Button>);
    await user.click(screen.getByRole("button"));
    expect(onClick).toHaveBeenCalledTimes(1);
    rerender(
      <Button onClick={onClick} disabled>
        go
      </Button>,
    );
    await user.click(screen.getByRole("button"));
    expect(onClick).toHaveBeenCalledTimes(1);
  });
});
