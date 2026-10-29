import { Navigate, Outlet, useLocation } from "react-router";
import { SessionChecking, SessionError } from "./BootState.tsx";
import { useSession } from "./hooks.ts";

/** 登录页从 location.state 读取返回路径、原因与预填用户名。 */
export interface LoginLocationState {
  from?: string;
  reason?: "expired" | "logout" | null;
  /** 刚完成初始化时预填的用户名（密码从不预填）。 */
  username?: string;
  /** 来自初始化页：显示第 2 步与“账户已创建”提示。 */
  setupDone?: boolean;
  /** 初始化页收到 setup_closed：已由他人完成初始化。 */
  setupClosed?: boolean;
}

/**
 * 会话守卫：未登录访问任意页面时跳转登录并保留返回路径（frontend-plan 3.2）；
 * 尚未初始化时转到初始化页。探测失败（网络等）显示可重试的错误态。
 */
export function RequireAuth() {
  const session = useSession();
  const location = useLocation();

  if (session.status === "unknown") return <SessionChecking />;
  if (session.status === "error") return <SessionError />;
  if (session.status === "anonymous") {
    if (session.setupRequired) return <Navigate to="/setup" replace />;
    // 主动登出后重新登录回到首页；会话过期则回到原页面继续操作。
    const state: LoginLocationState = {
      from:
        session.signedOutReason === "logout"
          ? undefined
          : location.pathname + location.search + location.hash,
      reason: session.signedOutReason,
    };
    return <Navigate to="/login" replace state={state} />;
  }
  return <Outlet />;
}
