import { describe, expect, it } from "vite-plus/test";
import { normalizePlanRun } from "../../api/normalize.ts";
import type { PlanRun } from "../../api/types.ts";
import { wireAction, wirePlan, wirePlanRun, wirePlanStep } from "../../test/fixtures.ts";
import {
  actionRunPath,
  isProblemStep,
  isTerminalStep,
  planReferences,
  planRunStatusText,
  planRunTone,
  planStats,
  runStages,
  stageNode,
  stepActions,
  stepsDoneText,
  stepStatusView,
} from "./model.ts";

type WireStep = Parameters<typeof wirePlanStep>[0];

/** 构造已规范化的运行（steps.result 补齐为完整对象）。 */
function runOf(
  steps: WireStep[],
  overrides: Partial<Parameters<typeof wirePlanRun>[0]> = {},
): PlanRun {
  return normalizePlanRun(wirePlanRun({ steps: steps.map((s) => wirePlanStep(s)), ...overrides }));
}

const stepOf = (overrides: WireStep = {}) => runOf([overrides]).steps[0];

describe("planStats / planReferences / stepActions", () => {
  it("统计阶段数、步骤数与是否含自关机阶段", () => {
    expect(planStats(wirePlan())).toEqual({ stages: 2, steps: 2, selfShutdown: true });
    const plain = wirePlan({
      stages: [
        {
          delay_s: 0,
          step_timeout_s: 30,
          offline_wait_s: 0,
          steps: [{ action_id: 1 }, { action_id: 2 }],
        },
      ],
    });
    expect(planStats(plain)).toEqual({ stages: 1, steps: 2, selfShutdown: false });
  });

  it("planReferences 只返回指向该计划的 plan 动作；动作未加载时为空", () => {
    const actions = [
      wireAction({ id: 1, type: "plan", config: { plan_id: 7 } }),
      wireAction({ id: 2, type: "plan", config: { plan_id: 8 } }),
      // 非 plan 动作即使带有 plan_id 也不算引用。
      wireAction({ id: 3, type: "ssh", config: { plan_id: 7 } }),
    ];
    expect(planReferences(7, actions).map((a) => a.id)).toEqual([1]);
    expect(planReferences(9, actions)).toEqual([]);
    expect(planReferences(7, undefined)).toEqual([]);
  });

  it("stepActions 只保留 ssh / script / http", () => {
    const actions = (["ssh", "script", "http", "notify", "plan"] as const).map((type, i) =>
      wireAction({ id: i + 1, type }),
    );
    expect(stepActions(actions).map((a) => a.type)).toEqual(["ssh", "script", "http"]);
    expect(stepActions(undefined)).toEqual([]);
  });
});

describe("runStages", () => {
  it("按阶段分组，阶段与步骤均升序，与输入顺序无关", () => {
    const run = runOf([
      { stage: 1, step: 0, action_id: 20 },
      { stage: 0, step: 1, action_id: 12 },
      { stage: 0, step: 0, action_id: 11 },
      { stage: 1, step: 1, action_id: 22 },
    ]);
    const stages = runStages(run);
    expect(stages.map((s) => s.stage)).toEqual([0, 1]);
    expect(stages[0].steps.map((s) => s.action_id)).toEqual([11, 12]);
    expect(stages[1].steps.map((s) => s.action_id)).toEqual([20, 22]);
  });

  it("阶段号不连续时仍按数值排序（10 排在 2 之后）", () => {
    const run = runOf([{ stage: 10 }, { stage: 2 }, { stage: 1 }]);
    expect(runStages(run).map((s) => s.stage)).toEqual([1, 2, 10]);
  });

  it("没有步骤时为空；不修改原 steps 顺序", () => {
    expect(runStages(runOf([]))).toEqual([]);
    const run = runOf([
      { stage: 0, step: 1 },
      { stage: 0, step: 0 },
    ]);
    runStages(run);
    expect(run.steps.map((s) => s.step)).toEqual([1, 0]);
  });
});

describe("stageNode", () => {
  const nodeOf = (run: PlanRun, stage = 0) =>
    stageNode(
      run,
      runStages(run).find((s) => s.stage === stage)!,
    );

  it("有执行中的步骤：now", () => {
    const run = runOf([{ status: "running" }, { step: 1, status: "waiting" }], {
      status: "running",
      current_stage: 0,
    });
    expect(nodeOf(run)).toBe("now");
  });

  it("运行执行中且 current_stage 指向该阶段：now（步骤尚未标记为 running）", () => {
    const run = runOf([{ status: "waiting" }], { status: "running", current_stage: 0 });
    expect(nodeOf(run)).toBe("now");
  });

  it("尚有未结束步骤：pending（倒计时中、后续阶段、被终止的运行）", () => {
    const countdown = runOf([{ status: "waiting" }], { status: "countdown", current_stage: 0 });
    expect(nodeOf(countdown)).toBe("pending");

    const later = runOf(
      [
        { stage: 0, status: "running" },
        { stage: 1, status: "waiting" },
      ],
      { status: "running", current_stage: 0 },
    );
    expect(nodeOf(later, 1)).toBe("pending");

    const aborted = runOf([{ status: "waiting" }], { status: "aborted", current_stage: 0 });
    expect(nodeOf(aborted)).toBe("pending");
  });

  it("全部结束且无问题：done（sent / offline 都算成功类终态）", () => {
    const run = runOf([{ status: "sent" }, { step: 1, status: "offline" }], {
      status: "completed",
      current_stage: 0,
    });
    expect(nodeOf(run)).toBe("done");
  });

  it("全部结束但有失败 / 未知 / 等待离线超时：warn", () => {
    const base = { status: "completed_with_errors" } as const;
    expect(nodeOf(runOf([{ status: "failed" }], base))).toBe("warn");
    expect(nodeOf(runOf([{ status: "unknown" }], base))).toBe("warn");
    expect(nodeOf(runOf([{ status: "sent", result: { offline_timeout: true } }], base))).toBe(
      "warn",
    );
    // 同一阶段只要有一个步骤有问题即 warn。
    expect(nodeOf(runOf([{ status: "offline" }, { step: 1, status: "failed" }], base))).toBe(
      "warn",
    );
  });

  it("全部步骤被取消：cancelled；取消与已完成混合：done", () => {
    const base = { status: "cancelled" } as const;
    expect(nodeOf(runOf([{ status: "cancelled" }, { step: 1, status: "cancelled" }], base))).toBe(
      "cancelled",
    );
    expect(nodeOf(runOf([{ status: "sent" }, { step: 1, status: "cancelled" }], base))).toBe(
      "done",
    );
  });
});

describe("stepStatusView", () => {
  it("各状态的文案、色调与图标", () => {
    expect(stepStatusView(stepOf({ status: "waiting" }))).toEqual({ label: "等待", tone: "gray" });
    expect(stepStatusView(stepOf({ status: "running" }))).toEqual({
      label: "执行中",
      tone: "blue",
    });
    expect(stepStatusView(stepOf({ status: "offline" }))).toMatchObject({
      label: "已离线",
      tone: "green",
    });
    expect(stepStatusView(stepOf({ status: "failed" }))).toMatchObject({
      label: "失败",
      tone: "red",
    });
    expect(stepStatusView(stepOf({ status: "cancelled" }))).toMatchObject({
      label: "已取消",
      tone: "gray",
    });
  });

  it("sent：命令已被接受；offline_timeout 时改为「等待离线超时」", () => {
    expect(stepStatusView(stepOf({ status: "sent" }))).toMatchObject({
      label: "已发送",
      tone: "blue",
    });
    expect(
      stepStatusView(stepOf({ status: "sent", result: { offline_timeout: true } })),
    ).toMatchObject({ label: "等待离线超时", tone: "orange", icon: "alert" });
  });

  it("unknown：未知（不重放），按警示色显示", () => {
    const view = stepStatusView(stepOf({ status: "unknown" }));
    expect(view.label).toMatch(/^未知/);
    expect(view.tone).toBe("orange");
  });

  it("offline_timeout 只对 sent 生效", () => {
    const view = stepStatusView(stepOf({ status: "offline", result: { offline_timeout: true } }));
    expect(view.label).toBe("已离线");
  });

  it("未知状态原样显示，色调为灰", () => {
    const step = { ...stepOf(), status: "weird" } as never;
    expect(stepStatusView(step)).toEqual({ label: "weird", tone: "gray" });
  });
});

describe("isProblemStep / isTerminalStep / stepsDoneText", () => {
  it("失败、未知、等待离线超时算问题步骤；其余不算", () => {
    expect(isProblemStep(stepOf({ status: "failed" }))).toBe(true);
    expect(isProblemStep(stepOf({ status: "unknown" }))).toBe(true);
    expect(isProblemStep(stepOf({ status: "sent", result: { offline_timeout: true } }))).toBe(true);
    expect(isProblemStep(stepOf({ status: "sent" }))).toBe(false);
    expect(isProblemStep(stepOf({ status: "cancelled" }))).toBe(false);
    expect(isProblemStep(stepOf({ status: "running" }))).toBe(false);
  });

  it("waiting / running 不是终态", () => {
    expect(isTerminalStep(stepOf({ status: "waiting" }))).toBe(false);
    expect(isTerminalStep(stepOf({ status: "running" }))).toBe(false);
    for (const status of ["sent", "offline", "failed", "cancelled", "unknown"] as const) {
      expect(isTerminalStep(stepOf({ status }))).toBe(true);
    }
  });

  it("已结束步骤数 / 总步骤数", () => {
    const run = runOf([
      { step: 1, status: "sent" },
      { step: 2, status: "running" },
      { step: 3, status: "waiting" },
      { step: 4, status: "failed" },
    ]);
    expect(stepsDoneText(run)).toBe("2 / 4");
    expect(stepsDoneText(runOf([]))).toBe("0 / 0");
  });
});

describe("planRunStatusText / planRunTone", () => {
  it("dry-run 运行在状态前加前缀", () => {
    expect(planRunStatusText({ status: "running", dry_run: false })).toBe("执行中");
    expect(planRunStatusText({ status: "running", dry_run: true })).toBe("Dry-run · 执行中");
    expect(planRunStatusText({ status: "completed_with_errors", dry_run: false })).toBe(
      "完成（有错误）",
    );
  });

  it("色调：dry-run 的完成 / 执行中为靛蓝，其余按状态", () => {
    expect(planRunTone({ status: "completed", dry_run: true })).toBe("indigo");
    expect(planRunTone({ status: "running", dry_run: true })).toBe("indigo");
    expect(planRunTone({ status: "aborted", dry_run: true })).toBe("red");
    expect(planRunTone({ status: "completed", dry_run: false })).toBe("green");
    expect(planRunTone({ status: "countdown", dry_run: false })).toBe("orange");
    expect(planRunTone({ status: "cancelled", dry_run: false })).toBe("gray");
  });
});

describe("actionRunPath", () => {
  it("有 action_run_id 时指向执行记录页并带上动作与记录 id", () => {
    const step = stepOf({ action_id: 5, result: { action_run_id: 42 } });
    expect(actionRunPath(step)).toBe("/hosts-actions?tab=runs&action=5&open=42");
  });

  it("尚未产生执行记录的步骤没有入口", () => {
    expect(actionRunPath(stepOf())).toBeNull();
  });
});
