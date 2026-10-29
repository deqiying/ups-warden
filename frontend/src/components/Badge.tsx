import type { ReactNode } from "react";
import { Icon, type IconName } from "../icons/Icon.tsx";

export type BadgeTone =
  | "gray"
  | "indigo"
  | "orange"
  | "green"
  | "red"
  | "blue"
  | "yellow"
  | "outline";

export interface BadgeProps {
  /** 色调，默认 "gray"（不加修饰类）。 */
  tone?: BadgeTone;
  icon?: IconName;
  className?: string;
  children?: ReactNode;
}

/** 小型标记（比 Pill 更克制，用于类型/来源等元信息）。 */
export function Badge({ tone = "gray", icon, className, children }: BadgeProps) {
  const cls = ["badge", tone !== "gray" && `badge-${tone}`, className].filter(Boolean).join(" ");
  return (
    <span className={cls}>
      {icon ? <Icon name={icon} /> : null}
      {children}
    </span>
  );
}
