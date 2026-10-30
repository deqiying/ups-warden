import { api } from "./client.ts";
import type { Page } from "./types.ts";

/*
 * 审计日志 API（GET /audit，docs/reference/monitoring-api.md）。
 * 只分页（limit 1～200、默认 50；before 为排他的正整数游标），没有筛选也没有 total，界面不显示总数。
 */

export interface AuditEntry {
  id: number;
  at: string;
  /** 用户被删除或未登录的失败登录时为 null。 */
  user_id: number | null;
  ip: string;
  /** 操作类型，如 `rule.update`、`login`、`settings.update`。 */
  kind: string;
  /** 对象标识：多为资源 id；用户相关为 `user:<id>`，失败登录为 `user:<用户名>`。 */
  target: string;
  /** 字段级差异：键为点分字段路径，值为 `{before, after}`，敏感字段为字符串“已修改”。 */
  diff: unknown;
  /** success、failure、accepted、requested。 */
  result: string;
}

export interface AuditQuery {
  limit?: number;
  before?: number;
}

export async function listAudit(
  query: AuditQuery,
  signal?: AbortSignal,
): Promise<Page<AuditEntry>> {
  const page = await api.request<Page<AuditEntry>>("/audit", { query: { ...query }, signal });
  return { items: page.items ?? [], next_before: page.next_before ?? 0 };
}

// ───────────────────────── diff 解析 ─────────────────────────

/** 敏感字段在 diff 中的固定文案；界面只显示它，不显示任何值。 */
export const MASKED_TEXT = "已修改";

/** 与后端 audit.sensitive 一致的敏感字段名片段（规范化：小写并去掉 `_`、`-`）。 */
const SENSITIVE_PARTS = [
  "password",
  "passwd",
  "passphrase",
  "community",
  "token",
  "secret",
  "privatekey",
  "authorization",
  "credential",
  "cookie",
  "header",
  "apikey",
];

function canonical(name: string): string {
  return name.toLowerCase().replace(/[_-]/g, "");
}

/**
 * 字段路径是否敏感。后端已把敏感字段的值替换为“已修改”；这里再按字段名判断一次作为纵深防御，
 * 即使响应里意外带有明文，界面也不会显示。
 */
export function isSensitiveField(path: string): boolean {
  return path.split(".").some((segment) => {
    const key = canonical(segment);
    return (
      key === "pass" ||
      key === "csrf" ||
      key === "newpass" ||
      SENSITIVE_PARTS.some((part) => key.includes(part))
    );
  });
}

export type AuditDiffRow =
  | { field: string; masked: true }
  | { field: string; masked: false; before: string | null; after: string | null };

const MAX_VALUE_CHARS = 200;

/** 值的显示文本：null / 缺失为 null（界面显示“—”）；对象按 JSON；过长截断。 */
function valueText(value: unknown): string | null {
  if (value === null || value === undefined) return null;
  const text = typeof value === "string" ? value : JSON.stringify(value);
  if (text === undefined) return null;
  return text.length > MAX_VALUE_CHARS ? `${text.slice(0, MAX_VALUE_CHARS)}…` : text;
}

/** 把 diff 对象转成按字段名排序的行；非对象（异常数据）视为无差异。 */
export function auditDiffRows(diff: unknown): AuditDiffRow[] {
  if (typeof diff !== "object" || diff === null || Array.isArray(diff)) return [];
  return Object.entries(diff)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([field, value]): AuditDiffRow => {
      if (value === MASKED_TEXT || isSensitiveField(field)) return { field, masked: true };
      if (typeof value === "object" && value !== null && !Array.isArray(value)) {
        const change = value as { before?: unknown; after?: unknown };
        return {
          field,
          masked: false,
          before: valueText(change.before),
          after: valueText(change.after),
        };
      }
      // 非 {before, after} 形态（契约外）：当作变更后的值。
      return { field, masked: false, before: null, after: valueText(value) };
    });
}
