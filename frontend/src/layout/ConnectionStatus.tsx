import { useLive } from "../live/hooks.ts";
import type { LiveSnapshot } from "../live/store.ts";

type StatusKind = "ok" | "syncing" | "offline" | "idle";

function statusOf(s: LiveSnapshot): StatusKind {
  const { phase, disconnectedAt } = s.connection;
  if (phase === "offline") return "offline";
  if (phase === "idle") return "idle";
  // 重连中或已连接但未收到 sync：断线期间不是连续观测，仍按断开显示。
  if (disconnectedAt !== null) return "offline";
  return phase === "open" && s.data.synced ? "ok" : "syncing";
}

const TEXT: Record<StatusKind, string> = {
  ok: "实时连接正常 · 已同步",
  syncing: "正在建立实时连接…",
  offline: "实时连接已断开，正在重连…",
  idle: "实时连接未启动",
};

/** 侧栏底部的实时连接状态；状态同时用颜色与文字表达。 */
export function ConnectionStatus() {
  const kind = useLive(statusOf);
  const dotClass =
    kind === "ok"
      ? "live-dot"
      : kind === "offline"
        ? "live-dot is-warn breathe"
        : "live-dot is-warn";
  return (
    <div className="live-status" role="status">
      <span className={dotClass} aria-hidden="true" />
      {TEXT[kind]}
    </div>
  );
}
