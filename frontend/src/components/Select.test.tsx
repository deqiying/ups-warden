import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vite-plus/test";
import { Select } from "./Select.tsx";

const OPTIONS = [
  { value: "utc", label: "UTC" },
  { value: "Asia/Shanghai", label: "Asia/Shanghai" },
  { value: "x", label: "不可选", disabled: true },
];

describe("Select", () => {
  it("选择后以字符串 value 调用 onChange", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Select options={OPTIONS} value="utc" onChange={onChange} aria-label="时区" />);
    await user.selectOptions(screen.getByRole("combobox", { name: "时区" }), "Asia/Shanghai");
    expect(onChange).toHaveBeenCalledExactlyOnceWith("Asia/Shanghai");
    expect(typeof onChange.mock.calls[0]?.[0]).toBe("string");
  });

  it("使用原生 select，外层 select-wrap 带装饰 chevron", () => {
    const { container } = render(
      <Select options={OPTIONS} value="utc" onChange={() => {}} aria-label="时区" />,
    );
    const wrap = container.firstElementChild;
    expect(wrap?.className).toBe("select-wrap");
    const select = screen.getByRole<HTMLSelectElement>("combobox");
    expect(select.tagName).toBe("SELECT");
    expect(select.className).toBe("select");
    expect(select.value).toBe("utc");
    const chev = wrap?.querySelector(".chev");
    expect(chev?.getAttribute("aria-hidden")).toBe("true");
    expect(chev?.querySelector("use")?.getAttribute("href")).toBe("#i-chev-updown");
  });

  it("plain、className、style 与透传属性", () => {
    const { container } = render(
      <Select
        options={OPTIONS}
        value="utc"
        onChange={() => {}}
        plain
        className="wide"
        style={{ minWidth: 150 }}
        id="tz"
        aria-label="时区"
        disabled
      />,
    );
    const wrap = container.firstElementChild as HTMLElement;
    expect(wrap.className).toBe("select-wrap wide");
    expect(wrap.style.minWidth).toBe("150px");
    const select = screen.getByRole<HTMLSelectElement>("combobox");
    expect(select.className).toBe("select is-plain");
    expect(select.id).toBe("tz");
    expect(select.disabled).toBe(true);
  });

  it("option 的 disabled 生效", () => {
    render(<Select options={OPTIONS} value="utc" onChange={() => {}} aria-label="时区" />);
    const option = screen.getByRole<HTMLOptionElement>("option", { name: "不可选" });
    expect(option.disabled).toBe(true);
  });
});
