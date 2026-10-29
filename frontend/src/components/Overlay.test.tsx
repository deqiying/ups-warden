import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useRef, useState } from "react";
import { describe, expect, it, vi } from "vite-plus/test";
import { Overlay } from "./Overlay.tsx";

function Harness({ dismissible = true }: { dismissible?: boolean }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        打开
      </button>
      <Overlay open={open} onClose={() => setOpen(false)} dismissible={dismissible}>
        <div role="dialog" aria-label="测试浮层">
          <button type="button">甲</button>
          <button type="button">乙</button>
          <button type="button">丙</button>
        </div>
      </Overlay>
    </>
  );
}

async function openHarness(user: ReturnType<typeof userEvent.setup>) {
  const trigger = screen.getByRole("button", { name: "打开" });
  await user.click(trigger);
  return trigger;
}

describe("Overlay", () => {
  it("open=false 时不渲染任何内容", () => {
    render(
      <Overlay open={false} onClose={() => {}}>
        <div>内容</div>
      </Overlay>,
    );
    expect(document.querySelector(".overlay")).toBeNull();
    expect(screen.queryByText("内容")).toBeNull();
  });

  it("portal 到 body，渲染 scrim 与内容", () => {
    const { container } = render(
      <Overlay open onClose={() => {}}>
        <div>内容</div>
      </Overlay>,
    );
    expect(container.querySelector(".overlay")).toBeNull();
    const overlay = document.body.querySelector(":scope > .overlay");
    expect(overlay?.querySelector(".scrim")).not.toBeNull();
    expect(overlay?.textContent).toBe("内容");
  });

  it("打开后焦点进入第一个可聚焦元素", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await openHarness(user);
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "甲" }));
  });

  it("initialFocusRef 优先于第一个可聚焦元素", () => {
    function Probe() {
      const ref = useRef<HTMLButtonElement>(null);
      return (
        <Overlay open onClose={() => {}} initialFocusRef={ref}>
          <button type="button">甲</button>
          <button type="button" ref={ref}>
            乙
          </button>
        </Overlay>
      );
    }
    render(<Probe />);
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "乙" }));
  });

  it("没有可聚焦元素时聚焦容器本身", () => {
    render(
      <Overlay open onClose={() => {}}>
        <p>只有文字</p>
      </Overlay>,
    );
    expect(document.activeElement).toBe(document.querySelector(".overlay"));
  });

  it("Tab / Shift+Tab 在浮层内循环，不会逃出", async () => {
    const user = userEvent.setup();
    render(
      <>
        <button type="button">背景按钮</button>
        <Harness />
      </>,
    );
    await openHarness(user);
    await user.tab();
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "乙" }));
    await user.tab();
    await user.tab();
    // 丙 → 回到 甲
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "甲" }));
    await user.tab({ shift: true });
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "丙" }));
  });

  it("Esc 调用 onClose，关闭后焦点回到触发按钮", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const trigger = await openHarness(user);
    expect(screen.getByRole("dialog")).toBeTruthy();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("dismissible=false 时 Esc 无效", async () => {
    const user = userEvent.setup();
    render(<Harness dismissible={false} />);
    await openHarness(user);
    await user.keyboard("{Escape}");
    expect(screen.getByRole("dialog")).toBeTruthy();
  });

  it("closeOnScrim 才会在点击遮罩时关闭，且受 dismissible 约束", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    const { rerender } = render(
      <Overlay open onClose={onClose}>
        <button type="button">x</button>
      </Overlay>,
    );
    const scrim = () => document.querySelector(".scrim") as HTMLElement;
    await user.click(scrim());
    expect(onClose).not.toHaveBeenCalled();

    rerender(
      <Overlay open onClose={onClose} closeOnScrim>
        <button type="button">x</button>
      </Overlay>,
    );
    await user.click(scrim());
    expect(onClose).toHaveBeenCalledTimes(1);

    rerender(
      <Overlay open onClose={onClose} closeOnScrim dismissible={false}>
        <button type="button">x</button>
      </Overlay>,
    );
    await user.click(scrim());
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("两层叠放时只有最上层响应 Esc 与焦点陷阱", async () => {
    const user = userEvent.setup();
    const closeBottom = vi.fn();
    const closeTop = vi.fn();
    const layers = (topOpen: boolean) => (
      <>
        <Overlay open onClose={closeBottom}>
          <button type="button">下层按钮</button>
        </Overlay>
        <Overlay open={topOpen} onClose={closeTop}>
          <button type="button">上层按钮</button>
        </Overlay>
      </>
    );
    const { rerender } = render(layers(true));
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "上层按钮" }));

    await user.keyboard("{Escape}");
    expect(closeTop).toHaveBeenCalledTimes(1);
    expect(closeBottom).not.toHaveBeenCalled();

    // Tab 只在上层内循环
    await user.tab();
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "上层按钮" }));

    rerender(layers(false));
    await user.keyboard("{Escape}");
    expect(closeBottom).toHaveBeenCalledTimes(1);
    expect(closeTop).toHaveBeenCalledTimes(1);
  });

  it("打开期间锁定 body 滚动，多层时最后一层关闭才恢复", () => {
    document.body.style.overflow = "auto";
    const layers = (a: boolean, b: boolean) => (
      <>
        <Overlay open={a} onClose={() => {}}>
          <button type="button">a</button>
        </Overlay>
        <Overlay open={b} onClose={() => {}}>
          <button type="button">b</button>
        </Overlay>
      </>
    );
    const { rerender } = render(layers(false, false));
    expect(document.body.style.overflow).toBe("auto");
    rerender(layers(true, true));
    expect(document.body.style.overflow).toBe("hidden");
    rerender(layers(false, true));
    expect(document.body.style.overflow).toBe("hidden");
    rerender(layers(false, false));
    expect(document.body.style.overflow).toBe("auto");
    document.body.style.overflow = "";
  });
});
