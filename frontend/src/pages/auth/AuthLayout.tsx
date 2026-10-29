import { useEffect, type ReactNode } from "react";
import { Icon } from "../../icons/Icon.tsx";

/** 初始化与登录共用的整屏布局（login.html）：应用图标居中，内容最宽 420px。 */
export function AuthLayout({ title, children }: { title: string; children: ReactNode }) {
  useEffect(() => {
    document.title = `${title} · UPS Warden`;
  }, [title]);

  return (
    <div className="auth-page">
      <main className="auth">
        <div className="auth-icon" aria-hidden="true">
          <Icon name="bolt" />
        </div>
        {children}
      </main>
    </div>
  );
}

/** 两步引导的进度点：第 1 步创建管理员，第 2 步登录。 */
export function StepDots({ step }: { step: 1 | 2 }) {
  return (
    <div className="step-dots" aria-hidden="true">
      <i className={step === 1 ? "on" : undefined} />
      <i className={step === 2 ? "on" : undefined} />
    </div>
  );
}

/**
 * 组合输入框的提交按钮（效果图 .go）。提交中显示 spinner 并禁用，避免重复提交。
 */
export function GoButton({
  label,
  disabled,
  busy,
}: {
  label: string;
  disabled: boolean;
  busy: boolean;
}) {
  return (
    <button
      type="submit"
      className="field-go"
      aria-label={label}
      disabled={disabled || busy}
      aria-busy={busy || undefined}
    >
      {busy ? (
        <span className="spinner-status" aria-hidden="true">
          <svg className="spinner" viewBox="0 0 16 16" focusable="false">
            <circle
              cx="8"
              cy="8"
              r="6"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeDasharray="26 12"
              strokeLinecap="round"
            />
          </svg>
        </span>
      ) : (
        <Icon name="arrow-right" />
      )}
    </button>
  );
}
