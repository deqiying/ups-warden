/*
 * 规则页的显示派生（F7）：字段与运算符选项、范围 / 触发摘要、评估日志文案。
 * 只使用统一字段目录与事件目录，不引用任何协议地址。
 */
import type { Action } from "../../api/actions.ts";
import type { Device, FieldMap } from "../../api/types.ts";
import {
  ALL_OPS,
  conditionValueOf,
  type ConditionDraft,
  type ConditionOp,
  type RuleCondition,
  type Rule,
  type RuleDraft,
  type RuleTemplate,
} from "../../api/rules.ts";
import type { PillTone } from "../../components/Pill.tsx";
import type { SelectOption } from "../../components/Select.tsx";
import type { IconName } from "../../icons/icons.ts";
import { eventIcon } from "../../model/events.ts";
import { FIELD_CATALOG, fieldLabel, lookupField, type FieldGroup } from "../../model/fields.ts";
import { durationText, fieldText } from "../../model/format.ts";
import { enumLabel, eventLabel, QUALITY_LABELS, reasonLabel } from "../../model/labels.ts";

export const OP_LABELS: Readonly<Record<ConditionOp, string>> = {
  eq: "等于",
  ne: "不等于",
  gt: ">",
  gte: "≥",
  lt: "<",
  lte: "≤",
};

export const OP_OPTIONS: readonly SelectOption[] = ALL_OPS.map((op) => ({
  value: op,
  label: OP_LABELS[op],
}));

/** 条件构建器的字段分组顺序：规则常用的派生与电池字段在前。 */
const GROUP_ORDER: readonly FieldGroup[] = [
  "derived",
  "battery",
  "flags",
  "input",
  "output",
  "bypass",
  "nominal",
  "ident",
];

/**
 * 条件字段选项：按相字段先给出不带相号的实例（取所有 good 相的最大值），再给出 L1～L3。
 * 选项文字带上字段键，便于与日志、模板对照。
 */
export function conditionFieldOptions(): SelectOption[] {
  const out: SelectOption[] = [];
  for (const group of GROUP_ORDER) {
    for (const spec of FIELD_CATALOG.filter((s) => s.group === group)) {
      out.push({
        value: spec.key,
        label: spec.phased
          ? `${spec.label}（各相最大）· ${spec.key}`
          : `${spec.label} · ${spec.key}`,
      });
      if (spec.phased) {
        for (const phase of [1, 2, 3]) {
          const key = `${spec.key}[${phase}]`;
          out.push({ value: key, label: `${spec.label} L${phase} · ${key}` });
        }
      }
    }
  }
  return out;
}

/** 条件值的显示文本（模板说明、确认文案）。 */
export function conditionValueText(field: string, value: unknown): string {
  const spec = lookupField(field);
  const base = field.replace(/\[\d\]$/, "");
  if (!spec) return String(value);
  switch (spec.kind) {
    case "duration":
      return typeof value === "number" ? durationText(value) : String(value);
    case "number":
      return spec.unit ? `${String(value)} ${spec.unit}` : String(value);
    case "bool":
      return value === true ? "是" : value === false ? "否" : String(value);
    case "enum":
      return typeof value === "string" ? enumLabel(base, value) : String(value);
    default:
      return `“${String(value)}”`;
  }
}

/** 条件的一句话描述，如“供电状态 等于 电池供电”“电池容量 ≤ 40 %，持续 10 秒”。 */
export function conditionText(cond: RuleCondition): string {
  const parts = `${fieldLabel(cond.field)} ${OP_LABELS[cond.op] ?? cond.op} ${conditionValueText(cond.field, cond.value)}`;
  return cond.for_s ? `${parts}，持续 ${durationText(cond.for_s)}` : parts;
}

/** 草稿条件的值提示：时长字段显示换算结果（如“= 10 分 0 秒”）。 */
export function conditionHint(cond: ConditionDraft): string {
  const spec = lookupField(cond.field);
  const value = conditionValueOf(cond);
  if (spec?.kind === "duration" && typeof value === "number" && value >= 0) {
    return `= ${durationText(value)}`;
  }
  return "";
}

/** 模板说明：事件模板列出事件，条件模板以“或”连接各组。 */
export function templateDescription(template: RuleTemplate): string {
  const trigger = template.rule.trigger;
  if (trigger.type === "event") {
    return `事件 ${(trigger.events ?? []).map((e) => `「${eventLabel(e)}」`).join("")}`;
  }
  return (trigger.any_of ?? []).map((g) => g.all_of.map(conditionText).join(" 且 ")).join("；或 ");
}

/** 作用范围摘要。 */
export function scopeText(
  rule: Pick<Rule, "scope">,
  devices: readonly Device[] | undefined,
): string {
  const scope = rule.scope;
  if (scope.type === "all") return "所有设备";
  if (scope.type === "tags") return `标签 ${(scope.tags ?? []).map((t) => `「${t}」`).join("")}`;
  const ids = scope.device_ids ?? [];
  if (ids.length === 1) {
    const name = devices?.find((d) => d.id === ids[0])?.name;
    return name ?? `设备 #${ids[0]}`;
  }
  return `${ids.length} 台设备`;
}

/** 草稿范围当前匹配的已启用设备（只评估启用设备；标签按任一匹配）。 */
export function matchedDevices(draft: RuleDraft, devices: readonly Device[] | undefined): Device[] {
  const enabled = (devices ?? []).filter((d) => d.enabled);
  switch (draft.scopeType) {
    case "all":
      return enabled;
    case "tags":
      return enabled.filter((d) => d.tags.some((t) => draft.tags.includes(t)));
    case "device":
      return enabled.filter((d) => draft.deviceIds.includes(d.id));
  }
}

/** 列表图标：事件规则取第一个事件的图标，条件规则用仪表。 */
export function ruleIcon(rule: Pick<Rule, "trigger">): IconName {
  if (rule.trigger.type === "event") return eventIcon(rule.trigger.events?.[0] ?? "");
  return "gauge";
}

export function triggerTypeText(rule: Pick<Rule, "trigger">): string {
  return rule.trigger.type === "event" ? "事件" : "条件";
}

/** 关闭 dry-run 确认框中“下次命中时将执行”的动作描述。 */
export function actionEffectText(
  action: Action | undefined,
  id: number,
  planName: (id: number) => string | undefined,
): string {
  if (!action) return `动作 #${id}`;
  switch (action.type) {
    case "plan": {
      const planId = action.config.plan_id;
      const name = planId ? planName(planId) : undefined;
      return `启动关机计划「${name ?? (planId ? `#${planId}` : "—")}」`;
    }
    case "notify":
      return `发送通知「${action.name}」`;
    case "ssh":
      return `在受控主机上执行 SSH 命令「${action.name}」`;
    case "script":
      return `运行本地脚本「${action.name}」`;
    case "http":
      return `发送 HTTP 请求「${action.name}」`;
  }
}

// ───────────────────────── 评估日志 ─────────────────────────

export const LOG_KIND_LABELS: Readonly<Record<string, string>> = {
  fired: "已触发",
  recovered: "已恢复",
  skipped_cooldown: "冷却跳过",
  quality_blocked: "质量不足",
  quality_restored: "质量恢复",
  dispatch_failed: "派发失败",
};

export const LOG_KIND_TONES: Readonly<Record<string, PillTone>> = {
  fired: "orange",
  recovered: "green",
  skipped_cooldown: "gray",
  quality_blocked: "gray",
  quality_restored: "green",
  dispatch_failed: "red",
};

export function logKindLabel(kind: string): string {
  return LOG_KIND_LABELS[kind] ?? kind;
}

/**
 * 质量原因串：后端格式为 `field:quality:reason`，多项以 `;` 连接（engine.go）。
 * 转为“电池容量 过期（通讯中断，显示旧值）”。
 */
function qualityReasonText(reason: string): string {
  return reason
    .split(";")
    .filter(Boolean)
    .map((item) => {
      const [field, quality, code] = item.split(":");
      const q = QUALITY_LABELS[quality as keyof typeof QUALITY_LABELS] ?? quality ?? "";
      const why = code ? reasonLabel(code) : "";
      return `${fieldLabel(field)} ${q}${why && why !== q ? `（${why}）` : ""}`;
    })
    .join("；");
}

/**
 * 日志依据文案。reason 取值（backend/internal/rule/engine.go、service.go）：
 * `condition_matched`、`condition_cleared`、`cooldown`、事件类型、`<事件>_cleared`、质量原因串；
 * 派发失败时追加 `;action_not_accepted`。未知原因原样显示。
 */
export function logReasonText(kind: string, reason: string): string {
  let rest = reason;
  let suffix = "";
  if (rest.endsWith(";action_not_accepted")) {
    rest = rest.slice(0, -";action_not_accepted".length);
    suffix = "；动作未被接受";
  }
  let text: string;
  if (kind === "quality_blocked") text = qualityReasonText(rest) || "字段质量不足";
  else if (kind === "quality_restored") text = "相关字段恢复为有效";
  else if (rest === "condition_matched") text = "条件成立";
  else if (rest === "condition_cleared") text = "所有条件组均确定不成立";
  else if (rest === "cooldown") text = "冷却期内，不执行动作";
  else if (
    rest.endsWith("_cleared") &&
    eventLabel(rest.slice(0, -"_cleared".length)) !== rest.slice(0, -"_cleared".length)
  )
    text = `事件「${eventLabel(rest.slice(0, -"_cleared".length))}」已解除`;
  else if (eventLabel(rest) !== rest) text = `事件「${eventLabel(rest)}」`;
  else text = rest || "—";
  return text + suffix;
}

/** 日志 values 摘要：最多 4 项；非 good 字段显示质量而不是 0。 */
export function logValuesText(values: FieldMap, limit = 4): string {
  return Object.entries(values)
    .slice(0, limit)
    .map(([key, field]) =>
      field.quality === "good"
        ? `${fieldLabel(key)} ${fieldText(key, field)}`
        : `${fieldLabel(key)} ${QUALITY_LABELS[field.quality]}`,
    )
    .join(" · ");
}
