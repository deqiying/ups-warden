import { useId } from "react";
import type { ReactNode } from "react";
import { Button } from "./Button.tsx";
import { Overlay } from "./Overlay.tsx";

export interface SheetProps {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  /** 头部左侧；默认是调用 onClose 的“取消”plain 按钮。 */
  leading?: ReactNode;
  /** 头部右侧操作（保存、下一步等）。 */
  trailing?: ReactNode;
  /** 写入 style.width，如 "min(560px, calc(100vw - 32px))"。 */
  width?: string;
  /** false 时禁用 Esc、遮罩点击与默认取消按钮（保存中使用）。默认 true。 */
  dismissible?: boolean;
  /** true 时 children 原样渲染（调用方自带 .steps / .sheet-body 等），否则包进 .sheet-body。 */
  bare?: boolean;
  "aria-describedby"?: string;
  children?: ReactNode;
}

/** 表单/向导类大浮层：左取消、中标题、右操作。 */
export function Sheet({
  open,
  onClose,
  title,
  leading,
  trailing,
  width,
  dismissible = true,
  bare = false,
  "aria-describedby": ariaDescribedBy,
  children,
}: SheetProps) {
  const titleId = useId();
  return (
    <Overlay open={open} onClose={onClose} dismissible={dismissible} closeOnScrim>
      <section
        className="sheet"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={ariaDescribedBy}
        style={width ? { width } : undefined}
      >
        <header className="sheet-head">
          {leading ?? (
            <Button variant="plain" onClick={onClose} disabled={!dismissible}>
              取消
            </Button>
          )}
          <div className="sheet-title" id={titleId}>
            {title}
          </div>
          {/* 右列始终存在，保证标题在三列 grid 中居中 */}
          <div className="hstack">{trailing}</div>
        </header>
        {bare ? children : <div className="sheet-body">{children}</div>}
      </section>
    </Overlay>
  );
}
