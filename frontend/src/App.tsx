import { QueryClientProvider } from "@tanstack/react-query";
import { useEffect, useMemo, type ReactNode } from "react";
import { BrowserRouter, Route, Routes } from "react-router";
import { ApiEffects } from "./app/ApiEffects.tsx";
import type { AppServices } from "./app/services.ts";
import { ToastProvider } from "./components/Toast.tsx";
import { IconSprite } from "./icons/Icon.tsx";
import { AppShell } from "./layout/AppShell.tsx";
import { LiveProvider } from "./live/LiveProvider.tsx";
import { AuthPlaceholderPage } from "./pages/auth/AuthPlaceholderPage.tsx";
import { NotFoundPage } from "./pages/NotFoundPage.tsx";
import { PlaceholderPage } from "./pages/PlaceholderPage.tsx";
import { SessionContext } from "./session/hooks.ts";
import { RequireAuth } from "./session/RequireAuth.tsx";

/** 全局 Provider：查询缓存、会话、实时存储、toast；启动时探测一次会话。 */
export function AppProviders({
  services,
  children,
}: {
  services: AppServices;
  children: ReactNode;
}) {
  const session = useMemo(() => ({ store: services.session, probe: services.probe }), [services]);

  useEffect(() => {
    if (services.session.getSnapshot().status === "unknown") void services.probe();
  }, [services]);

  return (
    <QueryClientProvider client={services.queryClient}>
      <SessionContext.Provider value={session}>
        <LiveProvider store={services.live} connection={services.connection}>
          <ToastProvider>
            <IconSprite />
            <ApiEffects />
            {children}
          </ToastProvider>
        </LiveProvider>
      </SessionContext.Provider>
    </QueryClientProvider>
  );
}

/**
 * 路由表（frontend-plan 3.2）。业务页面按里程碑逐步替换占位页；
 * 页内子视图（tab、筛选）写入 query string，不新增路由。
 */
export function AppRoutes() {
  return (
    <Routes>
      <Route path="/login" element={<AuthPlaceholderPage mode="login" />} />
      <Route path="/setup" element={<AuthPlaceholderPage mode="setup" />} />
      <Route element={<RequireAuth />}>
        <Route element={<AppShell />}>
          <Route index element={<PlaceholderPage title="总览" milestone="F2" />} />
          <Route path="devices" element={<PlaceholderPage title="设备" milestone="F3" />} />
          <Route path="devices/new" element={<PlaceholderPage title="添加设备" milestone="F3" />} />
          <Route path="devices/:id" element={<PlaceholderPage title="设备详情" milestone="F2" />} />
          <Route path="alerts" element={<PlaceholderPage title="告警与事件" milestone="F2" />} />
          <Route path="rules" element={<PlaceholderPage title="规则" milestone="F7" />} />
          <Route path="rules/:id" element={<PlaceholderPage title="规则" milestone="F7" />} />
          <Route path="plans" element={<PlaceholderPage title="关机计划" milestone="F6" />} />
          <Route path="plans/:id" element={<PlaceholderPage title="关机计划" milestone="F6" />} />
          <Route
            path="hosts-actions"
            element={<PlaceholderPage title="主机与动作" milestone="F5" />}
          />
          <Route
            path="notifications"
            element={<PlaceholderPage title="通知渠道" milestone="F4" />}
          />
          <Route path="settings" element={<PlaceholderPage title="设置" milestone="F8" />} />
          <Route
            path="settings/:section"
            element={<PlaceholderPage title="设置" milestone="F8" />}
          />
          <Route path="profiles" element={<PlaceholderPage title="Profile" milestone="F3" />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Route>
    </Routes>
  );
}

export default function App({ services }: { services: AppServices }) {
  return (
    <AppProviders services={services}>
      <BrowserRouter>
        <AppRoutes />
      </BrowserRouter>
    </AppProviders>
  );
}
