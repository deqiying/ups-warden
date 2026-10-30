/*
 * 关机计划页的纯函数：计划摘要、运行时间线分组、步骤与运行状态的显示。
 * 契约见 docs/reference/shutdown-plans-api.md。运行视图不含冻结的计划快照（Q-C5），
 * 阶段与步骤只能从 `run.steps` 推导，阶段参数（延迟、等待离线）无法从运行中得知。
 */
import type { Action } from "../../api/actions.ts";
import { STEP_ACTION_TYPES, type Plan } from "../../api/plans.ts";
import type { PlanRun, PlanRunStatus, PlanStep } from "../../api/types.ts";
import type { PillTone } from "../../components/index.ts";
import type { IconName } from "../../icons/Icon.tsx";
import { PLAN_RUN_STATUS_LABELS, PLAN_STEP_STATUS_LABELS } from "../../model/labels.ts";

export interface PlanStats {
  stages: number;
  steps: number;
  selfShutdown: boolean;
}

export function planStats(plan: Plan): PlanStats {
  return {
    stages: plan.stages.length,
    steps: plan.stages.reduce((n, s) => n + s.steps.length, 0),
    selfShutdown: plan.stages.some((s) => s.self_shutdown === true),
  };
}

/** 引用该计划的 plan 动作（规则通过它们触发计划；被引用时不能删除）。 */
export function planReferences(planId: number, actions: readonly Action[] | undefined): Action[] {
  return (actions ?? []).filter((a) => a.type === "plan" && a.config.plan_id === planId);
}

/** 可作为计划步骤的动作（ssh / script / http）。 */
export function stepActions(actions: readonly Action[] | undefined): Action[] {
  return (actions ?? []).filter((a) => STEP_ACTION_TYPES.has(a.type));
}

// ───────────────────────── 运行 ─────────────────────────

export const PLAN_RUN_TONES: Readonly<Record<PlanRunStatus, PillTone>> = {
  countdown: "orange",
  running: "blue",
  completed: "green",
  completed_with_errors: "orange",
  cancelled: "gray",
  aborted: "red",
};

export function planRunStatusText(run: Pick<PlanRun, "status" | "dry_run">): string {
  const label = PLAN_RUN_STATUS_LABELS[run.status] ?? run.status;
  return run.dry_run ? `Dry-run · ${label}` : label;
}

export function planRunTone(run: Pick<PlanRun, "status" | "dry_run">): PillTone {
  if (run.dry_run && (run.status === "completed" || run.status === "running")) return "indigo";
  return PLAN_RUN_TONES[run.status] ?? "gray";
}

const TERMINAL_STEP = new Set(["sent", "offline", "failed", "cancelled", "unknown"]);

export function isTerminalStep(step: Pick<PlanStep, "status">): boolean {
  return TERMINAL_STEP.has(step.status);
}

/** 已结束步骤数 / 总步骤数，如“3 / 6”。 */
export function stepsDoneText(run: PlanRun): string {
  return `${run.steps.filter(isTerminalStep).length} / ${run.steps.length}`;
}

/** 步骤是否有问题：失败、结果未知或等待离线超时。 */
export function isProblemStep(step: PlanStep): boolean {
  return step.status === "failed" || step.status === "unknown" || step.result.offline_timeout;
}

export interface RunStage {
  stage: number;
  steps: PlanStep[];
}

/** 按阶段分组（后端的阶段与步骤序号都从 0 开始，按序排列；显示阶段号用 stageNumber）。 */
export function runStages(run: PlanRun): RunStage[] {
  const map = new Map<number, PlanStep[]>();
  for (const step of run.steps) {
    const list = map.get(step.stage);
    if (list) list.push(step);
    else map.set(step.stage, [step]);
  }
  return [...map.entries()]
    .sort(([a], [b]) => a - b)
    .map(([stage, steps]) => ({ stage, steps: [...steps].sort((a, b) => a.step - b.step) }));
}

export type StageNode = "pending" | "now" | "done" | "warn" | "cancelled";

/** 时间线节点状态：执行中的阶段为 now；全部结束后按是否有问题区分 done / warn。 */
export function stageNode(run: PlanRun, stage: RunStage): StageNode {
  const active = run.status === "running" || run.status === "countdown";
  if (stage.steps.some((s) => s.status === "running")) return "now";
  if (active && run.status === "running" && run.current_stage === stage.stage) return "now";
  if (!stage.steps.every(isTerminalStep)) return "pending";
  if (stage.steps.some(isProblemStep)) return "warn";
  if (stage.steps.every((s) => s.status === "cancelled")) return "cancelled";
  return "done";
}

export interface StepStatusView {
  label: string;
  tone: PillTone;
  icon?: IconName;
}

/**
 * 步骤状态胶囊。`sent` 只表示动作成功类终态（命令已被接受），不代表关机完成；
 * `sent` 且 `result.offline_timeout` 表示等待离线超时，计为错误。
 */
export function stepStatusView(step: PlanStep): StepStatusView {
  switch (step.status) {
    case "waiting":
      return { label: PLAN_STEP_STATUS_LABELS.waiting, tone: "gray" };
    case "running":
      return { label: PLAN_STEP_STATUS_LABELS.running, tone: "blue" };
    case "sent":
      return step.result.offline_timeout
        ? { label: "等待离线超时", tone: "orange", icon: "alert" }
        : { label: PLAN_STEP_STATUS_LABELS.sent, tone: "blue", icon: "paperplane" };
    case "offline":
      return { label: PLAN_STEP_STATUS_LABELS.offline, tone: "green", icon: "check" };
    case "failed":
      return { label: PLAN_STEP_STATUS_LABELS.failed, tone: "red", icon: "x" };
    case "cancelled":
      return { label: PLAN_STEP_STATUS_LABELS.cancelled, tone: "gray", icon: "slash-circle" };
    case "unknown":
      return { label: "未知 · 未重放", tone: "orange", icon: "question" };
    default:
      return { label: String(step.status), tone: "gray" };
  }
}

/** 动作执行记录入口（每个已开始的步骤对应一条 action_runs）。 */
export function actionRunPath(step: PlanStep): string | null {
  const id = step.result.action_run_id;
  return id === null ? null : `/hosts-actions?tab=runs&action=${step.action_id}&open=${id}`;
}
