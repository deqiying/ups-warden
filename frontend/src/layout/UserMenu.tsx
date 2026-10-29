import { useEffect, useId, useRef, useState } from "react";
import { useTimeZone } from "../api/hooks.ts";
import { useToast } from "../components/toast-context.ts";
import { Icon } from "../icons/Icon.tsx";
import { formatDateTime } from "../model/time.ts";
import { useSession, useSessionActions } from "../session/hooks.ts";

function initials(username: string): string {
  return username.slice(0, 2).toUpperCase();
}

/**
 * 侧栏当前用户与账户菜单（效果图 user-row + chev-updown）。菜单目前只有「退出登录」；
 * Esc 或点击菜单外关闭并回焦到触发按钮。登出失败时保留会话并提示（服务端会话仍有效）。
 */
export function UserMenu() {
  const session = useSession();
  const { logout } = useSessionActions();
  const timeZone = useTimeZone();
  const toast = useToast();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const itemRef = useRef<HTMLButtonElement>(null);
  const menuId = useId();

  useEffect(() => {
    if (!open) return;
    itemRef.current?.focus();
    const onPointer = (e: PointerEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      setOpen(false);
      triggerRef.current?.focus();
    };
    document.addEventListener("pointerdown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  if (!session.user) return null;

  async function signOut() {
    setBusy(true);
    try {
      await logout();
      // 成功后会话守卫转到登录页，本组件随外壳卸载。
    } catch (err) {
      setBusy(false);
      setOpen(false);
      toast.show({
        tone: "warning",
        title: "退出登录失败",
        message: err instanceof Error ? err.message : "请稍后重试",
        dedupeKey: "logout-failed",
      });
    }
  }

  return (
    <div className="user-menu" ref={rootRef}>
      {open ? (
        <div className="popover" role="menu" id={menuId} aria-label="账户">
          <button
            ref={itemRef}
            type="button"
            role="menuitem"
            className="menu-item"
            disabled={busy}
            onClick={() => void signOut()}
          >
            {busy ? "正在退出…" : "退出登录"}
          </button>
        </div>
      ) : null}
      <button
        ref={triggerRef}
        type="button"
        className="user-row"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        onClick={() => setOpen((v) => !v)}
      >
        <span className="avatar" aria-hidden="true">
          {initials(session.user.username)}
        </span>
        <span style={{ flex: 1, minWidth: 0 }}>
          <span className="t-subhead strong" style={{ display: "block" }}>
            {session.user.username}
          </span>
          <span className="t-caption faint" style={{ display: "block" }}>
            管理员
            {session.expiresAt ? ` · 会话至 ${formatDateTime(session.expiresAt, timeZone)}` : ""}
          </span>
        </span>
        <Icon name="chev-updown" className="faint" />
      </button>
    </div>
  );
}
