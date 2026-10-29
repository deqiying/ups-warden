/*
 * 设备运行状态的显示派生（总览、设备详情、告警页共用）。
 * 只读取 DeviceState 与设备配置，不外推实时值：倒计时、放电计时由服务端给出的起点与本地时钟计算。
 */
import type {
  Device,
  DeviceConfig,
  DeviceState,
  EventItem,
  FieldMap,
  Source,
} from "../api/types.ts";
import type { PillTone } from "../components/Pill.tsx";
import type { IconName } from "../icons/icons.ts";
import { goodNumber } from "./format.ts";
import { enumLabel } from "./labels.ts";

export type DeviceTone = "battery" | "lost" | "warn" | "normal";

export interface PowerStatus {
  /** 胶囊文案，如“电池供电”“疑似停电”“通讯中断”。 */
  label: string;
  tone: PillTone;
  /** 圆点（含呼吸）或图标，二者择一。 */
  icon?: IconName;
  dot: boolean;
  breathe: boolean;
  /** 设备卡与 hero 的背景色调。 */
  card: DeviceTone;
}

/** 去抖窗口：停电确认（outage）或市电恢复确认（recovery）。 */
export interface PendingWindow {
  kind: "outage" | "recovery";
  elapsedS: number;
  totalS: number;
}

/**
 * 去抖进度（状态引擎 state/engine.go）：
 * - 当前非电池供电且有 pending_since：疑似停电，满 `outage_confirm_s` 后确认；
 * - 当前电池供电、原始判定为市电且有 pending_since：市电恢复确认中，满 `recovery_confirm_s` 后结束周期。
 * 缺少设备配置时无法给出总时长，返回 null（不猜测默认值）。
 */
export function pendingWindow(
  state: DeviceState,
  config: DeviceConfig | undefined,
  nowMs: number,
): PendingWindow | null {
  if (!state.pending_since || !config) return null;
  const since = Date.parse(state.pending_since);
  if (Number.isNaN(since)) return null;
  const elapsedS = Math.max(0, Math.floor((nowMs - since) / 1000));
  if (state.power_state === "on_battery") {
    if (state.power_state_raw !== "online") return null;
    return {
      kind: "recovery",
      elapsedS: Math.min(elapsedS, config.recovery_confirm_s),
      totalS: config.recovery_confirm_s,
    };
  }
  return {
    kind: "outage",
    elapsedS: Math.min(elapsedS, config.outage_confirm_s),
    totalS: config.outage_confirm_s,
  };
}

export function isCommLost(state: DeviceState): boolean {
  return state.comm_state === "lost";
}

/** 供电胶囊。失联优先显示（数值均为旧值）；其后是去抖中的疑似停电。 */
export function powerStatus(state: DeviceState): PowerStatus {
  if (isCommLost(state)) {
    return {
      label: "通讯中断",
      tone: "gray",
      icon: "wifi-off",
      dot: false,
      breathe: false,
      card: "lost",
    };
  }
  const base = { dot: true, breathe: false };
  switch (state.power_state) {
    case "on_battery":
      return state.pending_since && state.power_state_raw === "online"
        ? { ...base, label: "市电恢复确认中", tone: "orange", breathe: true, card: "battery" }
        : { ...base, label: "电池供电", tone: "orange", card: "battery" };
    case "online":
      return state.pending_since
        ? { ...base, label: "疑似停电", tone: "yellow", breathe: true, card: "normal" }
        : { ...base, label: "市电", tone: "green", card: "normal" };
    case "bypass":
      return { ...base, label: "旁路供电", tone: "yellow", card: "warn" };
    case "fault":
      return { ...base, label: "UPS 故障", tone: "red", card: "warn" };
    case "shutdown":
      return { ...base, label: "已关机", tone: "red", card: "warn" };
    default:
      return state.pending_since
        ? { ...base, label: "疑似停电", tone: "yellow", breathe: true, card: "warn" }
        : {
            ...base,
            label: enumLabel("power_state", state.power_state) || "未知",
            tone: "gray",
            card: "warn",
          };
  }
}

/** 失联时“保持”的供电状态说明，如“保持：市电供电”。 */
export function heldPowerText(state: DeviceState): string {
  return `保持：${enumLabel("power_state", state.power_state)}`;
}

export const COMM_LOST_POLICY_TEXT: Readonly<Record<DeviceConfig["comm_lost_policy"], string>> = {
  hold: "保持（沿用最后确认的供电状态）",
  ignore: "忽略（显示未知，重连后重新确认）",
  assume_on_battery_after: "超时后假定停电",
};

/**
 * 放电计时（秒）：从 on_battery_since 到 `refMs`。
 * 实时连接断开时调用方应传入断开时刻，避免把断流期间当作连续观测。
 */
export function onBatteryElapsed(state: DeviceState, refMs: number): number | null {
  if (!state.on_battery_since) return null;
  const since = Date.parse(state.on_battery_since);
  if (Number.isNaN(since)) return null;
  return Math.max(0, Math.floor((refMs - since) / 1000));
}

/** 相数：取 good 的 input_phases / output_phases，夹到 1～3；无有效值按单相。 */
export function phaseCount(fields: FieldMap, key: "input_phases" | "output_phases"): number {
  const n = goodNumber(fields[key]);
  if (n === null || !Number.isFinite(n)) return 1;
  return Math.min(Math.max(Math.trunc(n), 1), 3);
}

/** 告警是否“需要关注”：停电本身单独展示，其余 warning / critical 告警计入。 */
function isAttentionAlert(alert: EventItem): boolean {
  return alert.severity !== "info" && alert.type !== "mains_lost";
}

/** 需要关注：失联、非市电/电池的供电状态，或存在除停电外的告警。 */
export function needsAttention(state: DeviceState, alerts: readonly EventItem[] = []): boolean {
  if (isCommLost(state)) return true;
  if (!["online", "on_battery"].includes(state.power_state)) return true;
  return alerts.some(isAttentionAlert);
}

export function isOnBattery(state: DeviceState): boolean {
  return state.power_state === "on_battery";
}

/** “按状态”排序的等级：电池供电 → 需要关注 → 去抖中 → 正常。 */
export function statusRank(state: DeviceState, alerts: readonly EventItem[] = []): number {
  if (isOnBattery(state)) return 0;
  if (needsAttention(state, alerts)) return 1;
  if (state.pending_since) return 2;
  return 3;
}

/** 数据源协议显示名，如“SNMP v2c”“Modbus TCP”“AYI9 JSON”。 */
export function protocolLabel(source: Pick<Source, "protocol" | "params">): string {
  switch (source.protocol) {
    case "snmp": {
      const version = source.params?.version;
      return typeof version === "string" ? `SNMP v${version}` : "SNMP";
    }
    case "modbus":
      return "Modbus TCP";
    case "ayi9-json":
      return "AYI9 JSON";
    default:
      return source.protocol;
  }
}

/** 设备的当前数据源：P0 每台设备最多一个启用源，优先取启用的。 */
export function primarySource(sources: readonly Source[] | undefined): Source | undefined {
  if (!sources || sources.length === 0) return undefined;
  return sources.find((s) => s.enabled) ?? sources[0];
}

/** 设备显示名：设备记录尚未加载时回退为“设备 #id”。 */
export function deviceName(devices: readonly Device[] | undefined, id: number): string {
  return devices?.find((d) => d.id === id)?.name ?? `设备 #${id}`;
}

/** 相对时间：刚刚 / n 秒前 / n 分钟前 / n 小时前；超过一天返回 null，由调用方显示绝对时间。 */
export function relativeTime(at: string | null, nowMs: number): string | null {
  if (!at) return null;
  const t = Date.parse(at);
  if (Number.isNaN(t)) return null;
  const s = Math.max(0, Math.floor((nowMs - t) / 1000));
  if (s < 5) return "刚刚";
  if (s < 60) return `${s} 秒前`;
  if (s < 3600) return `${Math.floor(s / 60)} 分钟前`;
  if (s < 86400) return `${Math.floor(s / 3600)} 小时前`;
  return null;
}
