import { Navigate, useLocation } from "react-router";
import { Empty } from "../../components/Empty.tsx";
import { Spinner } from "../../components/Spinner.tsx";
import { useSession } from "../../session/hooks.ts";
import type { LoginLocationState } from "../../session/RequireAuth.tsx";

/**
 * F0 占位：初始化与登录页面在 F1 实现。已登录时直接回到返回路径，避免停在占位页。
 */
export function AuthPlaceholderPage({ mode }: { mode: "login" | "setup" }) {
  const session = useSession();
  const location = useLocation();
  const state = (location.state ?? {}) as LoginLocationState;

  if (session.status === "authenticated") {
    return <Navigate to={state.from && state.from !== "/login" ? state.from : "/"} replace />;
  }
  if (session.status === "unknown") {
    return (
      <div className="boot-state">
        <Spinner label="正在检查登录状态" />
      </div>
    );
  }
  return (
    <div className="boot-state">
      <Empty icon="lock" tone="info" title={mode === "login" ? "登录" : "首次初始化"}>
        {state.reason === "expired" ? "会话已过期或已在其他地方退出，请重新登录。" : null}
        {mode === "login" ? "登录页面" : "初始化页面"}将在 F1 交付。
      </Empty>
    </div>
  );
}
