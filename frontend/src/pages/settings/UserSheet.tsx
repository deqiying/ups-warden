import { useQueryClient } from "@tanstack/react-query";
import { useId, useState, type FormEvent } from "react";
import type { User } from "../../api/types.ts";
import { changePassword, createUser, renameUser } from "../../api/users.ts";
import { Banner, Button, Input, Sheet, useToast } from "../../components/index.ts";
import { Icon } from "../../icons/Icon.tsx";
import {
  checkPassword,
  isValidUsername,
  normalizeUsername,
  PASSWORD_MAX_BYTES,
  PASSWORD_MIN_CHARS,
} from "../auth/credentials.ts";
import { GridField } from "../devices/forms.tsx";
import { settingsKeys } from "./hooks.ts";
import { userFailureText } from "./model.ts";

export type UserSheetMode =
  | { kind: "create" }
  | { kind: "rename"; user: User }
  | { kind: "password"; user: User; self: boolean };

function Rule({ state, children }: { state: "ok" | "bad" | "idle"; children: string }) {
  return (
    <li className={state === "idle" ? undefined : state}>
      <Icon name={state === "ok" ? "check-circle" : state === "bad" ? "x-circle" : "info"} />
      {children}
      {/* 状态不只靠颜色与图标：读屏补充“已满足 / 未满足” */}
      <span className="sr-only">{state === "ok" ? "（已满足）" : "（未满足）"}</span>
    </li>
  );
}

const TITLES = {
  create: () => "添加管理员",
  rename: (mode: Extract<UserSheetMode, { kind: "rename" }>) =>
    `修改「${mode.user.username}」的用户名`,
  password: (mode: Extract<UserSheetMode, { kind: "password" }>) =>
    `修改「${mode.user.username}」的密码`,
};

/**
 * 管理员的添加 / 改名 / 修改密码（F8-4）。用户名与密码规则复用初始化页的校验
 * （pages/auth/credentials.ts）；最终以后端 422 为准。密码只存在于本组件状态，
 * 提交后随组件卸载丢弃，不写入存储、URL 或控制台，输入框也从不预填。
 */
export function UserSheet({ mode, onClose }: { mode: UserSheetMode; onClose: () => void }) {
  const ids = useId();
  const queryClient = useQueryClient();
  const toast = useToast();
  const [username, setUsername] = useState(mode.kind === "rename" ? mode.user.username : "");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const needsName = mode.kind !== "password";
  const needsPassword = mode.kind !== "rename";
  const checks = checkPassword(password, confirm);
  const nameOk =
    isValidUsername(username) && (mode.kind !== "rename" || username !== mode.user.username);
  const passwordOk = checks.minLength && checks.maxBytes && checks.matches;
  const canSubmit = (!needsName || nameOk) && (!needsPassword || passwordOk) && !busy;

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!canSubmit) return;
    setBusy(true);
    setFailure(null);
    try {
      let title: string;
      if (mode.kind === "create") {
        const created = await createUser(username, password);
        title = `已添加管理员「${created.username}」`;
      } else if (mode.kind === "rename") {
        const renamed = await renameUser(mode.user.id, username);
        title = `已改名为「${renamed.username}」`;
      } else {
        await changePassword(mode.user.id, password);
        title = "密码已修改";
      }
      void queryClient.invalidateQueries({ queryKey: settingsKeys.users });
      void queryClient.invalidateQueries({ queryKey: settingsKeys.audit });
      toast.show({ tone: "ok", title, duration: 4000 });
      onClose();
    } catch (err) {
      setFailure(userFailureText(mode.kind, err));
      setBusy(false);
    }
  }

  const formId = `${ids}-form`;
  const title =
    mode.kind === "create"
      ? TITLES.create()
      : mode.kind === "rename"
        ? TITLES.rename(mode)
        : TITLES.password(mode);
  const submitLabel = mode.kind === "create" ? "添加" : "保存";

  return (
    <Sheet
      open
      onClose={onClose}
      title={title}
      width="min(520px, calc(100vw - 32px))"
      dismissible={!busy}
      trailing={
        <Button
          variant="primary"
          size="sm"
          type="submit"
          form={formId}
          disabled={!canSubmit}
          aria-busy={busy}
        >
          {busy ? "正在保存…" : submitLabel}
        </Button>
      }
    >
      {failure ? (
        <Banner tone="critical" role="alert" title="保存失败" className="set-mb">
          {failure}
        </Banner>
      ) : null}
      {mode.kind === "password" ? (
        <Banner tone="info" className="set-mb">
          {mode.self
            ? "修改自己的密码会撤销你在其他设备上的登录会话；当前会话保持有效。"
            : `「${mode.user.username}」所有已登录的会话会立即失效，需要用新密码重新登录。`}
        </Banner>
      ) : null}
      <form id={formId} onSubmit={(event) => void submit(event)} noValidate>
        <div className="form-grid">
          {needsName ? (
            <GridField
              label="用户名"
              htmlFor={`${ids}-username`}
              hint="统一转为小写，仅限字母、数字、点、下划线与连字符，最多 64 个字符"
            >
              <Input
                id={`${ids}-username`}
                autoFocus
                autoComplete="off"
                autoCapitalize="none"
                spellCheck={false}
                maxLength={64}
                invalid={username !== "" && !isValidUsername(username)}
                value={username}
                // 与后端一致统一转小写，用户看到的就是将要保存的用户名。
                onChange={(e) => setUsername(normalizeUsername(e.target.value))}
              />
            </GridField>
          ) : null}
          {needsPassword ? (
            <>
              <GridField label="新密码" htmlFor={`${ids}-password`}>
                <Input
                  id={`${ids}-password`}
                  type="password"
                  autoFocus={!needsName}
                  autoComplete="new-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
              </GridField>
              <GridField label="确认密码" htmlFor={`${ids}-confirm`}>
                <Input
                  id={`${ids}-confirm`}
                  type="password"
                  autoComplete="new-password"
                  invalid={confirm !== "" && !checks.matches}
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                />
              </GridField>
            </>
          ) : null}
        </div>
        {needsPassword ? (
          <ul className="pw-rules">
            <Rule state={checks.minLength ? "ok" : password === "" ? "idle" : "bad"}>
              {`密码至少 ${PASSWORD_MIN_CHARS} 个字符`}
            </Rule>
            {checks.maxBytes ? null : (
              <Rule state="bad">{`密码不能超过 ${PASSWORD_MAX_BYTES} 字节`}</Rule>
            )}
            <Rule state={checks.matches ? "ok" : confirm !== "" ? "bad" : "idle"}>
              两次输入一致
            </Rule>
          </ul>
        ) : null}
      </form>
    </Sheet>
  );
}
