/*
 * 关机计划运行的显示派生（总览停电主卡、设备详情横幅）。
 * 倒计时由 countdown_until 与本地时钟计算；运行视图不含冻结的计划快照（Q-C5），
 * 阶段总数只能从 steps 推导，不显示“由哪条规则触发”。
 */
import type { PlanRun } from "../api/types.ts";
import { PLAN_RUN_STATUS_LABELS } from "./labels.ts";

/** 倒计时剩余秒数；非倒计时状态或时间无效时返回 null。 */
export function countdownRemaining(run: PlanRun, nowMs: number): number | null {
  if (run.status !== "countdown" || !run.countdown_until) return null;
  const until = Date.parse(run.countdown_until);
  if (Number.isNaN(until)) return null;
  return Math.max(0, Math.ceil((until - nowMs) / 1000));
}

/** 倒计时剩余比例（环形进度）：总时长取 started_at → countdown_until。 */
export function countdownFraction(run: PlanRun, nowMs: number): number | null {
  if (run.status !== "countdown" || !run.countdown_until) return null;
  const start = Date.parse(run.started_at);
  const until = Date.parse(run.countdown_until);
  if (Number.isNaN(start) || Number.isNaN(until) || until <= start) return null;
  return Math.min(1, Math.max(0, (until - nowMs) / (until - start)));
}

/** 由 steps 推导的阶段数（Q-C5）；没有步骤时为 0。 */
export function stageCount(run: PlanRun): number {
  return run.steps.reduce((max, s) => Math.max(max, s.stage), 0);
}

const DONE_STEP = new Set(["sent", "offline", "failed", "cancelled", "unknown"]);

/** 执行进度文案，如“执行中 · 阶段 2 / 3 · 已完成 3 / 5 步”。 */
export function runProgressText(run: PlanRun): string {
  const parts = [PLAN_RUN_STATUS_LABELS[run.status] ?? run.status];
  const stages = stageCount(run);
  if (run.status === "running" && run.current_stage > 0) {
    parts.push(stages > 0 ? `阶段 ${run.current_stage} / ${stages}` : `阶段 ${run.current_stage}`);
  }
  if (run.steps.length > 0) {
    const done = run.steps.filter((s) => DONE_STEP.has(s.status)).length;
    parts.push(`已完成 ${done} / ${run.steps.length} 步`);
  }
  return parts.join(" · ");
}

/** 计划运行的进度页入口（F6 实现运行视图）。 */
export function planRunPath(run: Pick<PlanRun, "plan_id">): string {
  return `/plans/${run.plan_id}?tab=run`;
}
