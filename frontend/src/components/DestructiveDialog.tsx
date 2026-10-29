import { useRef, useState } from "react";
import type { ReactNode } from "react";
import { isApiError } from "../api/client.ts";
import { Button } from "./Button.tsx";
import { Dialog } from "./Dialog.tsx";

export interface DestructiveDialogProps {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  message?: ReactNode;
  /** 删除会连带清除的内容，渲染为“将一并清除：”列表。 */
  consequences?: string[];
  /** 默认“删除”。 */
  confirmLabel?: string;
  /** 默认“取消”。 */
  cancelLabel?: string;
  /**
   * 用户点击确认后才调用。resolve 视为成功并调用 onClose；reject 则保持打开并显示错误。
   * 请求体里的 `{"confirm":true}` 由调用方构造。
   */
  onConfirm: () => Promise<void> | void;
  /**
   * 提供后，409（仍被引用）时显示“改为停用”按钮。
   * 组件只调用该回调，不会自动关闭；调用方决定是否关闭或接着发起停用请求。
   */
  onDisableInstead?: () => void;
  /** 默认“改为停用”。 */
  disableLabel?: string;
}

const CONFLICT_MESSAGE = "仍有其他配置引用它，无法删除。可以改为停用。";

function errorText(error: unknown): string {
  if (isApiError(error) && error.status === 409) return CONFLICT_MESSAGE;
  return error instanceof Error ? error.message : "操作失败，请重试";
}

function DestructiveDialogBody({
  onClose,
  title,
  message,
  consequences,
  confirmLabel = "删除",
  cancelLabel = "取消",
  onConfirm,
  onDisableInstead,
  disableLabel = "改为停用",
}: Omit<DestructiveDialogProps, "open">) {
  const cancelRef = useRef<HTMLButtonElement>(null);
  const [busy, setBusy] = useState(false);
  // 包一层对象以区分“没有错误”和“抛出了 undefined”。
  const [failure, setFailure] = useState<{ error: unknown } | null>(null);
  // state 在下一次渲染才生效，快速连点需要同步的重入保护。
  const inFlight = useRef(false);

  const handleConfirm = async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setFailure(null);
    try {
      await onConfirm();
    } catch (error) {
      setFailure({ error });
      setBusy(false);
      inFlight.current = false;
      return;
    }
    inFlight.current = false;
    setBusy(false);
    onClose();
  };

  const conflict = failure !== null && isApiError(failure.error) && failure.error.status === 409;

  return (
    <Dialog
      open
      onClose={onClose}
      title={title}
      message={message}
      icon="trash"
      tone="danger"
      dismissible={!busy}
      initialFocusRef={cancelRef}
      actions={
        <>
          <Button ref={cancelRef} onClick={onClose} disabled={busy}>
            {cancelLabel}
          </Button>
          {conflict && onDisableInstead ? (
            <Button variant="tinted" onClick={onDisableInstead} disabled={busy}>
              {disableLabel}
            </Button>
          ) : null}
          <Button variant="danger" onClick={handleConfirm} disabled={busy} aria-busy={busy}>
            {busy ? "处理中…" : confirmLabel}
          </Button>
        </>
      }
    >
      {consequences && consequences.length > 0 ? (
        <div className="dialog-consequences">
          <div className="t-footnote">将一并清除：</div>
          <ul>
            {consequences.map((item) => (
              <li key={item}>{item}</li>
            ))}
          </ul>
        </div>
      ) : null}
      {failure ? (
        <div className="dialog-error t-footnote ink-red" role="alert">
          {errorText(failure.error)}
        </div>
      ) : null}
    </Dialog>
  );
}

/**
 * 删除确认框。仅在打开期间挂载内部状态，因此每次重新打开都会清除忙碌与错误状态。
 * 处理中禁用按钮与 Esc，失败留在框内展示，避免用户误以为已删除。
 */
export function DestructiveDialog({ open, ...rest }: DestructiveDialogProps) {
  if (!open) return null;
  return <DestructiveDialogBody {...rest} />;
}
