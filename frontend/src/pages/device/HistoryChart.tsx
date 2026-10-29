import { formatDateTime, formatTime } from "../../model/time.ts";
import {
  historyUnitLabel,
  niceScale,
  tickLabel,
  type Interval,
  type Series,
  type SeriesPoint,
} from "./history.ts";

const W = 720;
const H = 240;
const PAD = { left: 44, right: 8, top: 16, bottom: 24 };
const PLOT_W = W - PAD.left - PAD.right;
const PLOT_H = H - PAD.top - PAD.bottom;
const X_TICKS = 5;

export interface HistoryChartProps {
  /** 同一单位的序列（不同单位分图）。 */
  series: readonly Series[];
  from: number;
  to: number;
  /** 1m 分辨率时绘制 min / max 带。 */
  band: boolean;
  outages: readonly Interval[];
  timeZone: string | undefined;
  /** 读屏摘要（图形本身对辅助技术隐藏细节）。 */
  label: string;
}

function axisTime(t: number, rangeMs: number, timeZone: string | undefined): string {
  if (rangeMs <= 24 * 3600_000) return formatTime(t, timeZone).slice(0, 5);
  // “10月28日 14:05” → 超过一天只显示日期。
  return formatDateTime(t, timeZone).split(" ")[0];
}

function pathOf(
  points: readonly SeriesPoint[],
  x: (t: number) => number,
  y: (v: number) => number,
  pick: (p: SeriesPoint) => number,
): string {
  return points
    .map((p, i) => `${i === 0 ? "M" : "L"}${x(p.t).toFixed(1)} ${y(pick(p)).toFixed(1)}`)
    .join(" ");
}

/**
 * 自写 SVG 折线（frontend-plan 第 6 节：不引入图表库）。
 * 多序列共用一根纵轴（调用方按单位分组）；缺失采样处断开；1m 分辨率绘制 min / max 带；
 * 停电区间以橙色阴影标出。
 */
export function HistoryChart({
  series,
  from,
  to,
  band,
  outages,
  timeZone,
  label,
}: HistoryChartProps) {
  const range = Math.max(1, to - from);
  let lo = Infinity;
  let hi = -Infinity;
  for (const s of series) {
    for (const seg of s.segments) {
      for (const p of seg) {
        lo = Math.min(lo, band ? p.min : p.avg);
        hi = Math.max(hi, band ? p.max : p.avg);
      }
    }
  }
  const unit = series[0]?.unit ?? "";
  const scale = niceScale(lo, hi, unit);
  const span = scale.max - scale.min || 1;
  const x = (t: number) => PAD.left + ((t - from) / range) * PLOT_W;
  const y = (v: number) => PAD.top + (1 - (v - scale.min) / span) * PLOT_H;
  const xTicks = Array.from({ length: X_TICKS }, (_, i) => from + (range * i) / (X_TICKS - 1));

  return (
    <svg className="chart" viewBox={`0 0 ${W} ${H}`} role="img" aria-label={label}>
      <g className="chart-grid">
        {scale.ticks.map((v) => (
          <line key={v} x1={PAD.left} x2={W - PAD.right} y1={y(v)} y2={y(v)} />
        ))}
      </g>
      {scale.ticks.map((v) => (
        <text key={v} x={PAD.left - 8} y={y(v) + 4} textAnchor="end">
          {tickLabel(v, unit)}
        </text>
      ))}
      <text x={PAD.left - 8} y={PAD.top - 6} textAnchor="end" className="chart-unit">
        {historyUnitLabel(unit)}
      </text>
      {outages.map((o) => (
        <g key={o.start} className="chart-outage">
          <rect
            x={x(o.start)}
            y={PAD.top}
            width={Math.max(1, x(o.end) - x(o.start))}
            height={PLOT_H}
          />
          <line x1={x(o.start)} x2={x(o.start)} y1={PAD.top} y2={PAD.top + PLOT_H} />
        </g>
      ))}
      {band
        ? series.map((s) =>
            s.segments.map((seg) =>
              seg.length > 1 ? (
                <path
                  key={`${s.key}-band-${seg[0].t}`}
                  className="chart-band"
                  d={`${pathOf(seg, x, y, (p) => p.max)} ${[...seg]
                    .reverse()
                    .map((p) => `L${x(p.t).toFixed(1)} ${y(p.min).toFixed(1)}`)
                    .join(" ")} Z`}
                  style={{ fill: s.color }}
                />
              ) : null,
            ),
          )
        : null}
      {series.map((s) =>
        s.segments.map((seg) =>
          seg.length === 1 ? (
            // 孤立采样画成点，避免单点段不可见。
            <circle
              key={`${s.key}-${seg[0].t}`}
              cx={x(seg[0].t)}
              cy={y(seg[0].avg)}
              r={2.5}
              style={{ fill: s.color }}
            />
          ) : (
            <path
              key={`${s.key}-${seg[0].t}`}
              className="chart-line"
              d={pathOf(seg, x, y, (p) => p.avg)}
              style={{ stroke: s.color }}
            />
          ),
        ),
      )}
      {xTicks.map((t, i) => (
        <text
          key={t}
          x={x(t)}
          y={H - 6}
          textAnchor={i === 0 ? "start" : i === X_TICKS - 1 ? "end" : "middle"}
        >
          {axisTime(t, range, timeZone)}
        </text>
      ))}
    </svg>
  );
}
