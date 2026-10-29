import { useQuery } from "@tanstack/react-query";
import { getSettings } from "./settings.ts";
import type { Settings } from "./types.ts";

/**
 * 查询键前缀。实时帧与全局错误处理按前缀失效，各资源页面在此基础上追加参数，
 * 例如 `[...queryKeys.planRuns, { plan_id }]`。
 */
export const queryKeys = {
  settings: ["settings"] as const,
  planRuns: ["plan-runs"] as const,
  events: ["events"] as const,
  notifyLogs: ["notify-logs"] as const,
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
