import { fetchAllPages, MAX_PAGE_LIMIT } from "./devices.ts";
import { api } from "./client.ts";
import { normalizePlanRun } from "./normalize.ts";
import { confirmBody } from "./patch.ts";
import type { Page, PlanRun, WirePlanRun } from "./types.ts";

/*
 * 关机计划 API（docs/reference/shutdown-plans-api.md）。
 * - PUT 只替换提供的顶层字段，`stages` 与 `notify_channel_ids` 一旦提交则整体替换；
 * - 运行开始时冻结计划快照，之后修改只影响后续运行；
 * - 手动执行必须输入与计划名称完全一致的 `confirm_name`，并显式提交 `dry_run`
 *   （**未传 dry_run 按真实执行处理**）。
 */

export type OnMainsRestored = "cancel_pending" | "continue";
export type OnRestart = "resume" | "abort";

export interface PlanStage {
  delay_s: number;
  step_timeout_s: number;
  offline_wait_s: number;
  /** 关闭 ups-warden 所在主机：只能是最后一个阶段、只含一个步骤且不等待离线。 */
  self_shutdown?: boolean;
  steps: { action_id: number }[];
}

export interface Plan {
  id: number;
  name: string;
  countdown_s: number;
  on_mains_restored: OnMainsRestored;
  on_restart: OnRestart;
  notify_channel_ids: number[];
  stages: PlanStage[];
}

// 契约限制（shutdown-plans-api.md「计划配置」）。
export const PLAN_LIMITS = {
  nameMax: 128,
  stagesMax: 16,
  stepsMax: 32,
  channelsMax: 16,
  secondsMax: 3600,
  defaultCountdownS: 60,
  defaultStepTimeoutS: 30,
} as const;

/** 计划步骤只能引用这三类动作（notify / plan 动作不能作为步骤）。 */
export const STEP_ACTION_TYPES: ReadonlySet<string> = new Set(["ssh", "script", "http"]);

export function normalizePlan(wire: Plan): Plan {
  return {
    ...wire,
    notify_channel_ids: wire.notify_channel_ids ?? [],
    stages: (wire.stages ?? []).map((s) => ({
      delay_s: s.delay_s ?? 0,
      step_timeout_s: s.step_timeout_s ?? PLAN_LIMITS.defaultStepTimeoutS,
      offline_wait_s: s.offline_wait_s ?? 0,
      ...(s.self_shutdown ? { self_shutdown: true } : {}),
      steps: s.steps ?? [],
    })),
  };
}

// ───────────────────────── 草稿 ─────────────────────────

export interface StageDraft {
  /** 仅用于 React key，不提交。 */
  key: number;
  delayS: number;
  stepTimeoutS: number;
  offlineWaitS: number;
  selfShutdown: boolean;
  actionIds: number[];
}

export interface PlanDraft {
  name: string;
  countdownS: number;
  onMainsRestored: OnMainsRestored;
  onRestart: OnRestart;
  notifyChannelIds: number[];
  stages: StageDraft[];
}

let stageKeySeq = 0;
export function newStageKey(): number {
  stageKeySeq += 1;
  return stageKeySeq;
}

export function emptyStageDraft(selfShutdown = false): StageDraft {
  return {
    key: newStageKey(),
    delayS: 0,
    stepTimeoutS: PLAN_LIMITS.defaultStepTimeoutS,
    offlineWaitS: 0,
    selfShutdown,
    actionIds: [],
  };
}

export function emptyPlanDraft(): PlanDraft {
  return {
    name: "",
    countdownS: PLAN_LIMITS.defaultCountdownS,
    onMainsRestored: "cancel_pending",
    onRestart: "resume",
    notifyChannelIds: [],
    stages: [emptyStageDraft()],
  };
}

export function draftFromPlan(plan: Plan): PlanDraft {
  return {
    name: plan.name,
    countdownS: plan.countdown_s,
    onMainsRestored: plan.on_mains_restored,
    onRestart: plan.on_restart,
    notifyChannelIds: [...plan.notify_channel_ids],
    stages: plan.stages.map((s) => ({
      key: newStageKey(),
      delayS: s.delay_s,
      stepTimeoutS: s.step_timeout_s,
      offlineWaitS: s.offline_wait_s,
      selfShutdown: s.self_shutdown === true,
      actionIds: s.steps.map((step) => step.action_id),
    })),
  };
}

export function stagesOf(draft: PlanDraft): PlanStage[] {
  return draft.stages.map((s) => ({
    delay_s: s.delayS,
    step_timeout_s: s.stepTimeoutS,
    // 自关机阶段必须不等待离线。
    offline_wait_s: s.selfShutdown ? 0 : s.offlineWaitS,
    ...(s.selfShutdown ? { self_shutdown: true } : {}),
    steps: s.actionIds.map((action_id) => ({ action_id })),
  }));
}

export type PlanDraftErrors = Partial<Record<string, string>>;

/** 整数秒且在 [min, 3600]；NaN（输入为空或非数字）视为无效。 */
function inSeconds(value: number, min: number): boolean {
  return Number.isInteger(value) && value >= min && value <= PLAN_LIMITS.secondsMax;
}

/**
 * 前端结构性校验，与后端 plan.Validate 对齐（frontend-plan F6-1）；跨表引用
 * （动作已删除、渠道停用等）以后端 422 为准。
 * `actionTypes` 为已加载动作的 id → type；未加载时跳过类型检查。
 * 错误键：`name`、`countdown`、`stages`、`channels`、`stage-<序号>`（从 0 开始）。
 */
export function validatePlanDraft(
  draft: PlanDraft,
  actionTypes?: ReadonlyMap<number, string>,
): PlanDraftErrors {
  const errors: PlanDraftErrors = {};
  const name = draft.name.trim();
  if (!name) errors.name = "请填写计划名称";
  // 后端按 UTF-8 字节数限制（len(name) <= 128）。
  else if (new TextEncoder().encode(name).length > PLAN_LIMITS.nameMax)
    errors.name = `名称最多 ${PLAN_LIMITS.nameMax} 字节`;
  else if (name.includes("\u0000")) errors.name = "名称不能包含空字符";

  if (!inSeconds(draft.countdownS, 0)) errors.countdown = "倒计时需在 0～3600 秒";

  if (draft.notifyChannelIds.length > PLAN_LIMITS.channelsMax) {
    errors.channels = `通知渠道最多 ${PLAN_LIMITS.channelsMax} 个`;
  }

  const n = draft.stages.length;
  if (n === 0) errors.stages = "至少需要 1 个阶段";
  else if (n > PLAN_LIMITS.stagesMax) errors.stages = `最多 ${PLAN_LIMITS.stagesMax} 个阶段`;

  const seen = new Set<number>();
  draft.stages.forEach((stage, i) => {
    const key = `stage-${i}`;
    if (
      !inSeconds(stage.delayS, 0) ||
      !inSeconds(stage.stepTimeoutS, 1) ||
      (!stage.selfShutdown && !inSeconds(stage.offlineWaitS, 0))
    )
      errors[key] = "延迟与等待离线需在 0～3600 秒，单步超时需在 1～3600 秒";
    else if (stage.actionIds.length === 0) errors[key] = "每个阶段至少需要 1 个步骤";
    else if (stage.actionIds.length > PLAN_LIMITS.stepsMax)
      errors[key] = `每个阶段最多 ${PLAN_LIMITS.stepsMax} 个步骤`;
    if (stage.selfShutdown) {
      if (i !== n - 1) errors[key] = "「关闭本机」必须是最后一个阶段";
      else if (stage.actionIds.length !== 1) errors[key] = "「关闭本机」阶段只能有 1 个步骤";
    }
    for (const id of stage.actionIds) {
      if (seen.has(id)) {
        errors[key] ??= "同一动作在计划中只能出现一次";
        continue;
      }
      seen.add(id);
      if (actionTypes) {
        const type = actionTypes.get(id);
        if (type === undefined) errors[key] ??= `动作 #${id} 不存在`;
        else if (!STEP_ACTION_TYPES.has(type))
          errors[key] ??= "步骤只能是 SSH、本地脚本或 HTTP 动作";
      }
    }
  });
  return errors;
}

export function buildPlanCreateBody(draft: PlanDraft): Record<string, unknown> {
  return {
    name: draft.name.trim(),
    countdown_s: draft.countdownS,
    on_mains_restored: draft.onMainsRestored,
    on_restart: draft.onRestart,
    notify_channel_ids: [...draft.notifyChannelIds],
    stages: stagesOf(draft),
  };
}

/** PUT 请求体：只含修改过的顶层字段；stages 与 notify_channel_ids 修改时整体提交。 */
export function buildPlanUpdateBody(original: Plan, draft: PlanDraft): Record<string, unknown> {
  const body: Record<string, unknown> = {};
  const name = draft.name.trim();
  if (name !== original.name) body.name = name;
  if (draft.countdownS !== original.countdown_s) body.countdown_s = draft.countdownS;
  if (draft.onMainsRestored !== original.on_mains_restored)
    body.on_mains_restored = draft.onMainsRestored;
  if (draft.onRestart !== original.on_restart) body.on_restart = draft.onRestart;
  if (JSON.stringify(draft.notifyChannelIds) !== JSON.stringify(original.notify_channel_ids))
    body.notify_channel_ids = [...draft.notifyChannelIds];
  const stages = stagesOf(draft);
  if (JSON.stringify(stages) !== JSON.stringify(normalizePlan(original).stages))
    body.stages = stages;
  return body;
}

/** 手动执行请求体：用户输入的名称原样提交（不 trim），`dry_run` 总是显式提交。 */
export function buildPlanRunBody(options: {
  confirmName: string;
  dryRun: boolean;
  deviceId: number | null;
}): { confirm_name: string; dry_run: boolean; device_id?: number } {
  return {
    confirm_name: options.confirmName,
    dry_run: options.dryRun,
    ...(options.deviceId !== null ? { device_id: options.deviceId } : {}),
  };
}

// ───────────────────────── 请求 ─────────────────────────

export function listPlans(signal?: AbortSignal): Promise<Plan[]> {
  return fetchAllPages((before) =>
    api.request<Page<Plan>>("/plans", { query: { limit: MAX_PAGE_LIMIT, before }, signal }),
  ).then((items) => items.map(normalizePlan));
}

export function createPlan(body: Record<string, unknown>): Promise<Plan> {
  return api.request<Plan>("/plans", { method: "POST", body }).then(normalizePlan);
}

export function updatePlan(id: number, body: Record<string, unknown>): Promise<Plan> {
  return api.request<Plan>(`/plans/${id}`, { method: "PUT", body }).then(normalizePlan);
}

/** 请求体 `{confirm:true}`；被 plan 动作引用或已有运行记录时 409 resource_conflict。 */
export function deletePlan(id: number): Promise<{ ok: boolean }> {
  return api.request<{ ok: boolean }>(`/plans/${id}`, { method: "DELETE", body: confirmBody() });
}

/**
 * 202 只表示运行已创建。409 plan_run_active / plan_cycle_executed / plan_cycle_ended /
 * snapshot_unavailable；422 confirmation_required（名称不一致）。
 */
export function runPlan(
  id: number,
  body: ReturnType<typeof buildPlanRunBody>,
): Promise<{ accepted: boolean; run_id: number }> {
  return api.request<{ accepted: boolean; run_id: number }>(`/plans/${id}/run`, {
    method: "POST",
    body,
  });
}

export interface PlanRunQuery {
  plan_id?: number;
  before?: number;
  limit?: number;
}

export async function listPlanRuns(
  query: PlanRunQuery,
  signal?: AbortSignal,
): Promise<Page<PlanRun>> {
  const page = await api.request<Page<WirePlanRun>>("/plan-runs", {
    query: { ...query },
    signal,
  });
  return { items: (page.items ?? []).map(normalizePlanRun), next_before: page.next_before };
}

export async function getPlanRun(id: number, signal?: AbortSignal): Promise<PlanRun> {
  return normalizePlanRun(await api.request<WirePlanRun>(`/plan-runs/${id}`, { signal }));
}

/**
 * 取消运行：请求体 `{confirm:true}`，用户确认后才调用。202 只表示未开始的步骤已取消，
 * 执行中的步骤不被打断；409 plan_run_finished；503 plan_unavailable。
 */
export function cancelPlanRun(id: number): Promise<{ accepted: boolean }> {
  return api.request<{ accepted: boolean }>(`/plan-runs/${id}/cancel`, {
    method: "POST",
    body: confirmBody(),
  });
}
