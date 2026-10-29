import { useLayoutEffect, useRef } from "react";
import type { ReactNode, RefObject } from "react";
import { createPortal } from "react-dom";

export interface OverlayProps {
  open: boolean;
  /** Esc 或（closeOnScrim 时）点击遮罩触发；由调用方决定是否真的关闭。 */
  onClose: () => void;
  /** false 时 Esc 与遮罩点击都不会触发 onClose（如保存/删除进行中）。默认 true。 */
  dismissible?: boolean;
  /** 点击遮罩是否关闭；仍受 dismissible 约束。默认 false。 */
  closeOnScrim?: boolean;
  /** 打开时优先获得焦点的元素；省略则聚焦第一个可聚焦元素，没有则聚焦容器。 */
  initialFocusRef?: RefObject<HTMLElement | null>;
  /** 浮层本体（.sheet / .dialog），与遮罩同级渲染。 */
  children: ReactNode;
}

const FOCUSABLE = [
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled]):not([type='hidden'])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  "[tabindex]:not([tabindex='-1'])",
].join(",");

function focusableIn(root: HTMLElement): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
    (el) => el.closest("[hidden]") === null,
  );
}

// 叠放的浮层栈：只有栈顶响应 Esc 与焦点陷阱。模块级即可，同一页面只有一份。
const stack: object[] = [];

// body 滚动锁按引用计数：最后一层关闭才恢复，且恢复的是第一层打开前的值。
let scrollLocks = 0;
let savedOverflow = "";

function lockScroll() {
  if (scrollLocks === 0) {
    savedOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
  }
  scrollLocks += 1;
}

function unlockScroll() {
  scrollLocks = Math.max(0, scrollLocks - 1);
  if (scrollLocks === 0) document.body.style.overflow = savedOverflow;
}

type BodyProps = Omit<OverlayProps, "open">;

// 仅在 open 时挂载：挂载/卸载即“打开/关闭”，焦点、栈、滚动锁都挂在这个生命周期上。
function OverlayBody({
  onClose,
  dismissible = true,
  closeOnScrim = false,
  initialFocusRef,
  children,
}: BodyProps) {
  const rootRef = useRef<HTMLDivElement>(null);
  // 事件监听只注册一次，通过 ref 读取最新的回调，避免父组件每次渲染都重新抢焦点。
  const latest = useRef({ onClose, dismissible, initialFocusRef });
  useLayoutEffect(() => {
    latest.current = { onClose, dismissible, initialFocusRef };
  });

  useLayoutEffect(() => {
    const root = rootRef.current;
    if (!root) return;
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const token = {};
    stack.push(token);
    lockScroll();

    const target = latest.current.initialFocusRef?.current ?? focusableIn(root)[0] ?? root;
    target.focus();

    const onKeyDown = (event: KeyboardEvent) => {
      if (stack[stack.length - 1] !== token) return;
      if (event.key === "Escape") {
        if (latest.current.dismissible && !event.defaultPrevented) {
          event.preventDefault();
          latest.current.onClose();
        }
        return;
      }
      if (event.key !== "Tab") return;
      const items = focusableIn(root);
      const first = items[0];
      const last = items[items.length - 1];
      if (!first || !last) {
        event.preventDefault();
        root.focus();
        return;
      }
      // 焦点不在可聚焦列表里（容器本身、刚被禁用的按钮、浮层外）时一律拉回边界。
      const index = items.indexOf(document.activeElement as HTMLElement);
      if (event.shiftKey) {
        if (index <= 0) {
          event.preventDefault();
          last.focus();
        }
      } else if (index === -1 || index === items.length - 1) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKeyDown);

    return () => {
      document.removeEventListener("keydown", onKeyDown);
      const at = stack.indexOf(token);
      if (at >= 0) stack.splice(at, 1);
      unlockScroll();
      if (previous?.isConnected) previous.focus();
    };
  }, []);

  return (
    <div className="overlay" ref={rootRef} tabIndex={-1}>
      <div
        className="scrim"
        aria-hidden="true"
        onClick={closeOnScrim && dismissible ? onClose : undefined}
      />
      {children}
    </div>
  );
}

/**
 * 浮层基础件：portal 到 body，负责焦点陷阱、焦点还原、Esc、遮罩点击与滚动锁。
 * `open=false` 时不渲染任何内容（不做退场动画）。
 */
export function Overlay({ open, ...rest }: OverlayProps) {
  if (!open) return null;
  return createPortal(<OverlayBody {...rest} />, document.body);
}
