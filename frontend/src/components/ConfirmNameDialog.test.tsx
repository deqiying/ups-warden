import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vite-plus/test";
import { ConfirmNameDialog } from "./ConfirmNameDialog.tsx";

const NAME = "Room-A";

function confirmButton() {
  return screen.getByRole<HTMLButtonElement>("button", { name: "真实执行" });
}

function renderDialog(props: Partial<Parameters<typeof ConfirmNameDialog>[0]> = {}) {
  const onConfirm = vi.fn();
  const onClose = vi.fn();
  const utils = render(
    <ConfirmNameDialog
      open
      onClose={onClose}
      title="手动执行「Room-A」"
      name={NAME}
      confirmLabel="真实执行"
      onConfirm={onConfirm}
      {...props}
    />,
  );
  return { onConfirm, onClose, ...utils };
}

describe("ConfirmNameDialog", () => {
  it("wide alertdialog，输入框由 label 关联，初始焦点在输入框", () => {
    renderDialog();
    expect(screen.getByRole("alertdialog").className).toBe("dialog dlg-wide");
    const input = screen.getByLabelText("输入「Room-A」以确认");
    expect(document.activeElement).toBe(input);
    expect(input.getAttribute("autocomplete")).toBe("off");
    expect(input.getAttribute("spellcheck")).toBe("false");
    expect(input.closest(".confirm-field")).not.toBeNull();
    expect(document.querySelector(".dialog-icon")?.className).toBe("dialog-icon is-danger");
  });

  it("支持自定义 label 与 placeholder", () => {
    renderDialog({ label: "计划名称", placeholder: "输入名称" });
    expect(screen.getByLabelText("计划名称").getAttribute("placeholder")).toBe("输入名称");
  });

  it("名称不完全一致（大小写、首尾空格、前缀）时确认禁用", async () => {
    const user = userEvent.setup();
    renderDialog();
    const input = screen.getByRole("textbox");
    expect(confirmButton().disabled).toBe(true);
    for (const wrong of ["room-a", " Room-A", "Room-A ", "Room-", "Room-AA"]) {
      await user.clear(input);
      await user.type(input, wrong);
      expect(confirmButton().disabled).toBe(true);
    }
  });

  it("一致后可确认，onConfirm 只调用一次并收到名称，成功后关闭", async () => {
    const user = userEvent.setup();
    let resolve!: () => void;
    const onConfirm = vi.fn(
      () =>
        new Promise<void>((res) => {
          resolve = res;
        }),
    );
    const { onClose } = renderDialog({ onConfirm });
    await user.type(screen.getByRole("textbox"), NAME);
    expect(confirmButton().disabled).toBe(false);
    // 请求未返回前连点两次，只应发出一次
    await user.dblClick(confirmButton());
    expect(onConfirm).toHaveBeenCalledTimes(1);
    expect(onConfirm).toHaveBeenCalledWith(NAME);
    expect(onClose).not.toHaveBeenCalled();
    await act(async () => {
      resolve();
    });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("回车在名称一致时提交，不一致时忽略", async () => {
    const user = userEvent.setup();
    const { onConfirm } = renderDialog();
    const input = screen.getByRole("textbox");
    await user.type(input, "Room{Enter}");
    expect(onConfirm).not.toHaveBeenCalled();
    await user.type(input, "-A{Enter}");
    expect(onConfirm).toHaveBeenCalledExactlyOnceWith(NAME);
  });

  it("confirmDisabled 为 true 时即使名称一致也禁用", async () => {
    const user = userEvent.setup();
    const { onConfirm } = renderDialog({ confirmDisabled: true });
    await user.type(screen.getByRole("textbox"), `${NAME}{Enter}`);
    expect(confirmButton().disabled).toBe(true);
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("children 渲染在确认输入框之上", () => {
    renderDialog({ children: <div data-testid="extra">dry-run</div> });
    const extra = screen.getByTestId("extra");
    const field = document.querySelector(".confirm-field") as HTMLElement;
    expect(extra.compareDocumentPosition(field) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("重新打开会清空输入", async () => {
    const user = userEvent.setup();
    const props = {
      onClose: () => {},
      title: "t",
      name: NAME,
      confirmLabel: "真实执行",
      onConfirm: () => {},
    };
    const { rerender } = render(<ConfirmNameDialog open {...props} />);
    await user.type(screen.getByRole("textbox"), NAME);
    expect(confirmButton().disabled).toBe(false);
    await act(async () => rerender(<ConfirmNameDialog open={false} {...props} />));
    await act(async () => rerender(<ConfirmNameDialog open {...props} />));
    expect((screen.getByRole("textbox") as HTMLInputElement).value).toBe("");
    expect(confirmButton().disabled).toBe(true);
  });

  it("busy 期间按钮禁用、Esc 无效；失败时显示错误并保持打开", async () => {
    const user = userEvent.setup();
    let reject!: (error: unknown) => void;
    const onConfirm = vi.fn(
      () =>
        new Promise<void>((_, rej) => {
          reject = rej;
        }),
    );
    const onClose = vi.fn();
    renderDialog({ onConfirm, onClose });
    await user.type(screen.getByRole("textbox"), NAME);
    await user.click(confirmButton());

    expect((screen.getByRole("button", { name: "处理中…" }) as HTMLButtonElement).disabled).toBe(
      true,
    );
    expect((screen.getByRole("button", { name: "取消" }) as HTMLButtonElement).disabled).toBe(true);
    await user.keyboard("{Escape}");
    expect(onClose).not.toHaveBeenCalled();

    await act(async () => {
      reject(new Error("计划正在运行"));
    });
    expect(screen.getByRole("alert").textContent).toBe("计划正在运行");
    expect(onClose).not.toHaveBeenCalled();
    expect(confirmButton().disabled).toBe(false);
  });

  it("取消按钮与 Esc 调用 onClose", async () => {
    const user = userEvent.setup();
    const { onClose } = renderDialog();
    await user.click(screen.getByRole("button", { name: "取消" }));
    await user.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledTimes(2);
  });
});
