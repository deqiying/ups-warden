export interface SpinnerProps {
  /** 读屏文字，默认「加载中」。 */
  label?: string;
}

/** 加载指示；图形取自效果图 shutdown-plans.html 的 spinner 标记。 */
export function Spinner({ label = "加载中" }: SpinnerProps) {
  return (
    <span className="spinner-status" role="status">
      <svg className="spinner" viewBox="0 0 16 16" aria-hidden="true" focusable="false">
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
      <span className="sr-only">{label}</span>
    </span>
  );
}
