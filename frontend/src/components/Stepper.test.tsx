import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vite-plus/test";
import { Stepper } from "./Stepper.tsx";

function setup(props: Partial<Parameters<typeof Stepper>[0]> = {}) {
  const onChange = vi.fn();
  const user = userEvent.setup();
  const utils = render(
    <Stepper
      value={60}
      onChange={onChange}
      min={0}
      max={100}
      aria-label="倒计时"
      unit="秒"
      {...props}
    />,
  );
  const input = screen.getByRole<HTMLInputElement>("textbox", { name: "倒计时" });
  return { onChange, user, input, ...utils };
}

describe("Stepper", () => {
  it("显示数值与单位，输入框使用 decimal 键盘", () => {
    const { input, container } = setup();
    expect(input.value).toBe("60");
    expect(input.getAttribute("inputmode")).toBe("decimal");
    expect(container.querySelector(".unit")?.textContent).toBe("秒");
  });

  it("按钮按步长增减，并以 number 调用 onChange", async () => {
    const { user, onChange } = setup({ step: 5 });
    await user.click(screen.getByRole("button", { name: "增加" }));
    expect(onChange).toHaveBeenLastCalledWith(65);
    await user.click(screen.getByRole("button", { name: "减少" }));
    expect(onChange).toHaveBeenLastCalledWith(55);
  });

  it("到达边界时禁用对应按钮", () => {
    const { rerender } = setup({ value: 0 });
    expect((screen.getByRole("button", { name: "减少" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "增加" }) as HTMLButtonElement).disabled).toBe(
      false,
    );
    rerender(<Stepper value={100} onChange={() => {}} min={0} max={100} aria-label="倒计时" />);
    expect((screen.getByRole("button", { name: "增加" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("输入超出范围的值，失焦时夹到边界且传出的是 number", async () => {
    const { user, input, onChange } = setup();
    await user.clear(input);
    await user.type(input, "150");
    await user.tab();
    expect(onChange).toHaveBeenCalledExactlyOnceWith(100);
    expect(typeof onChange.mock.calls[0]?.[0]).toBe("number");

    await user.clear(input);
    await user.type(input, "-8");
    await user.tab();
    expect(onChange).toHaveBeenLastCalledWith(0);
  });

  it("回车提交；随后失焦不会重复触发", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    function Controlled() {
      const [value, setValue] = useState(60);
      return (
        <Stepper
          value={value}
          onChange={(next) => {
            onChange(next);
            setValue(next);
          }}
          aria-label="倒计时"
        />
      );
    }
    render(<Controlled />);
    const input = screen.getByRole<HTMLInputElement>("textbox");
    await user.clear(input);
    await user.type(input, "30{Enter}");
    expect(onChange).toHaveBeenCalledExactlyOnceWith(30);
    await user.tab();
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(input.value).toBe("30");
  });

  it("非法输入不调用 onChange，并恢复为当前值", async () => {
    const { user, input, onChange } = setup();
    await user.clear(input);
    await user.type(input, "abc");
    await user.tab();
    expect(onChange).not.toHaveBeenCalled();
    expect(input.value).toBe("60");

    await user.clear(input);
    await user.tab();
    expect(onChange).not.toHaveBeenCalled();
    expect(input.value).toBe("60");

    await user.type(input, "1e3");
    await user.tab();
    expect(onChange).not.toHaveBeenCalled();
    expect(input.value).toBe("60");
  });

  it("整数步长取整，小数步长保留小数", async () => {
    const first = setup();
    await first.user.clear(first.input);
    await first.user.type(first.input, "12.7");
    await first.user.tab();
    expect(first.onChange).toHaveBeenCalledExactlyOnceWith(13);
    first.unmount();

    const second = setup({ value: 1, step: 0.5, min: 0, max: 10 });
    await second.user.clear(second.input);
    await second.user.type(second.input, "2.25");
    await second.user.tab();
    expect(second.onChange).toHaveBeenCalledExactlyOnceWith(2.25);
  });

  it("小数步长按钮不累积浮点误差", async () => {
    const { user, onChange } = setup({ value: 0.2, step: 0.1, max: 1 });
    await user.click(screen.getByRole("button", { name: "增加" }));
    expect(onChange).toHaveBeenCalledExactlyOnceWith(0.3);
  });

  it("外部 value 变化时同步草稿", () => {
    const { rerender, input } = setup();
    rerender(<Stepper value={75} onChange={() => {}} min={0} max={100} aria-label="倒计时" />);
    expect(input.value).toBe("75");
  });

  it("禁用时输入框与按钮都不可用", () => {
    const { input } = setup({ disabled: true });
    expect(input.disabled).toBe(true);
    expect((screen.getByRole("button", { name: "增加" }) as HTMLButtonElement).disabled).toBe(true);
  });
});
