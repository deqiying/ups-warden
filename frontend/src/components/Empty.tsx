import type { ReactNode } from "react";
import { Icon, type IconName } from "../icons/Icon.tsx";

export interface EmptyProps {
  /** 省略则不显示图标徽记。 */
  icon?: IconName;
  /** 图标徽记的色调，默认 "ok"。 */
  tone?: "info" | "warning" | "critical" | "ok";
  title: ReactNode;
  /** 说明文字。 */
  children?: ReactNode;
  /** 下一步操作（通常是按钮）。 */
  action?: ReactNode;
}

/** 空状态：图标 + 标题 + 说明 + 可选操作。 */
export function Empty({ icon, tone = "ok", title, children, action }: EmptyProps) {
  return (
    <div className="empty">
      {icon ? (
        <span className={`sev sev-${tone}`}>
          <Icon name={icon} />
        </span>
      ) : null}
      <div className="t-headline">{title}</div>
      {children ? <div className="t-subhead muted">{children}</div> : null}
      {action}
    </div>
  );
}
