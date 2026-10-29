import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vite-plus/test";
import { Sheet } from "./Sheet.tsx";

describe("Sheet", () => {
  it("dialog 语义，标题通过 aria-labelledby 关联，可设置宽度", () => {
    render(
      <Sheet open onClose={() => {}} title="添加设备" width="480px">
        内容
      </Sheet>,
    );
    const sheet = screen.getByRole("dialog", { name: "添加设备" });
    expect(sheet.tagName).toBe("SECTION");
    expect(sheet.getAttribute("aria-modal")).toBe("true");
    // happy-dom 不解析 min()/calc()，这里只验证 width 被写入 style
    expect(sheet.style.width).toBe("480px");
  });

  it("透传 aria-describedby", () => {
    render(
      <Sheet open onClose={() => {}} title="t" aria-describedby="hint">
        <p id="hint">说明</p>
      </Sheet>,
    );
    expect(screen.getByRole("dialog").getAttribute("aria-describedby")).toBe("hint");
  });

  it("默认“取消”plain 按钮调用 onClose", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(<Sheet open onClose={onClose} title="t" />);
    const cancel = screen.getByRole("button", { name: "取消" });
    expect(cancel.className).toBe("btn btn-plain");
    await user.click(cancel);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("leading 与 trailing 自定义头部", () => {
    render(
      <Sheet
        open
        onClose={() => {}}
        title="t"
        leading={<button type="button">返回</button>}
        trailing={<button type="button">保存</button>}
      />,
    );
    expect(screen.queryByRole("button", { name: "取消" })).toBeNull();
    const head = document.querySelector(".sheet-head") as HTMLElement;
    expect(head.children).toHaveLength(3);
    expect(head.children[0]?.textContent).toBe("返回");
    expect(head.children[1]?.className).toBe("sheet-title");
    expect(head.children[2]?.textContent).toBe("保存");
  });

  it("点击遮罩与 Esc 都会关闭", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(<Sheet open onClose={onClose} title="t" />);
    await user.click(document.querySelector(".scrim") as HTMLElement);
    expect(onClose).toHaveBeenCalledTimes(1);
    await user.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("dismissible=false 时遮罩、Esc、默认取消按钮都不可用", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(<Sheet open onClose={onClose} title="t" dismissible={false} />);
    await user.click(document.querySelector(".scrim") as HTMLElement);
    await user.keyboard("{Escape}");
    await user.click(screen.getByRole("button", { name: "取消" }));
    expect(onClose).not.toHaveBeenCalled();
    expect((screen.getByRole("button", { name: "取消" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("非 bare 时 children 包进 .sheet-body；bare 时原样渲染", () => {
    const { rerender } = render(
      <Sheet open onClose={() => {}} title="t">
        <p data-testid="child">内容</p>
      </Sheet>,
    );
    expect(screen.getByTestId("child").parentElement?.className).toBe("sheet-body");

    rerender(
      <Sheet open onClose={() => {}} title="t" bare>
        <div className="steps" data-testid="child">
          步骤
        </div>
      </Sheet>,
    );
    expect(screen.getByTestId("child").parentElement?.tagName).toBe("SECTION");
    expect(document.querySelector(".sheet-body")).toBeNull();
  });

  it("open=false 时不渲染", () => {
    render(<Sheet open={false} onClose={() => {}} title="t" />);
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});
