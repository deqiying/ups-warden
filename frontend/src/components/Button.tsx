import type { ButtonHTMLAttributes, ReactNode, Ref } from "react";
import { Icon, type IconName } from "../icons/Icon.tsx";

export type ButtonVariant = "primary" | "tinted" | "gray" | "plain" | "danger" | "danger-tinted";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  /** 视觉变体，默认 "gray"。 */
  variant?: ButtonVariant;
  size?: "sm" | "lg";
  /** 文字前的图标。 */
  icon?: IconName;
  /** 仅图标按钮（btn-icon）。没有可见文字，调用方必须传 aria-label。 */
  iconOnly?: boolean;
  /** 撑满容器宽度。 */
  block?: boolean;
  /** 圆角矩形而非胶囊形。 */
  rect?: boolean;
  children?: ReactNode;
  ref?: Ref<HTMLButtonElement>;
}

/** 通用按钮；`type` 默认为 "button"，避免在表单内意外提交。 */
export function Button({
  variant = "gray",
  size,
  icon,
  iconOnly,
  block,
  rect,
  type = "button",
  className,
  children,
  ...rest
}: ButtonProps) {
  const cls = [
    "btn",
    `btn-${variant}`,
    size && `btn-${size}`,
    iconOnly && "btn-icon",
    block && "btn-block",
    rect && "btn-rect",
    className,
  ]
    .filter(Boolean)
    .join(" ");
  return (
    <button {...rest} type={type} className={cls}>
      {icon ? <Icon name={icon} /> : null}
      {children}
    </button>
  );
}
