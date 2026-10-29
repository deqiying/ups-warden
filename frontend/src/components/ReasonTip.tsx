import { useId, useState, type KeyboardEvent, type ReactNode } from "react";

export interface ReasonTipProps {
  /** 提示内容，例如“Profile 未映射”。 */
  reason: string;
  /** 触发器的可见内容，默认「—」。 */
  children?: ReactNode;
  /** 触发器给屏幕阅读器的名称（可见内容只是符号时必需），默认“无数据”。 */
  label?: string;
  /** 作用于外层 span，例如 `metric-value na`。 */
  className?: string;
}

/**
 * 「—」的原因提示。效果图的 `[data-tip]` 只支持 hover，触屏和键盘用户看不到（frontend-plan 3.6），
 * 这里改为可聚焦按钮：悬停、聚焦或点按显示，Esc 或失焦隐藏；提示通过 aria-describedby 关联。
 */
export function ReasonTip({ reason, children = "—", label = "无数据", className }: ReasonTipProps) {
  const id = useId();
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  const [pinned, setPinned] = useState(false);
  const [dismissed, setDismissed] = useState(false);
  const visible = !dismissed && (hovered || focused || pinned);

  const onKeyDown = (event: KeyboardEvent<HTMLButtonElement>) => {
    if (event.key === "Escape" && visible) {
      event.stopPropagation();
      setDismissed(true);
      setPinned(false);
    }
  };

  return (
    <span
      className={["reason-tip", className].filter(Boolean).join(" ")}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => {
        setHovered(false);
        setDismissed(false);
      }}
    >
      <button
        type="button"
        className="reason-tip-trigger"
        aria-describedby={id}
        onClick={() => {
          setDismissed(false);
          setPinned((p) => !p);
        }}
        onFocus={() => setFocused(true)}
        onBlur={() => {
          setFocused(false);
          setPinned(false);
          setDismissed(false);
        }}
        onKeyDown={onKeyDown}
      >
        <span aria-hidden="true">{children}</span>
        <span className="sr-only">{label}</span>
      </button>
      <span role="tooltip" id={id} className="reason-tip-bubble" hidden={!visible}>
        {reason}
      </span>
    </span>
  );
}
