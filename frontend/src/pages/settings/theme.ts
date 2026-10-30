/*
 * 外观主题（设置 › 通用 › 外观）：本机偏好，只保存在 localStorage，不经后端、不影响其他管理员。
 * 与 styles/tokens.css 的约定一致：`<html data-theme="light|dark">` 强制浅色/深色，
 * 没有该属性时跟随系统（prefers-color-scheme）。这里只保存主题偏好，不保存任何会话或凭据。
 */

export type ThemePreference = "system" | "light" | "dark";

export const THEME_STORAGE_KEY = "ups-warden.theme";

export const THEME_OPTIONS: readonly { value: ThemePreference; label: string }[] = [
  { value: "system", label: "跟随系统" },
  { value: "light", label: "浅色" },
  { value: "dark", label: "深色" },
];

function isThemePreference(value: unknown): value is ThemePreference {
  return value === "system" || value === "light" || value === "dark";
}

/** 读取已保存的主题；存储不可用或值无效时视为“跟随系统”。 */
export function readTheme(): ThemePreference {
  try {
    const value = window.localStorage.getItem(THEME_STORAGE_KEY);
    return isThemePreference(value) ? value : "system";
  } catch {
    // 隐私模式或策略禁用存储：只是无法记住偏好，界面仍按系统主题工作。
    return "system";
  }
}

/** 应用到 `<html>`：system 移除 data-theme，让 CSS 的 prefers-color-scheme 生效。 */
export function applyTheme(preference: ThemePreference): void {
  const root = document.documentElement;
  if (preference === "system") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", preference);
}

/**
 * 保存并应用。存储写入失败时仍应用到当前页面，返回 false 让界面提示“仅本次有效”，
 * 而不是把失败显示为成功。
 */
export function saveTheme(preference: ThemePreference): boolean {
  applyTheme(preference);
  try {
    if (preference === "system") window.localStorage.removeItem(THEME_STORAGE_KEY);
    else window.localStorage.setItem(THEME_STORAGE_KEY, preference);
    return true;
  } catch {
    return false;
  }
}

/** 应用启动时恢复已保存的主题（由 SettingsPage 模块加载时调用一次）。 */
export function initTheme(): void {
  applyTheme(readTheme());
}
