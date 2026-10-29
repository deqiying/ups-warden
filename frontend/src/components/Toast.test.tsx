import { act, fireEvent, render, screen } from "@testing-library/react";
import { useEffect } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { ToastProvider } from "./Toast.tsx";
import { useToast } from "./toast-context.ts";
import type { ToastApi } from "./toast-context.ts";

/** 把 hook 返回的 API 取出来，便于在 act 中直接调用。 */
function setup() {
  const holder: { api: ToastApi | null } = { api: null };
  function Probe() {
    const api = useToast();
    useEffect(() => {
      holder.api = api;
    }, [api]);
    return null;
  }
  render(
    <ToastProvider>
      <Probe />
    </ToastProvider>,
  );
  const api = () => {
    if (!holder.api) throw new Error("ToastApi 尚未就绪");
    return holder.api;
  };
  return { api };
}

function advance(ms: number) {
  act(() => {
    vi.advanceTimersByTime(ms);
  });
}

describe("Toast", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("show 后出现，包含标题、说明与图标", () => {
    const { api } = setup();
    act(() => {
      api().show({
        tone: "info",
        title: "测试消息已加入队列",
        message: "结果稍后出现在发送记录中",
      });
    });
    const toast = screen.getByRole("status");
    expect(toast.className).toBe("toast");
    expect(toast.textContent).toContain("测试消息已加入队列");
    expect(toast.textContent).toContain("结果稍后出现在发送记录中");
    expect(toast.querySelector(".sev.sev-info")).not.toBeNull();
    expect(toast.parentElement?.className).toBe("toast-stack");
  });

  it("critical 用 role=alert，其余用 role=status", () => {
    const { api } = setup();
    act(() => {
      api().show({ tone: "critical", title: "市电中断", duration: null });
      api().show({ tone: "ok", title: "已保存", duration: null });
    });
    expect(screen.getByRole("alert").textContent).toContain("市电中断");
    expect(screen.getByRole("status").textContent).toContain("已保存");
  });

  it("默认 6 秒后自动消失", () => {
    const { api } = setup();
    act(() => {
      api().show({ tone: "info", title: "短暂提示" });
    });
    advance(5999);
    expect(screen.queryByText("短暂提示")).not.toBeNull();
    advance(1);
    expect(screen.queryByText("短暂提示")).toBeNull();
  });

  it("自定义 duration 生效；duration=null 常驻", () => {
    const { api } = setup();
    act(() => {
      api().show({ tone: "info", title: "两秒", duration: 2000 });
      api().show({ tone: "warning", title: "常驻", duration: null });
    });
    advance(2000);
    expect(screen.queryByText("两秒")).toBeNull();
    advance(10 * 60 * 1000);
    expect(screen.queryByText("常驻")).not.toBeNull();
  });

  it("悬停暂停计时，移开后只等剩余时间", () => {
    const { api } = setup();
    act(() => {
      api().show({ tone: "info", title: "悬停" });
    });
    advance(3000);
    const toast = screen.getByRole("status");
    fireEvent.mouseEnter(toast);
    advance(60_000);
    expect(screen.queryByText("悬停")).not.toBeNull();
    fireEvent.mouseLeave(toast);
    advance(2999);
    expect(screen.queryByText("悬停")).not.toBeNull();
    advance(1);
    expect(screen.queryByText("悬停")).toBeNull();
  });

  it("焦点在 toast 内时暂停计时，离开后恢复", () => {
    const { api } = setup();
    act(() => {
      api().show({ tone: "info", title: "聚焦" });
    });
    const toast = screen.getByRole("status");
    fireEvent.focusIn(toast);
    advance(60_000);
    expect(screen.queryByText("聚焦")).not.toBeNull();
    fireEvent.focusOut(toast, { relatedTarget: null });
    advance(6000);
    expect(screen.queryByText("聚焦")).toBeNull();
  });

  // 注意：假计时器下不使用 user-event——RTL 的 asyncWrapper 只识别 jest 假计时器，
  // 在 vitest 下会因等待真实 setTimeout 而挂起，所以这里用同步的 fireEvent。
  it("关闭按钮立即关闭该 toast", () => {
    const { api } = setup();
    act(() => {
      api().show({ tone: "info", title: "甲", duration: null });
      api().show({ tone: "info", title: "乙", duration: null });
    });
    const closeButtons = screen.getAllByRole("button", { name: "关闭" });
    expect(closeButtons).toHaveLength(2);
    fireEvent.click(closeButtons[0] as HTMLElement);
    expect(screen.queryByText("甲")).toBeNull();
    expect(screen.queryByText("乙")).not.toBeNull();
  });

  it("dismiss(id) 关闭指定 toast", () => {
    const { api } = setup();
    let id = 0;
    act(() => {
      id = api().show({ tone: "info", title: "待关闭", duration: null });
    });
    act(() => {
      api().dismiss(id);
    });
    expect(screen.queryByText("待关闭")).toBeNull();
  });

  it("操作按钮执行回调并关闭 toast，默认第一个为 tinted、其余为 plain", () => {
    const { api } = setup();
    const view = vi.fn();
    act(() => {
      api().show({
        tone: "info",
        title: "带操作",
        duration: null,
        actions: [
          { label: "查看发送记录", onClick: view },
          { label: "好", onClick: () => {} },
        ],
      });
    });
    expect(screen.getByRole("button", { name: "查看发送记录" }).className).toBe(
      "btn btn-tinted btn-sm",
    );
    expect(screen.getByRole("button", { name: "好" }).className).toBe("btn btn-plain btn-sm");
    fireEvent.click(screen.getByRole("button", { name: "查看发送记录" }));
    expect(view).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("带操作")).toBeNull();
  });

  it("最多同时显示 3 条，超出时移除最旧的", () => {
    const { api } = setup();
    act(() => {
      for (const title of ["一", "二", "三", "四"]) {
        api().show({ tone: "info", title, duration: null });
      }
    });
    expect(screen.queryByText("一")).toBeNull();
    expect(
      screen.getAllByRole("status").map((el) => el.querySelector(".t-headline")?.textContent),
    ).toEqual(["二", "三", "四"]);
  });

  it("dedupeKey 相同则替换内容而不是新增，并重新计时", () => {
    const { api } = setup();
    let first = 0;
    let second = 0;
    act(() => {
      first = api().show({ tone: "warning", title: "通讯中断", dedupeKey: "link" });
    });
    advance(4000);
    act(() => {
      second = api().show({ tone: "critical", title: "通讯仍未恢复", dedupeKey: "link" });
    });
    expect(second).toBe(first);
    expect(screen.queryByText("通讯中断")).toBeNull();
    expect(screen.getAllByRole("alert")).toHaveLength(1);
    expect(screen.getByText("通讯仍未恢复")).toBeTruthy();
    // 距首次 show 已 8 秒，但替换时重新计时，所以仍在
    advance(4000);
    expect(screen.queryByText("通讯仍未恢复")).not.toBeNull();
    advance(2000);
    expect(screen.queryByText("通讯仍未恢复")).toBeNull();
  });

  it("不同 dedupeKey 不互相替换", () => {
    const { api } = setup();
    act(() => {
      api().show({ tone: "info", title: "甲", dedupeKey: "a", duration: null });
      api().show({ tone: "info", title: "乙", dedupeKey: "b", duration: null });
    });
    expect(screen.getAllByRole("status")).toHaveLength(2);
  });

  it("在 ToastProvider 外调用 useToast 会抛出带说明的错误", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    function Orphan() {
      useToast();
      return null;
    }
    expect(() => render(<Orphan />)).toThrow(/ToastProvider/);
    spy.mockRestore();
  });
});
