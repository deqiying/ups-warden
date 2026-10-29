import type { IconName } from "../icons/icons.ts";

export interface NavItem {
  id: string;
  label: string;
  /** 窄屏标签栏的短名（效果图：告警、计划）。 */
  tabLabel?: string;
  to: string;
  icon: IconName;
  /** 导航图标底色；辅助色只用于导航与类型图标（foundations「颜色」）。 */
  color: string;
}

export interface NavGroup {
  group: string;
  items: NavItem[];
}

/** 侧栏导航：分组反映任务（监控 → 自动化 → 通知 → 系统），与 apple-ui.js 的 NAV 一致。 */
export const NAV: readonly NavGroup[] = [
  {
    group: "监控",
    items: [
      { id: "overview", label: "总览", to: "/", icon: "grid", color: "var(--accent)" },
      { id: "devices", label: "设备", to: "/devices", icon: "ups", color: "var(--green)" },
      {
        id: "alerts",
        label: "告警与事件",
        tabLabel: "告警",
        to: "/alerts",
        icon: "bell",
        color: "var(--red)",
      },
    ],
  },
  {
    group: "自动化",
    items: [
      { id: "rules", label: "规则", to: "/rules", icon: "branch", color: "var(--indigo)" },
      {
        id: "plans",
        label: "关机计划",
        tabLabel: "计划",
        to: "/plans",
        icon: "power",
        color: "var(--orange)",
      },
      {
        id: "hosts",
        label: "主机与动作",
        to: "/hosts-actions",
        icon: "desktop",
        color: "var(--teal)",
      },
    ],
  },
  {
    group: "通知",
    items: [
      {
        id: "notifications",
        label: "通知渠道",
        to: "/notifications",
        icon: "paperplane",
        color: "var(--purple)",
      },
    ],
  },
  {
    group: "系统",
    items: [
      { id: "profiles", label: "Profile", to: "/profiles", icon: "doc", color: "var(--brown)" },
      { id: "settings", label: "设置", to: "/settings", icon: "gear", color: "var(--gray)" },
    ],
  },
];

/** 窄屏底部标签栏五项。 */
export const TAB_IDS = ["overview", "alerts", "devices", "plans", "settings"] as const;

export const NAV_ITEMS: readonly NavItem[] = NAV.flatMap((g) => g.items);

export function navItem(id: string): NavItem | undefined {
  return NAV_ITEMS.find((item) => item.id === id);
}
