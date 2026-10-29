import type { CSSProperties, ReactNode } from "react";

export interface ListProps {
  /** 行首带图标：分隔线缩进到图标之后（has-icons）。 */
  hasIcons?: boolean;
  /** 去掉卡片底色、圆角与阴影（is-flat），用于嵌在卡片内。 */
  flat?: boolean;
  /** 分隔线左缩进（px），写入 --row-inset；省略时按 hasIcons 取默认值。 */
  inset?: number;
  className?: string;
  role?: string;
  "aria-label"?: string;
  children?: ReactNode;
}

/** 分组列表容器，内部放 {@link Row} / {@link FormRow}。 */
export function List({
  hasIcons,
  flat,
  inset,
  className,
  role,
  "aria-label": ariaLabel,
  children,
}: ListProps) {
  const cls = ["list", hasIcons && "has-icons", flat && "is-flat", className]
    .filter(Boolean)
    .join(" ");
  const style =
    inset === undefined ? undefined : ({ "--row-inset": `${inset}px` } as CSSProperties);
  return (
    <div className={cls} style={style} role={role} aria-label={ariaLabel}>
      {children}
    </div>
  );
}

export interface ListHeaderProps {
  children?: ReactNode;
}

/** 列表上方的小标题。 */
export function ListHeader({ children }: ListHeaderProps) {
  return <div className="list-header">{children}</div>;
}

export interface ListCaptionProps {
  children?: ReactNode;
}

/** 列表下方的说明文字。 */
export function ListCaption({ children }: ListCaptionProps) {
  return <p className="list-caption">{children}</p>;
}
