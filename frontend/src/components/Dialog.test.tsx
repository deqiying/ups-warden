import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useRef, useState } from "react";
import { describe, expect, it, vi } from "vite-plus/test";
import { Dialog } from "./Dialog.tsx";

describe("Dialog", () => {
  it("alertdialog 语义：labelledby 指向标题，describedby 指向说明", () => {
    render(
      <Dialog
        open
        onClose={() => {}}
        title="取消运行？"
        message="尚未开始的步骤将被取消。"
        actions={<button type="button">继续</button>}
      />,
    );
    const dialog = screen.getByRole("alertdialog", { name: "取消运行？" });
    expect(dialog.getAttribute("aria-modal")).toBe("true");
    const describedBy = dialog.getAttribute("aria-describedby");
    expect(describedBy).toBeTruthy();
    expect(document.getElementById(describedBy ?? "")?.textContent).toBe(
      "尚未开始的步骤将被取消。",
    );
  });

  it("没有 message 时不设置 aria-describedby", () => {
    render(
      <Dialog open onClose={() => {}} title="t" actions={<button type="button">好</button>} />,
    );
    expect(screen.getByRole("alertdialog").getAttribute("aria-describedby")).toBeNull();
  });

  it("icon 与 tone 决定图标块类，wide 加 dlg-wide", () => {
    const { rerender } = render(
      <Dialog
        open
        onClose={() => {}}
        title="t"
        icon="alert"
        tone="warning"
        wide
        actions={<button type="button">好</button>}
      />,
    );
    expect(document.querySelector(".dialog-icon")?.className).toBe("dialog-icon is-warning");
    expect(screen.getByRole("alertdialog").className).toBe("dialog dlg-wide");

    rerender(
      <Dialog
        open
        onClose={() => {}}
        title="t"
        icon="info"
        actions={<button type="button">好</button>}
      />,
    );
    expect(document.querySelector(".dialog-icon")?.className).toBe("dialog-icon is-neutral");
    expect(screen.getByRole("alertdialog").className).toBe("dialog");
  });

  it("children 位于 message 与 actions 之间", () => {
    render(
      <Dialog
        open
        onClose={() => {}}
        title="t"
        message="m"
        actions={<button type="button">好</button>}
      >
        <p data-testid="extra">额外内容</p>
      </Dialog>,
    );
    const dialog = screen.getByRole("alertdialog");
    const classes = Array.from(dialog.children).map(
      (el) => el.getAttribute("data-testid") ?? el.className,
    );
    expect(classes).toEqual(["dialog-title", "dialog-msg", "extra", "dialog-actions"]);
  });

  it("Esc 调用 onClose；点击遮罩不会关闭", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(<Dialog open onClose={onClose} title="t" actions={<button type="button">好</button>} />);
    await user.click(document.querySelector(".scrim") as HTMLElement);
    expect(onClose).not.toHaveBeenCalled();
    await user.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("dismissible=false 时 Esc 无效", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(
      <Dialog
        open
        onClose={onClose}
        title="t"
        dismissible={false}
        actions={<button type="button">好</button>}
      />,
    );
    await user.keyboard("{Escape}");
    expect(onClose).not.toHaveBeenCalled();
  });

  it("initialFocusRef 获得初始焦点；关闭后焦点回到触发按钮", async () => {
    const user = userEvent.setup();
    function Harness() {
      const [open, setOpen] = useState(false);
      const cancelRef = useRef<HTMLButtonElement>(null);
      return (
        <>
          <button type="button" onClick={() => setOpen(true)}>
            触发
          </button>
          <Dialog
            open={open}
            onClose={() => setOpen(false)}
            title="t"
            initialFocusRef={cancelRef}
            actions={
              <>
                <button type="button">危险</button>
                <button type="button" ref={cancelRef} onClick={() => setOpen(false)}>
                  取消
                </button>
              </>
            }
          />
        </>
      );
    }
    render(<Harness />);
    const trigger = screen.getByRole("button", { name: "触发" });
    await user.click(trigger);
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "取消" }));
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });
});
