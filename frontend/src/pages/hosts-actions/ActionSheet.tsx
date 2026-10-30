import { useQueryClient } from "@tanstack/react-query";
import { useId, useState } from "react";
import { Link } from "react-router";
import {
  buildActionCreateBody,
  buildActionUpdateBody,
  createAction,
  deleteAction,
  draftFromAction,
  emptyActionDraft,
  HTTP_METHODS,
  updateAction,
  type Action,
  type ActionDraft,
  type ActionSecretKey,
  type ActionType,
  type HttpMethod,
} from "../../api/actions.ts";
import { isApiError } from "../../api/client.ts";
import {
  queryKeys,
  useChannels,
  useHosts,
  usePlans,
  useScripts,
  useSettings,
} from "../../api/hooks.ts";
import {
  Banner,
  Button,
  ChipSelect,
  DestructiveDialog,
  Input,
  Select,
  Sheet,
  Switch,
  Textarea,
  TokenInput,
  useToast,
} from "../../components/index.ts";
import { GridField } from "../devices/forms.tsx";
import { IntInput, SecretInput } from "./fields.tsx";
import {
  ACTION_TYPE_LABELS,
  failureText,
  ACTION_TYPE_ORDER,
  validateActionDraft,
  validateStatusToken,
} from "./model.ts";

export interface ActionSheetProps {
  /** 已保存动作；undefined 表示新建。 */
  action?: Action;
  onClose: () => void;
}

/**
 * 动作新建 / 编辑浮层（F5-4）。
 * - 编辑只提交修改过的字段；提供 config 即整体替换；切换类型会清空旧类型配置与凭据；
 * - HTTP 的地址、Headers、正文模板只写不读；保存可能被 HTTP 护栏拒绝（422，不返回具体原因）。
 */
export function ActionSheet({ action, onClose }: ActionSheetProps) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const ids = useId();
  const [base, setBase] = useState<Action | undefined>(action);
  const [draft, setDraft] = useState<ActionDraft>(() =>
    action ? draftFromAction(action) : emptyActionDraft(),
  );
  const [showErrors, setShowErrors] = useState(false);
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState<{ text: string; guardHint: boolean } | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);

  const hosts = useHosts().data ?? [];
  const channels = useChannels().data ?? [];
  const plans = usePlans().data ?? [];
  const scripts = useScripts(draft.type === "script");
  const guardOn = useSettings().data?.http_action_guard_ups_hosts;

  const set = <K extends keyof ActionDraft>(key: K, value: ActionDraft[K]) =>
    setDraft((d) => ({ ...d, [key]: value }));
  const setCleared = (key: ActionSecretKey, on: boolean) =>
    setDraft((d) => ({
      ...d,
      clearSecrets: on
        ? [...new Set([...d.clearSecrets, key])]
        : d.clearSecrets.filter((k) => k !== key),
    }));

  const errors = validateActionDraft(draft, base);
  const shown = (key: string) => (showErrors ? errors[key] : undefined);
  let body: Record<string, unknown> | null = null;
  if (base) {
    try {
      body = buildActionUpdateBody(base, draft);
    } catch {
      body = null; // Header 文本无效：由校验给出具体提示
    }
  }
  const dirty = base ? body === null || Object.keys(body).length > 0 : true;
  const typeChanged = base !== undefined && base.type !== draft.type;
  const httpSet = (key: "url_set" | "headers_set" | "body_template_set") =>
    !typeChanged && base?.type === "http" && base[key];

  const save = async () => {
    setShowErrors(true);
    if (Object.keys(errors).length > 0) return;
    setSaving(true);
    setFailure(null);
    try {
      if (base) {
        const saved = await updateAction(base.id, body ?? {});
        setBase(saved);
        setDraft(draftFromAction(saved));
        setShowErrors(false);
        await queryClient.invalidateQueries({ queryKey: queryKeys.actions });
        toast.show({ tone: "ok", title: "动作已保存", duration: 3000 });
      } else {
        await createAction(buildActionCreateBody(draft));
        await queryClient.invalidateQueries({ queryKey: queryKeys.actions });
        toast.show({ tone: "ok", title: "动作已创建", duration: 3000 });
        onClose();
      }
    } catch (err) {
      setFailure({
        text: failureText(err),
        // 护栏拒绝与其他校验失败同为 422 validation_failed，后端不返回具体原因。
        guardHint:
          draft.type === "http" &&
          isApiError(err) &&
          err.status === 422 &&
          err.code === "validation_failed",
      });
    } finally {
      setSaving(false);
    }
  };

  const typeOptions = ACTION_TYPE_ORDER.map((value) => ({
    value,
    label: ACTION_TYPE_LABELS[value],
  }));

  const scriptOptions = [
    { value: "", label: scripts.isPending ? "正在读取脚本目录…" : "选择脚本…" },
    ...(scripts.data ?? []).map((s) => ({ value: s, label: s })),
  ];
  // 已保存的脚本不在当前目录中（被管理员移除）时仍显示，避免静默改值。
  if (draft.script && !(scripts.data ?? []).includes(draft.script)) {
    scriptOptions.push({ value: draft.script, label: `${draft.script}（目录中不存在）` });
  }

  return (
    <>
      <Sheet
        open
        onClose={onClose}
        title={base ? `编辑「${base.name}」` : "新建动作"}
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
            {saving ? "正在保存…" : base ? "保存" : "创建"}
          </Button>
        }
      >
        {failure ? (
          <Banner tone="critical" role="alert" title="保存失败" className="ha-mb">
            {failure.text}
            {failure.guardHint && guardOn !== false
              ? "。若地址指向已配置的 UPS 数据源主机，会被 HTTP 护栏拒绝。"
              : ""}
          </Banner>
        ) : null}
        {typeChanged ? (
          <Banner tone="warning" role="status" className="ha-mb">
            切换类型会清除原「{ACTION_TYPE_LABELS[base.type]}」的配置
            {base.type === "http" ? "与全部凭据" : ""}。
          </Banner>
        ) : null}

        <div className="form-grid">
          <GridField label="名称" htmlFor={`${ids}-name`} error={shown("name")}>
            <Input
              id={`${ids}-name`}
              value={draft.name}
              maxLength={128}
              invalid={Boolean(shown("name"))}
              placeholder="例如：关闭 NAS-01"
              onChange={(e) => set("name", e.target.value)}
            />
          </GridField>
          <GridField label="类型" htmlFor={`${ids}-type`}>
            <Select
              id={`${ids}-type`}
              options={typeOptions}
              value={draft.type}
              onChange={(value) =>
                setDraft((d) => ({ ...d, type: value as ActionType, clearSecrets: [] }))
              }
            />
          </GridField>
          <GridField
            label="超时"
            htmlFor={`${ids}-timeout`}
            error={shown("timeoutS")}
            hint="1～3600 秒；在关机计划中由阶段的单步超时覆盖"
          >
            <span className="hstack">
              <IntInput
                id={`${ids}-timeout`}
                value={draft.timeoutS}
                invalid={Boolean(shown("timeoutS"))}
                onChange={(v) => set("timeoutS", v)}
              />
              <span className="t-subhead muted">秒</span>
            </span>
          </GridField>

          {draft.type === "notify" ? (
            <GridField
              label="通知渠道"
              error={shown("channelIds")}
              hint="只能选择已启用的渠道；入队后的发送结果见通知发送记录"
            >
              <ChipSelect
                aria-label="通知渠道"
                values={draft.channelIds}
                onChange={(v) => set("channelIds", v)}
                max={64}
                options={channels.map((c) => ({
                  value: c.id,
                  label: c.enabled ? c.name : `${c.name}（已停用）`,
                  disabled: !c.enabled,
                }))}
                missingLabel={(id) => `渠道 #${id}（不存在）`}
              />
            </GridField>
          ) : null}

          {draft.type === "http" ? (
            <>
              <GridField label="方法" htmlFor={`${ids}-method`}>
                <Select
                  id={`${ids}-method`}
                  options={HTTP_METHODS.map((m) => ({ value: m, label: m }))}
                  value={draft.method}
                  onChange={(v) => set("method", v as HttpMethod)}
                />
              </GridField>
              <GridField
                label="请求地址"
                htmlFor={`${ids}-url`}
                error={shown("url")}
                hint="只写不读；不支持模板变量，不跟随重定向，不使用环境代理"
              >
                <SecretInput
                  id={`${ids}-url`}
                  label="请求地址"
                  kind="text"
                  required
                  isSet={httpSet("url_set")}
                  value={draft.url}
                  invalid={Boolean(shown("url"))}
                  placeholder={httpSet("url_set") ? "留空保留当前地址" : "https://example.com/hook"}
                  onChange={(v) => set("url", v)}
                />
              </GridField>
              <GridField label="期望状态码" error={shown("expectedStatus")} hint="留空表示任意 2xx">
                <TokenInput
                  aria-label="期望状态码"
                  mono
                  addLabel="添加"
                  placeholder="204"
                  values={draft.expectedStatus.map(String)}
                  validate={validateStatusToken}
                  onChange={(v) => set("expectedStatus", v.map(Number))}
                />
              </GridField>
              <GridField label="Headers" htmlFor={`${ids}-headers`} error={shown("headers")}>
                <SecretInput
                  id={`${ids}-headers`}
                  label="Headers"
                  kind="multiline"
                  isSet={httpSet("headers_set")}
                  value={draft.headersText}
                  invalid={Boolean(shown("headers"))}
                  placeholder={
                    httpSet("headers_set")
                      ? "留空保留当前设置；每行一个「名称: 值」"
                      : "X-API-Key: ..."
                  }
                  cleared={draft.clearSecrets.includes("headers")}
                  onClearedChange={(on) => setCleared("headers", on)}
                  onChange={(v) => set("headersText", v)}
                />
              </GridField>
              <GridField
                label="正文模板"
                htmlFor={`${ids}-body`}
                hint="可选；使用通知同款变量，按 JSON 字符串转义。只写不读"
              >
                <SecretInput
                  id={`${ids}-body`}
                  label="正文模板"
                  kind="multiline"
                  isSet={httpSet("body_template_set")}
                  value={draft.bodyTemplate}
                  placeholder={
                    httpSet("body_template_set")
                      ? "留空保留当前模板"
                      : '{"device":"{{device.name}}","event":"{{event.type}}"}'
                  }
                  cleared={draft.clearSecrets.includes("body_template")}
                  onClearedChange={(on) => setCleared("body_template", on)}
                  onChange={(v) => set("bodyTemplate", v)}
                />
              </GridField>
            </>
          ) : null}

          {draft.type === "script" ? (
            <>
              <GridField
                label="脚本"
                htmlFor={`${ids}-script`}
                error={shown("script") ?? (scripts.isError ? "脚本目录读取失败" : undefined)}
                hint={
                  scripts.data && scripts.data.length === 0
                    ? "脚本目录为空：请由管理员在服务端 --script-dir 中放入脚本"
                    : "只能从服务端脚本目录选择，不能在线创建或编辑"
                }
              >
                <Select
                  id={`${ids}-script`}
                  options={scriptOptions}
                  value={draft.script}
                  onChange={(v) => set("script", v)}
                />
              </GridField>
              <GridField
                label="参数"
                hint="以数组传入，不经 shell 拼接；批处理脚本只接受安全 ASCII 字符"
              >
                <TokenInput
                  aria-label="脚本参数"
                  mono
                  addLabel="添加参数"
                  values={draft.args}
                  onChange={(v) => set("args", v)}
                />
              </GridField>
            </>
          ) : null}

          {draft.type === "ssh" ? (
            <>
              <GridField label="主机" htmlFor={`${ids}-host`} error={shown("hostId")}>
                <Select
                  id={`${ids}-host`}
                  options={[
                    { value: "", label: hosts.length === 0 ? "还没有受控主机" : "选择主机…" },
                    ...hosts.map((h) => ({
                      value: String(h.id),
                      label: `${h.name}（${h.address}）${h.hostkey_fingerprint ? "" : " · 指纹未确认"}`,
                    })),
                  ]}
                  value={draft.hostId === null ? "" : String(draft.hostId)}
                  onChange={(v) => set("hostId", v ? Number(v) : null)}
                />
              </GridField>
              <GridField
                label="命令"
                htmlFor={`${ids}-cmd`}
                error={shown("command")}
                hint="主机启用 sudo 时以 sudo -n -- 执行"
              >
                <Textarea
                  id={`${ids}-cmd`}
                  mono
                  rows={3}
                  value={draft.command}
                  invalid={Boolean(shown("command"))}
                  placeholder="shutdown -h now"
                  spellCheck={false}
                  onChange={(e) => set("command", e.target.value)}
                />
              </GridField>
              <GridField
                label="关机命令"
                hint="命令被接受后连接断开记为「已发送」；已发送不代表关机完成，计划会做离线确认"
              >
                <Switch
                  aria-label="关机命令"
                  checked={draft.shutdown}
                  onChange={(v) => set("shutdown", v)}
                />
              </GridField>
            </>
          ) : null}

          {draft.type === "plan" ? (
            <GridField
              label="关机计划"
              htmlFor={`${ids}-plan`}
              error={shown("planId")}
              hint="由规则触发时启动该计划；dry-run 规则创建 dry-run 运行。不能手动执行"
            >
              <Select
                id={`${ids}-plan`}
                options={[
                  { value: "", label: plans.length === 0 ? "还没有关机计划" : "选择计划…" },
                  ...plans.map((p) => ({ value: String(p.id), label: p.name })),
                ]}
                value={draft.planId === null ? "" : String(draft.planId)}
                onChange={(v) => set("planId", v ? Number(v) : null)}
              />
            </GridField>
          ) : null}
        </div>

        {draft.type === "http" && guardOn ? (
          <Banner tone="neutral" icon="shield" className="mt-4">
            HTTP 护栏已开启：地址不能指向任何已配置的 UPS 数据源主机，保存和每次执行时都会检查。
          </Banner>
        ) : null}
        {draft.type === "plan" ? (
          <p className="list-caption">
            手动执行关机计划请到 <Link to="/plans">关机计划</Link> 页输入计划名称确认。
          </p>
        ) : null}

        {base ? (
          <div className="hstack mt-6">
            <Button variant="danger-tinted" icon="trash" onClick={() => setDeleteOpen(true)}>
              删除动作
            </Button>
          </div>
        ) : null}
      </Sheet>
      {base ? (
        <DestructiveDialog
          open={deleteOpen}
          onClose={() => setDeleteOpen(false)}
          title={`删除动作「${base.name}」？`}
          message="删除后无法恢复。被规则或关机计划引用、有执行记录或正在执行的动作不能删除。"
          conflictMessage="动作正被规则或关机计划引用，或正在执行，暂不能删除。请先移除引用或等待执行结束。"
          confirmLabel="删除动作"
          onConfirm={async () => {
            await deleteAction(base.id);
            await queryClient.invalidateQueries({ queryKey: queryKeys.actions });
            toast.show({ tone: "ok", title: "动作已删除", duration: 3000 });
            onClose();
          }}
        />
      ) : null}
    </>
  );
}
