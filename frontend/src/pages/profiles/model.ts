/* Profile 页的纯逻辑：校验错误文案与出错行摘录。 */
import { isApiError } from "../../api/client.ts";
import { profileErrorLocation } from "../../api/profiles.ts";

/** 字段路径的中文说明；后端只返回受限路径（profiles.go profileLocation）。 */
const PATH_LABELS: Readonly<Record<string, string>> = {
  schema: "schema",
  key: "key",
  name: "name",
  version: "version",
  protocol: "protocol",
  snmp: "snmp 读取计划",
  modbus: "modbus 读取计划",
  ayi9: "ayi9 读取计划",
  phases: "phases",
  power_hints: "power_hints",
  match: "match",
};

function pathLabel(path: string): string {
  const field = /^fields\[(\d+)\]$/.exec(path);
  if (field) return `fields 第 ${Number(field[1]) + 1} 项`;
  return PATH_LABELS[path] ?? path;
}

/**
 * Profile 校验 / 导入错误的界面文案。422 profile_invalid 附带位置；
 * 409 区分内置只读与 key 冲突。后端不回显输入值，这里也只拼接位置。
 */
export function profileErrorText(error: unknown): string {
  if (!isApiError(error)) return error instanceof Error ? error.message : "操作失败，请重试";
  if (error.code === "profile_invalid") {
    const { line, path } = profileErrorLocation(error.details);
    const where = [line ? `第 ${line} 行` : "", path ? pathLabel(path) : ""]
      .filter(Boolean)
      .join(" · ");
    return where ? `解析或语义校验失败（${where}）` : "解析或语义校验失败，请检查声明格式";
  }
  if (error.code === "builtin_readonly") return "不能使用内置 Profile 的 key，请换一个 key";
  if (error.code === "resource_conflict") return "已存在同名 key 的 Profile，请换一个 key";
  return error.message;
}

/** 出错行的原文摘录（只在本地显示用户自己的输入），超长截断。 */
export function lineExcerpt(content: string, line: number, max = 160): string {
  const text = content.split("\n")[line - 1] ?? "";
  return text.length > max ? `${text.slice(0, max)}…` : text;
}
