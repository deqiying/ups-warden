import { api } from "./client.ts";
import type {
  Device,
  HistoryResponse,
  Page,
  ProbeResult,
  Source,
  SourceDiagnostics,
} from "./types.ts";

/** 契约分页上限；一次取满以减少“取全部”时的请求次数。 */
export const MAX_PAGE_LIMIT = 200;

/** 防御性上限：游标异常（不前进）时避免无限翻页。 */
const MAX_PAGES = 50;

/**
 * 逐页读取到 `next_before=0`。用于设备、数据源这类总量很小的列表；
 * 事件等可能很多的列表改用“加载更多”，不要用这里。
 */
export async function fetchAllPages<T>(
  fetchPage: (before: number | undefined) => Promise<Page<T>>,
): Promise<T[]> {
  const items: T[] = [];
  let before: number | undefined;
  for (let i = 0; i < MAX_PAGES; i++) {
    const page = await fetchPage(before);
    items.push(...(page.items ?? []));
    // next_before 为 0（或旧实现返回 null）表示没有下一页；游标不前进时同样停止。
    if (!page.next_before || page.next_before === before) return items;
    before = page.next_before;
  }
  return items;
}

/** 全部设备（含停用），按 id 降序。 */
export function listAllDevices(signal?: AbortSignal): Promise<Device[]> {
  return fetchAllPages((before) =>
    api.request<Page<Device>>("/devices", {
      query: { limit: MAX_PAGE_LIMIT, before },
      signal,
    }),
  );
}

export function getDevice(id: number, signal?: AbortSignal): Promise<Device> {
  return api.request<Device>(`/devices/${id}`, { signal });
}

// Go 零值时间表示“从未成功”。
const GO_ZERO_TIME = /^0001-01-01T/;

/** 补齐数据源的可选部分：status 缺省为 null，零值 last_ok_at 为 null。 */
export function normalizeSource(wire: Source): Source {
  const status = wire.status ?? null;
  return {
    ...wire,
    params: wire.params ?? null,
    status: status
      ? {
          ...status,
          last_ok_at:
            !status.last_ok_at || GO_ZERO_TIME.test(status.last_ok_at) ? null : status.last_ok_at,
          error_code: status.error_code ?? "",
        }
      : null,
  };
}

/** 某设备的全部数据源。 */
export async function listDeviceSources(deviceId: number, signal?: AbortSignal): Promise<Source[]> {
  const items = await fetchAllPages((before) =>
    api.request<Page<Source>>(`/devices/${deviceId}/sources`, {
      query: { limit: MAX_PAGE_LIMIT, before },
      signal,
    }),
  );
  return items.map(normalizeSource);
}

/**
 * 并发受限地执行任务（Q-C1：设备列表不含数据源摘要，只能逐台请求，并发限制在 4～6）。
 * 结果顺序与输入一致；任一任务失败则整体失败。
 */
export async function mapLimit<T, R>(
  items: readonly T[],
  limit: number,
  task: (item: T) => Promise<R>,
): Promise<R[]> {
  const results = new Array<R>(items.length);
  let next = 0;
  const worker = async () => {
    while (next < items.length) {
      const index = next++;
      results[index] = await task(items[index]);
    }
  };
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker));
  return results;
}

export const SOURCE_FETCH_CONCURRENCY = 4;

/** 各设备的数据源，键为设备 id。 */
export async function listSourcesByDevice(
  deviceIds: readonly number[],
  signal?: AbortSignal,
): Promise<Record<number, Source[]>> {
  const lists = await mapLimit(deviceIds, SOURCE_FETCH_CONCURRENCY, (id) =>
    listDeviceSources(id, signal),
  );
  const out: Record<number, Source[]> = {};
  deviceIds.forEach((id, i) => {
    out[id] = lists[i];
  });
  return out;
}

/**
 * 使用已保存配置的只读连接测试，请求体固定为 `{}`。
 * 失败：409 source_busy；502 source_password_wrong（AYI9 密码错误 103）/ probe_failed。
 */
export function testSource(sourceId: number): Promise<ProbeResult> {
  return api.request<ProbeResult>(`/sources/${sourceId}/test`, { method: "POST", body: {} });
}

/** 最近一次内存诊断；暂无数据时 404（diagnostic_unavailable）。 */
export function getSourceDiagnostics(
  sourceId: number,
  signal?: AbortSignal,
): Promise<SourceDiagnostics> {
  return api.request<SourceDiagnostics>(`/sources/${sourceId}/raw`, { signal });
}

export interface HistoryQuery {
  /** RFC3339；区间为 `[from, to)`。 */
  from: string;
  to: string;
  /** 只接受 History=true 的统一字段实例（按相字段须带相号）。 */
  fields: readonly string[];
  resolution?: "auto" | "raw" | "1m";
  /** 1～1000；超出时由后端降采样。 */
  maxPoints?: number;
}

export function getHistory(
  deviceId: number,
  query: HistoryQuery,
  signal?: AbortSignal,
): Promise<HistoryResponse> {
  return api.request<HistoryResponse>(`/devices/${deviceId}/history`, {
    query: {
      from: query.from,
      to: query.to,
      fields: query.fields.join(","),
      resolution: query.resolution ?? "auto",
      max_points: query.maxPoints ?? 1000,
    },
    signal,
  });
}
