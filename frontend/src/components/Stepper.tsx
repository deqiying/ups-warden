import { useState } from "react";
import type { KeyboardEvent } from "react";

export interface StepperProps {
  value: number;
  /** 始终以 number 调用，绝不传字符串；仅在数值实际变化时触发。 */
  onChange: (value: number) => void;
  min?: number;
  max?: number;
  /** 步长，默认 1。整数步长时输入值会取整，小数步长则保留输入的小数。 */
  step?: number;
  unit?: string;
  /** 标注数值输入框，必须提供。 */
  "aria-label": string;
  disabled?: boolean;
}

/** 十进制小数位数（仅处理普通小数写法，足够覆盖步长）。 */
function decimalsOf(n: number): number {
  const text = String(n);
  const dot = text.indexOf(".");
  return dot < 0 ? 0 : text.length - dot - 1;
}

// 只接受朴素十进制，拒绝 "1e3"、"0x10"、"Infinity" 等 Number() 会放行的写法。
const DECIMAL = /^[+-]?(\d+\.?\d*|\.\d+)$/;

/** 带单位的数值步进器：可点按钮增减，也可直接键入。 */
export function Stepper({
  value,
  onChange,
  min,
  max,
  step = 1,
  unit,
  "aria-label": ariaLabel,
  disabled,
}: StepperProps) {
  const [draft, setDraft] = useState(String(value));
  const [syncedValue, setSyncedValue] = useState(value);
  // 外部 value 变化（含父组件夹取/回滚）时丢弃草稿；在渲染期同步可避免多一帧旧值闪烁。
  if (value !== syncedValue) {
    setSyncedValue(value);
    setDraft(String(value));
  }

  const clamp = (n: number) => {
    let result = n;
    if (min !== undefined && result < min) result = min;
    if (max !== undefined && result > max) result = max;
    return result;
  };

  const emit = (next: number) => {
    setDraft(String(next));
    if (next !== value) onChange(next);
  };

  const commit = () => {
    const text = draft.trim();
    if (!DECIMAL.test(text)) {
      setDraft(String(value));
      return;
    }
    const parsed = Number(text);
    emit(clamp(Number.isInteger(step) ? Math.round(parsed) : parsed));
  };

  const bump = (direction: 1 | -1) => {
    // 按步长精度修正浮点误差（0.1 + 0.2），否则会出现 0.30000000000000004。
    const next = Number(
      (value + direction * step).toFixed(Math.max(decimalsOf(step), decimalsOf(value))),
    );
    emit(clamp(next));
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "Enter") {
      event.preventDefault();
      commit();
    }
  };

  return (
    <div className="stepper">
      <input
        className="val"
        type="text"
        inputMode="decimal"
        aria-label={ariaLabel}
        value={draft}
        disabled={disabled}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={commit}
        onKeyDown={handleKeyDown}
      />
      {unit ? <span className="unit">{unit}</span> : null}
      <button
        type="button"
        aria-label="减少"
        disabled={disabled || (min !== undefined && value <= min)}
        onClick={() => bump(-1)}
      >
        −
      </button>
      <button
        type="button"
        aria-label="增加"
        disabled={disabled || (max !== undefined && value >= max)}
        onClick={() => bump(1)}
      >
        +
      </button>
    </div>
  );
}
