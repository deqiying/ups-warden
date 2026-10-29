import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vite-plus/test";
import { queryKeys } from "../api/hooks.ts";
import type { LiveConnection } from "../live/connection.ts";
import { LiveProvider } from "../live/LiveProvider.tsx";
import { LiveStore } from "../live/store.ts";
import { settings, T0 } from "../test/fixtures.ts";
import { LiveBanner } from "./LiveBanner.tsx";

function setup() {
  const store = new LiveStore();
  const reconnectNow = vi.fn();
  const connection = { reconnectNow } as unknown as LiveConnection;
  const queryClient = new QueryClient();
  // 预置设置，避免发出请求；时区取 Asia/Shanghai。
  queryClient.setQueryData(queryKeys.settings, settings());
  render(
    <QueryClientProvider client={queryClient}>
      <LiveProvider store={store} connection={connection}>
        <LiveBanner />
      </LiveProvider>
    </QueryClientProvider>,
  );
  return { store, reconnectNow };
}

describe("LiveBanner", () => {
  it("未断线时不显示", () => {
    const { store } = setup();
    expect(screen.queryByRole("status")).toBeNull();
    act(() => store.setConnection({ phase: "open" }));
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("等待重连时显示次数、倒计时与数据截止时刻，可立即重连", async () => {
    // 只伪造时钟与 useNow 的 setInterval：倒计时可确定推进，userEvent 的 setTimeout 照常运行。
    vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] });
    try {
      const user = userEvent.setup();
      const { store, reconnectNow } = setup();
      act(() =>
        store.setConnection({
          phase: "offline",
          attempt: 2,
          disconnectedAt: T0,
          retryAt: Date.now() + 5000,
        }),
      );
      const banner = screen.getByRole("status");
      expect(banner.textContent).toContain("实时连接已断开");
      expect(banner.textContent).toContain("正在第 2 次重连，5 秒后重试。");
      // T0 = 06:20:58Z，按设置时区 Asia/Shanghai 显示。
      expect(banner.textContent).toContain("下方是 14:20:58 之前的数据");

      act(() => {
        vi.advanceTimersByTime(2000);
      });
      expect(banner.textContent).toContain("3 秒后重试。");

      await user.click(screen.getByRole("button", { name: "立即重连" }));
      expect(reconnectNow).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it("探测会话期间与重连中的文案；正在连接时不提供重连按钮", () => {
    const { store } = setup();
    act(() => store.setConnection({ phase: "offline", attempt: 1, disconnectedAt: T0 }));
    expect(screen.getByRole("status").textContent).toContain("正在检查登录状态…");

    act(() => store.setConnection({ phase: "connecting" }));
    expect(screen.getByRole("status").textContent).toContain("正在重新连接并同步…");
    expect(screen.queryByRole("button", { name: "立即重连" })).toBeNull();
  });

  it("重连后收到 sync（disconnectedAt 清空）即隐藏", () => {
    const { store } = setup();
    act(() => store.setConnection({ phase: "open", disconnectedAt: T0 }));
    expect(screen.getByRole("status")).toBeTruthy();
    act(() => store.setConnection({ disconnectedAt: null, attempt: 0 }));
    expect(screen.queryByRole("status")).toBeNull();
  });
});
