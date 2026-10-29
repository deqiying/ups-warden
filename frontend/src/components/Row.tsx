import type { CSSProperties, KeyboardEvent, ReactNode } from "react";
import { Icon, type IconName } from "../icons/Icon.tsx";

export interface RowProps {
  icon?: IconName;
  /** 图标底色，写入 --c，如 "var(--orange)"。 */
  iconColor?: string;
  /** 浅底图标（is-soft）：图标着色、底为浅灰。 */
  softIcon?: boolean;
  title?: ReactNode;
  sub?: ReactNode;
  /** 右侧的值（.row-value）。 */
  value?: ReactNode;
  /** 末尾显示右箭头，提示可进入下一级。 */
  chevron?: boolean;
  /** 当前选中行：加 is-selected 并设置 aria-current。 */
  selected?: boolean;
  /**
   * 使整行可点击（is-link、role="button"、可聚焦，Enter/Space 触发）。
   * 行内含开关、按钮等其他交互控件时不要使用：交互元素嵌套会破坏可访问性，
   * 此时改用行内链接/按钮作为入口。
   */
  onActivate?: () => void;
  /** 自定义列，位于主文本之后、value 之前。 */
  children?: ReactNode;
  className?: string;
  style?: CSSProperties;
}

/** 列表行。 */
export function Row({
  icon,
  iconColor,
  softIcon,
  title,
  sub,
  value,
  chevron,
  selected,
  onActivate,
  children,
  className,
  style,
}: RowProps) {
  const cls = ["row", onActivate && "is-link", selected && "is-selected", className]
    .filter(Boolean)
    .join(" ");

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    // 只响应行自身的按键，避免劫持行内子元素（输入框等）的 Enter/Space。
    if (!onActivate || event.target !== event.currentTarget) return;
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      onActivate();
    }
  };

  const iconStyle = iconColor ? ({ "--c": iconColor } as CSSProperties) : undefined;

  return (
    <div
      className={cls}
      style={style}
      role={onActivate ? "button" : undefined}
      tabIndex={onActivate ? 0 : undefined}
      aria-current={selected ? "true" : undefined}
      onClick={onActivate}
      onKeyDown={onActivate ? handleKeyDown : undefined}
    >
      {icon ? (
        <span className={softIcon ? "row-icon is-soft" : "row-icon"} style={iconStyle}>
          <Icon name={icon} />
        </span>
      ) : null}
      <div className="row-main">
        {title ? <div className="row-title">{title}</div> : null}
        {sub ? <div className="row-sub">{sub}</div> : null}
      </div>
      {children}
      {value !== undefined && value !== null ? <span className="row-value">{value}</span> : null}
      {chevron ? <Icon name="chev-right" className="row-chevron" /> : null}
    </div>
  );
}
