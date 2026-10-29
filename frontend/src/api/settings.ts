import { api } from "./client.ts";
import type { Settings } from "./types.ts";

export function getSettings(signal?: AbortSignal): Promise<Settings> {
  return api.request<Settings>("/settings", { signal });
}
