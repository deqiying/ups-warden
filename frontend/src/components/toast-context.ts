import { createContext, useContext } from "react";
import type { ReactNode } from "react";

export type ToastTone = "info" | "ok" | "warning" | "critical";

export interface ToastAction {
  label: string;
  /** 点击后先执行，再关闭该 toast。 */
  onClick: () => void;
  /** 省略时第一个操作为 "tinted"，其余为 "plain"。 */
  variant?: "tinted" | "plain";
}

export interface ToastOptions {
  tone: ToastTone;
  title: string;
  message?: ReactNode;
  actions?: ToastAction[];
  /** 毫秒，默认 6000；null 表示常驻直到手动关闭。 */
  duration?: number | null;
  /** 同 key 的 toast 已存在时替换其内容并重新计时，而不是新增。 */
  dedupeKey?: string;
}

export interface ToastApi {
  /** 返回 toast id（dedupeKey 命中时为已有 toast 的 id）。 */
  show: (options: ToastOptions) => number;
  dismiss: (id: number) => void;
}

export const ToastContext = createContext<ToastApi | null>(null);

/** 必须在 `<ToastProvider>` 内调用。 */
export function useToast(): ToastApi {
  const api = useContext(ToastContext);
  if (!api) {
    throw new Error("useToast 必须在 <ToastProvider> 内使用：请在应用根部包裹 ToastProvider。");
  }
  return api;
}
