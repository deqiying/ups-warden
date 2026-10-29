import type { CSSProperties, Ref, SelectHTMLAttributes } from "react";
import { Icon } from "../icons/Icon.tsx";

export interface SelectOption {
  value: string;
  label: string;
  disabled?: boolean;
}

export interface SelectProps extends Omit<
  SelectHTMLAttributes<HTMLSelectElement>,
  "value" | "onChange" | "className" | "style" | "size" | "multiple" | "children"
> {
  options: SelectOption[];
  value: string;
  /** 以选中项的 value（字符串）调用。 */
  onChange: (value: string) => void;
  /** 无底色的弹出按钮（.is-plain），用于工具栏内。 */
  plain?: boolean;
  /** 加在外层 span.select-wrap 上，而非 <select>。 */
  className?: string;
  /** 同 className，作用于外层（如设置 min-width）。 */
  style?: CSSProperties;
  /** 指向内部的原生 <select>。 */
  ref?: Ref<HTMLSelectElement>;
}

/**
 * 下拉选择。使用原生 <select> 以保留键盘、读屏与移动端选择器行为，
 * 外观通过隐藏原生箭头并叠放装饰性 chevron 贴近 macOS 弹出按钮。
 */
export function Select({
  options,
  value,
  onChange,
  plain,
  className,
  style,
  ref,
  ...rest
}: SelectProps) {
  const wrapCls = ["select-wrap", className].filter(Boolean).join(" ");
  return (
    <span className={wrapCls} style={style}>
      <select
        {...rest}
        ref={ref}
        className={plain ? "select is-plain" : "select"}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      >
        {options.map((option) => (
          <option key={option.value} value={option.value} disabled={option.disabled}>
            {option.label}
          </option>
        ))}
      </select>
      <span className="chev" aria-hidden="true">
        <Icon name="chev-updown" />
      </span>
    </span>
  );
}
