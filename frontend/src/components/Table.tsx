import type { ReactNode } from "react";

export interface TableProps {
  /** 表头吸顶（is-sticky），需要外层容器可滚动。 */
  sticky?: boolean;
  /** 视觉隐藏、仅供读屏的表格标题。 */
  caption?: string;
  className?: string;
  /** 由调用方提供 thead / tbody。 */
  children?: ReactNode;
}

/** 数据表格外壳。 */
export function Table({ sticky, caption, className, children }: TableProps) {
  const cls = ["table", sticky && "is-sticky", className].filter(Boolean).join(" ");
  return (
    <table className={cls}>
      {caption ? <caption className="sr-only">{caption}</caption> : null}
      {children}
    </table>
  );
}
