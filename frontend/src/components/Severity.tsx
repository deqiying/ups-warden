import { Icon, type IconName } from "../icons/Icon.tsx";

export type SeverityLevel = "info" | "warning" | "critical" | "ok";

/** 各级别默认的可读名称，供图标 label 与外部展示复用。 */
// 规格要求与组件同文件导出该常量；它是不可变的纯数据，不影响 Fast Refresh 的实际行为。
// oxlint-disable-next-line react/only-export-components
export const SEVERITY_LABELS: Record<SeverityLevel, string> = {
  info: "信息",
  warning: "警告",
  critical: "严重",
  ok: "正常",
};

const SEVERITY_ICONS: Record<SeverityLevel, IconName> = {
  info: "info",
  warning: "alert",
  critical: "alert",
  ok: "check",
};

export interface SeverityProps {
  level: SeverityLevel;
  /** 覆盖图标的可读名称，默认取 {@link SEVERITY_LABELS}。 */
  label?: string;
  className?: string;
}

/** 严重度图标徽记；颜色之外还通过图标形状与 label 传达级别。 */
export function Severity({ level, label, className }: SeverityProps) {
  const cls = ["sev", `sev-${level}`, className].filter(Boolean).join(" ");
  return (
    <span className={cls}>
      <Icon name={SEVERITY_ICONS[level]} label={label ?? SEVERITY_LABELS[level]} />
    </span>
  );
}
