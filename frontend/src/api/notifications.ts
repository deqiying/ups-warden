import { api } from "./client.ts";
import type { NotificationResult, Page } from "./types.ts";

export interface NotifyLogQuery {
  before?: number;
  limit?: number;
  channel_id?: number;
  event_id?: number;
  ok?: boolean;
  /** RFC3339；区间为 `from <= at < to`。 */
  from?: string;
  to?: string;
}

export function listNotifyLogs(
  query: NotifyLogQuery,
  signal?: AbortSignal,
): Promise<Page<NotificationResult>> {
  return api.request<Page<NotificationResult>>("/notify-logs", { query: { ...query }, signal });
}
