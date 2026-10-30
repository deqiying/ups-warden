import { Icon } from "../icons/Icon.tsx";
import { Select } from "./Select.tsx";

export interface ChipOption {
  value: number;
  label: string;
  /** 不能再被添加（如已停用的渠道）；已选中的仍显示，可移除。 */
  disabled?: boolean;
}

export interface ChipSelectProps {
  /** 已选 ID，按选择顺序。 */
  values: number[];
  onChange: (next: number[]) => void;
  options: ChipOption[];
  /** 最多可选项数；到达上限后隐藏添加入口。 */
  max?: number;
  /** 添加下拉的占位文案，默认“添加…”。 */
  addLabel?: string;
  /** 已选 ID 不在 options 中（已删除）时的显示文案。 */
  missingLabel?: (id: number) => string;
  /** 标注整组，必须提供。 */
  "aria-label": string;
  disabled?: boolean;
}

/**
 * 从已有资源中多选 ID（渠道、动作等）：已选项显示为可移除的标签，末尾的原生下拉只列出未选项。
 * 与 TokenInput 的区别是取值只能来自给定选项，不能自由输入。
 */
export function ChipSelect({
  values,
  onChange,
  options,
  max,
  addLabel = "添加…",
  missingLabel = (id) => `#${id}（已删除）`,
  "aria-label": ariaLabel,
  disabled,
}: ChipSelectProps) {
  const byId = new Map(options.map((o) => [o.value, o]));
  const remaining = options.filter((o) => !values.includes(o.value) && !o.disabled);
  const full = max !== undefined && values.length >= max;

  return (
    <div className="token-input" role="group" aria-label={ariaLabel}>
      {values.map((id) => {
        const label = byId.get(id)?.label ?? missingLabel(id);
        return (
          <span key={id} className="token">
            {label}
            <button
              type="button"
              className="x"
              aria-label={`移除 ${label}`}
              disabled={disabled}
              onClick={() => onChange(values.filter((v) => v !== id))}
            >
              <Icon name="x" />
            </button>
          </span>
        );
      })}
      {!full && remaining.length > 0 ? (
        <Select
          aria-label={`${ariaLabel}：添加`}
          disabled={disabled}
          options={[
            { value: "", label: addLabel },
            ...remaining.map((o) => ({ value: String(o.value), label: o.label })),
          ]}
          value=""
          onChange={(value) => {
            if (value) onChange([...values, Number(value)]);
          }}
        />
      ) : null}
      {values.length === 0 && remaining.length === 0 ? (
        <span className="t-footnote faint">没有可选项</span>
      ) : null}
    </div>
  );
}
