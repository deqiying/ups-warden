import type { ReactNode } from "react";
import { Icon, type IconName } from "../icons/Icon.tsx";

export type PillTone =
  | "gray"
  | "green"
  | "orange"
  | "red"
  | "yellow"
  | "indigo"
  | "blue"
  | "solid-red";

export interface PillProps {
  /** 色调，默认 "gray"（不加修饰类）。 */
  tone?: PillTone;
  size?: "sm";
  icon?: IconName;
  /** 在文字前显示状态圆点。 */
  dot?: boolean;
  /** 圆点呼吸动画，用于「进行中/实时」状态；需要 dot。 */
  breathe?: boolean;
  className?: string;
  children?: ReactNode;
}

/** 状态胶囊。 */
export function Pill({ tone = "gray", size, icon, dot, breathe, className, children }: PillProps) {
  const cls = ["pill", tone !== "gray" && `pill-${tone}`, size && `pill-${size}`, className]
    .filter(Boolean)
    .join(" ");
  return (
    <span className={cls}>
      {dot ? <span className={breathe ? "dot breathe" : "dot"} /> : null}
      {icon ? <Icon name={icon} /> : null}
      {children}
    </span>
  );
}
