/*
 * 写请求体构造的通用约定：
 * - 部分更新只提交用户修改过的字段（PATCH / 部分 PUT），未修改的字段不发送，避免覆盖并发修改。
 * - 凭据字段只写不读：留空表示保留原值；清除必须显式提交 `clear_secrets`，且同一字段不能同时设置和清除。
 */

function sameValue(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) return false;
  // 配置对象、数组只含 JSON 值，按序列化结果比较即可；键顺序不同视为修改，最多导致多提交一个整体替换字段。
  return JSON.stringify(a) === JSON.stringify(b);
}

/** 返回 draft 中与 original 不同的字段；`keys` 限定参与比较的字段（默认 draft 的全部键）。 */
export function changedFields<T extends object>(
  original: T,
  draft: T,
  keys: readonly (keyof T)[] = Object.keys(draft) as (keyof T)[],
): Partial<T> {
  const out: Partial<T> = {};
  for (const key of keys) {
    if (!sameValue(original[key], draft[key])) out[key] = draft[key];
  }
  return out;
}

/** 凭据值“留空”：undefined、空串或空对象（如 Webhook headers `{}`）都表示保留原值。 */
function isBlankSecret(value: unknown): boolean {
  if (value === undefined || value === null || value === "") return true;
  return typeof value === "object" && !Array.isArray(value) && Object.keys(value).length === 0;
}

/**
 * 构造凭据部分：非空值原样提交，留空省略；`clear` 中的字段放入 `clear_secrets`。
 * 同一字段既有新值又要求清除属于调用方逻辑错误，直接抛错而不是猜测意图。
 */
export function secretFields(
  values: Readonly<Record<string, unknown>>,
  clear: readonly string[] = [],
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(values)) {
    if (!isBlankSecret(value)) out[key] = value;
  }
  const unique = [...new Set(clear)];
  for (const key of unique) {
    if (key in out) throw new Error(`凭据字段 ${key} 不能同时设置新值和清除`);
  }
  if (unique.length > 0) out.clear_secrets = unique;
  return out;
}

/** 删除类请求的确认体；只在用户完成确认对话框后才构造并发送。 */
export function confirmBody(): { confirm: true } {
  return { confirm: true };
}
