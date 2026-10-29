import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vite-plus/test";
import { Segmented } from "./Segmented.tsx";

type Range = "day" | "week" | "month";

const OPTIONS: { value: Range; label: string; count?: number }[] = [
  { value: "day", label: "日" },
  { value: "week", label: "周", count: 3 },
  { value: "month", label: "月" },
];

describe("Segmented", () => {
  it("用 aria-pressed 标记当前项，容器为带名称的 group", () => {
    render(<Segmented options={OPTIONS} value="week" onChange={() => {}} aria-label="范围" />);
    expect(screen.getByRole("group", { name: "范围" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "日" }).getAttribute("aria-pressed")).toBe("false");
    expect(screen.getByRole("button", { name: /周/ }).getAttribute("aria-pressed")).toBe("true");
  });

  it("渲染 count 角标与尺寸/块级类", () => {
    const { container } = render(
      <Segmented
        options={OPTIONS}
        value="day"
        onChange={() => {}}
        aria-label="范围"
        size="lg"
        block
      />,
    );
    expect(container.querySelector(".count")?.textContent).toBe("3");
    expect(container.firstElementChild?.className).toContain("seg seg-lg seg-block");
  });

  it("点击选项调用 onChange，且不自行切换", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Segmented options={OPTIONS} value="day" onChange={onChange} aria-label="范围" />);
    await user.click(screen.getByRole("button", { name: "月" }));
    expect(onChange).toHaveBeenCalledExactlyOnceWith("month");
    expect(screen.getByRole("button", { name: "日" }).getAttribute("aria-pressed")).toBe("true");
  });

  it("左右方向键在选项间移动并选中，首尾循环", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Segmented options={OPTIONS} value="day" onChange={onChange} aria-label="范围" />);
    screen.getByRole("button", { name: "日" }).focus();
    await user.keyboard("{ArrowRight}");
    expect(onChange).toHaveBeenLastCalledWith("week");
    expect(document.activeElement).toBe(screen.getByRole("button", { name: /周/ }));
    await user.keyboard("{ArrowLeft}{ArrowLeft}");
    // 从「周」向左两次：日 → 月（循环）
    expect(onChange).toHaveBeenLastCalledWith("month");
  });

  it("禁用时按钮不可用", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <Segmented options={OPTIONS} value="day" onChange={onChange} aria-label="范围" disabled />,
    );
    await user.click(screen.getByRole("button", { name: /周/ }));
    expect(onChange).not.toHaveBeenCalled();
  });

  it("首次测量写入滑块变量，并临时加 no-anim 防止从左缘飞入", async () => {
    render(<Segmented options={OPTIONS} value="week" onChange={() => {}} aria-label="范围" />);
    const group = screen.getByRole("group");
    expect(group.style.getPropertyValue("--seg-w")).not.toBe("");
    expect(group.style.getPropertyValue("--seg-x")).not.toBe("");
    expect(group.classList.contains("no-anim")).toBe(true);
    await waitFor(() => expect(group.classList.contains("no-anim")).toBe(false));
  });
});
