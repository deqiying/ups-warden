import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vite-plus/test";
import { TokenInput, type TokenInputProps } from "./TokenInput.tsx";

/** 受控外壳：把 onChange 的结果回写到 values，同时暴露给断言。 */
function Harness({
  initial = [],
  onChange,
  ...rest
}: { initial?: string[]; onChange?: (next: string[]) => void } & Partial<TokenInputProps>) {
  const [values, setValues] = useState(initial);
  return (
    <TokenInput
      aria-label="Chat ID"
      {...rest}
      values={values}
      onChange={(next) => {
        onChange?.(next);
        setValues(next);
      }}
    />
  );
}

async function openInput(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "添加" }));
  return screen.getByRole<HTMLInputElement>("textbox");
}

describe("TokenInput", () => {
  it("渲染已有值，mono 时加 mono 类", () => {
    const { container } = render(<Harness initial={["a", "b"]} mono />);
    const tokens = container.querySelectorAll("span.token");
    expect(tokens).toHaveLength(2);
    expect(tokens[0]?.className).toBe("token mono");
    expect(screen.getByRole("group", { name: "Chat ID" })).toBeTruthy();
  });

  it("点「添加」后输入，回车提交并 trim", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness onChange={onChange} />);
    const input = await openInput(user);
    expect(document.activeElement).toBe(input);
    await user.type(input, "  1001  {Enter}");
    expect(onChange).toHaveBeenCalledExactlyOnceWith(["1001"]);
    expect(screen.getByRole("button", { name: "移除 1001" })).toBeTruthy();
  });

  it("失焦时提交非空草稿并收起输入框", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness onChange={onChange} />);
    const input = await openInput(user);
    await user.type(input, "tag-1");
    await user.tab();
    expect(onChange).toHaveBeenCalledExactlyOnceWith(["tag-1"]);
    expect(screen.queryByRole("textbox")).toBeNull();
  });

  it("空白输入被忽略，失焦后收起", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness onChange={onChange} />);
    const input = await openInput(user);
    await user.type(input, "   {Enter}");
    expect(onChange).not.toHaveBeenCalled();
    await user.tab();
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.queryByRole("textbox")).toBeNull();
  });

  it("移除按钮删除对应项", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness initial={["a", "b", "c"]} onChange={onChange} />);
    await user.click(screen.getByRole("button", { name: "移除 b" }));
    expect(onChange).toHaveBeenCalledExactlyOnceWith(["a", "c"]);
    expect(screen.queryByRole("button", { name: "移除 b" })).toBeNull();
  });

  it("重复值显示「已存在」且不提交", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness initial={["a"]} onChange={onChange} />);
    const input = await openInput(user);
    await user.type(input, "a{Enter}");
    expect(onChange).not.toHaveBeenCalled();
    const message = screen.getByText("已存在");
    expect(input.getAttribute("aria-invalid")).toBe("true");
    expect(input.getAttribute("aria-describedby")).toBe(message.id);
    expect(message.className).toContain("ink-red");
  });

  it("normalizeKey 参与去重比较", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <Harness initial={["Alpha"]} onChange={onChange} normalizeKey={(v) => v.toLowerCase()} />,
    );
    const input = await openInput(user);
    await user.type(input, "ALPHA{Enter}");
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByText("已存在")).toBeTruthy();
  });

  it("达到 max 后再提交显示「最多 N 项」", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness initial={["a", "b"]} max={2} onChange={onChange} />);
    const input = await openInput(user);
    await user.type(input, "c{Enter}");
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByText("最多 2 项")).toBeTruthy();
  });

  it("提交后恰好达到 max 时自动收起输入框", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness initial={["a"]} max={2} onChange={onChange} />);
    const input = await openInput(user);
    await user.type(input, "b{Enter}");
    expect(onChange).toHaveBeenCalledExactlyOnceWith(["a", "b"]);
    expect(screen.queryByRole("textbox")).toBeNull();
  });

  it("validate 返回错误时就地显示、不提交，修改后清除错误", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const validate = (v: string) => (/^-?\d+$/.test(v) ? null : "必须是整数");
    render(<Harness onChange={onChange} validate={validate} />);
    const input = await openInput(user);
    await user.type(input, "abc{Enter}");
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole("alert").textContent).toBe("必须是整数");

    // 失焦同样不会提交非法值，且保留输入框供修正
    await user.tab();
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole("textbox")).toBeTruthy();

    await user.clear(input);
    expect(screen.queryByRole("alert")).toBeNull();
    await user.type(input, "-100123{Enter}");
    expect(onChange).toHaveBeenCalledExactlyOnceWith(["-100123"]);
  });

  it("Esc 取消输入，不提交草稿", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness onChange={onChange} />);
    const input = await openInput(user);
    await user.type(input, "draft{Escape}");
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.getByRole("button", { name: "添加" })).toBeTruthy();
  });

  it("支持自定义添加按钮文案，禁用时不可操作", () => {
    render(<Harness initial={["a"]} addLabel="新增 Chat ID" disabled />);
    expect(
      (screen.getByRole("button", { name: "新增 Chat ID" }) as HTMLButtonElement).disabled,
    ).toBe(true);
    expect((screen.getByRole("button", { name: "移除 a" }) as HTMLButtonElement).disabled).toBe(
      true,
    );
  });
});
