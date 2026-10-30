/*
 * 设置页的纯函数与文案：分区、时区选项、审计展示、账户操作的失败文案。
 * 契约见 docs/reference/settings-api.md 与 monitoring-api.md「初始化、登录与用户」「审计」。
 */
import { isApiError, isConfigSavedRuntimeError } from "../../api/client.ts";
import type { AuditEntry } from "../../api/audit.ts";
import type { PillTone, SelectOption } from "../../components/index.ts";
import type { IconName } from "../../icons/Icon.tsx";
import { formatDateHeading, formatFullDateTime } from "../../model/time.ts";
import { failureText } from "../hosts-actions/model.ts";

// ───────────────────────── 分区 ─────────────────────────

export type SectionKey = "general" | "retention" | "defaults" | "security" | "account" | "audit";

export interface SectionMeta {
  key: SectionKey;
  label: string;
  icon: IconName;
  /** 图标底色（tokens.css 变量）。 */
  color: string;
  /** 侧栏分组：1 为系统设置，2 为账户与审计。 */
  group: 1 | 2;
}

export const SECTIONS: readonly SectionMeta[] = [
  { key: "general", label: "通用", icon: "gear", color: "var(--gray)", group: 1 },
  { key: "retention", label: "数据保留", icon: "hourglass", color: "var(--indigo)", group: 1 },
  { key: "defaults", label: "设备默认值", icon: "sliders", color: "var(--orange)", group: 1 },
  { key: "security", label: "安全", icon: "shield", color: "var(--accent)", group: 1 },
  { key: "account", label: "管理员", icon: "people", color: "var(--teal)", group: 2 },
  { key: "audit", label: "审计日志", icon: "doc", color: "var(--brown)", group: 2 },
];

/** 路由参数 → 分区；未知值返回 null（由页面重定向到 general）。 */
export function parseSection(value: string | undefined): SectionKey | null {
  return SECTIONS.find((s) => s.key === value)?.key ?? null;
}

// ───────────────────────── 通用：时区 ─────────────────────────

const offsetCache = new Map<string, string>();

/** `UTC+08:00`；运行环境不支持该时区时返回空串。 */
export function utcOffsetLabel(zone: string, at: Date = new Date()): string {
  const cached = offsetCache.get(zone);
  if (cached !== undefined) return cached;
  let label = "";
  try {
    const part = new Intl.DateTimeFormat("en-US", { timeZone: zone, timeZoneName: "longOffset" })
      .formatToParts(at)
      .find((p) => p.type === "timeZoneName")?.value;
    if (part) label = part === "GMT" ? "UTC+00:00" : part.replace("GMT", "UTC");
  } catch {
    // RangeError：运行环境不认识该时区名，选项里只显示名称。
    label = "";
  }
  offsetCache.set(zone, label);
  return label;
}

/**
 * 时区下拉选项：运行环境支持的 IANA 时区，保证包含 UTC（后端默认值）和当前已保存的时区
 * （即使浏览器不认识它，也能如实显示而不是悄悄换成别的值）。
 */
export function timeZoneOptions(current: string): SelectOption[] {
  const supported =
    (Intl as unknown as { supportedValuesOf?: (key: string) => string[] }).supportedValuesOf?.(
      "timeZone",
    ) ?? [];
  const zones = new Set<string>(["UTC", ...supported]);
  if (current) zones.add(current);
  return [...zones].sort().map((zone) => {
    const offset = utcOffsetLabel(zone);
    return { value: zone, label: offset ? `${zone}（${offset}）` : zone };
  });
}

// ───────────────────────── 失败文案 ─────────────────────────

/**
 * 设置保存失败的文案。503 configuration_saved_runtime_error 表示配置已保存、全局已提示，
 * 返回 null，调用方不再显示错误（也不能回滚表单）。
 */
export function settingsFailureText(err: unknown): string | null {
  if (isConfigSavedRuntimeError(err)) return null;
  return failureText(err);
}

export type UserOperation = "create" | "rename" | "delete" | "password";

/**
 * 账户操作的失败文案。后端对不同操作复用同一错误码，含义靠操作区分：
 * - `user_conflict`（409）：创建/改名时为用户名已存在；删除时为最后一位管理员；
 * - `validation_failed`（422）：创建/改名/改密为输入不合规；删除时为不能删除自己
 *   （后端 message 固定为“用户名或密码不符合要求”，对删除并不准确，因此不用后端原文）。
 */
export function userFailureText(op: UserOperation, err: unknown): string {
  if (!isApiError(err)) return failureText(err);
  if (err.code === "user_not_found") return "管理员不存在，可能已被其他人删除";
  if (op === "delete") {
    if (err.status === 422) return "不能删除当前登录的账户";
    if (err.status === 409) return "不能删除最后一位管理员";
  } else if (err.status === 409) {
    return "用户名已存在";
  } else if (err.status === 422) {
    return op === "password"
      ? "密码不符合要求：至少 8 个字符，最多 1024 字节"
      : "用户名或密码不符合要求：用户名为 1～64 位字母、数字、. _ -，密码至少 8 个字符";
  }
  return failureText(err);
}

// ───────────────────────── 审计展示 ─────────────────────────

export const AUDIT_KIND_LABELS: Readonly<Record<string, string>> = {
  setup: "初始化管理员",
  login: "登录",
  logout: "退出登录",
  "user.create": "添加管理员",
  "user.rename": "修改用户名",
  "user.delete": "删除管理员",
  "user.password": "修改密码",
  "settings.update": "修改系统设置",
  "device.create": "添加设备",
  "device.update": "修改设备",
  "device.delete": "删除设备",
  "source.create": "添加数据源",
  "source.update": "修改数据源",
  "source.delete": "删除数据源",
  "profile.create": "添加 Profile",
  "profile.update": "修改 Profile",
  "profile.delete": "删除 Profile",
  "channel.create": "添加通知渠道",
  "channel.update": "修改通知渠道",
  "channel.delete": "删除通知渠道",
  "channel.test": "发送测试",
  "notify_template.update": "修改通知模板",
  "rule.create": "添加规则",
  "rule.update": "修改规则",
  "rule.delete": "删除规则",
  "action.create": "添加动作",
  "action.update": "修改动作",
  "action.delete": "删除动作",
  "action.run": "手动执行动作",
  "host.create": "添加主机",
  "host.update": "修改主机",
  "host.delete": "删除主机",
  "host.hostkey.fetch": "获取主机指纹",
  "host.hostkey.confirm": "信任主机指纹",
  "host.test_connection": "测试主机连接",
  "host.test_command": "测试主机命令",
  "plan.create": "添加关机计划",
  "plan.update": "修改关机计划",
  "plan.delete": "删除关机计划",
  "plan.run": "执行关机计划",
  "plan_run.cancel": "取消计划运行",
};

/** 未收录的操作类型直接显示原始 kind，不隐藏。 */
export function auditKindLabel(kind: string): string {
  return Object.hasOwn(AUDIT_KIND_LABELS, kind) ? AUDIT_KIND_LABELS[kind] : kind;
}

export function auditResultView(result: string): { label: string; tone: PillTone } {
  switch (result) {
    case "success":
      return { label: "成功", tone: "green" };
    case "failure":
      return { label: "失败", tone: "red" };
    case "accepted":
      return { label: "已受理", tone: "blue" };
    case "requested":
      return { label: "已请求", tone: "blue" };
    default:
      return { label: result || "—", tone: "gray" };
  }
}

const TARGET_NOUNS: Readonly<Record<string, string>> = {
  device: "设备",
  source: "数据源",
  channel: "渠道",
  rule: "规则",
  action: "动作",
  host: "主机",
  plan: "计划",
};

/**
 * 审计对象的显示文本。契约只给出对象标识（多为 id，用户相关为 `user:<id>`），
 * 不含名称；用户名可由已加载的管理员列表解析，其余按类型加 id 显示。
 */
export function auditTargetText(
  entry: Pick<AuditEntry, "kind" | "target" | "result">,
  userName: (id: number) => string | undefined,
): string {
  const { kind, target } = entry;
  if (target.startsWith("user:")) {
    const value = target.slice("user:".length);
    // 失败登录的对象是尝试的用户名（可能并不存在）；其余是用户 id。
    if (/^\d+$/.test(value) && entry.result !== "failure") {
      const name = userName(Number(value));
      return name ? `管理员「${name}」` : `管理员 #${value}`;
    }
    return `用户「${value}」`;
  }
  if (kind === "settings.update") return "系统设置";
  if (kind === "plan_run.cancel") return `运行 #${target}`;
  if (kind === "notify_template.update") return `模板 ${target}`;
  if (kind.startsWith("profile.")) return `Profile「${target}」`;
  const noun = TARGET_NOUNS[kind.split(".")[0]];
  if (noun && target) return /^\d+$/.test(target) ? `${noun} #${target}` : `${noun} ${target}`;
  return target || "—";
}

/** 时区内的日历日 `YYYY-MM-DD`，用于按天分组。 */
export function dayKey(value: string | number | Date, timeZone: string | undefined): string {
  return formatFullDateTime(value, timeZone).slice(0, 10);
}

export interface AuditDayGroup {
  key: string;
  label: string;
  items: AuditEntry[];
}

const DAY_MS = 86_400_000;

/** 按时区内的日期分组（输入已按时间倒序，保持顺序）；标题带“今天 / 昨天”。 */
export function groupAuditByDay(
  entries: readonly AuditEntry[],
  timeZone: string | undefined,
  nowMs: number,
): AuditDayGroup[] {
  const today = dayKey(nowMs, timeZone);
  const yesterday = dayKey(nowMs - DAY_MS, timeZone);
  const groups: AuditDayGroup[] = [];
  for (const entry of entries) {
    const key = dayKey(entry.at, timeZone);
    const last = groups[groups.length - 1];
    if (last && last.key === key) {
      last.items.push(entry);
      continue;
    }
    const prefix = key === today ? "今天 · " : key === yesterday ? "昨天 · " : "";
    groups.push({
      key,
      label: `${prefix}${formatDateHeading(entry.at, timeZone)}`,
      items: [entry],
    });
  }
  return groups;
}

// ───────────────────────── 保留期时间线 ─────────────────────────

/** 对数刻度位置（%）：1 天为 0，3650 天（10 年）为 100。 */
export function retentionPosition(days: number): number {
  if (!Number.isFinite(days) || days <= 1) return 0;
  return Math.min(100, (Math.log(days) / Math.log(3650)) * 100);
}
