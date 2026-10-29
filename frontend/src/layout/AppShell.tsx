import { useEffect } from "react";
import { Outlet } from "react-router";
import { useLiveContextConnection } from "../live/hooks.ts";
import { LiveEffects } from "./LiveEffects.tsx";
import { Sidebar } from "./Sidebar.tsx";
import { TabBar } from "./TabBar.tsx";

/**
 * 登录后的应用外壳：侧栏 + 主内容 + 窄屏标签栏。
 * 外壳挂载期间保持唯一的实时连接；离开（登出、会话失效）时停止。
 */
export function AppShell() {
  const connection = useLiveContextConnection();

  useEffect(() => {
    connection?.start();
    return () => connection?.stop();
  }, [connection]);

  return (
    <>
      <a className="skip-link" href="#main">
        跳到主要内容
      </a>
      <div className="app">
        <Sidebar />
        <main className="main" id="main" tabIndex={-1}>
          <Outlet />
        </main>
      </div>
      <TabBar />
      <LiveEffects />
    </>
  );
}
