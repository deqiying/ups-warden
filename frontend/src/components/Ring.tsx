import type { ReactNode } from "react";

export interface RingProps {
  /** 完成比例；夹到 0～1，NaN 视为 0。 */
  fraction: number;
  /** 外框边长（px），默认 64。 */
  size?: number;
  /** 描边宽度（px），默认 5。 */
  stroke?: number;
  /** 进度弧颜色，默认 "var(--accent)"。 */
  color?: string;
  /** 提供时以 role="img" 暴露给读屏；省略则视为装饰。 */
  label?: string;
  /** 圆环中心内容（.ring-center）。 */
  children?: ReactNode;
}

/** 把比例夹到 [0, 1]；NaN 视为 0。 */
function clampFraction(fraction: number): number {
  if (Number.isNaN(fraction)) return 0;
  return Math.min(1, Math.max(0, fraction));
}

/** 环形进度，只用于有明确终点的倒计时等场景。 */
export function Ring({
  fraction,
  size = 64,
  stroke = 5,
  color = "var(--accent)",
  label,
  children,
}: RingProps) {
  const percent = Number((clampFraction(fraction) * 100).toFixed(2));
  const center = size / 2;
  const radius = (size - stroke) / 2;
  return (
    <div
      className="ring-wrap"
      style={{ width: size, height: size }}
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
    >
      <svg className="ring" viewBox={`0 0 ${size} ${size}`} width={size} height={size}>
        <circle className="track" cx={center} cy={center} r={radius} strokeWidth={stroke} />
        {/* pathLength=100 使 dasharray 直接以百分比表示 */}
        <circle
          className="bar"
          cx={center}
          cy={center}
          r={radius}
          strokeWidth={stroke}
          pathLength={100}
          strokeDasharray={`${percent} 100`}
          style={{ stroke: color }}
        />
      </svg>
      <div className="ring-center">{children}</div>
    </div>
  );
}
