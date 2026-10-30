import { QueryClientProvider } from "@tanstack/react-query";
import { useEffect, useMemo, type ReactNode } from "react";
import { BrowserRouter, Route, Routes } from "react-router";
import { ApiEffects } from "./app/ApiEffects.tsx";
import type { AppServices } from "./app/services.ts";
import { ToastProvider } from "./components/Toast.tsx";
import { IconSprite } from "./icons/Icon.tsx";
import { AppShell } from "./layout/AppShell.tsx";
import { LiveProvider } from "./live/LiveProvider.tsx";
import { AlertsPage } from "./pages/alerts/AlertsPage.tsx";
import { LoginPage } from "./pages/auth/LoginPage.tsx";
import { SetupPage } from "./pages/auth/SetupPage.tsx";
import { DeviceDetailPage } from "./pages/device/DeviceDetailPage.tsx";
import { DevicesPage } from "./pages/devices/DevicesPage.tsx";
import { HostsActionsPage } from "./pages/hosts-actions/HostsActionsPage.tsx";
import { NotFoundPage } from "./pages/NotFoundPage.tsx";
import { NotificationsPage } from "./pages/notifications/NotificationsPage.tsx";
import { OverviewPage } from "./pages/overview/OverviewPage.tsx";
import { PlansPage } from "./pages/plans/PlansPage.tsx";
import { ProfilesPage } from "./pages/profiles/ProfilesPage.tsx";
import { RulesPage } from "./pages/rules/RulesPage.tsx";
import { SettingsPage } from "./pages/settings/SettingsPage.tsx";
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
  const session = useMemo(
    () => ({ store: services.session, probe: services.probe, logout: services.logout }),
    [services],
  );

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
      <Route path="/login" element={<LoginPage />} />
      <Route path="/setup" element={<SetupPage />} />
      <Route element={<RequireAuth />}>
        <Route element={<AppShell />}>
          <Route index element={<OverviewPage />} />
          <Route path="devices" element={<DevicesPage />} />
          <Route path="devices/new" element={<DevicesPage />} />
          <Route path="devices/:id" element={<DeviceDetailPage />} />
          <Route path="alerts" element={<AlertsPage />} />
          <Route path="rules" element={<RulesPage />} />
          <Route path="rules/:id" element={<RulesPage />} />
          <Route path="plans" element={<PlansPage />} />
          <Route path="plans/:id" element={<PlansPage />} />
          <Route path="hosts-actions" element={<HostsActionsPage />} />
          <Route path="notifications" element={<NotificationsPage />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="settings/:section" element={<SettingsPage />} />
          <Route path="profiles" element={<ProfilesPage />} />
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
