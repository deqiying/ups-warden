import { useEffect, useId, useRef, useState } from "react";
import type { KeyboardEvent } from "react";
import { Icon } from "../icons/Icon.tsx";
import { Input } from "./Input.tsx";

export interface TokenInputProps {
  values: string[];
  /** 添加或移除后以新数组调用；组件不保存 values。 */
  onChange: (next: string[]) => void;
  /** 「添加」按钮文字。 */
  addLabel?: string;
  placeholder?: string;
  /** 最多项数；到达上限后再提交会提示「最多 N 项」。 */
  max?: number;
  mono?: boolean;
  /** 返回错误文案则就地显示且不提交；合法返回 null。入参已 trim。 */
  validate?: (value: string) => string | null;
  /** 去重比较用的归一化函数（如忽略大小写），默认按原值比较。 */
  normalizeKey?: (value: string) => string;
  /** 标注整组标签，必须提供。 */
  "aria-label": string;
  disabled?: boolean;
}

type SubmitResult = "added" | "empty" | "error";

const identity = (value: string) => value;

/** 标签/Chat ID 等字符串列表的编辑器：已有项可移除，点「添加」后键入新项。 */
export function TokenInput({
  values,
  onChange,
  addLabel = "添加",
  placeholder,
  max,
  mono,
  validate,
  normalizeKey = identity,
  "aria-label": ariaLabel,
  disabled,
}: TokenInputProps) {
  const [adding, setAdding] = useState(false);
  const [draft, setDraft] = useState("");
  const [error, setError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  // 主动关闭输入框（Esc / 提交后收起）时，卸载过程中可能再触发 blur，需要忽略，
  // 否则会用过期的草稿重复提交。
  const closing = useRef(false);
  const errorId = useId();

  useEffect(() => {
    if (adding) inputRef.current?.focus();
  }, [adding]);

  const close = () => {
    closing.current = true;
    setAdding(false);
    setDraft("");
    setError(null);
  };

  const submit = (): SubmitResult => {
    const value = draft.trim();
    if (!value) {
      setError(null);
      return "empty";
    }
    const key = normalizeKey(value);
    let message: string | null = null;
    if (values.some((existing) => normalizeKey(existing) === key)) {
      message = "已存在";
    } else if (max !== undefined && values.length >= max) {
      message = `最多 ${max} 项`;
    } else {
      message = validate?.(value) ?? null;
    }
    if (message) {
      setError(message);
      return "error";
    }
    onChange([...values, value]);
    setDraft("");
    setError(null);
    return "added";
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "Enter") {
      // 输入框常处于表单内，回车不应连带提交整个表单。
      event.preventDefault();
      const result = submit();
      // 已到上限时没有继续输入的意义，直接收起。
      if (result === "added" && max !== undefined && values.length + 1 >= max) close();
    } else if (event.key === "Escape") {
      event.preventDefault();
      close();
    }
  };

  const handleBlur = () => {
    if (closing.current) return;
    // 校验失败时保留输入框和错误提示，让用户修正；其余情况收起。
    if (submit() !== "error") close();
  };

  const open = () => {
    closing.current = false;
    setAdding(true);
  };

  return (
    <div className="token-input" role="group" aria-label={ariaLabel}>
      {values.map((value, index) => (
        <span key={value} className={mono ? "token mono" : "token"}>
          {value}
          <button
            type="button"
            className="x"
            aria-label={`移除 ${value}`}
            disabled={disabled}
            onClick={() => onChange(values.filter((_, i) => i !== index))}
          >
            <Icon name="x" />
          </button>
        </span>
      ))}
      {adding ? (
        <Input
          ref={inputRef}
          mono={mono}
          value={draft}
          placeholder={placeholder}
          aria-label={`${ariaLabel}：新增`}
          invalid={error !== null}
          aria-describedby={error ? errorId : undefined}
          disabled={disabled}
          onChange={(event) => {
            setDraft(event.target.value);
            setError(null);
          }}
          onKeyDown={handleKeyDown}
          onBlur={handleBlur}
        />
      ) : (
        <button type="button" className="token token-add" disabled={disabled} onClick={open}>
          {addLabel}
        </button>
      )}
      {error ? (
        <span id={errorId} className="t-caption ink-red token-input-error" role="alert">
          {error}
        </span>
      ) : null}
    </div>
  );
}
