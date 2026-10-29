import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { useNavigate } from "react-router";
import { queryKeys } from "../api/hooks.ts";
import { useToast } from "../components/toast-context.ts";
import { useLiveStore } from "../live/hooks.ts";

const NOTIFY_LOGS_PATH = "/notifications?tab=logs";

/**
 * 把实时存储的副作用转为查询失效与 toast（frontend-plan 3.4）：
 * - sync：断线期间可能有运行结束、事件变化，计划运行、事件、设备与数据源查询失效。
 * - plan_progress 进入终态：计划运行查询失效。
 * - notify_failed：弹出 toast；断线补齐的多条合并为一条摘要。
 */
export function LiveEffects() {
  const store = useLiveStore();
  const queryClient = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();

  useEffect(() => {
    const viewLogs = { label: "查看发送记录", onClick: () => void navigate(NOTIFY_LOGS_PATH) };
    return store.onEffect((effect) => {
      switch (effect.type) {
        case "resynced":
          void queryClient.invalidateQueries({ queryKey: queryKeys.planRuns });
          void queryClient.invalidateQueries({ queryKey: queryKeys.events });
          // 设备、数据源、Profile 配置变更后后端同样推送 sync（monitoring-api.md「SSE」）。
          void queryClient.invalidateQueries({ queryKey: queryKeys.devices });
          void queryClient.invalidateQueries({ queryKey: queryKeys.sources });
          return;
        case "plan_run_finished":
          void queryClient.invalidateQueries({ queryKey: queryKeys.planRuns });
          return;
        case "notify_failed": {
          void queryClient.invalidateQueries({ queryKey: queryKeys.notifyLogs });
          if (effect.source === "backfill") {
            toast.show({
              tone: "warning",
              title: `断线期间有 ${effect.items.length} 条通知发送失败`,
              message: "实时连接断开时未能及时提示，详情见发送记录。",
              actions: [viewLogs],
              duration: null,
              dedupeKey: "notify-backfill",
            });
            return;
          }
          for (const item of effect.items) {
            toast.show({
              tone: "warning",
              title: "通知发送失败",
              message: [item.summary, item.target, item.error].filter(Boolean).join(" · "),
              actions: [viewLogs],
              dedupeKey: `notify-failed-${item.id}`,
            });
          }
          return;
        }
        case "notify_backfill_failed":
          toast.show({
            tone: "warning",
            title: "未能补齐断线期间的通知失败记录",
            message: "可能有发送失败未提示，请在发送记录中查看。",
            actions: [viewLogs],
            duration: null,
            dedupeKey: "notify-backfill",
          });
          return;
      }
    });
  }, [store, queryClient, toast, navigate]);

  return null;
}
