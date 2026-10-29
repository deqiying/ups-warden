import type { InputHTMLAttributes, Ref, TextareaHTMLAttributes } from "react";

export interface InputProps extends Omit<InputHTMLAttributes<HTMLInputElement>, "size"> {
  /** 大号输入框（input-lg），用于登录等主操作场景。 */
  size?: "lg";
  /** 等宽字体，用于 ID、地址、密钥片段等。 */
  mono?: boolean;
  /** 校验失败：加 is-invalid 并设置 aria-invalid。错误文案由调用方另行渲染并用 aria-describedby 关联。 */
  invalid?: boolean;
  ref?: Ref<HTMLInputElement>;
}

/** 单行文本输入框。 */
export function Input({
  size,
  mono,
  invalid,
  className,
  "aria-invalid": ariaInvalid,
  ...rest
}: InputProps) {
  const cls = [
    "input",
    size && `input-${size}`,
    mono && "input-mono",
    invalid && "is-invalid",
    className,
  ]
    .filter(Boolean)
    .join(" ");
  return <input {...rest} className={cls} aria-invalid={invalid ? true : ariaInvalid} />;
}

export interface TextareaProps extends TextareaHTMLAttributes<HTMLTextAreaElement> {
  mono?: boolean;
  /** 同 {@link InputProps.invalid}。 */
  invalid?: boolean;
  ref?: Ref<HTMLTextAreaElement>;
}

/** 多行文本输入框。 */
export function Textarea({
  mono,
  invalid,
  className,
  "aria-invalid": ariaInvalid,
  ...rest
}: TextareaProps) {
  const cls = ["textarea", mono && "input-mono", invalid && "is-invalid", className]
    .filter(Boolean)
    .join(" ");
  return <textarea {...rest} className={cls} aria-invalid={invalid ? true : ariaInvalid} />;
}
