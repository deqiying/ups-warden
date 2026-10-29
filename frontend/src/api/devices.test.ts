import { describe, expect, it } from "vite-plus/test";
import type { Source } from "./types.ts";
import { fetchAllPages, mapLimit, normalizeSource } from "./devices.ts";

describe("api/devices", () => {
  it("逐页读取到 next_before=0", async () => {
    const calls: (number | undefined)[] = [];
    const items = await fetchAllPages(async (before) => {
      calls.push(before);
      if (before === undefined) return { items: [3, 2], next_before: 2 };
      return { items: [1], next_before: 0 };
    });
    expect(items).toEqual([3, 2, 1]);
    expect(calls).toEqual([undefined, 2]);
  });

  it("游标不前进时停止，避免无限翻页", async () => {
    let count = 0;
    const items = await fetchAllPages(async () => {
      count++;
      return { items: [1], next_before: 5 };
    });
    expect(count).toBe(2);
    expect(items).toEqual([1, 1]);
  });

  it("mapLimit 限制并发并保持结果顺序", async () => {
    let running = 0;
    let peak = 0;
    const result = await mapLimit([1, 2, 3, 4, 5, 6, 7], 3, async (n) => {
      running++;
      peak = Math.max(peak, running);
      await new Promise((r) => setTimeout(r, 5 * (8 - n)));
      running--;
      return n * 10;
    });
    expect(result).toEqual([10, 20, 30, 40, 50, 60, 70]);
    expect(peak).toBe(3);
  });

  it("数据源：status 缺省为 null，Go 零值 last_ok_at 为 null", () => {
    const base = {
      id: 1,
      device_id: 1,
      protocol: "snmp",
      host: "127.0.0.1",
      port: 161,
      params: { version: "2c" },
      profile_key: "rfc1628-generic",
      profile_version: 1,
      poll_ms: 3000,
      timeout_ms: 1000,
      retries: 0,
      enabled: true,
      password_set: false,
      community_set: false,
    } as const;
    expect(normalizeSource({ ...base } as unknown as Source).status).toBeNull();
    const withStatus = normalizeSource({
      ...base,
      status: {
        last_ok_at: "0001-01-01T00:00:00Z",
        latency_ms: 0,
        failures: 3,
        error_code: "timeout",
      },
    });
    expect(withStatus.status).toEqual({
      last_ok_at: null,
      latency_ms: 0,
      failures: 3,
      error_code: "timeout",
    });
  });
});
