import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { Button } from "./Button.tsx";
import { Severity } from "./Severity.tsx";
import { ToastContext } from "./toast-context.ts";
import type { ToastApi, ToastOptions } from "./toast-context.ts";

export type { ToastAction, ToastApi, ToastOptions, ToastTone } from "./toast-context.ts";

const MAX_TOASTS = 3;
const DEFAULT_DURATION = 6000;

interface ToastItem extends ToastOptions {
  id: number;
  /** 被 dedupeKey 替换的次数，用于重置计时。 */
  rev: number;
}

interface ToastViewProps {
  item: ToastItem;
  onDismiss: (id: number) => void;
}

function ToastView({ item, onDismiss }: ToastViewProps) {
  const duration = item.duration === undefined ? DEFAULT_DURATION : item.duration;
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  const paused = hovered || focused;

  // 剩余时间：暂停时扣除已过去的部分，恢复时只等剩下的，而不是重新计满。
  const remaining = useRef(duration ?? 0);
  useEffect(() => {
    remaining.current = duration ?? 0;
  }, [item.rev, duration]);

  useEffect(() => {
    if (duration === null || paused) return;
    const startedAt = Date.now();
    const timer = setTimeout(() => onDismiss(item.id), Math.max(0, remaining.current));
    return () => {
      clearTimeout(timer);
      remaining.current -= Date.now() - startedAt;
    };
  }, [duration, paused, item.rev, item.id, onDismiss]);

  return (
    <div
      className="toast"
      role={item.tone === "critical" ? "alert" : "status"}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      onFocus={() => setFocused(true)}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setFocused(false);
      }}
    >
      <Severity level={item.tone} />
      <div className="toast-body">
        <div className="t-headline">{item.title}</div>
        {item.message ? <div className="t-footnote muted">{item.message}</div> : null}
        {item.actions && item.actions.length > 0 ? (
          <div className="hstack mt-2">
            {item.actions.map((action, index) => (
              <Button
                key={action.label}
                size="sm"
                variant={action.variant ?? (index === 0 ? "tinted" : "plain")}
                onClick={() => {
                  action.onClick();
                  onDismiss(item.id);
                }}
              >
                {action.label}
              </Button>
            ))}
          </div>
        ) : null}
      </div>
      <Button
        variant="plain"
        size="sm"
        iconOnly
        icon="x"
        aria-label="关闭"
        onClick={() => onDismiss(item.id)}
      />
    </div>
  );
}

/** 提供 `useToast()` 并在右上角渲染 toast 栈（最多 3 条，超出时移除最旧的）。 */
export function ToastProvider({ children }: { children?: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  // show 需要同步返回 id 并判断 dedupeKey，所以以 ref 为准，再同步到 state 触发渲染。
  const itemsRef = useRef<ToastItem[]>([]);
  const nextId = useRef(1);

  const commit = useCallback((next: ToastItem[]) => {
    itemsRef.current = next;
    setItems(next);
  }, []);

  const show = useCallback(
    (options: ToastOptions) => {
      const current = itemsRef.current;
      const existing =
        options.dedupeKey === undefined
          ? undefined
          : current.find((t) => t.dedupeKey === options.dedupeKey);
      if (existing) {
        commit(current.map((t) => (t === existing ? { ...options, id: t.id, rev: t.rev + 1 } : t)));
        return existing.id;
      }
      const id = nextId.current++;
      commit([...current, { ...options, id, rev: 0 }].slice(-MAX_TOASTS));
      return id;
    },
    [commit],
  );

  const dismiss = useCallback(
    (id: number) => commit(itemsRef.current.filter((t) => t.id !== id)),
    [commit],
  );

  const api = useMemo<ToastApi>(() => ({ show, dismiss }), [show, dismiss]);

  return (
    <ToastContext value={api}>
      {children}
      <div className="toast-stack">
        {items.map((item) => (
          <ToastView key={item.id} item={item} onDismiss={dismiss} />
        ))}
      </div>
    </ToastContext>
  );
}
