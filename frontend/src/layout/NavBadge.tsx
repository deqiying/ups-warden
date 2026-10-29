/** 导航角标：数字仅作视觉提示，读屏通过补充文字获得完整含义；0 时不显示。 */
export function NavBadge({ count }: { count: number }) {
  if (count <= 0) return null;
  return (
    <span className="nav-badge">
      <span aria-hidden="true">{count > 99 ? "99+" : count}</span>
      <span className="sr-only">，{count} 条活动告警</span>
    </span>
  );
}
