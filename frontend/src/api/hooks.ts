import { useQuery } from "@tanstack/react-query";
import { listActions, listScripts, type Action } from "./actions.ts";
import { getDevice, listAllDevices, listDeviceSources, listSourcesByDevice } from "./devices.ts";
import { listHosts, type Host } from "./hosts.ts";
import { channelKeys, listChannels, type Channel } from "./notifications.ts";
import { listPlans, type Plan } from "./plans.ts";
import { listProfiles } from "./profiles.ts";
import { getSettings } from "./settings.ts";
import type { Device, ProfileSummary, Settings, Source } from "./types.ts";

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
  /** Profile 列表与详情：`[...profiles]`、`[...profiles, key]`。 */
  profiles: ["profiles"] as const,
  /** 受控主机列表。 */
  hosts: ["hosts"] as const,
  /** 动作列表。 */
  actions: ["actions"] as const,
  /** 动作执行记录：`[...actionRuns, filters]`。 */
  actionRuns: ["action-runs"] as const,
  /** 服务端脚本目录。 */
  scripts: ["scripts"] as const,
  /** 关机计划列表。 */
  plans: ["plans"] as const,
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

/** 全部 Profile；导入、复制、删除后由 Profile 页主动失效。 */
export function useProfiles() {
  return useQuery<ProfileSummary[]>({
    queryKey: queryKeys.profiles,
    queryFn: ({ signal }) => listProfiles(signal),
    staleTime: 60_000,
  });
}

/** 全部受控主机；写入后由页面主动失效。 */
export function useHosts() {
  return useQuery<Host[]>({
    queryKey: queryKeys.hosts,
    queryFn: ({ signal }) => listHosts(signal),
  });
}

/** 全部动作（含 plan / notify）；写入后由页面主动失效。 */
export function useActions() {
  return useQuery<Action[]>({
    queryKey: queryKeys.actions,
    queryFn: ({ signal }) => listActions(signal),
  });
}

/** 全部关机计划。 */
export function usePlans() {
  return useQuery<Plan[]>({
    queryKey: queryKeys.plans,
    queryFn: ({ signal }) => listPlans(signal),
  });
}

/** 脚本目录由管理员在服务端维护，API 不能修改；打开选择器时读取即可。 */
export function useScripts(enabled = true) {
  return useQuery<string[]>({
    queryKey: queryKeys.scripts,
    queryFn: ({ signal }) => listScripts(signal),
    enabled,
  });
}

/** 全部通知渠道（与通知页共用查询键）。 */
export function useChannels() {
  return useQuery<Channel[]>({
    queryKey: [...channelKeys.all, "list"],
    queryFn: ({ signal }) => listChannels(signal),
  });
}
