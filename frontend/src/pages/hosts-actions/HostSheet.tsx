import { useQueryClient } from "@tanstack/react-query";
import { useId, useState } from "react";
import { queryKeys } from "../../api/hooks.ts";
import {
  buildHostCreateBody,
  buildHostUpdateBody,
  checkHostOnline,
  createHost,
  deleteHost,
  draftFromHost,
  emptyHostDraft,
  hostKeyWillReset,
  testHostCommand,
  testHostConnection,
  updateHost,
  type Host,
  type HostAuthMethod,
  type HostCommandResult,
  type HostDraft,
  type HostKind,
} from "../../api/hosts.ts";
import {
  Banner,
  Button,
  DestructiveDialog,
  Dialog,
  Input,
  Segmented,
  Select,
  Sheet,
  Switch,
  useToast,
} from "../../components/index.ts";
import { GridField } from "../devices/forms.tsx";
import { IntInput, SecretInput } from "./fields.tsx";
import { FingerprintFlow } from "./FingerprintFlow.tsx";
import { failureText, HOST_KIND_LABELS, validateHostDraft } from "./model.ts";

const KIND_OPTIONS = (Object.keys(HOST_KIND_LABELS) as HostKind[]).map((value) => ({
  value,
  label: HOST_KIND_LABELS[value],
}));

export interface HostSheetProps {
  /** 已保存主机；undefined 表示新建。 */
  host?: Host;
  onClose: () => void;
  /** 新建成功后切换为编辑该主机（继续获取指纹）。 */
  onCreated: (host: Host) => void;
}

/**
 * 受控主机新建 / 编辑浮层（F5-1、F5-3）。
 * - 编辑只提交修改过的字段；凭据只写，切换认证方式会清除旧凭据；
 * - 修改地址或 SSH 端口会清除已确认的指纹（保存前提示）；
 * - 已保存主机提供指纹、连接测试、测试命令、在线检测与删除。
 */
export function HostSheet({ host, onClose, onCreated }: HostSheetProps) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const ids = useId();
  // base：表单比较的基线，保存成功后更新为服务端返回值；host：最新查询数据（指纹状态等）。
  const [base, setBase] = useState<Host | undefined>(host);
  const [draft, setDraft] = useState<HostDraft>(() =>
    host ? draftFromHost(host) : emptyHostDraft(),
  );
  const [showErrors, setShowErrors] = useState(false);
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);

  const set = <K extends keyof HostDraft>(key: K, value: HostDraft[K]) =>
    setDraft((d) => ({ ...d, [key]: value }));

  const errors = validateHostDraft(draft, base);
  const shown = (key: string) => (showErrors ? errors[key] : undefined);
  const body = base ? buildHostUpdateBody(base, draft) : null;
  const dirty = base ? Object.keys(body ?? {}).length > 0 : true;
  const authChanged = base !== undefined && base.conn.auth_method !== draft.authMethod;
  const resetsKey = host !== undefined && hostKeyWillReset(host, draft);

  const save = async () => {
    setShowErrors(true);
    if (Object.keys(errors).length > 0) return;
    setSaving(true);
    setFailure(null);
    try {
      if (base) {
        const saved = await updateHost(base.id, body ?? {});
        setBase(saved);
        setDraft(draftFromHost(saved));
        setShowErrors(false);
        await queryClient.invalidateQueries({ queryKey: queryKeys.hosts });
        toast.show({ tone: "ok", title: "主机已保存", duration: 3000 });
      } else {
        const created = await createHost(buildHostCreateBody(draft));
        // 先刷新列表再切换到编辑：否则新主机尚不在列表中，浮层会短暂消失。
        await queryClient.invalidateQueries({ queryKey: queryKeys.hosts });
        toast.show({
          tone: "ok",
          title: "主机已添加",
          message: "下一步：获取并核对主机指纹，确认后才能连接。",
        });
        onCreated(created);
      }
    } catch (err) {
      setFailure(failureText(err));
    } finally {
      setSaving(false);
    }
  };

  const latest = host ?? base;

  return (
    <>
      <Sheet
        open
        onClose={onClose}
        title={base ? `编辑「${base.name}」` : "添加主机"}
        width="min(640px, calc(100vw - 32px))"
        dismissible={!saving}
        trailing={
          <Button
            variant="primary"
            size="sm"
            disabled={saving || !dirty}
            aria-busy={saving}
            onClick={() => void save()}
          >
            {saving ? "正在保存…" : base ? "保存" : "添加"}
          </Button>
        }
      >
        {failure ? (
          <Banner tone="critical" role="alert" title="保存失败" className="ha-mb">
            {failure}
          </Banner>
        ) : null}
        {resetsKey ? (
          <Banner tone="warning" role="status" className="ha-mb">
            修改地址或 SSH 端口后，已确认的指纹会被清除，需要重新获取并确认。
          </Banner>
        ) : null}

        <div className="form-grid">
          <GridField label="名称" htmlFor={`${ids}-name`} error={shown("name")}>
            <Input
              id={`${ids}-name`}
              value={draft.name}
              maxLength={128}
              invalid={Boolean(shown("name"))}
              placeholder="例如：NAS-01"
              onChange={(e) => set("name", e.target.value)}
            />
          </GridField>
          <GridField label="类型" htmlFor={`${ids}-kind`}>
            <Select
              id={`${ids}-kind`}
              options={KIND_OPTIONS}
              value={draft.kind}
              onChange={(value) => set("kind", value as HostKind)}
            />
          </GridField>
          <GridField
            label="地址"
            htmlFor={`${ids}-addr`}
            error={shown("address")}
            hint="主机名或 IP；只支持 SSH"
          >
            <Input
              id={`${ids}-addr`}
              mono
              value={draft.address}
              invalid={Boolean(shown("address"))}
              placeholder="nas-01.lan"
              onChange={(e) => set("address", e.target.value)}
            />
          </GridField>
          <GridField label="SSH 端口" htmlFor={`${ids}-port`} error={shown("port")}>
            <IntInput
              id={`${ids}-port`}
              value={draft.port}
              invalid={Boolean(shown("port"))}
              onChange={(port) =>
                setDraft((d) => ({
                  ...d,
                  port,
                  // 在线检测端口默认跟随 SSH 端口，用户改过则保留。
                  onlinePort: d.onlinePort === d.port ? port : d.onlinePort,
                }))
              }
            />
          </GridField>
          <GridField label="用户名" htmlFor={`${ids}-user`} error={shown("username")}>
            <Input
              id={`${ids}-user`}
              mono
              value={draft.username}
              invalid={Boolean(shown("username"))}
              autoComplete="off"
              onChange={(e) => set("username", e.target.value)}
            />
          </GridField>
          <GridField
            label="认证方式"
            hint={authChanged ? "切换认证方式会清除已保存的全部凭据" : undefined}
          >
            <Segmented<HostAuthMethod>
              aria-label="认证方式"
              options={[
                { value: "password", label: "密码" },
                { value: "private_key", label: "私钥" },
              ]}
              value={draft.authMethod}
              onChange={(authMethod) => setDraft((d) => ({ ...d, authMethod, clearSecrets: [] }))}
            />
          </GridField>
          {draft.authMethod === "password" ? (
            <GridField label="密码" htmlFor={`${ids}-pw`} error={shown("password")}>
              <SecretInput
                id={`${ids}-pw`}
                label="密码"
                required
                isSet={!authChanged && (base?.password_set ?? false)}
                value={draft.password}
                invalid={Boolean(shown("password"))}
                onChange={(v) => set("password", v)}
              />
            </GridField>
          ) : (
            <>
              <GridField label="私钥" htmlFor={`${ids}-key`} error={shown("privateKey")}>
                <SecretInput
                  id={`${ids}-key`}
                  label="私钥"
                  kind="multiline"
                  required
                  isSet={!authChanged && (base?.private_key_set ?? false)}
                  value={draft.privateKey}
                  invalid={Boolean(shown("privateKey"))}
                  placeholder={
                    !authChanged && base?.private_key_set
                      ? "留空保留当前私钥"
                      : "-----BEGIN OPENSSH PRIVATE KEY-----"
                  }
                  onChange={(v) => set("privateKey", v)}
                />
              </GridField>
              <GridField label="私钥口令" htmlFor={`${ids}-pp`}>
                <SecretInput
                  id={`${ids}-pp`}
                  label="私钥口令"
                  isSet={!authChanged && (base?.passphrase_set ?? false)}
                  value={draft.passphrase}
                  cleared={draft.clearSecrets.includes("passphrase")}
                  onClearedChange={(on) => set("clearSecrets", on ? ["passphrase"] : [])}
                  onChange={(v) => set("passphrase", v)}
                />
              </GridField>
            </>
          )}
          <GridField
            label="sudo"
            hint="普通动作以 sudo -n 执行，需在主机上为该用户配置 NOPASSWD；不传输 sudo 密码"
          >
            <Switch
              aria-label="使用 sudo"
              checked={draft.sudo}
              onChange={(sudo) => set("sudo", sudo)}
            />
          </GridField>
          <GridField
            label="在线检测"
            hint={
              draft.onlineType === "icmp"
                ? "ICMP 仅支持 IPv4，可能需要额外权限；权限不足时改用 TCP"
                : "只探测这一个端口，3 秒超时；连接成功即在线"
            }
          >
            <div className="hstack wrap">
              <Segmented<"tcp" | "icmp">
                aria-label="在线检测方式"
                options={[
                  { value: "tcp", label: "TCP 端口" },
                  { value: "icmp", label: "ICMP" },
                ]}
                value={draft.onlineType}
                onChange={(onlineType) => set("onlineType", onlineType)}
              />
              {draft.onlineType === "tcp" ? (
                <IntInput
                  id={`${ids}-online`}
                  value={draft.onlinePort}
                  invalid={Boolean(shown("onlinePort"))}
                  onChange={(v) => set("onlinePort", v)}
                />
              ) : null}
            </div>
            {shown("onlinePort") ? (
              <div className="hint is-error" role="alert">
                {shown("onlinePort")}
              </div>
            ) : null}
          </GridField>
        </div>

        {latest && base ? (
          <>
            <div className="list-header mt-6">主机指纹</div>
            <div className="card card-pad">
              <FingerprintFlow host={latest} />
            </div>
            <div className="list-header mt-6">连接与检测</div>
            <HostTools host={latest} dirty={dirty} />
            <div className="hstack mt-6">
              <Button variant="danger-tinted" icon="trash" onClick={() => setDeleteOpen(true)}>
                删除主机
              </Button>
            </div>
          </>
        ) : (
          <p className="list-caption">
            添加后需要获取并人工核对主机指纹；指纹未确认时拒绝认证和执行任何命令。凭据加密保存，之后只显示「已设置」。
          </p>
        )}
      </Sheet>
      {base ? (
        <DestructiveDialog
          open={deleteOpen}
          onClose={() => setDeleteOpen(false)}
          title={`删除主机「${base.name}」？`}
          message="删除后无法恢复，已保存的凭据与指纹一并删除。"
          conflictMessage="主机被 SSH 动作引用，或已有关机计划运行记录，不能删除。请先修改或删除引用它的动作。"
          confirmLabel="删除主机"
          onConfirm={async () => {
            await deleteHost(base.id);
            await queryClient.invalidateQueries({ queryKey: queryKeys.hosts });
            toast.show({ tone: "ok", title: "主机已删除", duration: 3000 });
            onClose();
          }}
        />
      ) : null}
    </>
  );
}

type ToolState =
  | { kind: "idle" }
  | { kind: "busy" }
  | { kind: "ok"; text: string; detail?: string }
  | { kind: "fail"; text: string };

/** 连接测试、测试命令、在线检测（F5-3）。指纹未确认时连接与命令不可用。 */
function HostTools({ host, dirty }: { host: Host; dirty: boolean }) {
  const [conn, setConn] = useState<ToolState>({ kind: "idle" });
  const [cmd, setCmd] = useState<ToolState>({ kind: "idle" });
  const [online, setOnline] = useState<ToolState>({ kind: "idle" });
  const [cmdConfirm, setCmdConfirm] = useState(false);
  const confirmed = host.hostkey_fingerprint !== "";

  const run = async <T,>(
    setState: (s: ToolState) => void,
    call: () => Promise<T>,
    ok: (result: T) => ToolState,
  ) => {
    setState({ kind: "busy" });
    try {
      setState(ok(await call()));
    } catch (err) {
      setState({ kind: "fail", text: failureText(err) });
    }
  };

  const commandResult = (r: HostCommandResult): ToolState => ({
    kind: r.status === "success" ? "ok" : "fail",
    text: `${r.status === "success" ? "命令执行成功" : "命令执行失败"}${r.exit_code !== undefined ? ` · 退出码 ${r.exit_code}` : ""} · ${r.duration_ms} ms`,
    detail: [r.stdout, r.stderr].filter(Boolean).join("\n") || undefined,
  });

  return (
    <>
      {dirty ? (
        <p className="list-caption" style={{ marginTop: 0 }}>
          以下检测使用已保存的配置，请先保存修改。
        </p>
      ) : null}
      <div className="list ha-tools">
        <ToolRow
          title="连接测试"
          sub="校验指纹并完成 SSH 认证，不打开命令会话"
          state={conn}
          disabled={!confirmed}
          disabledHint="指纹未确认，无法连接"
          label="测试连接"
          onRun={() =>
            void run(
              setConn,
              () => testHostConnection(host.id),
              () => ({
                kind: "ok",
                text: "认证成功",
              }),
            )
          }
        />
        <ToolRow
          title="测试命令"
          sub="执行固定的 echo ups-warden-ok，不使用 sudo"
          state={cmd}
          disabled={!confirmed}
          disabledHint="指纹未确认，无法执行命令"
          label="执行测试命令…"
          onRun={() => setCmdConfirm(true)}
        />
        <ToolRow
          title="在线检测"
          sub={
            host.online_check.type === "tcp"
              ? `TCP ${host.online_check.port}，3 秒超时`
              : "ICMP echo（IPv4），3 秒超时"
          }
          state={online}
          label="立即检测"
          onRun={() =>
            void run(
              setOnline,
              () => checkHostOnline(host.id),
              (r) =>
                r.online
                  ? { kind: "ok", text: "在线" }
                  : { kind: "fail", text: "离线（未响应或拒绝连接）" },
            )
          }
        />
      </div>
      <Dialog
        open={cmdConfirm}
        onClose={() => setCmdConfirm(false)}
        title={`在「${host.name}」上执行测试命令？`}
        message="将通过 SSH 执行固定命令 echo ups-warden-ok，不使用 sudo，不会执行其他内容。"
        icon="terminal"
        tone="info"
        actions={
          <>
            <Button onClick={() => setCmdConfirm(false)}>取消</Button>
            <Button
              variant="primary"
              onClick={() => {
                setCmdConfirm(false);
                void run(setCmd, () => testHostCommand(host.id), commandResult);
              }}
            >
              执行
            </Button>
          </>
        }
      />
    </>
  );
}

function ToolRow({
  title,
  sub,
  state,
  label,
  onRun,
  disabled,
  disabledHint,
}: {
  title: string;
  sub: string;
  state: ToolState;
  label: string;
  onRun: () => void;
  disabled?: boolean;
  disabledHint?: string;
}) {
  return (
    <div className="row ha-tool">
      <div className="row-main">
        <div className="row-title">{title}</div>
        <div className="row-sub">{disabled && disabledHint ? disabledHint : sub}</div>
        {state.kind === "ok" || state.kind === "fail" ? (
          <div
            className={
              state.kind === "ok" ? "t-footnote ink-green mt-2" : "t-footnote ink-red mt-2"
            }
            role={state.kind === "fail" ? "alert" : "status"}
          >
            {state.text}
          </div>
        ) : null}
        {state.kind === "ok" && state.detail ? (
          <pre className="code-block mt-2">{state.detail}</pre>
        ) : null}
      </div>
      <Button
        size="sm"
        disabled={disabled || state.kind === "busy"}
        aria-busy={state.kind === "busy"}
        onClick={onRun}
      >
        {state.kind === "busy" ? "进行中…" : label}
      </Button>
    </div>
  );
}
