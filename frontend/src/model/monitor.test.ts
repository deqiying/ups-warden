import { describe, expect, it } from "vite-plus/test";
import { normalizeDeviceState, normalizeEventView, normalizePlanRun } from "../api/normalize.ts";
import type { DeviceConfig } from "../api/types.ts";
import { settings, wireDevice, wireEventView, wirePlanRun } from "../test/fixtures.ts";
import {
  needsAttention,
  onBatteryElapsed,
  pendingWindow,
  phaseCount,
  powerStatus,
  protocolLabel,
  relativeTime,
  statusRank,
} from "./device.ts";
import { compareAlerts, eventDurationSeconds, shortDuration, summaryText } from "./events.ts";
import { countdownFraction, countdownRemaining, runProgressText, stageCount } from "./plan.ts";

const config: DeviceConfig = {
  ...settings().device_defaults,
  outage_confirm_s: 10,
  recovery_confirm_s: 60,
};
const at = (iso: string) => Date.parse(iso);

describe("model/device", () => {
  it("疑似停电：pending_since 加 outage_confirm_s 计算进度，不超过总时长", () => {
    const state = normalizeDeviceState(
      wireDevice({ power_state: "online", pending_since: "2026-10-27T06:00:00Z" }),
    );
    expect(pendingWindow(state, config, at("2026-10-27T06:00:06Z"))).toEqual({
      kind: "outage",
      elapsedS: 6,
      totalS: 10,
    });
    expect(pendingWindow(state, config, at("2026-10-27T06:01:00Z"))?.elapsedS).toBe(10);
    expect(powerStatus(state)).toMatchObject({ label: "疑似停电", breathe: true });
  });

  it("市电恢复确认：电池供电且原始判定为市电时使用 recovery_confirm_s", () => {
    const state = normalizeDeviceState(
      wireDevice({
        power_state: "on_battery",
        power_state_raw: "online",
        pending_since: "2026-10-27T06:00:00Z",
      }),
    );
    expect(pendingWindow(state, config, at("2026-10-27T06:00:30Z"))).toEqual({
      kind: "recovery",
      elapsedS: 30,
      totalS: 60,
    });
    expect(powerStatus(state).label).toBe("市电恢复确认中");
  });

  it("没有设备配置时不猜测去抖总时长", () => {
    const state = normalizeDeviceState(wireDevice({ pending_since: "2026-10-27T06:00:00Z" }));
    expect(pendingWindow(state, undefined, Date.now())).toBeNull();
  });

  it("失联优先显示为通讯中断，并计入需要关注", () => {
    const state = normalizeDeviceState(wireDevice({ comm_state: "lost", power_state: "online" }));
    expect(powerStatus(state)).toMatchObject({ label: "通讯中断", icon: "wifi-off", card: "lost" });
    expect(needsAttention(state)).toBe(true);
  });

  it("需要关注：停电告警本身不计入，其他 warning 告警计入", () => {
    const online = normalizeDeviceState(wireDevice({ power_state: "on_battery" }));
    const lost = normalizeEventView(wireEventView({ type: "mains_lost", severity: "critical" }));
    const overload = normalizeEventView(
      wireEventView({ id: 2, type: "overload", severity: "warning" }),
    );
    expect(needsAttention(online, [lost])).toBe(false);
    expect(needsAttention(online, [lost, overload])).toBe(true);
    expect(statusRank(online)).toBe(0);
  });

  it("放电计时从 on_battery_since 到参考时刻", () => {
    const state = normalizeDeviceState(wireDevice({ on_battery_since: "2026-10-27T06:00:00Z" }));
    expect(onBatteryElapsed(state, at("2026-10-27T06:10:13Z"))).toBe(613);
    expect(onBatteryElapsed(normalizeDeviceState(wireDevice()), Date.now())).toBeNull();
  });

  it("相数只取 good 值并夹到 1～3", () => {
    const fields = normalizeDeviceState(
      wireDevice({
        fields: {
          input_phases: { num: 3, quality: "good" },
          output_phases: { num: 3, quality: "invalid", reason: "out_of_range" },
        },
      }),
    ).fields;
    expect(phaseCount(fields, "input_phases")).toBe(3);
    expect(phaseCount(fields, "output_phases")).toBe(1);
  });

  it("协议显示名", () => {
    expect(protocolLabel({ protocol: "snmp", params: { version: "2c" } })).toBe("SNMP v2c");
    expect(protocolLabel({ protocol: "modbus", params: { unit_id: 1 } })).toBe("Modbus TCP");
    expect(protocolLabel({ protocol: "ayi9-json", params: {} })).toBe("AYI9 JSON");
  });

  it("相对时间：超过一天返回 null 交给调用方显示绝对时间", () => {
    const now = at("2026-10-27T06:00:00Z");
    expect(relativeTime("2026-10-27T05:59:58Z", now)).toBe("刚刚");
    expect(relativeTime("2026-10-27T05:59:30Z", now)).toBe("30 秒前");
    expect(relativeTime("2026-10-27T05:57:00Z", now)).toBe("3 分钟前");
    expect(relativeTime("2026-10-25T05:57:00Z", now)).toBeNull();
    expect(relativeTime(null, now)).toBeNull();
  });
});

describe("model/events", () => {
  it("摘要只显示 good 字段；omitempty 省略的 good 0 值显示为 0，非 good 省略而不是 0", () => {
    const event = normalizeEventView(
      wireEventView({
        summary: {
          battery_capacity_pct: { num: 89, quality: "good" },
          "output_load_pct[1]": { quality: "invalid", reason: "placeholder" },
          // good 且 num 被 omitempty 省略：真实值为 0 V（停电时的输入电压）。
          "input_voltage[1]": { quality: "good" },
        },
      }),
    );
    expect(summaryText(event.summary)).toBe("容量 89% · 输入 0 V");
  });

  it("持续时间：已结束取 cleared_at，进行中取当前时间，无结束时间的非活动事件为 null", () => {
    const now = at("2026-10-27T06:30:00Z");
    const active = normalizeEventView(wireEventView({ started_at: "2026-10-27T06:20:00Z" }));
    const cleared = normalizeEventView(
      wireEventView({
        active: false,
        started_at: "2026-10-27T06:00:00Z",
        cleared_at: "2026-10-27T06:06:12Z",
      }),
    );
    const point = normalizeEventView(wireEventView({ active: false, cleared_at: null }));
    expect(eventDurationSeconds(active, now)).toBe(600);
    expect(eventDurationSeconds(cleared, now)).toBe(372);
    expect(eventDurationSeconds(point, now)).toBeNull();
    expect(shortDuration(43)).toBe("43 秒");
    expect(shortDuration(600)).toBe("10 分钟");
  });

  it("告警排序：严重度优先，同级 id 倒序", () => {
    const items = [
      normalizeEventView(wireEventView({ id: 1, severity: "warning" })),
      normalizeEventView(wireEventView({ id: 2, severity: "critical" })),
      normalizeEventView(wireEventView({ id: 3, severity: "warning" })),
    ].sort(compareAlerts);
    expect(items.map((e) => e.id)).toEqual([2, 3, 1]);
  });
});

describe("model/plan", () => {
  it("倒计时剩余与比例来自 started_at → countdown_until", () => {
    const run = normalizePlanRun(
      wirePlanRun({ started_at: "2026-10-27T06:00:00Z", countdown_until: "2026-10-27T06:01:00Z" }),
    );
    expect(countdownRemaining(run, at("2026-10-27T06:00:13Z"))).toBe(47);
    expect(countdownFraction(run, at("2026-10-27T06:00:15Z"))).toBeCloseTo(0.75);
    expect(countdownRemaining(run, at("2026-10-27T06:02:00Z"))).toBe(0);
  });

  it("阶段下标从 0 开始：current_stage=0 的执行中运行显示为阶段 1，倒计时中不显示阶段", () => {
    const steps = [
      {
        stage: 0,
        step: 0,
        action_id: 1,
        host_id: null,
        status: "waiting" as const,
        started_at: null,
        ended_at: null,
        result: null,
      },
      {
        stage: 1,
        step: 0,
        action_id: 2,
        host_id: null,
        status: "waiting" as const,
        started_at: null,
        ended_at: null,
        result: null,
      },
    ];
    const running = normalizePlanRun(
      wirePlanRun({ status: "running", current_stage: 0, countdown_until: null, steps }),
    );
    expect(runProgressText(running)).toContain("阶段 1 / 2");
    const countdown = normalizePlanRun(
      wirePlanRun({ status: "countdown", current_stage: 0, steps }),
    );
    expect(runProgressText(countdown)).not.toContain("阶段");
  });

  it("执行进度由 steps 推导阶段数（Q-C5）", () => {
    const run = normalizePlanRun(
      wirePlanRun({
        status: "running",
        current_stage: 1,
        countdown_until: null,
        steps: [
          {
            stage: 0,
            step: 0,
            action_id: 1,
            host_id: null,
            status: "offline",
            started_at: null,
            ended_at: null,
            result: null,
          },
          {
            stage: 1,
            step: 0,
            action_id: 2,
            host_id: null,
            status: "running",
            started_at: null,
            ended_at: null,
            result: null,
          },
          {
            stage: 2,
            step: 0,
            action_id: 3,
            host_id: null,
            status: "waiting",
            started_at: null,
            ended_at: null,
            result: null,
          },
        ],
      }),
    );
    expect(stageCount(run)).toBe(3);
    expect(countdownRemaining(run, Date.now())).toBeNull();
    expect(runProgressText(run)).toBe("执行中 · 阶段 2 / 3 · 已完成 1 / 3 步");
  });
});
