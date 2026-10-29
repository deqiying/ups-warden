import { useId } from "react";
import type { ReactNode, RefObject } from "react";
import { Icon, type IconName } from "../icons/Icon.tsx";
import { Overlay } from "./Overlay.tsx";

export type DialogTone = "danger" | "warning" | "neutral" | "info";

export interface DialogProps {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  message?: ReactNode;
  /** 省略则不显示图标块。 */
  icon?: IconName;
  /** 图标块配色，默认 "neutral"。 */
  tone?: DialogTone;
  /** 加宽（dlg-wide），用于带输入框的确认框。 */
  wide?: boolean;
  /** 位于 message 与操作按钮之间的额外内容。 */
  children?: ReactNode;
  /** 放入 .dialog-actions 的按钮；按钮会等分宽度。 */
  actions: ReactNode;
  initialFocusRef?: RefObject<HTMLElement | null>;
  /** 仅控制 Esc；点击遮罩永远不会关闭 alert 对话框。默认 true。 */
  dismissible?: boolean;
}

/** alert 对话框基础件：必须由用户显式选择，故遮罩点击不关闭。 */
export function Dialog({
  open,
  onClose,
  title,
  message,
  icon,
  tone = "neutral",
  wide,
  children,
  actions,
  initialFocusRef,
  dismissible = true,
}: DialogProps) {
  const titleId = useId();
  const messageId = useId();
  return (
    <Overlay
      open={open}
      onClose={onClose}
      dismissible={dismissible}
      initialFocusRef={initialFocusRef}
    >
      <div
        className={wide ? "dialog dlg-wide" : "dialog"}
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={message ? messageId : undefined}
      >
        {icon ? (
          <div className={`dialog-icon is-${tone}`}>
            <Icon name={icon} />
          </div>
        ) : null}
        <div className="dialog-title" id={titleId}>
          {title}
        </div>
        {message ? (
          <div className="dialog-msg" id={messageId}>
            {message}
          </div>
        ) : null}
        {children}
        <div className="dialog-actions">{actions}</div>
      </div>
    </Overlay>
  );
}
