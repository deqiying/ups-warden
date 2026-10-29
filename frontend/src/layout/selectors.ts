import type { LiveSnapshot } from "../live/store.ts";

/** 活动告警总数（侧栏与标签栏角标）。 */
export function activeAlertCount(snapshot: LiveSnapshot): number {
  let count = 0;
  for (const items of Object.values(snapshot.data.alerts)) count += items.length;
  return count;
}
