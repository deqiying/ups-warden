import { api } from "./client.ts";
import { normalizeEventView } from "./normalize.ts";
import type { EventItem, Page, Severity, WireEventView } from "./types.ts";

export interface EventQuery {
  before?: number;
  limit?: number;
  device_id?: number;
  /** 契约只支持单值（Q-C3）。 */
  type?: string;
  severity?: Severity;
  /** RFC3339 UTC。 */
  from?: string;
  to?: string;
}

async function fetchEvents(
  path: "/events" | "/alerts",
  query: EventQuery,
  signal?: AbortSignal,
): Promise<Page<EventItem>> {
  const page = await api.request<Page<WireEventView>>(path, { query: { ...query }, signal });
  return { items: (page.items ?? []).map(normalizeEventView), next_before: page.next_before ?? 0 };
}

/** 事件（含已结束），按 id 降序，游标分页。 */
export function listEvents(query: EventQuery, signal?: AbortSignal): Promise<Page<EventItem>> {
  return fetchEvents("/events", query, signal);
}

/** 只列 active=true 的事件；实时页面以 SSE alert 为准，此接口用于补充查询。 */
export function listAlerts(query: EventQuery, signal?: AbortSignal): Promise<Page<EventItem>> {
  return fetchEvents("/alerts", query, signal);
}
