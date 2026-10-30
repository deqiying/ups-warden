import { useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useTimeZone } from "../../api/hooks.ts";
import { isApiError } from "../../api/client.ts";
import type { User } from "../../api/types.ts";
import { deleteUser } from "../../api/users.ts";
import {
  Badge,
  Banner,
  Button,
  DestructiveDialog,
  ListCaption,
  Spinner,
  useToast,
} from "../../components/index.ts";
import { formatDateTime, formatFullDateTime } from "../../model/time.ts";
import { useSession } from "../../session/hooks.ts";
import { settingsKeys, useUsers } from "./hooks.ts";
import { userFailureText } from "./model.ts";
import { PaneHead } from "./shared.tsx";
import { UserSheet, type UserSheetMode } from "./UserSheet.tsx";

function initials(username: string): string {
  return username.slice(0, 2).toUpperCase();
}

/**
 * 管理员（F8-4）：列表、添加（由页面工具栏触发）、改名、删除、修改密码。
 * 删除用 DestructiveDialog，请求体带 `{confirm:true}`；后端拒绝时：
 * 422 不能删除自己，409 不能删除最后一位管理员（界面对自己的账户直接禁用删除，这里处理并发等边界）。
 */
export function AccountSection({
  addOpen,
  onAddClose,
}: {
  addOpen: boolean;
  onAddClose: () => void;
}) {
  const query = useUsers();
  const session = useSession();
  const timeZone = useTimeZone();
  const queryClient = useQueryClient();
  const toast = useToast();
  const [sheet, setSheet] = useState<UserSheetMode | null>(null);
  const [deleting, setDeleting] = useState<User | null>(null);

  // 后端按 id 降序返回；界面按创建先后展示。
  const users = useMemo(() => [...(query.data ?? [])].sort((a, b) => a.id - b.id), [query.data]);
  const selfId = session.user?.id;
  const sheetMode: UserSheetMode | null = addOpen ? { kind: "create" } : sheet;
  const closeSheet = () => (addOpen ? onAddClose() : setSheet(null));

  const confirmDelete = async () => {
    if (!deleting) return;
    try {
      await deleteUser(deleting.id);
    } catch (err) {
      // 409 交给 DestructiveDialog 显示 conflictMessage；其余（含 422）显示按操作区分的文案。
      if (isApiError(err) && err.status === 409) throw err;
      throw new Error(userFailureText("delete", err), { cause: err });
    }
    void queryClient.invalidateQueries({ queryKey: settingsKeys.users });
    void queryClient.invalidateQueries({ queryKey: settingsKeys.audit });
    toast.show({ tone: "ok", title: `已删除管理员「${deleting.username}」`, duration: 4000 });
  };

  let body;
  if (query.isPending) {
    body = (
      <div className="card">
        <div className="empty">
          <Spinner />
          <div className="t-subhead muted">正在加载管理员…</div>
        </div>
      </div>
    );
  } else if (query.isError && !query.data) {
    body = (
      <Banner
        tone="critical"
        role="alert"
        title="管理员列表加载失败"
        actions={
          <Button size="sm" onClick={() => void query.refetch()}>
            重试
          </Button>
        }
      >
        {query.error.message}
      </Banner>
    );
  } else {
    body = (
      <div className="list users" style={{ ["--row-inset" as string]: "62px" }}>
        {users.map((user) => {
          const self = user.id === selfId;
          return (
            <div className="row" key={user.id}>
              <span className="avatar" aria-hidden="true">
                {initials(user.username)}
              </span>
              <div className="row-main">
                <div className="row-title strong">
                  {user.username}{" "}
                  {self ? (
                    <Badge tone="blue">
                      你<span className="sr-only">（当前登录的账户）</span>
                    </Badge>
                  ) : null}
                </div>
                <div className="row-sub">
                  {user.last_login_at
                    ? `上次登录 ${formatDateTime(user.last_login_at, timeZone)}`
                    : "从未登录"}{" "}
                  · {formatFullDateTime(user.created_at, timeZone).slice(0, 10)} 创建
                </div>
              </div>
              <div className="hstack wrap gap-1 set-user-actions">
                <Button
                  size="sm"
                  aria-label={`修改 ${user.username} 的用户名`}
                  onClick={() => setSheet({ kind: "rename", user })}
                >
                  改名
                </Button>
                <Button
                  size="sm"
                  aria-label={`修改 ${user.username} 的密码`}
                  onClick={() => setSheet({ kind: "password", user, self })}
                >
                  修改密码…
                </Button>
                <Button
                  size="sm"
                  variant="danger-tinted"
                  aria-label={`删除 ${user.username}`}
                  disabled={self}
                  title={self ? "不能删除当前登录的账户" : undefined}
                  onClick={() => setDeleting(user)}
                >
                  删除
                </Button>
              </div>
            </div>
          );
        })}
      </div>
    );
  }

  return (
    <section aria-label="管理员">
      <PaneHead title="管理员" />
      {body}
      <ListCaption>
        所有账户都是管理员，拥有相同权限。不能删除自己，也不能删除最后一位管理员。用户名仅限字母、数字及
        . _ -，统一转为小写。
      </ListCaption>

      {sheetMode ? <UserSheet key={sheetMode.kind} mode={sheetMode} onClose={closeSheet} /> : null}

      <DestructiveDialog
        open={deleting !== null}
        onClose={() => setDeleting(null)}
        title={`删除管理员「${deleting?.username ?? ""}」？`}
        message="该管理员的所有登录会话会立即失效，此操作无法撤销。"
        confirmLabel="删除"
        conflictMessage="不能删除最后一位管理员"
        onConfirm={confirmDelete}
      />
    </section>
  );
}
