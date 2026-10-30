import { describe, expect, it } from "vite-plus/test";
import { wirePlan } from "../test/fixtures.ts";
import {
  buildPlanCreateBody,
  buildPlanRunBody,
  buildPlanUpdateBody,
  draftFromPlan,
  emptyPlanDraft,
  emptyStageDraft,
  normalizePlan,
  stagesOf,
  validatePlanDraft,
  type PlanDraft,
  type StageDraft,
} from "./plans.ts";

/** 单阶段、单步骤的合法草稿；可覆盖顶层与首个阶段。 */
function validDraft(
  overrides: Partial<PlanDraft> = {},
  stage: Partial<StageDraft> = {},
): PlanDraft {
  return {
    ...emptyPlanDraft(),
    name: "机房关机",
    stages: [{ ...emptyStageDraft(), actionIds: [1], ...stage }],
    ...overrides,
  };
}

const stageWith = (actionIds: number[], overrides: Partial<StageDraft> = {}): StageDraft => ({
  ...emptyStageDraft(),
  actionIds,
  ...overrides,
});

describe("buildPlanRunBody", () => {
  it("confirm_name 原样提交，不 trim", () => {
    const body = buildPlanRunBody({ confirmName: " 计划A ", dryRun: true, deviceId: null });
    expect(body.confirm_name).toBe(" 计划A ");
    expect(body).toStrictEqual({ confirm_name: " 计划A ", dry_run: true });
  });

  it("dry_run 总是显式存在（true / false）", () => {
    const dry = buildPlanRunBody({ confirmName: "p", dryRun: true, deviceId: null });
    const real = buildPlanRunBody({ confirmName: "p", dryRun: false, deviceId: null });
    expect(dry).toHaveProperty("dry_run", true);
    expect(real).toHaveProperty("dry_run", false);
    expect(Object.keys(real)).toEqual(["confirm_name", "dry_run"]);
  });

  it("deviceId 为 null 时不含 device_id，指定时提交", () => {
    expect(buildPlanRunBody({ confirmName: "p", dryRun: true, deviceId: null })).not.toHaveProperty(
      "device_id",
    );
    expect(buildPlanRunBody({ confirmName: "p", dryRun: false, deviceId: 7 })).toStrictEqual({
      confirm_name: "p",
      dry_run: false,
      device_id: 7,
    });
  });
});

describe("stagesOf", () => {
  it("自关机阶段 offline_wait_s 强制为 0 并带 self_shutdown", () => {
    const stages = stagesOf(
      validDraft({
        stages: [
          stageWith([1], { offlineWaitS: 90 }),
          stageWith([2], { selfShutdown: true, offlineWaitS: 300 }),
        ],
      }),
    );
    expect(stages[0]).toStrictEqual({
      delay_s: 0,
      step_timeout_s: 30,
      offline_wait_s: 90,
      steps: [{ action_id: 1 }],
    });
    expect(stages[1]).toStrictEqual({
      delay_s: 0,
      step_timeout_s: 30,
      offline_wait_s: 0,
      self_shutdown: true,
      steps: [{ action_id: 2 }],
    });
  });

  it("普通阶段不输出 self_shutdown 键；key 不提交", () => {
    const [stage] = stagesOf(validDraft());
    expect(stage).not.toHaveProperty("self_shutdown");
    expect(stage).not.toHaveProperty("key");
  });
});

describe("draftFromPlan / normalizePlan", () => {
  it("往返：草稿还原为同一份 stages，且每个阶段 key 唯一", () => {
    const plan = wirePlan();
    const draft = draftFromPlan(plan);
    expect(stagesOf(draft)).toEqual(plan.stages);
    expect(new Set(draft.stages.map((s) => s.key)).size).toBe(draft.stages.length);
    expect(draft.stages[1].selfShutdown).toBe(true);
    expect(draft.notifyChannelIds).toEqual([1]);
  });

  it("normalizePlan 补齐缺省字段（null stages / channels）", () => {
    const wire = { ...wirePlan(), notify_channel_ids: null, stages: null } as never;
    const plan = normalizePlan(wire);
    expect(plan.notify_channel_ids).toEqual([]);
    expect(plan.stages).toEqual([]);
  });
});

describe("validatePlanDraft：名称", () => {
  it("合法草稿无错误", () => {
    expect(validatePlanDraft(validDraft())).toEqual({});
  });

  it("名称按 UTF-8 字节限制 128：42 个汉字（126 字节）通过，43 个（129 字节）报错", () => {
    expect(validatePlanDraft(validDraft({ name: "汉".repeat(42) })).name).toBeUndefined();
    expect(validatePlanDraft(validDraft({ name: "汉".repeat(43) })).name).toMatch(/128 字节/);
  });

  it("ASCII 128 个字符通过，129 个报错", () => {
    expect(validatePlanDraft(validDraft({ name: "a".repeat(128) })).name).toBeUndefined();
    expect(validatePlanDraft(validDraft({ name: "a".repeat(129) })).name).toBeDefined();
  });

  it("空名称、纯空白、含 NUL 均报错；首尾空白按 trim 后计算", () => {
    expect(validatePlanDraft(validDraft({ name: "" })).name).toBeDefined();
    expect(validatePlanDraft(validDraft({ name: "   " })).name).toBeDefined();
    expect(validatePlanDraft(validDraft({ name: "a\u0000b" })).name).toMatch(/空字符/);
    expect(validatePlanDraft(validDraft({ name: ` ${"a".repeat(128)} ` })).name).toBeUndefined();
  });
});

describe("validatePlanDraft：秒数与数量范围", () => {
  it("倒计时：0 与 3600 合法；NaN、-1、3601、小数报错", () => {
    for (const ok of [0, 60, 3600]) {
      expect(validatePlanDraft(validDraft({ countdownS: ok })).countdown).toBeUndefined();
    }
    for (const bad of [Number.NaN, -1, 3601, 1.5]) {
      expect(validatePlanDraft(validDraft({ countdownS: bad })).countdown).toBeDefined();
    }
  });

  it("阶段延迟 / 等待离线：0～3600，NaN 与 3601 报错", () => {
    expect(validatePlanDraft(validDraft({}, { delayS: Number.NaN }))["stage-0"]).toBeDefined();
    expect(validatePlanDraft(validDraft({}, { delayS: 3601 }))["stage-0"]).toBeDefined();
    expect(validatePlanDraft(validDraft({}, { offlineWaitS: 3601 }))["stage-0"]).toBeDefined();
    expect(
      validatePlanDraft(validDraft({}, { offlineWaitS: Number.NaN }))["stage-0"],
    ).toBeDefined();
    expect(validatePlanDraft(validDraft({}, { delayS: 3600, offlineWaitS: 3600 }))).toEqual({});
  });

  it("单步超时：下限 1（0 报错），上限 3600（3601 报错）", () => {
    expect(validatePlanDraft(validDraft({}, { stepTimeoutS: 0 }))["stage-0"]).toBeDefined();
    expect(validatePlanDraft(validDraft({}, { stepTimeoutS: 3601 }))["stage-0"]).toBeDefined();
    expect(
      validatePlanDraft(validDraft({}, { stepTimeoutS: Number.NaN }))["stage-0"],
    ).toBeDefined();
    expect(validatePlanDraft(validDraft({}, { stepTimeoutS: 1 }))).toEqual({});
    expect(validatePlanDraft(validDraft({}, { stepTimeoutS: 3600 }))).toEqual({});
  });

  it("阶段数 1～16、步骤数 1～32、通知渠道最多 16", () => {
    expect(validatePlanDraft(validDraft({ stages: [] })).stages).toMatch(/至少/);
    const many = Array.from({ length: 17 }, (_, i) => stageWith([i + 1]));
    expect(validatePlanDraft(validDraft({ stages: many })).stages).toMatch(/最多 16/);

    const steps = (n: number) => Array.from({ length: n }, (_, i) => i + 1);
    expect(validatePlanDraft(validDraft({}, { actionIds: [] }))["stage-0"]).toMatch(/至少需要 1/);
    expect(validatePlanDraft(validDraft({}, { actionIds: steps(32) }))).toEqual({});
    expect(validatePlanDraft(validDraft({}, { actionIds: steps(33) }))["stage-0"]).toMatch(
      /最多 32/,
    );

    const channels = (n: number) => Array.from({ length: n }, (_, i) => i + 1);
    expect(
      validatePlanDraft(validDraft({ notifyChannelIds: channels(16) })).channels,
    ).toBeUndefined();
    expect(
      validatePlanDraft(validDraft({ notifyChannelIds: channels(17) })).channels,
    ).toBeDefined();
  });
});

describe("validatePlanDraft：自关机阶段与动作引用", () => {
  it("self_shutdown 必须是最后一个阶段", () => {
    const errors = validatePlanDraft(
      validDraft({ stages: [stageWith([1], { selfShutdown: true }), stageWith([2])] }),
    );
    expect(errors["stage-0"]).toMatch(/最后一个阶段/);
    expect(errors["stage-1"]).toBeUndefined();
  });

  it("self_shutdown 阶段只能有 1 个步骤", () => {
    const errors = validatePlanDraft(
      validDraft({ stages: [stageWith([1]), stageWith([2, 3], { selfShutdown: true })] }),
    );
    expect(errors["stage-1"]).toMatch(/只能有 1 个步骤/);
    expect(errors["stage-0"]).toBeUndefined();
  });

  it("最后一个阶段 + 1 个步骤的 self_shutdown 合法；其等待离线不参与校验（提交时强制为 0）", () => {
    const draft = validDraft({
      stages: [stageWith([1]), stageWith([2], { selfShutdown: true, offlineWaitS: Number.NaN })],
    });
    expect(validatePlanDraft(draft)).toEqual({});
  });

  it("同一动作在计划中只能出现一次（同阶段与跨阶段）", () => {
    const sameStage = validatePlanDraft(validDraft({ stages: [stageWith([1, 1])] }));
    expect(sameStage["stage-0"]).toMatch(/只能出现一次/);

    const crossStage = validatePlanDraft(
      validDraft({ stages: [stageWith([1]), stageWith([2, 1])] }),
    );
    expect(crossStage["stage-0"]).toBeUndefined();
    expect(crossStage["stage-1"]).toMatch(/只能出现一次/);
  });

  it("提供动作类型表时：不存在的动作与 notify / plan 动作报错；未提供则跳过", () => {
    const types = new Map([
      [1, "ssh"],
      [2, "notify"],
      [3, "plan"],
      [4, "http"],
      [5, "script"],
    ]);
    const check = (ids: number[]) =>
      validatePlanDraft(validDraft({ stages: [stageWith(ids)] }), types)["stage-0"];
    expect(check([1, 4, 5])).toBeUndefined();
    expect(check([2])).toMatch(/SSH、本地脚本或 HTTP/);
    expect(check([3])).toMatch(/SSH、本地脚本或 HTTP/);
    expect(check([99])).toMatch(/#99 不存在/);
    expect(validatePlanDraft(validDraft({ stages: [stageWith([99])] }))).toEqual({});
  });
});

describe("buildPlanCreateBody", () => {
  it("名称 trim，stages 经 stagesOf 转换，通知渠道复制", () => {
    const draft = validDraft({ name: " 机房关机 ", notifyChannelIds: [3, 1] });
    expect(buildPlanCreateBody(draft)).toStrictEqual({
      name: "机房关机",
      countdown_s: 60,
      on_mains_restored: "cancel_pending",
      on_restart: "resume",
      notify_channel_ids: [3, 1],
      stages: [{ delay_s: 0, step_timeout_s: 30, offline_wait_s: 0, steps: [{ action_id: 1 }] }],
    });
  });
});

describe("buildPlanUpdateBody", () => {
  it("未修改时为空对象", () => {
    const plan = wirePlan();
    expect(buildPlanUpdateBody(plan, draftFromPlan(plan))).toStrictEqual({});
  });

  it("没有通知渠道的计划同样视为未修改", () => {
    const plan = wirePlan({ notify_channel_ids: [] });
    expect(buildPlanUpdateBody(plan, draftFromPlan(plan))).toStrictEqual({});
  });

  it("只提交修改过的顶层字段", () => {
    const plan = wirePlan();
    const draft = {
      ...draftFromPlan(plan),
      name: " 新计划 ",
      countdownS: 120,
      onMainsRestored: "continue" as const,
      onRestart: "abort" as const,
    };
    expect(buildPlanUpdateBody(plan, draft)).toStrictEqual({
      name: "新计划",
      countdown_s: 120,
      on_mains_restored: "continue",
      on_restart: "abort",
    });
  });

  it("名称仅首尾空白差异不算修改", () => {
    const plan = wirePlan();
    expect(buildPlanUpdateBody(plan, { ...draftFromPlan(plan), name: ` ${plan.name} ` })).toEqual(
      {},
    );
  });

  it("通知渠道修改时整体提交（含清空）", () => {
    const plan = wirePlan();
    expect(
      buildPlanUpdateBody(plan, { ...draftFromPlan(plan), notifyChannelIds: [1, 2] }),
    ).toStrictEqual({ notify_channel_ids: [1, 2] });
    expect(
      buildPlanUpdateBody(plan, { ...draftFromPlan(plan), notifyChannelIds: [] }),
    ).toStrictEqual({ notify_channel_ids: [] });
  });

  it("stages 任一处修改时整体提交全部阶段，而不是 diff", () => {
    const plan = wirePlan();
    const draft = draftFromPlan(plan);
    draft.stages[0].delayS = 10;
    const body = buildPlanUpdateBody(plan, draft);
    expect(Object.keys(body)).toEqual(["stages"]);
    expect(body.stages).toEqual([{ ...plan.stages[0], delay_s: 10 }, plan.stages[1]]);
  });

  it("新增 / 移除 / 重排步骤都会触发 stages 整体提交", () => {
    const plan = wirePlan();
    const added = draftFromPlan(plan);
    added.stages[0].actionIds = [1, 3];
    expect(buildPlanUpdateBody(plan, added).stages).toBeDefined();

    const removed = draftFromPlan(plan);
    removed.stages.pop();
    expect(buildPlanUpdateBody(plan, removed).stages).toHaveLength(1);
  });
});
