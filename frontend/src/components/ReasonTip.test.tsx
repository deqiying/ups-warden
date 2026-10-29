import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vite-plus/test";
import { ReasonTip } from "./ReasonTip.tsx";

function tooltip(): HTMLElement {
  return screen.getByRole("tooltip", { hidden: true });
}

describe("ReasonTip", () => {
  it("触发器是可聚焦按钮，屏幕阅读器名称与提示内容通过 aria-describedby 关联", () => {
    render(<ReasonTip reason="Profile 未映射" label="输入电压无数据" />);
    const trigger = screen.getByRole("button", { name: "输入电压无数据" });
    expect(trigger.getAttribute("type")).toBe("button");
    expect(trigger.getAttribute("aria-describedby")).toBe(tooltip().id);
    expect(tooltip().textContent).toBe("Profile 未映射");
    expect(tooltip().hidden).toBe(true);
    // 可见内容默认是「—」，对读屏隐藏。
    expect(trigger.querySelector('[aria-hidden="true"]')?.textContent).toBe("—");
  });

  it("键盘聚焦显示，Esc 隐藏，失焦后再次聚焦可重新显示", async () => {
    const user = userEvent.setup();
    render(
      <>
        <ReasonTip reason="设备不支持" />
        <button type="button">下一个</button>
      </>,
    );
    await user.tab();
    expect(tooltip().hidden).toBe(false);
    await user.keyboard("{Escape}");
    expect(tooltip().hidden).toBe(true);
    await user.tab();
    await user.tab({ shift: true });
    expect(tooltip().hidden).toBe(false);
    await user.tab();
    expect(tooltip().hidden).toBe(true);
  });

  it("悬停显示，移开隐藏", async () => {
    const user = userEvent.setup();
    render(<ReasonTip reason="占位值" />);
    const trigger = screen.getByRole("button");
    await user.hover(trigger);
    expect(tooltip().hidden).toBe(false);
    await user.unhover(trigger);
    expect(tooltip().hidden).toBe(true);
  });

  it("点按显示（触屏没有悬停）", async () => {
    const user = userEvent.setup();
    render(<ReasonTip reason="解码失败" />);
    await user.pointer({ keys: "[TouchA]", target: screen.getByRole("button") });
    expect(tooltip().hidden).toBe(false);
  });

  it("className 作用于外层", () => {
    const { container } = render(<ReasonTip reason="x" className="metric-value na" />);
    expect(container.firstElementChild?.className).toBe("reason-tip metric-value na");
  });
});
