import type { CSSProperties, ReactNode } from "react";
import { Icon, type IconName } from "../icons/Icon.tsx";

export type BannerTone = "info" | "warning" | "critical" | "indigo" | "neutral";

const DEFAULT_ICONS: Record<BannerTone, IconName> = {
  info: "info",
  warning: "alert",
  critical: "x-circle",
  indigo: "flask",
  neutral: "info",
};

export interface BannerProps {
  /** 默认 "info"（无修饰类）。 */
  tone?: BannerTone;
  /** 覆盖按色调选择的默认图标。 */
  icon?: IconName;
  title?: ReactNode;
  /** 正文，渲染在 .banner-text 中。 */
  children?: ReactNode;
  /** 右侧操作区（按钮/链接）。 */
  actions?: ReactNode;
  /** 动态出现的提示建议设置：critical/warning 用 "alert"，其余用 "status"。 */
  role?: "status" | "alert";
  className?: string;
  style?: CSSProperties;
}

/** 提示条。 */
export function Banner({
  tone = "info",
  icon,
  title,
  children,
  actions,
  role,
  className,
  style,
}: BannerProps) {
  const cls = ["banner", tone !== "info" && `banner-${tone}`, className].filter(Boolean).join(" ");
  return (
    <div className={cls} style={style} role={role}>
      <Icon name={icon ?? DEFAULT_ICONS[tone]} className="banner-icon" />
      <div>
        {title ? <div className="banner-title">{title}</div> : null}
        {children ? <div className="banner-text">{children}</div> : null}
      </div>
      {actions ? <div className="actions">{actions}</div> : null}
    </div>
  );
}
