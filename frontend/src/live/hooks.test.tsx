import { act, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vite-plus/test";
import { wireDevice, wireSync } from "../test/fixtures.ts";
import { shallowArrayEqual, useLive, useLiveStale } from "./hooks.ts";
import { LiveProvider } from "./LiveProvider.tsx";
import { LiveStore } from "./store.ts";

function OnBattery({ onRender }: { onRender: () => void }) {
  // 内联选择器每次渲染都是新函数，且返回新数组：缓存与 isEqual 必须避免无限重渲染。
  const devices = useLive(
    (s) => s.data.devices.filter((d) => d.power_state === "on_battery"),
    shallowArrayEqual,
  );
  onRender();
  return <span data-testid="ids">{devices.map((d) => d.device_id).join(",")}</span>;
}

function Stale() {
  return <span data-testid="stale">{String(useLiveStale())}</span>;
}

describe("useLive", () => {
  it("内联选择器不会导致无限渲染；无关变化不重渲染，相关变化更新", () => {
    const store = new LiveStore();
    act(() => {
      store.dispatch({
        type: "sync",
        data: wireSync({
          devices: [
            wireDevice({ device_id: 1, power_state: "on_battery" }),
            wireDevice({ device_id: 2 }),
          ],
        }),
      });
    });
    const onRender = vi.fn();
    render(
      <LiveProvider store={store}>
        <OnBattery onRender={onRender} />
      </LiveProvider>,
    );
    expect(screen.getByTestId("ids").textContent).toBe("1");
    const renders = onRender.mock.calls.length;

    // 连接状态变化与另一台设备更新都不影响选择结果（设备 1 保持原引用）。
    act(() => store.setConnection({ phase: "open" }));
    act(() => store.dispatch({ type: "device_state", data: wireDevice({ device_id: 2 }) }));
    expect(onRender).toHaveBeenCalledTimes(renders);

    act(() =>
      store.dispatch({
        type: "device_state",
        data: wireDevice({ device_id: 2, power_state: "on_battery" }),
      }),
    );
    expect(screen.getByTestId("ids").textContent).toBe("1,2");
    expect(onRender).toHaveBeenCalledTimes(renders + 1);
  });

  it("useLiveStale 跟随连接状态", () => {
    const store = new LiveStore();
    render(
      <LiveProvider store={store}>
        <Stale />
      </LiveProvider>,
    );
    expect(screen.getByTestId("stale").textContent).toBe("false");
    act(() => store.setConnection({ phase: "offline", disconnectedAt: "2026-10-27T06:20:58Z" }));
    expect(screen.getByTestId("stale").textContent).toBe("true");
  });

  it("在 LiveProvider 之外使用时报错", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    expect(() => render(<Stale />)).toThrow("useLive* 必须在 <LiveProvider> 内使用");
    vi.restoreAllMocks();
  });
});

describe("shallowArrayEqual", () => {
  it("逐项引用比较", () => {
    const a = { id: 1 };
    expect(shallowArrayEqual([a], [a])).toBe(true);
    expect(shallowArrayEqual([a], [{ id: 1 }])).toBe(false);
    expect(shallowArrayEqual([a], [a, a])).toBe(false);
    expect(shallowArrayEqual([], [])).toBe(true);
  });
});
