/*
 * 监控页面（总览、设备详情）共用的小组件。
 * 数值一律经 FieldValue / formatField 显示，非 good 字段为「—」加原因，绝不显示 0。
 */
import type { DeviceState } from "../../api/types.ts";
import { FieldValue } from "../../components/Metric.tsx";
import { Pill } from "../../components/Pill.tsx";
import { Ring } from "../../components/Ring.tsx";
import { powerStatus } from "../../model/device.ts";
import { goodNumber } from "../../model/format.ts";
import "./monitor.css";

export interface PowerPillProps {
  state: DeviceState;
  /** 附加在文案后的内容（如放电计时）。 */
  suffix?: string;
  size?: "sm";
}

/** 供电状态胶囊：颜色之外同时带圆点 / 图标与文字。 */
export function PowerPill({ state, suffix, size }: PowerPillProps) {
  const status = powerStatus(state);
  return (
    <Pill
      tone={status.tone}
      dot={status.dot}
      breathe={status.breathe}
      icon={status.icon}
      size={size}
    >
      {suffix ? `${status.label} · ${suffix}` : status.label}
    </Pill>
  );
}

/** 容量环颜色：失联或断线为灰，电池供电为橙，低电为红，其余为绿。 */
function ringColor(state: DeviceState, stale: boolean, capacity: number | null): string {
  if (stale || state.comm_state === "lost" || capacity === null) return "var(--label-4)";
  if (state.fields.low_battery?.quality === "good" && state.fields.low_battery.bool) {
    return "var(--red)";
  }
  return state.power_state === "on_battery" ? "var(--orange)" : "var(--green)";
}

export interface CapacityRingProps {
  state: DeviceState;
  stale: boolean;
  size: number;
  stroke: number;
  /** 数值下方的小字（“电池”“3 分钟前”）。 */
  caption: string;
  numClassName: string;
  captionClassName: string;
}

/**
 * 电池容量环。容量非 good 时环为空、中心显示「—」与原因。
 * 不复用 <Ring>：它在无 label 时整体 aria-hidden，会把中心的数值与原因提示一并对读屏隐藏；
 * 这里只隐藏装饰性的圆环。
 */
export function CapacityRing({
  state,
  stale,
  size,
  stroke,
  caption,
  numClassName,
  captionClassName,
}: CapacityRingProps) {
  const field = state.fields.battery_capacity_pct;
  const capacity = goodNumber(field);
  const isStale = stale || field?.quality === "stale";
  const percent = capacity === null ? 0 : Math.min(100, Math.max(0, capacity));
  const center = size / 2;
  const radius = (size - stroke) / 2;
  return (
    <div className="ring-wrap" style={{ width: size, height: size }}>
      <svg
        className="ring"
        viewBox={`0 0 ${size} ${size}`}
        width={size}
        height={size}
        aria-hidden="true"
      >
        <circle className="track" cx={center} cy={center} r={radius} strokeWidth={stroke} />
        <circle
          className="bar"
          cx={center}
          cy={center}
          r={radius}
          strokeWidth={stroke}
          pathLength={100}
          strokeDasharray={`${percent} 100`}
          style={{ stroke: ringColor(state, isStale, capacity) }}
        />
      </svg>
      <div className="ring-center">
        <div>
          <FieldValue
            fieldKey="battery_capacity_pct"
            field={field}
            stale={stale}
            className={numClassName}
          />
          <div className={captionClassName}>{caption}</div>
        </div>
      </div>
    </div>
  );
}

/** 倒计时环（停电主卡）：剩余比例，中心为 mm:ss。 */
export function CountdownRing({
  fraction,
  text,
  size = 88,
}: {
  fraction: number;
  text: string;
  size?: number;
}) {
  return (
    <Ring
      fraction={fraction}
      size={size}
      stroke={6}
      color="var(--orange)"
      label={`倒计时剩余 ${text}`}
    >
      <span className="cd-num">{text}</span>
    </Ring>
  );
}
