import type { ReactNode } from "react";

export interface SplitViewProps {
  /** 左侧列表（规则、计划、渠道、设置分区等）。 */
  list: ReactNode;
  detail: ReactNode;
  /** 窄列表（240px，设置分区导航）；默认 320px。 */
  narrow?: boolean;
  /** 列表随页面滚动时吸顶。 */
  stickyList?: boolean;
  listLabel?: string;
}

/** 列表-详情分栏；≤860px 时由 CSS 改为上下叠放。 */
export function SplitView({ list, detail, narrow, stickyList, listLabel }: SplitViewProps) {
  return (
    <div className={narrow ? "split split-narrow" : "split"}>
      <section className={stickyList ? "sticky-col" : undefined} aria-label={listLabel}>
        {list}
      </section>
      <section>{detail}</section>
    </div>
  );
}
