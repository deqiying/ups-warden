import { useQuery } from "@tanstack/react-query";
import { getDevice, listAllDevices, listDeviceSources, listSourcesByDevice } from "./devices.ts";
import { getSettings } from "./settings.ts";
import type { Device, Settings, Source } from "./types.ts";

/**
 * 查询键前缀。实时帧与全局错误处理按前缀失效，各资源页面在此基础上追加参数，
 * 例如 `[...queryKeys.planRuns, { plan_id }]`。
 */
export const queryKeys = {
  settings: ["settings"] as const,
  planRuns: ["plan-runs"] as const,
  events: ["events"] as const,
  notifyLogs: ["notify-logs"] as const,
  /** 设备列表与单台设备：`[...devices]`、`[...devices, id]`。 */
  devices: ["devices"] as const,
  /** 数据源：`[...sources, "device", id]`、`[...sources, "by-device", ids]`、诊断。 */
  sources: ["sources"] as const,
  history: ["history"] as const,
};

export function useSettings() {
  return useQuery<Settings>({
    queryKey: queryKeys.settings,
    queryFn: ({ signal }) => getSettings(signal),
    // 设置很少变化；保存设置时由设置页主动失效。
    staleTime: 5 * 60_000,
  });
}

/** 界面显示时区（settings.timezone）；未加载或失败时为 undefined，格式化函数回退为浏览器时区。 */
export function useTimeZone(): string | undefined {
  return useSettings().data?.timezone;
}

/**
 * 全部设备（含停用）。配置变更后后端会推送 sync，由 LiveEffects 使本查询失效，
 * 因此这里不轮询。
 */
export function useDevices() {
  return useQuery<Device[]>({
    queryKey: queryKeys.devices,
    queryFn: ({ signal }) => listAllDevices(signal),
    staleTime: 60_000,
  });
}

export function useDevice(id: number) {
  return useQuery<Device>({
    queryKey: [...queryKeys.devices, id],
    queryFn: ({ signal }) => getDevice(id, signal),
    enabled: Number.isInteger(id) && id > 0,
    staleTime: 60_000,
  });
}

/** 单台设备的数据源；运行状态（status）随采集变化，详情页按需刷新。 */
export function useDeviceSources(deviceId: number, refetchInterval?: number) {
  return useQuery<Source[]>({
    queryKey: [...queryKeys.sources, "device", deviceId],
    queryFn: ({ signal }) => listDeviceSources(deviceId, signal),
    enabled: Number.isInteger(deviceId) && deviceId > 0,
    refetchInterval,
  });
}

/** 多台设备的数据源摘要（Q-C1：逐台请求、并发受限）。 */
export function useSourcesByDevice(deviceIds: readonly number[]) {
  const ids = [...deviceIds].sort((a, b) => a - b);
  return useQuery<Record<number, Source[]>>({
    queryKey: [...queryKeys.sources, "by-device", ids],
    queryFn: ({ signal }) => listSourcesByDevice(ids, signal),
    enabled: ids.length > 0,
    staleTime: 60_000,
  });
}
