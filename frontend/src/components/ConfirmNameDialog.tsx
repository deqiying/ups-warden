import { useId, useRef, useState } from "react";
import type { ReactNode } from "react";
import type { IconName } from "../icons/Icon.tsx";
import { Button } from "./Button.tsx";
import { Dialog, type DialogTone } from "./Dialog.tsx";
import { Input } from "./Input.tsx";

export interface ConfirmNameDialogProps {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  message?: ReactNode;
  /** 需要输入的名称：必须完全一致，区分大小写，不做 trim。 */
  name: string;
  /** 输入框标签，默认 `输入「${name}」以确认`。 */
  label?: string;
  placeholder?: string;
  confirmLabel: string;
  /** 默认“取消”。 */
  cancelLabel?: string;
  /** 默认 "power"。 */
  icon?: IconName;
  /** 默认 "danger"。 */
  tone?: DialogTone;
  /** 渲染在确认输入框之上（如 dry-run 分段控件、设备选择）。 */
  children?: ReactNode;
  /** 仅在名称一致且点击确认后调用，参数为输入值。resolve 视为成功并调用 onClose。 */
  onConfirm: (typedName: string) => Promise<void> | void;
  /** 外部附加条件（如必须先选设备）；为 true 时确认按钮禁用。 */
  confirmDisabled?: boolean;
}

function ConfirmNameDialogBody({
  onClose,
  title,
  message,
  name,
  label,
  placeholder,
  confirmLabel,
  cancelLabel = "取消",
  icon = "power",
  tone = "danger",
  children,
  onConfirm,
  confirmDisabled,
}: Omit<ConfirmNameDialogProps, "open">) {
  const inputId = useId();
  const inputRef = useRef<HTMLInputElement>(null);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<{ error: unknown } | null>(null);
  const inFlight = useRef(false);

  const canConfirm = typed === name && !confirmDisabled && !busy;

  const handleConfirm = async () => {
    if (!canConfirm || inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setFailure(null);
    try {
      await onConfirm(typed);
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

  return (
    <Dialog
      open
      onClose={onClose}
      title={title}
      message={message}
      icon={icon}
      tone={tone}
      wide
      dismissible={!busy}
      initialFocusRef={inputRef}
      actions={
        <>
          <Button onClick={onClose} disabled={busy}>
            {cancelLabel}
          </Button>
          <Button variant="danger" onClick={handleConfirm} disabled={!canConfirm} aria-busy={busy}>
            {busy ? "处理中…" : confirmLabel}
          </Button>
        </>
      }
    >
      {children}
      <div className="confirm-field">
        <label htmlFor={inputId}>{label ?? `输入「${name}」以确认`}</label>
        <Input
          id={inputId}
          ref={inputRef}
          value={typed}
          placeholder={placeholder}
          autoComplete="off"
          spellCheck={false}
          // 忙碌时只读而非禁用，避免焦点丢失
          readOnly={busy}
          onChange={(event) => {
            setTyped(event.target.value);
            setFailure(null);
          }}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              void handleConfirm();
            }
          }}
        />
      </div>
      {failure ? (
        <div className="dialog-error t-footnote ink-red" role="alert">
          {failure.error instanceof Error ? failure.error.message : "操作失败，请重试"}
        </div>
      ) : null}
    </Dialog>
  );
}

/**
 * 需要键入名称才能确认的高风险操作框（如手动执行关机计划）。
 * 内部状态只在打开期间存在，因此每次重新打开输入都是空的。
 */
export function ConfirmNameDialog({ open, ...rest }: ConfirmNameDialogProps) {
  if (!open) return null;
  return <ConfirmNameDialogBody {...rest} />;
}
