import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { isConfigSavedRuntimeError } from "../../api/client.ts";
import { queryKeys } from "../../api/hooks.ts";
import { updateSettings, type SettingsPatch } from "../../api/settings.ts";
import type { User } from "../../api/types.ts";
import { listUsers } from "../../api/users.ts";

/** 设置页自己的查询键；设置本身使用全局的 `queryKeys.settings`。 */
export const settingsKeys = {
  users: ["users"] as const,
  audit: ["audit"] as const,
};

/** 全部管理员；账户增删改后由账户分区主动失效。 */
export function useUsers() {
  return useQuery<User[]>({
    queryKey: settingsKeys.users,
    queryFn: ({ signal }) => listUsers(signal),
  });
}

/**
 * 保存系统设置（PATCH /settings）。成功后用响应更新缓存并使 `queryKeys.settings` 失效，
 * 全站时间显示（useTimeZone 读取该查询）随之变化；每次设置变更都会记入审计，因此同时使审计列表失效。
 * 503 configuration_saved_runtime_error 表示已保存、不能当作回滚：同样重新读取设置。
 */
export function useSaveSettings() {
  const queryClient = useQueryClient();
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: queryKeys.settings });
    void queryClient.invalidateQueries({ queryKey: settingsKeys.audit });
  };
  return useMutation({
    mutationFn: (patch: SettingsPatch) => updateSettings(patch),
    onSuccess: (saved) => {
      queryClient.setQueryData(queryKeys.settings, saved);
      refresh();
    },
    onError: (err) => {
      if (isConfigSavedRuntimeError(err)) refresh();
    },
  });
}
