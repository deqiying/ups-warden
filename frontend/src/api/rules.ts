import { lookupField, type FieldKind } from "../model/fields.ts";
import { api } from "./client.ts";
import { fetchAllPages, MAX_PAGE_LIMIT } from "./devices.ts";
import { normalizeFields } from "./normalize.ts";
import { confirmBody } from "./patch.ts";
import type { FieldMap, Page, WireField } from "./types.ts";

/*
 * 规则 API（docs/reference/rules-actions-hosts-api.md「规则」；对照 backend/internal/rule/types.go
 * 与 internal/api/rules.go）。
 * - 新建规则默认 dry-run；POST 未传 enabled 时后端按启用处理。
 * - PUT 只替换提供的顶层字段，`scope`、`trigger`、`actions`、`recovery_actions` 一旦提交则整体替换。
 * - 条件只支持两层 OR of AND；布尔、文本与枚举只允许 eq / ne；value 必须是字段类型对应的 JSON 值，
 *   不能提交字符串数字。
 * - 后端不要求为“关闭 dry-run”提供确认字段（Q-P2），确认只在界面完成。
 */

export type ScopeType = "device" | "tags" | "all";
export type TriggerType = "event" | "condition";
export type ConditionOp = "eq" | "ne" | "gt" | "gte" | "lt" | "lte";
export type ConditionValue = number | boolean | string;

export interface RuleScope {
  type: ScopeType;
  device_ids?: number[];
  tags?: string[];
}

export interface RuleCondition {
  field: string;
  op: ConditionOp;
  value: ConditionValue;
  /** 条件连续成立的秒数；0 时后端省略（omitempty）。 */
  for_s?: number;
}

export interface RuleTrigger {
  type: TriggerType;
  events?: string[];
  any_of?: { all_of: RuleCondition[] }[];
}

export interface ActionRef {
  action_id: number;
}

export interface Rule {
  id: number;
  name: string;
  enabled: boolean;
  dry_run: boolean;
  scope: RuleScope;
  trigger: RuleTrigger;
  actions: ActionRef[];
  recovery_actions: ActionRef[];
  cooldown_s: number;
}

/** POST / PUT 响应：规则加顶层 warnings（例如依赖 runtime_estimate 的提醒，不阻止保存）。 */
export interface RuleSaveResult {
  rule: Rule;
  warnings: string[];
}

/** GET /rules/templates 项：模板默认停用且 dry-run，动作留空，需用户显式选择。 */
export interface RuleTemplate {
  key: string;
  rule: Rule;
  /** "notify" 或 "plan"；关机模板为 plan，要求用户选择引用计划的 plan 动作。 */
  suggested_action_type: string;
}

export type RuleLogKind =
  | "fired"
  | "recovered"
  | "skipped_cooldown"
  | "quality_blocked"
  | "quality_restored"
  | "dispatch_failed";

interface WireRuleLog {
  id: number;
  rule_id: number;
  device_id: number;
  at: string;
  kind: string;
  reason: string;
  values: Record<string, WireField> | null;
  action_run_ids: number[] | null;
}

/** 规则评估日志（GET /rule-logs）。kind 未知时原样保留。 */
export interface RuleLog {
  id: number;
  rule_id: number;
  device_id: number;
  at: string;
  kind: RuleLogKind | (string & {});
  reason: string;
  values: FieldMap;
  action_run_ids: number[];
}

// 后端 rule.Validate 的上限。
export const RULE_LIMITS = {
  nameMax: 128,
  devicesMax: 100,
  tagsMax: 32,
  tagBytesMax: 64,
  eventsMax: 16,
  groupsMax: 16,
  conditionsMax: 16,
  actionsMax: 32,
  secondsMax: 604800,
  textBytesMax: 256,
} as const;

export const ALL_OPS: readonly ConditionOp[] = ["eq", "ne", "gt", "gte", "lt", "lte"];
const EQUALITY_OPS: readonly ConditionOp[] = ["eq", "ne"];

/** 字段类型允许的运算符：布尔、文本、枚举只允许 eq / ne。 */
export function opsForKind(kind: FieldKind): readonly ConditionOp[] {
  return kind === "number" || kind === "duration" ? ALL_OPS : EQUALITY_OPS;
}

export function normalizeRule(wire: Rule): Rule {
  const trigger = wire.trigger ?? { type: "event" };
  return {
    ...wire,
    scope: {
      type: wire.scope?.type ?? "all",
      ...(wire.scope?.device_ids ? { device_ids: wire.scope.device_ids } : {}),
      ...(wire.scope?.tags ? { tags: wire.scope.tags } : {}),
    },
    trigger: {
      type: trigger.type,
      ...(trigger.events ? { events: trigger.events } : {}),
      ...(trigger.any_of
        ? { any_of: trigger.any_of.map((g) => ({ all_of: g.all_of ?? [] })) }
        : {}),
    },
    actions: wire.actions ?? [],
    recovery_actions: wire.recovery_actions ?? [],
    cooldown_s: wire.cooldown_s ?? 0,
  };
}

function normalizeLog(wire: WireRuleLog): RuleLog {
  return {
    ...wire,
    values: normalizeFields(wire.values),
    action_run_ids: wire.action_run_ids ?? [],
  };
}

// ───────────────────────── 草稿 ─────────────────────────

export interface ConditionDraft {
  /** 仅用于 React key，不提交。 */
  key: number;
  field: string;
  op: ConditionOp;
  /** 输入框原文；提交时按字段类型转换（数值转为 number、布尔转为 boolean）。 */
  value: string;
  forS: number;
}

export interface GroupDraft {
  key: number;
  conditions: ConditionDraft[];
}

export interface RuleDraft {
  name: string;
  enabled: boolean;
  dryRun: boolean;
  scopeType: ScopeType;
  deviceIds: number[];
  tags: string[];
  triggerType: TriggerType;
  events: string[];
  groups: GroupDraft[];
  actionIds: number[];
  recoveryActionIds: number[];
  cooldownS: number;
}

let keySeq = 0;
function nextKey(): number {
  keySeq += 1;
  return keySeq;
}

/** 字段类型的默认值：枚举取第一个取值，布尔为 true，数值与文本为空。 */
export function defaultValueFor(field: string): string {
  const spec = lookupField(field);
  if (!spec) return "";
  if (spec.kind === "enum") return spec.enum[0] ?? "";
  if (spec.kind === "bool") return "true";
  return "";
}

export function emptyConditionDraft(field = "power_state"): ConditionDraft {
  return { key: nextKey(), field, op: "eq", value: defaultValueFor(field), forS: 0 };
}

export function emptyGroupDraft(): GroupDraft {
  return { key: nextKey(), conditions: [emptyConditionDraft()] };
}

function valueText(value: ConditionValue | undefined): string {
  if (value === undefined || value === null) return "";
  return String(value);
}

export function draftFromRule(rule: Rule): RuleDraft {
  const r = normalizeRule(rule);
  return {
    name: r.name,
    enabled: r.enabled,
    dryRun: r.dry_run,
    scopeType: r.scope.type,
    deviceIds: [...(r.scope.device_ids ?? [])],
    tags: [...(r.scope.tags ?? [])],
    triggerType: r.trigger.type,
    events: [...(r.trigger.events ?? [])],
    groups:
      r.trigger.type === "condition"
        ? (r.trigger.any_of ?? []).map((g) => ({
            key: nextKey(),
            conditions: g.all_of.map((c) => ({
              key: nextKey(),
              field: c.field,
              op: c.op,
              value: valueText(c.value),
              forS: c.for_s ?? 0,
            })),
          }))
        : [],
    actionIds: r.actions.map((a) => a.action_id),
    recoveryActionIds: r.recovery_actions.map((a) => a.action_id),
    cooldownS: r.cooldown_s,
  };
}

/** 新建规则：默认启用、dry-run，所有设备，事件触发。 */
export function emptyRuleDraft(): RuleDraft {
  return {
    name: "",
    enabled: true,
    dryRun: true,
    scopeType: "all",
    deviceIds: [],
    tags: [],
    triggerType: "event",
    events: [],
    groups: [],
    actionIds: [],
    recoveryActionIds: [],
    cooldownS: 0,
  };
}

/** 条件值按字段类型转换；无法转换时返回 undefined（由校验给出错误）。 */
export function conditionValueOf(
  draft: Pick<ConditionDraft, "field" | "value">,
): ConditionValue | undefined {
  const spec = lookupField(draft.field);
  if (!spec) return undefined;
  switch (spec.kind) {
    case "number":
    case "duration": {
      if (draft.value.trim() === "") return undefined;
      const n = Number(draft.value);
      return Number.isFinite(n) ? n : undefined;
    }
    case "bool":
      return draft.value === "true" ? true : draft.value === "false" ? false : undefined;
    default:
      return draft.value;
  }
}

export function scopeOf(draft: RuleDraft): RuleScope {
  switch (draft.scopeType) {
    case "device":
      return { type: "device", device_ids: [...draft.deviceIds] };
    case "tags":
      return { type: "tags", tags: [...draft.tags] };
    default:
      return { type: "all" };
  }
}

export function triggerOf(draft: RuleDraft): RuleTrigger {
  if (draft.triggerType === "event") return { type: "event", events: [...draft.events] };
  return {
    type: "condition",
    any_of: draft.groups.map((g) => ({
      all_of: g.conditions.map((c) => ({
        field: c.field,
        op: c.op,
        // 校验通过后才提交；未通过时保留原文便于定位，不会到达后端。
        value: conditionValueOf(c) ?? c.value,
        ...(c.forS > 0 ? { for_s: c.forS } : {}),
      })),
    })),
  };
}

const refs = (ids: readonly number[]): ActionRef[] => ids.map((action_id) => ({ action_id }));

export type RuleDraftErrors = Partial<Record<string, string>>;

const utf8Bytes = (text: string) => new TextEncoder().encode(text).length;
const inSeconds = (value: number) =>
  Number.isInteger(value) && value >= 0 && value <= RULE_LIMITS.secondsMax;

export interface ValidateRuleOptions {
  /** 已加载动作的 id → type；未加载时跳过存在性与类型检查。 */
  actionTypes?: ReadonlyMap<number, string>;
  /** 来自模板的建议动作类型；为 "plan" 时命中动作中必须包含 plan 动作（关机模板）。 */
  requiredActionType?: string | null;
}

/**
 * 前端结构性校验，与后端 rule.Validate 对齐（frontend-plan F7）；跨表引用
 * （设备或动作已删除）以后端 422 为准。
 * 错误键：`name`、`cooldown`、`scope`、`trigger`、`cond-<组>-<序号>`、`actions`、`recovery`。
 */
export function validateRuleDraft(
  draft: RuleDraft,
  options: ValidateRuleOptions = {},
): RuleDraftErrors {
  const errors: RuleDraftErrors = {};
  const name = draft.name.trim();
  if (!name) errors.name = "请填写规则名称";
  else if (utf8Bytes(name) > RULE_LIMITS.nameMax)
    errors.name = `名称最多 ${RULE_LIMITS.nameMax} 字节`;
  else if (name.includes("\u0000")) errors.name = "名称不能包含空字符";

  if (!inSeconds(draft.cooldownS)) errors.cooldown = "冷却时间需在 0～604800 秒";

  if (draft.scopeType === "device") {
    if (draft.deviceIds.length === 0) errors.scope = "请至少选择 1 台设备";
    else if (draft.deviceIds.length > RULE_LIMITS.devicesMax)
      errors.scope = `最多选择 ${RULE_LIMITS.devicesMax} 台设备`;
  } else if (draft.scopeType === "tags") {
    if (draft.tags.length === 0) errors.scope = "请至少添加 1 个标签";
    else if (draft.tags.length > RULE_LIMITS.tagsMax)
      errors.scope = `最多 ${RULE_LIMITS.tagsMax} 个标签`;
    else if (draft.tags.some((t) => validateTag(t) !== null)) errors.scope = "标签无效";
    else if (new Set(draft.tags).size !== draft.tags.length) errors.scope = "标签不能重复";
  }

  if (draft.triggerType === "event") {
    if (draft.events.length === 0) errors.trigger = "请至少选择 1 个事件";
    else if (draft.events.length > RULE_LIMITS.eventsMax)
      errors.trigger = `最多选择 ${RULE_LIMITS.eventsMax} 个事件`;
  } else {
    if (draft.groups.length === 0) errors.trigger = "请至少添加 1 个条件组";
    else if (draft.groups.length > RULE_LIMITS.groupsMax)
      errors.trigger = `最多 ${RULE_LIMITS.groupsMax} 个条件组`;
    draft.groups.forEach((group, gi) => {
      if (group.conditions.length === 0 || group.conditions.length > RULE_LIMITS.conditionsMax) {
        errors.trigger ??= `每个条件组需包含 1～${RULE_LIMITS.conditionsMax} 个条件`;
      }
      group.conditions.forEach((cond, ci) => {
        const message = validateCondition(cond);
        if (message) errors[`cond-${gi}-${ci}`] = message;
      });
    });
  }

  validateRefs(draft.actionIds, "actions", errors, options.actionTypes, true);
  validateRefs(draft.recoveryActionIds, "recovery", errors, options.actionTypes, false);
  if (
    !errors.actions &&
    options.requiredActionType === "plan" &&
    options.actionTypes &&
    !draft.actionIds.some((id) => options.actionTypes?.get(id) === "plan")
  ) {
    errors.actions = "关机模板需要选择一个「启动关机计划」（plan）动作，模板不会自动挑选计划";
  }
  return errors;
}

function validateRefs(
  ids: readonly number[],
  key: "actions" | "recovery",
  errors: RuleDraftErrors,
  actionTypes: ReadonlyMap<number, string> | undefined,
  required: boolean,
) {
  if (required && ids.length === 0) errors[key] = "请至少选择 1 个动作";
  else if (ids.length > RULE_LIMITS.actionsMax)
    errors[key] = `最多 ${RULE_LIMITS.actionsMax} 个动作`;
  else if (new Set(ids).size !== ids.length) errors[key] = "同一列表中的动作不能重复";
  else if (actionTypes) {
    const missing = ids.find((id) => !actionTypes.has(id));
    if (missing !== undefined) errors[key] = `动作 #${missing} 不存在（可能已删除），请移除`;
  }
}

/** 标签校验（TokenInput 入参已 trim）：非空、≤64 字节、不含空字符。 */
export function validateTag(tag: string): string | null {
  if (!tag.trim()) return "标签不能为空";
  if (utf8Bytes(tag) > RULE_LIMITS.tagBytesMax) return `标签最多 ${RULE_LIMITS.tagBytesMax} 字节`;
  if (tag.includes("\u0000")) return "标签不能包含空字符";
  return null;
}

/** 单个条件的错误文案；合法返回 null。 */
export function validateCondition(cond: ConditionDraft): string | null {
  const spec = lookupField(cond.field);
  if (!spec) return "请选择字段";
  if (!opsForKind(spec.kind).includes(cond.op)) return "该字段只支持“等于 / 不等于”";
  if (!inSeconds(cond.forS)) return "持续时间需在 0～604800 秒";
  const value = conditionValueOf(cond);
  switch (spec.kind) {
    case "number":
    case "duration":
      if (value === undefined) return "请填写有效数值";
      break;
    case "bool":
      if (value === undefined) return "请选择是或否";
      break;
    case "enum":
      if (!spec.enum.includes(cond.value)) return "请选择取值";
      break;
    case "text":
      if (utf8Bytes(cond.value) > RULE_LIMITS.textBytesMax)
        return `文本最多 ${RULE_LIMITS.textBytesMax} 字节`;
      if (cond.value.includes("\u0000")) return "文本不能包含空字符";
      break;
  }
  return null;
}

/** POST 请求体：显式提交 enabled 与 dry_run（新建默认 dry-run）。 */
export function buildRuleCreateBody(draft: RuleDraft): Record<string, unknown> {
  return {
    name: draft.name.trim(),
    enabled: draft.enabled,
    dry_run: draft.dryRun,
    scope: scopeOf(draft),
    trigger: triggerOf(draft),
    actions: refs(draft.actionIds),
    recovery_actions: refs(draft.recoveryActionIds),
    cooldown_s: draft.cooldownS,
  };
}

/**
 * PUT 请求体：只含修改过的顶层字段。原规则先经过同一草稿转换再比较，
 * 避免字段顺序或省略的 for_s 造成误判。
 */
export function buildRuleUpdateBody(original: Rule, draft: RuleDraft): Record<string, unknown> {
  const base = draftFromRule(original);
  const body: Record<string, unknown> = {};
  const name = draft.name.trim();
  if (name !== original.name) body.name = name;
  if (draft.enabled !== original.enabled) body.enabled = draft.enabled;
  if (draft.dryRun !== original.dry_run) body.dry_run = draft.dryRun;
  const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
  if (!same(scopeOf(draft), scopeOf(base))) body.scope = scopeOf(draft);
  if (!same(triggerOf(draft), triggerOf(base))) body.trigger = triggerOf(draft);
  if (!same(draft.actionIds, base.actionIds)) body.actions = refs(draft.actionIds);
  if (!same(draft.recoveryActionIds, base.recoveryActionIds))
    body.recovery_actions = refs(draft.recoveryActionIds);
  if (draft.cooldownS !== original.cooldown_s) body.cooldown_s = draft.cooldownS;
  return body;
}

// ───────────────────────── 请求 ─────────────────────────

export function listRules(signal?: AbortSignal): Promise<Rule[]> {
  return fetchAllPages((before) =>
    api.request<Page<Rule>>("/rules", { query: { limit: MAX_PAGE_LIMIT, before }, signal }),
  ).then((items) => items.map(normalizeRule));
}

function toSaveResult(wire: Rule & { warnings?: string[] | null }): RuleSaveResult {
  const { warnings, ...rule } = wire;
  return { rule: normalizeRule(rule), warnings: warnings ?? [] };
}

/** 422 validation_failed：message 给出原因（含设备 / 动作不存在）。 */
export function createRule(body: Record<string, unknown>): Promise<RuleSaveResult> {
  return api
    .request<Rule & { warnings?: string[] | null }>("/rules", { method: "POST", body })
    .then(toSaveResult);
}

export function updateRule(id: number, body: Record<string, unknown>): Promise<RuleSaveResult> {
  return api
    .request<Rule & { warnings?: string[] | null }>(`/rules/${id}`, { method: "PUT", body })
    .then(toSaveResult);
}

/** 请求体 `{confirm:true}`；已有评估日志（需保留证据）时 409 resource_conflict。 */
export function deleteRule(id: number): Promise<{ ok: boolean }> {
  return api.request<{ ok: boolean }>(`/rules/${id}`, { method: "DELETE", body: confirmBody() });
}

export async function listRuleTemplates(signal?: AbortSignal): Promise<RuleTemplate[]> {
  const res = await api.request<{ items: RuleTemplate[] | null }>("/rules/templates", { signal });
  return (res.items ?? []).map((t) => ({ ...t, rule: normalizeRule(t.rule) }));
}

export interface RuleLogQuery {
  rule_id?: number;
  device_id?: number;
  before?: number;
  limit?: number;
}

export async function listRuleLogs(
  query: RuleLogQuery,
  signal?: AbortSignal,
): Promise<Page<RuleLog>> {
  const page = await api.request<Page<WireRuleLog>>("/rule-logs", { query: { ...query }, signal });
  return { items: (page.items ?? []).map(normalizeLog), next_before: page.next_before };
}
