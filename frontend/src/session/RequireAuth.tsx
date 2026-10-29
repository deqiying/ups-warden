import { Navigate, Outlet, useLocation } from "react-router";
import { Button } from "../components/Button.tsx";
import { Empty } from "../components/Empty.tsx";
import { Spinner } from "../components/Spinner.tsx";
import { useSession, useSessionActions } from "./hooks.ts";

/** 登录页从 location.state 读取返回路径与原因。 */
export interface LoginLocationState {
  from?: string;
  reason?: "expired" | "logout" | null;
}

/**
 * 会话守卫：未登录访问任意页面时跳转登录并保留返回路径（frontend-plan 3.2）。
 * 探测失败（网络等）不当作未登录，显示可重试的错误态，避免把断网误判为会话过期。
 */
export function RequireAuth() {
  const session = useSession();
  const { probe } = useSessionActions();
  const location = useLocation();

  if (session.status === "unknown") {
    return (
      <div className="boot-state">
        <Spinner label="正在检查登录状态" />
      </div>
    );
  }
  if (session.status === "error") {
    return (
      <div className="boot-state">
        <Empty
          icon="wifi-off"
          tone="warning"
          title="无法连接到 UPS Warden"
          action={
            <Button variant="tinted" onClick={() => void probe()}>
              重试
            </Button>
          }
        >
          {session.error}
        </Empty>
      </div>
    );
  }
  if (session.status === "anonymous") {
    const state: LoginLocationState = {
      from: location.pathname + location.search + location.hash,
      reason: session.signedOutReason,
    };
    return <Navigate to="/login" replace state={state} />;
  }
  return <Outlet />;
}
