import { NavLink } from "react-router";
import { Icon } from "../icons/Icon.tsx";
import { useLive } from "../live/hooks.ts";
import { NavBadge } from "./NavBadge.tsx";
import { navItem, TAB_IDS, type NavItem } from "./nav.ts";
import { activeAlertCount } from "./selectors.ts";

const TABS: NavItem[] = TAB_IDS.map((id) => {
  const item = navItem(id);
  if (!item) throw new Error(`标签栏引用了不存在的导航项：${id}`);
  return item;
});

/** 窄屏（≤860px）底部标签栏五项；宽屏由 CSS 隐藏。 */
export function TabBar() {
  const alertCount = useLive(activeAlertCount);
  return (
    <nav className="tabbar" aria-label="标签栏">
      {TABS.map((item) => (
        <NavLink key={item.id} to={item.to} end={item.to === "/"} className="tab">
          <Icon name={item.icon} />
          {item.id === "alerts" ? <NavBadge count={alertCount} /> : null}
          {item.tabLabel ?? item.label}
        </NavLink>
      ))}
    </nav>
  );
}
