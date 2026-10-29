import type { CSSProperties } from "react";

export interface MeterProps {
  /** 比例；夹到 0～1，NaN 视为 0。 */
  fraction: number;
  /** 填充色，写入 --c，如 "var(--orange)"。 */
  color?: string;
  /** 读屏名称，必须提供（例如「负载 L1」）。 */
  label: string;
  className?: string;
  style?: CSSProperties;
}

/** 细条指标（role="meter"，范围 0～100）。 */
export function Meter({ fraction, color, label, className, style }: MeterProps) {
  const percent = Number.isNaN(fraction) ? 0 : Math.min(1, Math.max(0, fraction)) * 100;
  const cls = ["meter", className].filter(Boolean).join(" ");
  return (
    <div
      className={cls}
      style={style}
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(percent)}
    >
      <span
        style={{ width: `${percent}%`, ...(color ? ({ "--c": color } as CSSProperties) : null) }}
      />
    </div>
  );
}
