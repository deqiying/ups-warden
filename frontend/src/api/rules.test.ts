import { describe, expect, it } from "vite-plus/test";
import {
  buildRuleCreateBody,
  buildRuleUpdateBody,
  conditionValueOf,
  draftFromRule,
  emptyConditionDraft,
  emptyRuleDraft,
  normalizeRule,
  opsForKind,
  triggerOf,
  validateCondition,
  validateRuleDraft,
  type Rule,
  type RuleDraft,
} from "./rules.ts";

const conditionRule = (): Rule =>
  normalizeRule({
    id: 3,
    name: "停电 10 分钟或低电量",
    enabled: true,
    dry_run: true,
    scope: { type: "tags", tags: ["核心"] },
    trigger: {
      type: "condition",
      any_of: [
        {
          all_of: [
            { field: "power_state", op: "eq", value: "on_battery" },
            { field: "on_battery_elapsed", op: "gte", value: 600 },
          ],
        },
        { all_of: [{ field: "battery_capacity_pct", op: "lte", value: 40, for_s: 10 }] },
      ],
    },
    actions: [{ action_id: 7 }],
    recovery_actions: [],
    cooldown_s: 60,
  } as Rule);

const validDraft = (patch: Partial<RuleDraft> = {}): RuleDraft => ({
  ...emptyRuleDraft(),
  name: "停电通知",
  events: ["mains_lost"],
  actionIds: [1],
  ...patch,
});

describe("normalizeRule / draftFromRule", () => {
  it("缺省的 recovery_actions、scope 子字段补齐，条件值转为输入原文", () => {
    const rule = normalizeRule({
      id: 1,
      name: "r",
      enabled: true,
      dry_run: true,
      scope: { type: "all" },
      trigger: { type: "event", events: ["mains_lost"] },
      actions: [{ action_id: 1 }],
      recovery_actions: null,
      cooldown_s: 0,
    } as unknown as Rule);
    expect(rule.recovery_actions).toEqual([]);
    const draft = draftFromRule(conditionRule());
    expect(draft.groups).toHaveLength(2);
    expect(draft.groups[0].conditions.map((c) => c.value)).toEqual(["on_battery", "600"]);
    expect(draft.groups[1].conditions[0].forS).toBe(10);
  });
});

describe("条件值与运算符", () => {
  it("布尔、文本、枚举只允许 eq / ne；数值与时长允许六种", () => {
    expect(opsForKind("bool")).toEqual(["eq", "ne"]);
    expect(opsForKind("enum")).toEqual(["eq", "ne"]);
    expect(opsForKind("text")).toEqual(["eq", "ne"]);
    expect(opsForKind("number")).toHaveLength(6);
    expect(opsForKind("duration")).toHaveLength(6);
  });

  it("数值提交为 number，不提交字符串数字；布尔提交为 boolean", () => {
    expect(conditionValueOf({ field: "battery_capacity_pct", value: "40" })).toBe(40);
    expect(conditionValueOf({ field: "battery_capacity_pct", value: "" })).toBeUndefined();
    expect(conditionValueOf({ field: "battery_capacity_pct", value: "abc" })).toBeUndefined();
    expect(conditionValueOf({ field: "low_battery", value: "false" })).toBe(false);
    expect(conditionValueOf({ field: "power_state", value: "on_battery" })).toBe("on_battery");
    const draft = draftFromRule(conditionRule());
    const trigger = triggerOf(draft);
    expect(trigger.any_of?.[0].all_of[1]).toEqual({
      field: "on_battery_elapsed",
      op: "gte",
      value: 600,
    });
    expect(trigger.any_of?.[1].all_of[0]).toEqual({
      field: "battery_capacity_pct",
      op: "lte",
      value: 40,
      for_s: 10,
    });
  });

  it("按相字段可省略相号或指定 [1]～[3]", () => {
    const cond = { ...emptyConditionDraft("input_voltage"), op: "lt" as const, value: "180" };
    expect(validateCondition(cond)).toBeNull();
    expect(validateCondition({ ...cond, field: "input_voltage[2]" })).toBeNull();
    expect(validateCondition({ ...cond, field: "input_freq[2]" })).toBe("请选择字段");
  });

  it("条件错误：枚举值、运算符、持续时间", () => {
    const base = emptyConditionDraft("power_state");
    expect(validateCondition(base)).toBeNull();
    expect(validateCondition({ ...base, op: "gt" })).toMatch(/等于/);
    expect(validateCondition({ ...base, value: "nope" })).toBe("请选择取值");
    expect(validateCondition({ ...base, forS: 604801 })).toMatch(/持续时间/);
    expect(validateCondition({ ...base, forS: 1.5 })).toMatch(/持续时间/);
  });
});

describe("validateRuleDraft", () => {
  it("合法草稿无错误", () => {
    expect(validateRuleDraft(validDraft())).toEqual({});
  });

  it("名称、范围、触发、动作的结构性错误", () => {
    const errors = validateRuleDraft(
      validDraft({
        name: " ",
        scopeType: "device",
        deviceIds: [],
        events: [],
        actionIds: [],
        recoveryActionIds: [2, 2],
        cooldownS: -1,
      }),
    );
    expect(Object.keys(errors).sort()).toEqual(
      ["actions", "cooldown", "name", "recovery", "scope", "trigger"].sort(),
    );
  });

  it("名称按 UTF-8 字节限制 128", () => {
    expect(validateRuleDraft(validDraft({ name: "停".repeat(43) })).name).toMatch(/128/);
    expect(validateRuleDraft(validDraft({ name: "停".repeat(42) })).name).toBeUndefined();
  });

  it("条件规则至少一个组，错误键定位到具体条件", () => {
    const draft = draftFromRule(conditionRule());
    expect(validateRuleDraft({ ...draft, groups: [] }).trigger).toMatch(/条件组/);
    draft.groups[1].conditions[0].value = "";
    expect(validateRuleDraft(draft)["cond-1-0"]).toBe("请填写有效数值");
  });

  it("动作已加载时检查存在性；关机模板要求 plan 动作", () => {
    const types = new Map([
      [1, "notify"],
      [9, "plan"],
    ]);
    expect(
      validateRuleDraft(validDraft({ actionIds: [5] }), { actionTypes: types }).actions,
    ).toMatch(/#5 不存在/);
    expect(
      validateRuleDraft(validDraft(), { actionTypes: types, requiredActionType: "plan" }).actions,
    ).toMatch(/plan/);
    expect(
      validateRuleDraft(validDraft({ actionIds: [9] }), {
        actionTypes: types,
        requiredActionType: "plan",
      }),
    ).toEqual({});
  });
});

describe("请求体", () => {
  it("新建默认 dry-run 且显式提交 enabled / dry_run", () => {
    const body = buildRuleCreateBody(validDraft());
    expect(body).toEqual({
      name: "停电通知",
      enabled: true,
      dry_run: true,
      scope: { type: "all" },
      trigger: { type: "event", events: ["mains_lost"] },
      actions: [{ action_id: 1 }],
      recovery_actions: [],
      cooldown_s: 0,
    });
  });

  it("未修改时 PUT 请求体为空；只提交改过的顶层字段", () => {
    const rule = conditionRule();
    expect(buildRuleUpdateBody(rule, draftFromRule(rule))).toEqual({});
    const draft = draftFromRule(rule);
    expect(buildRuleUpdateBody(rule, { ...draft, dryRun: false })).toEqual({ dry_run: false });
    expect(buildRuleUpdateBody(rule, { ...draft, recoveryActionIds: [7] })).toEqual({
      recovery_actions: [{ action_id: 7 }],
    });
  });

  it("修改条件时整体提交 trigger；修改范围整体提交 scope", () => {
    const rule = conditionRule();
    const draft = draftFromRule(rule);
    draft.groups[1].conditions[0].value = "30";
    const body = buildRuleUpdateBody(rule, { ...draft, scopeType: "all" });
    expect(Object.keys(body).sort()).toEqual(["scope", "trigger"]);
    expect(body.scope).toEqual({ type: "all" });
    expect(JSON.stringify(body.trigger)).toContain('"value":30');
  });
});
