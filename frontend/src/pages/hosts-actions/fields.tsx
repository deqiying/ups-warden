/*
 * 主机与动作表单的小部件：端口输入、只写凭据输入。
 * 凭据输入框永不预填，只显示“已设置 / 未设置”（来自 `*_set`）；留空 = 保留，清除需显式标记。
 */
import { Button, Input, Textarea } from "../../components/index.ts";
import { Icon } from "../../icons/Icon.tsx";

/** 端口 / 秒数等整数输入：空串或非数字交给校验给出提示（NaN），不提交字符串。 */
export function IntInput({
  id,
  value,
  onChange,
  invalid,
  className = "field-short",
}: {
  id: string;
  value: number;
  onChange: (next: number) => void;
  invalid?: boolean;
  className?: string;
}) {
  return (
    <Input
      id={id}
      mono
      inputMode="numeric"
      className={className}
      invalid={invalid}
      value={Number.isNaN(value) ? "" : String(value)}
      onChange={(event) => {
        const text = event.target.value.trim();
        onChange(/^\d+$/.test(text) ? Number(text) : Number.NaN);
      }}
    />
  );
}

export interface SecretInputProps {
  id: string;
  /** 用于清除按钮的无障碍名称。 */
  label: string;
  /** 服务端是否已保存（`*_set`）。 */
  isSet: boolean;
  /** 必需凭据不提供“清除”（后端拒绝清除）。 */
  required?: boolean;
  value: string;
  onChange: (value: string) => void;
  cleared?: boolean;
  onClearedChange?: (cleared: boolean) => void;
  kind?: "password" | "text" | "multiline";
  placeholder?: string;
  invalid?: boolean;
}

/** 只写凭据输入：状态 + 输入框 + 可选“清除”。清除标记与新值互斥（标记时输入框禁用并清空）。 */
export function SecretInput({
  id,
  label,
  isSet,
  required,
  value,
  onChange,
  cleared = false,
  onClearedChange,
  kind = "password",
  placeholder,
  invalid,
}: SecretInputProps) {
  const hint = placeholder ?? (isSet ? "留空保留当前值" : required ? "必填" : "未设置");
  const common = {
    id,
    value,
    disabled: cleared,
    invalid,
    placeholder: cleared ? "保存时将清除" : hint,
    autoComplete: "off",
    spellCheck: false,
    onChange: (event: { target: { value: string } }) => onChange(event.target.value),
  };
  return (
    <div className="ha-secret">
      <span className={isSet ? "secret-state is-set" : "secret-state"}>
        {isSet ? <Icon name="lock" /> : null}
        {isSet ? "已设置" : "未设置"}
        {cleared ? <span className="ink-orange"> · 保存时将清除</span> : null}
      </span>
      {kind === "multiline" ? (
        <Textarea {...common} mono rows={4} autoCapitalize="off" />
      ) : (
        <Input
          {...common}
          mono={kind === "text"}
          type={kind === "password" ? "password" : "text"}
          autoComplete={kind === "password" ? "new-password" : "off"}
        />
      )}
      {isSet && !required && onClearedChange ? (
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
    </div>
  );
}
