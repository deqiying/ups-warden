import type { CSSProperties } from "react";
import { NavLink } from "react-router";
import { useTimeZone } from "../api/hooks.ts";
import { Icon } from "../icons/Icon.tsx";
import { useLive } from "../live/hooks.ts";
import { formatDateTime } from "../model/time.ts";
import { useSession } from "../session/hooks.ts";
import { ConnectionStatus } from "./ConnectionStatus.tsx";
import { NAV } from "./nav.ts";
import { activeAlertCount } from "./selectors.ts";
import { NavBadge } from "./NavBadge.tsx";

function initials(username: string): string {
  return username.slice(0, 2).toUpperCase();
}

/**
 * 侧栏：品牌、四组导航、实时连接状态与当前用户。
 * 效果图的全局搜索（⌘K）没有对应契约，P0 不提供；品牌副标题没有站点名称字段，改为运行中设备数。
 */
export function Sidebar() {
  const deviceCount = useLive((s) => (s.data.syncedAt === null ? null : s.data.devices.length));
  const alertCount = useLive(activeAlertCount);
  const session = useSession();
  const timeZone = useTimeZone();

  return (
    <aside className="sidebar">
      <div className="brand">
        <span className="app-icon">
          <Icon name="bolt" />
        </span>
        <div>
          <div className="brand-name">UPS Warden</div>
          <div className="brand-sub">
            {deviceCount === null ? "正在同步…" : `${deviceCount} 台设备运行中`}
          </div>
        </div>
      </div>
      <nav aria-label="主导航">
        {NAV.map((group) => (
          <div className="nav-group" key={group.group}>
            <div className="nav-label">{group.group}</div>
            {group.items.map((item) => (
              <NavLink key={item.id} to={item.to} end={item.to === "/"} className="nav-item">
                <span className="nav-ico" style={{ "--c": item.color } as CSSProperties}>
                  <Icon name={item.icon} />
                </span>
                {item.label}
                {item.id === "alerts" ? <NavBadge count={alertCount} /> : null}
              </NavLink>
            ))}
          </div>
        ))}
      </nav>
      <div className="side-foot">
        <ConnectionStatus />
        {session.user ? (
          <div className="user-row">
            <span className="avatar" aria-hidden="true">
              {initials(session.user.username)}
            </span>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div className="t-subhead strong">{session.user.username}</div>
              <div className="t-caption faint">
                管理员
                {session.expiresAt
                  ? ` · 会话至 ${formatDateTime(session.expiresAt, timeZone)}`
                  : ""}
              </div>
            </div>
          </div>
        ) : null}
      </div>
    </aside>
  );
}
