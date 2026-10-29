export interface SwitchProps {
  checked: boolean;
  /**
   * 点击时以 `!checked` 调用。组件本身不保存状态（受控）：
   * 需要二次确认的开关由调用方先弹确认框，确认后再更新 `checked`。
   */
  onChange: (next: boolean) => void;
  /** "indigo" 用于 dry-run 等「模拟」语义，默认 "green"。 */
  tone?: "green" | "indigo";
  size?: "sm";
  disabled?: boolean;
  id?: string;
  /** 开关没有可见文字，必须提供 aria-label 或 aria-labelledby 之一。 */
  "aria-label"?: string;
  "aria-labelledby"?: string;
  "aria-describedby"?: string;
}

/** 开关（role="switch"）。 */
export function Switch({
  checked,
  onChange,
  tone = "green",
  size,
  disabled,
  id,
  "aria-label": ariaLabel,
  "aria-labelledby": ariaLabelledBy,
  "aria-describedby": ariaDescribedBy,
}: SwitchProps) {
  const cls = ["switch", tone === "indigo" && "is-indigo", size && `switch-${size}`]
    .filter(Boolean)
    .join(" ");
  return (
    <button
      type="button"
      role="switch"
      id={id}
      className={cls}
      aria-checked={checked}
      aria-label={ariaLabel}
      aria-labelledby={ariaLabelledBy}
      aria-describedby={ariaDescribedBy}
      disabled={disabled}
      onClick={() => onChange(!checked)}
    />
  );
}
