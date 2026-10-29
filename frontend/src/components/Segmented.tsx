import { useEffect, useLayoutEffect, useRef } from "react";
import type { KeyboardEvent, ReactNode } from "react";
import { Icon, type IconName } from "../icons/Icon.tsx";

export interface SegmentedOption<T extends string> {
  value: T;
  label: ReactNode;
  icon?: IconName;
  /** 选项右侧的计数角标。 */
  count?: number;
}

export interface SegmentedProps<T extends string> {
  options: SegmentedOption<T>[];
  value: T;
  onChange: (value: T) => void;
  size?: "lg";
  /** 撑满容器宽度，各选项等分。 */
  block?: boolean;
  /** 控件没有可见标题，必须提供。 */
  "aria-label": string;
  disabled?: boolean;
  className?: string;
}

/** 把滑块（.seg::before）对齐到当前选中的按钮；尺寸由 CSS 变量传入。 */
function measure(el: HTMLElement) {
  const active = el.querySelector<HTMLElement>('button[aria-pressed="true"]');
  if (!active) return;
  el.style.setProperty("--seg-x", `${active.offsetLeft}px`);
  el.style.setProperty("--seg-w", `${active.offsetWidth}px`);
}

/**
 * 暂时关闭滑块过渡，下一帧恢复。直接操作 classList：className 只在 props 变化时
 * 才会被 React 重写，此时过渡被提前恢复也无妨。
 */
function suppressAnimation(el: HTMLElement, frame: { current: number }) {
  el.classList.add("no-anim");
  cancelAnimationFrame(frame.current);
  frame.current = requestAnimationFrame(() => el.classList.remove("no-anim"));
}

/**
 * 分段控件（受控）。滑块位置通过 ref 直接写入容器的 CSS 变量，不经过 React 状态，
 * 因此不会因测量触发额外渲染。
 */
export function Segmented<T extends string>({
  options,
  value,
  onChange,
  size,
  block,
  "aria-label": ariaLabel,
  disabled,
  className,
}: SegmentedProps<T>) {
  const ref = useRef<HTMLDivElement>(null);
  const frame = useRef(0);

  // 每次提交后都重新测量：标签、计数或选中项变化都可能改变按钮宽度。
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    // 首次测量没有旧位置可过渡，否则滑块会从左缘飞入。
    if (!el.style.getPropertyValue("--seg-w")) suppressAnimation(el, frame);
    measure(el);
  });

  // 容器尺寸变化（窗口缩放、字体加载）属于布局修正，不应播放滑动动画。
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const remeasure = () => {
      suppressAnimation(el, frame);
      measure(el);
    };
    if (typeof ResizeObserver === "undefined") {
      window.addEventListener("resize", remeasure);
      return () => window.removeEventListener("resize", remeasure);
    }
    const observer = new ResizeObserver(remeasure);
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  useEffect(() => () => cancelAnimationFrame(frame.current), []);

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (disabled || (event.key !== "ArrowLeft" && event.key !== "ArrowRight")) return;
    const buttons = Array.from(event.currentTarget.querySelectorAll("button"));
    const focused = buttons.indexOf(document.activeElement as HTMLButtonElement);
    const current = focused >= 0 ? focused : options.findIndex((o) => o.value === value);
    const step = event.key === "ArrowRight" ? 1 : -1;
    const next = (current + step + options.length) % options.length;
    const target = options[next];
    if (!target) return;
    event.preventDefault();
    buttons[next]?.focus();
    if (target.value !== value) onChange(target.value);
  };

  const cls = ["seg", size && `seg-${size}`, block && "seg-block", className]
    .filter(Boolean)
    .join(" ");

  return (
    <div ref={ref} className={cls} role="group" aria-label={ariaLabel} onKeyDown={handleKeyDown}>
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          aria-pressed={option.value === value}
          disabled={disabled}
          onClick={() => onChange(option.value)}
        >
          {option.icon ? <Icon name={option.icon} /> : null}
          {option.label}
          {option.count === undefined ? null : <span className="count">{option.count}</span>}
        </button>
      ))}
    </div>
  );
}
