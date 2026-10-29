import { describe, expect, it } from "vite-plus/test";

import {
  isActivePlanRun,
  normalizeDeviceState,
  normalizeEventView,
  normalizeField,
  normalizeFields,
  normalizePlanRun,
  normalizeSseEvent,
} from "./normalize.ts";
import type { PlanRunStatus, WireDeviceState, WirePlanRun, WirePlanStep } from "./types.ts";

describe("normalizeField", () => {
  it("good 字段省略 num/bool/str/reason 时补齐默认值", () => {
    expect(normalizeField({ quality: "good" })).toEqual({
      num: 0,
      bool: false,
      str: "",
      quality: "good",
      reason: "",
    });
  });

  it("保留已有的 num、bool、str、reason", () => {
    expect(
      normalizeField({ num: 230.5, bool: true, str: "x", quality: "stale", reason: "timeout" }),
    ).toEqual({ num: 230.5, bool: true, str: "x", quality: "stale", reason: "timeout" });
  });

  it("非 good 字段同样补齐，quality 原样保留", () => {
    expect(normalizeField({ quality: "unsupported", reason: "not_mapped" })).toEqual({
      num: 0,
      bool: false,
      str: "",
      quality: "unsupported",
      reason: "not_mapped",
    });
  });
});

describe("normalizeFields", () => {
  it("null / undefined → 空对象", () => {
    expect(normalizeFields(null)).toEqual({});
    expect(normalizeFields(undefined)).toEqual({});
  });

  it("逐项补齐，并丢弃非对象的值", () => {
    const wire = {
      "input_voltage[1]": { num: 220, quality: "good" },
      temperature: { quality: "good" },
      bad: null,
    } as unknown as Parameters<typeof normalizeFields>[0];
    expect(normalizeFields(wire)).toEqual({
      "input_voltage[1]": { num: 220, bool: false, str: "", quality: "good", reason: "" },
      temperature: { num: 0, bool: false, str: "", quality: "good", reason: "" },
    });
  });
});

describe("normalizeDeviceState", () => {
  const base: WireDeviceState = {
    device_id: 3,
    schema_version: 1,
    at: "2026-10-27T06:20:58Z",
    fields: { mains_ok: { bool: true, quality: "good" } },
    power_state_raw: "online",
    power_state: "online",
    comm_state: "ok",
    last_ok_at: "2026-10-27T06:20:58Z",
    runtime_source: "native",
  };

  it("Go 零值时间 last_ok_at → null", () => {
    expect(
      normalizeDeviceState({ ...base, last_ok_at: "0001-01-01T00:00:00Z" }).last_ok_at,
    ).toBeNull();
  });

  it("有效时间原样保留", () => {
    const state = normalizeDeviceState({
      ...base,
      pending_since: "2026-10-27T06:00:00Z",
      on_battery_since: "2026-10-27T06:10:00Z",
      outage_cycle_id: 7,
    });
    expect(state.last_ok_at).toBe("2026-10-27T06:20:58Z");
    expect(state.pending_since).toBe("2026-10-27T06:00:00Z");
    expect(state.on_battery_since).toBe("2026-10-27T06:10:00Z");
    expect(state.outage_cycle_id).toBe(7);
  });

  it("缺少 pending_since / on_battery_since / outage_cycle_id → null", () => {
    const state = normalizeDeviceState(base);
    expect(state.pending_since).toBeNull();
    expect(state.on_battery_since).toBeNull();
    expect(state.outage_cycle_id).toBeNull();
  });

  it("outage_cycle_id 为 0 时保留 0（不是缺失）", () => {
    expect(normalizeDeviceState({ ...base, outage_cycle_id: 0 }).outage_cycle_id).toBe(0);
  });

  it("空串时间视为缺失", () => {
    expect(normalizeDeviceState({ ...base, pending_since: "" }).pending_since).toBeNull();
  });

  it("fields 为 null → {}，且字段被补齐", () => {
    expect(normalizeDeviceState({ ...base, fields: null }).fields).toEqual({});
    expect(normalizeDeviceState(base).fields).toEqual({
      mains_ok: { num: 0, bool: true, str: "", quality: "good", reason: "" },
    });
  });

  it("原样透传标量字段", () => {
    const state = normalizeDeviceState(base);
    expect(state).toMatchObject({
      device_id: 3,
      schema_version: 1,
      at: "2026-10-27T06:20:58Z",
      power_state_raw: "online",
      power_state: "online",
      comm_state: "ok",
      runtime_source: "native",
    });
  });
});

describe("normalizeEventView", () => {
  it("cleared_at 为 null 保持 null，clears 恒为 null", () => {
    const item = normalizeEventView({
      id: 1,
      device_id: 2,
      type: "on_battery",
      severity: "critical",
      started_at: "2026-10-27T06:00:00Z",
      cleared_at: null,
      active: true,
      summary: null,
    });
    expect(item).toEqual({
      id: 1,
      device_id: 2,
      type: "on_battery",
      severity: "critical",
      started_at: "2026-10-27T06:00:00Z",
      cleared_at: null,
      active: true,
      clears: null,
      summary: {},
    });
  });

  it("保留有效的 cleared_at，补齐 summary 字段", () => {
    const item = normalizeEventView({
      id: 1,
      device_id: 2,
      type: "on_battery",
      severity: "warning",
      started_at: "2026-10-27T06:00:00Z",
      cleared_at: "2026-10-27T06:30:00Z",
      active: false,
      summary: { battery_capacity_pct: { num: 80, quality: "good" } },
    });
    expect(item.cleared_at).toBe("2026-10-27T06:30:00Z");
    expect(item.summary.battery_capacity_pct).toEqual({
      num: 80,
      bool: false,
      str: "",
      quality: "good",
      reason: "",
    });
  });
});

describe("normalizeSseEvent", () => {
  const wire = {
    id: 9,
    device_id: 4,
    type: "mains_restored",
    severity: "info" as const,
    at: "2026-10-27T06:20:58Z",
    active: false,
    summary: null,
  };

  it("at 映射为 started_at，cleared_at 为 null，clears 缺省为 null", () => {
    const item = normalizeSseEvent(wire);
    expect(item.started_at).toBe("2026-10-27T06:20:58Z");
    expect(item.cleared_at).toBeNull();
    expect(item.clears).toBeNull();
    expect(item.summary).toEqual({});
    expect("at" in item).toBe(false);
  });

  it("clears 有值时保留，空串视为 null", () => {
    expect(normalizeSseEvent({ ...wire, clears: "on_battery" }).clears).toBe("on_battery");
    expect(normalizeSseEvent({ ...wire, clears: "" }).clears).toBeNull();
  });
});

describe("normalizePlanRun", () => {
  const baseRun: WirePlanRun = {
    id: 1,
    plan_id: 2,
    plan_name: "夜间关机",
    device_id: 3,
    outage_cycle_id: 5,
    dry_run: true,
    status: "running",
    trigger: { kind: "x" },
    countdown_until: "2026-10-27T06:30:00Z",
    current_stage: 1,
    started_at: "2026-10-27T06:20:00Z",
    ended_at: null,
    steps: null,
  };

  it("steps 为 null → []", () => {
    expect(normalizePlanRun(baseRun).steps).toEqual([]);
  });

  it("step.result 为 null → 默认结果", () => {
    const step: WirePlanStep = {
      stage: 1,
      step: 1,
      action_id: 10,
      host_id: null,
      status: "waiting",
      started_at: null,
      ended_at: null,
      result: null,
    };
    const run = normalizePlanRun({ ...baseRun, steps: [step] });
    expect(run.steps).toEqual([
      {
        stage: 1,
        step: 1,
        action_id: 10,
        host_id: null,
        status: "waiting",
        started_at: null,
        ended_at: null,
        result: { action_run_id: null, action_status: "", error: "", offline_timeout: false },
      },
    ]);
  });

  it("step.result 有值时保留，零值时间与缺失 host_id 归一", () => {
    const step = {
      stage: 2,
      step: 3,
      action_id: 11,
      status: "sent",
      started_at: "0001-01-01T00:00:00Z",
      ended_at: "2026-10-27T06:25:00Z",
      result: { action_run_id: 0, action_status: "success", error: "e", offline_timeout: true },
    } as unknown as WirePlanStep;
    const [out] = normalizePlanRun({ ...baseRun, steps: [step] }).steps;
    expect(out.host_id).toBeNull();
    expect(out.started_at).toBeNull();
    expect(out.ended_at).toBe("2026-10-27T06:25:00Z");
    expect(out.result).toEqual({
      action_run_id: 0,
      action_status: "success",
      error: "e",
      offline_timeout: true,
    });
  });

  it("result 部分字段缺失时只补缺失项", () => {
    const step: WirePlanStep = {
      stage: 1,
      step: 1,
      action_id: 1,
      host_id: 7,
      status: "failed",
      started_at: null,
      ended_at: null,
      result: { error: "boom" },
    };
    const [out] = normalizePlanRun({ ...baseRun, steps: [step] }).steps;
    expect(out.host_id).toBe(7);
    expect(out.result).toEqual({
      action_run_id: null,
      action_status: "",
      error: "boom",
      offline_timeout: false,
    });
  });

  it("顶层可空字段归一，其余字段透传", () => {
    const run = normalizePlanRun({
      ...baseRun,
      countdown_until: "0001-01-01T00:00:00Z",
      device_id: null,
      outage_cycle_id: null,
    });
    expect(run.countdown_until).toBeNull();
    expect(run.device_id).toBeNull();
    expect(run.outage_cycle_id).toBeNull();
    expect(run.plan_name).toBe("夜间关机");
    expect(run.dry_run).toBe(true);
    expect(run.trigger).toEqual({ kind: "x" });
  });
});

describe("isActivePlanRun", () => {
  it.each<[PlanRunStatus, boolean]>([
    ["countdown", true],
    ["running", true],
    ["completed", false],
    ["completed_with_errors", false],
    ["cancelled", false],
    ["aborted", false],
  ])("%s → %s", (status, expected) => {
    expect(isActivePlanRun({ status })).toBe(expected);
  });
});
