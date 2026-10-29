import { useQueryClient } from "@tanstack/react-query";
import { useId, useState, type ReactNode } from "react";
import { isApiError, isConfigSavedRuntimeError } from "../../api/client.ts";
import {
  buildCreateBody,
  buildUpdateBody,
  channelKeys,
  createChannel,
  updateChannel,
  type Channel,
  type ChannelDraft,
  type ChannelType,
  type SecretKey,
} from "../../api/notifications.ts";
import {
  Banner,
  Button,
  FormRow,
  Input,
  Segmented,
  Switch,
  TokenInput,
  Textarea,
  useToast,
} from "../../components/index.ts";
import {
  CHANNEL_TYPE_LABELS,
  draftFromChannel,
  emptyDraft,
  isSecretSet,
  validateChatId,
  validateDraft,
} from "./channelDraft.ts";
import { SecretField } from "./SecretField.tsx";

export type FormResult = { kind: "saved"; channel: Channel } | { kind: "saved-runtime-error" };

export interface ChannelFormProps {
  /** 已保存渠道；undefined 表示新建。 */
  channel?: Channel;
  /** 卡片头部（渠道标题、测试按钮、启停开关）。 */
  header: ReactNode;
  /** 底部操作条左侧（删除按钮）。 */
  footerStart?: ReactNode;
  onSettled: (result: FormResult) => void;
}

const TYPE_OPTIONS = (["telegram", "webhook"] as const).map((value) => ({
  value,
  label: CHANNEL_TYPE_LABELS[value],
}));

/**
 * 渠道新建 / 编辑表单（F4-1）。
 * - 编辑用 PUT，只提交修改过的顶层字段；提供 config 时为整体替换；
 * - 凭据输入框永不预填，显示“已设置 / 未设置”，留空保留，清除走显式 `clear_secrets`；
 * - 503 configuration_saved_runtime_error：配置已保存，不回滚，交给上层重新读取并重置表单。
 */
export function ChannelForm({ channel, header, footerStart, onSettled }: ChannelFormProps) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const nameId = useId();
  const apiBaseId = useId();
  const bodyTemplateId = useId();

  const [draft, setDraft] = useState<ChannelDraft>(() =>
    channel ? draftFromChannel(channel) : emptyDraft(),
  );
  const [saving, setSaving] = useState(false);
  const [showErrors, setShowErrors] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const set = <K extends keyof ChannelDraft>(key: K, value: ChannelDraft[K]) =>
    setDraft((current) => ({ ...current, [key]: value }));
  const setCleared = (key: SecretKey, on: boolean) =>
    setDraft((current) => ({
      ...current,
      clearSecrets: on
        ? [...new Set([...current.clearSecrets, key])]
        : current.clearSecrets.filter((item) => item !== key),
    }));
  const switchType = (type: ChannelType) =>
    // 清除标记属于旧类型的凭据，切换类型后不再适用。
    setDraft((current) => ({ ...current, type, clearSecrets: [] }));

  const typeChanged = channel !== undefined && channel.type !== draft.type;
  // 旧类型的凭据状态只对同类型有意义；切换类型后一律视为未设置。
  const setOf = (key: SecretKey) => !typeChanged && isSecretSet(channel, key);

  const errors = validateDraft(draft, channel);
  const invalid = Object.keys(errors).length > 0;
  const shown = (key: string) => (showErrors ? errors[key] : undefined);

  let updateBody: Record<string, unknown> | null = null;
  if (channel) {
    try {
      updateBody = buildUpdateBody(channel, draft);
    } catch {
      updateBody = null; // 如 Header 文本无效：由 validateDraft 给出具体提示
    }
  }
  const dirty = channel ? updateBody === null || Object.keys(updateBody).length > 0 : true;

  const reset = () => {
    setDraft(channel ? draftFromChannel(channel) : emptyDraft());
    setShowErrors(false);
    setFailure(null);
  };

  const submit = async () => {
    setShowErrors(true);
    if (invalid) return;
    setFailure(null);
    setSaving(true);
    try {
      const body = channel ? updateBody : buildCreateBody(draft);
      if (!body) return;
      const saved = channel ? await updateChannel(channel.id, body) : await createChannel(body);
      await queryClient.invalidateQueries({ queryKey: channelKeys.all });
      toast.show({ tone: "ok", title: channel ? "渠道已保存" : "渠道已创建", duration: 3000 });
      onSettled({ kind: "saved", channel: saved });
    } catch (err) {
      if (isConfigSavedRuntimeError(err)) {
        // 配置已经写入，只是运行恢复失败：不回滚为旧值，重新读取后以服务端为准（全局已 toast）。
        await queryClient.invalidateQueries({ queryKey: channelKeys.all });
        onSettled({ kind: "saved-runtime-error" });
      } else {
        setFailure(isApiError(err) ? err.message : "保存失败，请重试");
      }
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <section className="card">
        {header}
        {typeChanged ? (
          <div className="nt-pad">
            <Banner tone="warning" role="status" title="切换渠道类型">
              保存后会清除原 {CHANNEL_TYPE_LABELS[channel.type]} 的配置与全部凭据，并要求填写{" "}
              {CHANNEL_TYPE_LABELS[draft.type]} 的必需字段。
            </Banner>
          </div>
        ) : null}
        <div className="list is-flat">
          <FormRow label="名称" htmlFor={nameId}>
            <Input
              id={nameId}
              value={draft.name}
              maxLength={128}
              invalid={Boolean(shown("name"))}
              placeholder="例如：值班群"
              onChange={(event) => set("name", event.target.value)}
            />
            {shown("name") ? <FieldError>{shown("name")}</FieldError> : null}
          </FormRow>
          <FormRow
            label="渠道类型"
            description={channel ? "切换类型会清除旧类型的配置与凭据" : undefined}
          >
            <Segmented
              aria-label="渠道类型"
              options={TYPE_OPTIONS}
              value={draft.type}
              onChange={switchType}
            />
          </FormRow>
          {channel ? null : (
            <FormRow label="启用">
              <Switch
                aria-label="创建后启用"
                checked={draft.enabled}
                onChange={(next) => set("enabled", next)}
              />
            </FormRow>
          )}

          {draft.type === "telegram" ? (
            <>
              <SecretField
                label="Bot Token"
                description="只写不读，加密保存"
                required
                isSet={setOf("token")}
                value={draft.token}
                onChange={(value) => set("token", value)}
                cleared={false}
                onClearedChange={() => {}}
                error={shown("token")}
              />
              <FormRow label="Chat ID" description="按顺序分别发送，最多 32 个">
                <div>
                  <TokenInput
                    aria-label="Chat ID"
                    mono
                    max={32}
                    addLabel="添加"
                    placeholder="如 -100123456 或 @channel"
                    values={draft.chatIds}
                    onChange={(next) => set("chatIds", next)}
                    validate={validateChatId}
                    normalizeKey={(value) => value.toLowerCase()}
                  />
                  {shown("chatIds") ? <FieldError>{shown("chatIds")}</FieldError> : null}
                </div>
              </FormRow>
              <FormRow label="消息格式">
                <Segmented
                  aria-label="消息格式"
                  options={[
                    { value: "text", label: "纯文本" },
                    { value: "html", label: "HTML" },
                  ]}
                  value={draft.telegramFormat}
                  onChange={(value) => set("telegramFormat", value)}
                />
              </FormRow>
              <FormRow
                label="API 地址"
                htmlFor={apiBaseId}
                description="可改为自建反向代理；不要在此填写凭据"
              >
                <Input
                  id={apiBaseId}
                  mono
                  value={draft.apiBase}
                  invalid={Boolean(shown("apiBase"))}
                  onChange={(event) => set("apiBase", event.target.value)}
                />
                {shown("apiBase") ? <FieldError>{shown("apiBase")}</FieldError> : null}
              </FormRow>
              <SecretField
                label="HTTP 代理"
                description="仅支持 HTTP/HTTPS，含账号时整体加密"
                kind="text"
                isSet={setOf("proxy_url")}
                value={draft.proxyUrl}
                onChange={(value) => set("proxyUrl", value)}
                cleared={draft.clearSecrets.includes("proxy_url")}
                onClearedChange={(on) => setCleared("proxy_url", on)}
                error={shown("proxyUrl")}
              />
            </>
          ) : (
            <>
              <SecretField
                label="接收地址"
                description="只写不读；地址里的路径或 query 可能含凭据，因此不回显"
                kind="text"
                required
                isSet={setOf("url")}
                value={draft.url}
                onChange={(value) => set("url", value)}
                cleared={false}
                onClearedChange={() => {}}
                placeholder={setOf("url") ? "留空保留当前地址" : "https://example.com/notify"}
                error={shown("url")}
              />
              <FormRow label="请求方法">
                <Segmented
                  aria-label="请求方法"
                  options={[
                    { value: "POST", label: "POST" },
                    { value: "PUT", label: "PUT" },
                  ]}
                  value={draft.method}
                  onChange={(value) => set("method", value)}
                />
              </FormRow>
              <FormRow label="正文格式" description="Webhook 固定为 JSON">
                <span className="t-subhead muted">JSON（固定）</span>
              </FormRow>
              <SecretField
                label="自定义 Headers"
                description="每行一个「名称: 值」；整体加密，留空保留当前设置"
                kind="multiline"
                isSet={setOf("headers")}
                value={draft.headersText}
                onChange={(value) => set("headersText", value)}
                cleared={draft.clearSecrets.includes("headers")}
                onClearedChange={(on) => setCleared("headers", on)}
                placeholder={"X-API-Key: ..."}
                error={shown("headers")}
              />
              <SecretField
                label="签名密钥"
                description="设置后附加 X-UPS-Warden-Timestamp / Signature（HMAC-SHA256）"
                isSet={setOf("signing_key")}
                value={draft.signingKey}
                onChange={(value) => set("signingKey", value)}
                cleared={draft.clearSecrets.includes("signing_key")}
                onClearedChange={(on) => setCleared("signing_key", on)}
                error={shown("signingKey")}
              />
              <FormRow
                label="正文模板"
                htmlFor={bodyTemplateId}
                description="可选；留空使用对应事件的全局 JSON 模板。请勿在此粘贴凭据"
                className="nt-stack"
              >
                <Textarea
                  id={bodyTemplateId}
                  mono
                  rows={5}
                  value={draft.bodyTemplate}
                  placeholder={'{"event":"{{event.type}}","device":"{{device.name}}"}'}
                  onChange={(event) => set("bodyTemplate", event.target.value)}
                />
              </FormRow>
            </>
          )}
        </div>
      </section>

      <div className="list-header mt-6">失败重试</div>
      <div className="card card-pad">
        <div className="nt-retry" aria-label="重试节奏">
          <span className="dotx">1</span>首次发送
          <span className="line" />2 秒<span className="line" />
          <span className="dotx">2</span>
          <span className="line" />4 秒<span className="line" />
          <span className="dotx">3</span>
          <span className="line" />8 秒<span className="line" />
          <span className="dotx">4</span>最后一次
        </div>
        <p className="t-footnote muted mt-3">
          单次请求最长 10 秒，不跟随重定向。Telegram 要求更长的等待（429）时按其要求等待。已成功的
          Chat ID 不会因其他目标失败而重发。
        </p>
      </div>

      {failure ? (
        <Banner tone="critical" role="alert" title="保存失败" className="mt-4">
          {failure}
        </Banner>
      ) : null}

      <div className="hstack mt-6 nt-actions">
        {footerStart}
        <span className="spacer" />
        <Button onClick={reset} disabled={saving || (channel !== undefined && !dirty)}>
          还原
        </Button>
        <Button
          variant="primary"
          onClick={() => void submit()}
          disabled={saving || (channel !== undefined && !dirty)}
          aria-busy={saving}
        >
          {saving ? "保存中…" : channel ? "保存" : "创建渠道"}
        </Button>
      </div>
    </>
  );
}

function FieldError({ children }: { children: ReactNode }) {
  return (
    <div className="t-caption ink-red nt-field-error" role="alert">
      {children}
    </div>
  );
}
