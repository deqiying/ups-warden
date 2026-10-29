import { useMemo, type ReactNode } from "react";
import type { LiveConnection } from "./connection.ts";
import { LiveContext } from "./hooks.ts";
import type { LiveStore } from "./store.ts";

export function LiveProvider({
  store,
  connection = null,
  children,
}: {
  store: LiveStore;
  connection?: LiveConnection | null;
  children: ReactNode;
}) {
  const value = useMemo(() => ({ store, connection }), [store, connection]);
  return <LiveContext.Provider value={value}>{children}</LiveContext.Provider>;
}
