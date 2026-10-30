import { useMemo, useState } from "react";
import { useDevices, useSourcesByDevice, useTimeZone } from "../../api/hooks.ts";
import { buildGuardPatch } from "../../api/settings.ts";
import type { Settings } from "../../api/types.ts";
import {
  Banner,
  Button,
  Dialog,
  FormRow,
  List,
  ListCaption,
  ListHeader,
  Row,
  Switch,
  useToast,
} from "../../components/index.ts";
import { formatDateTime } from "../../model/time.ts";
import { useSession, useSessionActions } from "../../session/hooks.ts";
import { useSaveSettings } from "./hooks.ts";
import { settingsFailureText } from "./model.ts";
import { PaneHead } from "./shared.tsx";

/** 全部数据源的主机（去重、排序），含已停用设备的数据源——护栏对它们同样生效。 */
function useSourceHosts(): string[] {
  const devices = useDevices();
  const sources = useSourcesByDevice((devices.data ?? []).map((d) => d.id));
  return useMemo(() => {
    const hosts = new Set<string>();
    for (const list of Object.values(sources.data ?? {})) {
      for (const source of list) if (source.host) hosts.add(source.host);
    }
    return [...hosts].sort();
  }, [sources.data]);
}

/**
 * 安全（F8-3）：
 * - HTTP 护栏：关闭前必须确认，确认后才提交 `confirm_disable_guard:true`；开启无需确认；
 * - 登录会话：到期时间取自会话存储（空闲与绝对期限中较早者）；退出登录复用会话的 logout。
 */
export function SecuritySection({ settings }: { settings: Settings }) {
  const save = useSaveSettings();
  const toast = useToast();
  const session = useSession();
  const { logout } = useSessionActions();
  const timeZone = useTimeZone();
  const hosts = useSourceHosts();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [signingOut, setSigningOut] = useState(false);

  const guardOn = save.isPending
    ? (save.variables?.http_action_guard_ups_hosts ?? settings.http_action_guard_ups_hosts)
    : settings.http_action_guard_ups_hosts;

  const setGuard = (enabled: boolean) => {
    setFailure(null);
    save.mutate(buildGuardPatch(enabled), {
      onSuccess: () =>
        toast.show({
          tone: enabled ? "ok" : "warning",
          title: enabled ? "已开启 HTTP 动作护栏" : "已关闭 HTTP 动作护栏",
          message: enabled ? undefined : "HTTP 动作现在可以访问 UPS 数据源，该变更已记入审计日志。",
          duration: enabled ? 3000 : 6000,
        }),
      onError: (err) => setFailure(settingsFailureText(err)),
    });
  };

  const onToggle = (next: boolean) => {
    // 关闭护栏先确认；开启无需确认。
    if (next) setGuard(true);
    else setConfirmOpen(true);
  };

  const signOut = async () => {
    setSigningOut(true);
    try {
      await logout();
      // 成功后会话守卫转到登录页。
    } catch (err) {
      setSigningOut(false);
      toast.show({
        tone: "warning",
        title: "退出登录失败",
        message: err instanceof Error ? err.message : "请稍后重试",
        dedupeKey: "logout-failed",
      });
    }
  };

  return (
    <section aria-label="安全">
      <PaneHead title="安全" />
      {failure ? (
        <Banner tone="critical" role="alert" title="保存失败" className="set-mb">
          {failure}
        </Banner>
      ) : null}

      <ListHeader>HTTP 动作</ListHeader>
      <List>
        <FormRow
          label={<strong>禁止访问 UPS 数据源</strong>}
          description="HTTP 动作不能指向下列任何数据源的主机，保存和每次执行时都会检查，避免把请求误发到 UPS 网卡。"
        >
          <Switch
            checked={guardOn}
            disabled={save.isPending}
            aria-label="禁止 HTTP 动作访问 UPS 数据源"
            onChange={onToggle}
          />
        </FormRow>
        {hosts.length > 0 ? (
          <div className="guard-hosts">
            {hosts.map((host) => (
              <span className="token mono" key={host}>
                {host}
              </span>
            ))}
          </div>
        ) : null}
      </List>
      <ListCaption>包含已停用设备的数据源。此列表随数据源配置自动更新。</ListCaption>
      {guardOn ? null : (
        <Banner tone="warning" role="status" className="set-mt">
          护栏已关闭：HTTP 动作可以向 UPS 数据源发送请求。
        </Banner>
      )}

      <div className="list-header mt-6">登录会话</div>
      <List>
        <Row
          title="当前会话"
          sub={`${session.user?.username ?? "—"} · 这台设备上的浏览器`}
          value={
            session.expiresAt ? `${formatDateTime(session.expiresAt, timeZone)} 到期` : undefined
          }
        />
        <Row title="自动退出" sub="空闲 24 小时，或登录满 7 天，以先到者为准" />
        <div className="row">
          <div className="row-main">
            <Button
              variant="danger-tinted"
              size="sm"
              disabled={signingOut}
              aria-busy={signingOut}
              onClick={() => void signOut()}
            >
              {signingOut ? "正在退出…" : "退出登录"}
            </Button>
          </div>
        </div>
      </List>

      <Dialog
        open={confirmOpen}
        onClose={() => setConfirmOpen(false)}
        icon="shield"
        tone="danger"
        title="允许 HTTP 动作访问 UPS？"
        message="关闭后，HTTP 动作可以向 UPS 网卡发送请求。一次配置错误就可能让 UPS 关机或改动设备设置。只有确实需要通过 HTTP 控制 UPS 时才关闭。"
        actions={
          <>
            <Button onClick={() => setConfirmOpen(false)}>保持开启</Button>
            <Button
              variant="danger"
              onClick={() => {
                setConfirmOpen(false);
                setGuard(false);
              }}
            >
              关闭护栏
            </Button>
          </>
        }
      />
    </section>
  );
}
