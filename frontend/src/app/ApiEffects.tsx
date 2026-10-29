import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { setApiHooks } from "../api/client.ts";
import { useToast } from "../components/toast-context.ts";

/**
 * 503 / configuration_saved_runtime_error 的全局提示：配置**已经保存**，只是运行时恢复失败。
 * 不能当作回滚处理——重新读取全部查询，表单以服务端最新值为准（monitoring-api.md）。
 */
export function ApiEffects() {
  const toast = useToast();
  const queryClient = useQueryClient();

  useEffect(() => {
    setApiHooks({
      onConfigSavedRuntimeError: (err) => {
        toast.show({
          tone: "warning",
          title: "配置已保存，但运行恢复失败",
          message:
            err.serverMessage || "新配置已写入，但相关服务未能按新配置恢复运行，请检查后重试。",
          duration: null,
          dedupeKey: "config-saved-runtime-error",
        });
        void queryClient.invalidateQueries();
      },
    });
    return () => setApiHooks({ onConfigSavedRuntimeError: () => {} });
  }, [toast, queryClient]);

  return null;
}
