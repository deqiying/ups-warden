import { useId } from "react";
import type { ReactNode } from "react";

export interface FormRowProps {
  label: ReactNode;
  /** 关联的控件 id；提供时标签渲染为 <label for>，点击标签可聚焦控件。 */
  htmlFor?: string;
  description?: ReactNode;
  /**
   * 说明文字（.row-sub）的 id，供控件的 aria-describedby 引用。
   * 控件在调用方手里，所以需要引用时由调用方传入固定 id；省略则自动生成（此时无法被外部引用）。
   */
  descriptionId?: string;
  className?: string;
  /** 右侧控件，放入 div.control。 */
  children?: ReactNode;
}

/** 「标签在左、控件在右」的设置行。 */
export function FormRow({
  label,
  htmlFor,
  description,
  descriptionId,
  className,
  children,
}: FormRowProps) {
  const generatedId = useId();
  const descId = descriptionId ?? generatedId;
  const cls = ["row", "form-row", className].filter(Boolean).join(" ");
  return (
    <div className={cls}>
      <div className="row-main">
        {htmlFor ? (
          <label className="row-title" htmlFor={htmlFor}>
            {label}
          </label>
        ) : (
          <div className="row-title">{label}</div>
        )}
        {description ? (
          <div className="row-sub" id={descId}>
            {description}
          </div>
        ) : null}
      </div>
      <div className="control">{children}</div>
    </div>
  );
}
