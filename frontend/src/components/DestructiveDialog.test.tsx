import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vite-plus/test";
import { ApiError } from "../api/client.ts";
import { DestructiveDialog } from "./DestructiveDialog.tsx";

function deferred() {
  let resolve!: () => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<void>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("DestructiveDialog", () => {
  it("默认文案、danger 图标，初始焦点在取消按钮", () => {
    render(<DestructiveDialog open onClose={() => {}} title="删除设备？" onConfirm={() => {}} />);
    expect(screen.getByRole("alertdialog", { name: "删除设备？" })).toBeTruthy();
    expect(document.querySelector(".dialog-icon")?.className).toBe("dialog-icon is-danger");
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "取消" }));
    expect(screen.getByRole("button", { name: "删除" }).className).toContain("btn-danger");
  });

  it("渲染 consequences 列表", () => {
    render(
      <DestructiveDialog
        open
        onClose={() => {}}
        title="删除设备？"
        consequences={["历史采样", "关联的告警规则"]}
        onConfirm={() => {}}
      />,
    );
    expect(screen.getByText("将一并清除：")).toBeTruthy();
    expect(screen.getAllByRole("listitem").map((li) => li.textContent)).toEqual([
      "历史采样",
      "关联的告警规则",
    ]);
  });

  it("没有 consequences 时不渲染列表", () => {
    render(<DestructiveDialog open onClose={() => {}} title="t" onConfirm={() => {}} />);
    expect(screen.queryByText("将一并清除：")).toBeNull();
  });

  it("只有点击确认才调用 onConfirm；成功后调用 onClose", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn().mockResolvedValue(undefined);
    const onClose = vi.fn();
    render(
      <DestructiveDialog
        open
        onClose={onClose}
        title="t"
        confirmLabel="删除设备"
        onConfirm={onConfirm}
      />,
    );
    expect(onConfirm).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "删除设备" }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("取消按钮调用 onClose，不调用 onConfirm", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    const onClose = vi.fn();
    render(<DestructiveDialog open onClose={onClose} title="t" onConfirm={onConfirm} />);
    await user.click(screen.getByRole("button", { name: "取消" }));
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("409 显示停用建议与“改为停用”按钮，保持打开", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    const onDisableInstead = vi.fn();
    const onConfirm = vi.fn().mockRejectedValue(new ApiError({ status: 409, code: "conflict" }));
    render(
      <DestructiveDialog
        open
        onClose={onClose}
        title="t"
        onConfirm={onConfirm}
        onDisableInstead={onDisableInstead}
      />,
    );
    expect(screen.queryByRole("button", { name: "改为停用" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "删除" }));
    expect((await screen.findByRole("alert")).textContent).toBe(
      "仍有其他配置引用它，无法删除。可以改为停用。",
    );
    expect(onClose).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "改为停用" }));
    expect(onDisableInstead).toHaveBeenCalledTimes(1);
  });

  it("409 但没有提供 onDisableInstead 时不显示停用按钮", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn().mockRejectedValue(new ApiError({ status: 409, code: "conflict" }));
    render(<DestructiveDialog open onClose={() => {}} title="t" onConfirm={onConfirm} />);
    await user.click(screen.getByRole("button", { name: "删除" }));
    await screen.findByRole("alert");
    expect(screen.queryByRole("button", { name: "改为停用" })).toBeNull();
  });

  it("非 409 错误显示 err.message，按钮恢复可用", async () => {
    const user = userEvent.setup();
    const error = new ApiError({ status: 500, code: "internal" });
    const onConfirm = vi.fn().mockRejectedValue(error);
    const onClose = vi.fn();
    render(
      <DestructiveDialog
        open
        onClose={onClose}
        title="t"
        onConfirm={onConfirm}
        onDisableInstead={() => {}}
      />,
    );
    await user.click(screen.getByRole("button", { name: "删除" }));
    expect((await screen.findByRole("alert")).textContent).toBe(error.message);
    expect(screen.queryByRole("button", { name: "改为停用" })).toBeNull();
    expect(onClose).not.toHaveBeenCalled();
    expect((screen.getByRole("button", { name: "删除" }) as HTMLButtonElement).disabled).toBe(
      false,
    );
  });

  it("busy 期间两个按钮禁用且 Esc 无效，完成后关闭", async () => {
    const user = userEvent.setup();
    const pending = deferred();
    const onConfirm = vi.fn(() => pending.promise);
    const onClose = vi.fn();
    render(<DestructiveDialog open onClose={onClose} title="t" onConfirm={onConfirm} />);
    await user.click(screen.getByRole("button", { name: "删除" }));

    const confirm = screen.getByRole("button", { name: "处理中…" }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    expect((screen.getByRole("button", { name: "取消" }) as HTMLButtonElement).disabled).toBe(true);
    await user.keyboard("{Escape}");
    expect(onClose).not.toHaveBeenCalled();
    expect(onConfirm).toHaveBeenCalledTimes(1);

    await act(async () => {
      pending.resolve();
    });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("重新打开时清除上一次的错误", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn().mockRejectedValue(new Error("boom"));
    const { rerender } = render(
      <DestructiveDialog open onClose={() => {}} title="t" onConfirm={onConfirm} />,
    );
    await user.click(screen.getByRole("button", { name: "删除" }));
    expect((await screen.findByRole("alert")).textContent).toBe("boom");
    rerender(<DestructiveDialog open={false} onClose={() => {}} title="t" onConfirm={onConfirm} />);
    rerender(<DestructiveDialog open onClose={() => {}} title="t" onConfirm={onConfirm} />);
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
