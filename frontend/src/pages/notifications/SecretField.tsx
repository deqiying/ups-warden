import { useId, type ReactNode } from "react";
import { Button, FormRow, Input, Textarea } from "../../components/index.ts";
import { Icon } from "../../icons/Icon.tsx";

export interface SecretFieldProps {
  label: string;
  description?: ReactNode;
  /** 来自 `*_set`：服务端是否已保存该凭据。 */
  isSet: boolean;
  /** 必需凭据：不提供“清除”（后端拒绝清除必需的 URL / Token）。 */
  required?: boolean;
  /** 新值；永不预填，空串表示保留原值。 */
  value: string;
  onChange: (value: string) => void;
  /** 是否标记为“保存时清除”；与新值互斥。 */
  cleared: boolean;
  onClearedChange: (cleared: boolean) => void;
  /** 输入形式：密码框 / 单行文本 / 多行（Headers）。 */
  kind?: "password" | "text" | "multiline";
  placeholder?: string;
  error?: string;
}

/**
 * 只写凭据行：显示“已设置 / 未设置”，输入框永不预填。
 * 留空 = 保留；输入新值 = 更换；点“清除”= 保存时显式 `clear_secrets`（此时输入框禁用且清空，不会与新值同时提交）。
 */
export function SecretField({
  label,
  description,
  isSet,
  required,
  value,
  onChange,
  cleared,
  onClearedChange,
  kind = "password",
  placeholder,
  error,
}: SecretFieldProps) {
  const id = useId();
  const errorId = `${id}-error`;
  const hint = placeholder ?? (isSet ? "留空保留当前值" : required ? "必填" : "未设置");
  const common = {
    id,
    value,
    disabled: cleared,
    placeholder: cleared ? "保存时将清除" : hint,
    invalid: Boolean(error),
    "aria-describedby": error ? errorId : undefined,
    autoComplete: "off",
    spellCheck: false,
    onChange: (event: { target: { value: string } }) => onChange(event.target.value),
  };

  return (
    <FormRow label={label} htmlFor={id} description={description} className="nt-secret">
      <span className={isSet ? "secret-set is-set" : "secret-set"}>
        {isSet ? <Icon name="lock" /> : null}
        {isSet ? "已设置" : "未设置"}
        {cleared ? <span className="ink-orange">· 保存时将清除</span> : null}
      </span>
      {kind === "multiline" ? (
        <Textarea
          {...common}
          mono
          rows={3}
          className="nt-secret-input"
          // 凭据输入框不允许浏览器记忆。
          autoCapitalize="off"
        />
      ) : (
        <Input
          {...common}
          mono={kind === "text"}
          type={kind === "password" ? "password" : "text"}
          className="nt-secret-input"
          autoComplete={kind === "password" ? "new-password" : "off"}
        />
      )}
      {isSet && !required ? (
        <Button
          variant="plain"
          size="sm"
          aria-label={cleared ? `撤销清除${label}` : `清除${label}`}
          onClick={() => {
            if (!cleared) onChange("");
            onClearedChange(!cleared);
          }}
        >
          {cleared ? "撤销清除" : "清除"}
        </Button>
      ) : null}
      {error ? (
        <div id={errorId} className="t-caption ink-red nt-field-error" role="alert">
          {error}
        </div>
      ) : null}
    </FormRow>
  );
}
