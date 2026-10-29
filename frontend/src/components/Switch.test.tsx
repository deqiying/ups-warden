import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vite-plus/test";
import { Switch } from "./Switch.tsx";

describe("Switch", () => {
  it("未选中时点击传出 true，且自身不切换", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Switch checked={false} onChange={onChange} aria-label="启用" />);
    const toggle = screen.getByRole("switch", { name: "启用" });
    expect(toggle.getAttribute("aria-checked")).toBe("false");
    await user.click(toggle);
    expect(onChange).toHaveBeenCalledExactlyOnceWith(true);
    // 受控：父组件没更新 checked 就保持原状
    expect(toggle.getAttribute("aria-checked")).toBe("false");
  });

  it("已选中时点击传出 false", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Switch checked onChange={onChange} aria-label="启用" />);
    const toggle = screen.getByRole("switch");
    expect(toggle.getAttribute("aria-checked")).toBe("true");
    await user.click(toggle);
    expect(onChange).toHaveBeenCalledExactlyOnceWith(false);
  });

  it("type=button，禁用时不触发", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Switch checked={false} onChange={onChange} disabled aria-label="启用" />);
    const toggle = screen.getByRole("switch");
    expect(toggle.getAttribute("type")).toBe("button");
    await user.click(toggle);
    expect(onChange).not.toHaveBeenCalled();
  });

  it("tone 与 size 对应修饰类", () => {
    render(<Switch checked onChange={() => {}} tone="indigo" size="sm" aria-label="演练" />);
    expect(screen.getByRole("switch").className).toBe("switch is-indigo switch-sm");
  });

  it("透传 id 与 aria-labelledby", () => {
    render(
      <>
        <span id="lbl">自动关机</span>
        <Switch id="sw" checked={false} onChange={() => {}} aria-labelledby="lbl" />
      </>,
    );
    const toggle = screen.getByRole("switch", { name: "自动关机" });
    expect(toggle.id).toBe("sw");
  });
});
