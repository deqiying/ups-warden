import { useInfiniteQuery } from "@tanstack/react-query";
import { queryKeys } from "../../api/hooks.ts";
import { listRuleLogs, type RuleLog } from "../../api/rules.ts";

const PAGE_SIZE = 50;

/**
 * 单条规则的评估日志（`GET /rule-logs?rule_id=`，游标分页）。编辑器头部的“最近触发”
 * 与日志卡片共用同一查询键，不重复请求。日志不经 SSE 推送，需手动刷新。
 */
export function useRuleLogs(ruleId: number) {
  return useInfiniteQuery({
    queryKey: [...queryKeys.ruleLogs, { rule_id: ruleId }],
    queryFn: ({ pageParam, signal }) =>
      listRuleLogs(
        { rule_id: ruleId, limit: PAGE_SIZE, before: pageParam > 0 ? pageParam : undefined },
        signal,
      ),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.next_before > 0 ? last.next_before : undefined),
  });
}

/** 已加载日志中最近一次 `fired`；不在已加载范围内时为 undefined（契约没有“最近触发”字段）。 */
export function latestFired(
  pages: readonly { items: RuleLog[] }[] | undefined,
): RuleLog | undefined {
  for (const page of pages ?? []) {
    const hit = page.items.find((log) => log.kind === "fired");
    if (hit) return hit;
  }
  return undefined;
}

/** 执行记录页入口；后端日志只给出执行记录 ID，不含动作 ID，因此不按动作过滤。 */
export function actionRunLink(runId: number): string {
  return `/hosts-actions?tab=runs&open=${runId}`;
}
