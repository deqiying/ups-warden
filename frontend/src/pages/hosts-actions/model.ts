/*
 * 主机与动作页的纯函数：文案、图标、表单校验、摘要与执行记录筛选。
 * 契约见 docs/reference/rules-actions-hosts-api.md。
 */
import {
  ACTION_FORBIDDEN_HEADERS,
  type Action,
  type ActionDraft,
  type ActionRun,
  type ActionTrigger,
  type ActionType,
} from "../../api/actions.ts";
import type { Host, HostDraft, HostKind } from "../../api/hosts.ts";
import { parseHeaders } from "../../api/notifications.ts";
import type { PillTone } from "../../components/index.ts";
import type { IconName } from "../../icons/Icon.tsx";

export const HOST_KIND_LABELS: Readonly<Record<HostKind, string>> = {
  linux: "Linux",
  windows: "Windows",
  nas: "NAS",
  esxi: "ESXi",
  macos: "macOS",
  other: "其他",
};

export const HOST_KIND_ICONS: Readonly<Record<HostKind, IconName>> = {
  linux: "server",
  windows: "desktop",
  nas: "stack",
  esxi: "server",
  macos: "desktop",
  other: "server",
};

export const ACTION_TYPE_LABELS: Readonly<Record<ActionType, string>> = {
  notify: "通知",
  plan: "启动关机计划",
  ssh: "SSH 命令",
  http: "HTTP",
  script: "本地脚本",
};

/** 动作列表的分组顺序（与效果图一致）。 */
export const ACTION_TYPE_ORDER: readonly ActionType[] = ["notify", "plan", "ssh", "http", "script"];

export const ACTION_TYPE_ICONS: Readonly<Record<ActionType, IconName>> = {
  notify: "bell",
  plan: "power",
  ssh: "terminal",
  http: "globe",
  script: "doc",
};

export const ACTION_TYPE_COLORS: Readonly<Record<ActionType, string>> = {
  notify: "var(--accent)",
  plan: "var(--orange)",
  ssh: "var(--gray)",
  http: "var(--teal)",
  script: "var(--indigo)",
};

export type Errors = Partial<Record<string, string>>;

const byteLength = (text: string) => new TextEncoder().encode(text).length;

/** 主机 / 动作名称：与后端一致按 UTF-8 字节数限制，且不能含 NUL。 */
function nameError(name: string): string | undefined {
  const trimmed = name.trim();
  if (!trimmed) return "请填写名称";
  if (byteLength(trimmed) > 128) return "名称最多 128 字节";
  if (trimmed.includes("\u0000")) return "名称不能包含空字符";
  return undefined;
}

function portError(port: number): string | undefined {
  return Number.isInteger(port) && port >= 1 && port <= 65535 ? undefined : "端口应为 1～65535";
}

/** 主机表单校验（镜像 host.Validate 的结构性规则；凭据格式以后端为准）。 */
export function validateHostDraft(draft: HostDraft, original?: Host): Errors {
  const errors: Errors = {};
  const name = nameError(draft.name);
  if (name) errors.name = name;
  const address = draft.address.trim();
  if (!address) errors.address = "请填写主机地址";
  else if (/\s|\/|@/.test(address)) errors.address = "只填写主机名或 IP，不含协议、路径或用户名";
  const port = portError(draft.port);
  if (port) errors.port = port;
  const username = draft.username.trim();
  if (!username) errors.username = "请填写 SSH 用户名";
  else if (byteLength(username) > 128) errors.username = "用户名最多 128 字节";
  else if (["\u0000", "\r", "\n"].some((c) => username.includes(c)))
    errors.username = "用户名不能包含换行或空字符";
  if (draft.onlineType === "tcp") {
    const online = portError(draft.onlinePort);
    if (online) errors.onlinePort = online;
  }
  // 切换认证方式或新建时，后端没有可保留的旧凭据。
  const authKept = original !== undefined && original.conn.auth_method === draft.authMethod;
  if (draft.authMethod === "password") {
    if (!draft.password && !(authKept && original.password_set)) errors.password = "请填写密码";
  } else if (!draft.privateKey && !(authKept && original.private_key_set)) {
    errors.privateKey = "请粘贴私钥";
  }
  return errors;
}

function validStatusCode(value: number): boolean {
  return Number.isInteger(value) && value >= 200 && value <= 599;
}

/** 期望状态码 token 校验（TokenInput 的 validate）。 */
export function validateStatusToken(value: string): string | null {
  if (!/^\d{3}$/.test(value) || !validStatusCode(Number(value))) return "应为 200～599 的状态码";
  return null;
}

/** 动作表单校验（镜像 action.Validate 的结构性规则；HTTP 护栏与引用存在性以后端为准）。 */
export function validateActionDraft(draft: ActionDraft, original?: Action): Errors {
  const errors: Errors = {};
  const name = nameError(draft.name);
  if (name) errors.name = name;
  if (!Number.isInteger(draft.timeoutS) || draft.timeoutS < 1 || draft.timeoutS > 3600) {
    errors.timeoutS = "超时应为 1～3600 秒";
  }
  switch (draft.type) {
    case "notify":
      if (draft.channelIds.length === 0) errors.channelIds = "请选择至少一个通知渠道";
      else if (draft.channelIds.length > 64) errors.channelIds = "最多 64 个渠道";
      break;
    case "http": {
      const keptUrl = original?.type === "http" && original.url_set;
      const url = draft.url.trim();
      if (!url && !keptUrl) errors.url = "请填写请求地址";
      else if (url) {
        let parsed: URL | null = null;
        try {
          parsed = new URL(url);
        } catch {
          parsed = null;
        }
        if (!parsed || (parsed.protocol !== "http:" && parsed.protocol !== "https:")) {
          errors.url = "地址应以 http:// 或 https:// 开头";
        } else if (parsed.username || parsed.password) {
          errors.url = "地址不能包含用户名或密码，请改用 Headers";
        } else if (parsed.hash || url.includes("{{")) {
          errors.url = "地址不能包含片段（#）或模板变量";
        }
      }
      const { error } = parseHeaders(draft.headersText, ACTION_FORBIDDEN_HEADERS);
      if (error) errors.headers = error;
      if (draft.expectedStatus.some((v) => !validStatusCode(v))) {
        errors.expectedStatus = "状态码应为 200～599";
      }
      break;
    }
    case "script": {
      const script = draft.script.trim();
      if (!script) errors.script = "请选择脚本";
      else if (script.startsWith("/") || /[\\:]/.test(script) || script.split("/").includes("..")) {
        errors.script = "只能选择脚本目录内的相对路径";
      }
      break;
    }
    case "ssh":
      if (draft.hostId === null) errors.hostId = "请选择受控主机";
      if (!draft.command.trim()) errors.command = "请填写命令";
      else if (byteLength(draft.command) > 65536) errors.command = "命令最多 64 KiB";
      else if (draft.command.includes("\u0000")) errors.command = "命令不能包含空字符";
      break;
    case "plan":
      if (draft.planId === null) errors.planId = "请选择关机计划";
      break;
  }
  return errors;
}

/** 状态码列表文案：空表示默认 2xx。 */
export function expectedStatusText(codes: readonly number[] | undefined): string {
  return codes && codes.length > 0 ? `期望 ${codes.join(" / ")}` : "期望 2xx";
}

export interface SummaryContext {
  hosts?: readonly Host[];
  channelName?: (id: number) => string;
  planName?: (id: number) => string;
}

/** 动作行副标题（不含任何凭据：HTTP 地址只说明“已加密”）。 */
export function actionSummary(action: Action, ctx: SummaryContext): string {
  const c = action.config;
  switch (action.type) {
    case "notify": {
      const ids = c.channel_ids ?? [];
      return ids.length > 0
        ? ids.map((id) => ctx.channelName?.(id) ?? `渠道 #${id}`).join("、")
        : "未选择渠道";
    }
    case "plan":
      return `计划「${c.plan_id ? (ctx.planName?.(c.plan_id) ?? `#${c.plan_id}`) : "—"}」· 只能由规则触发；手动执行请到关机计划页`;
    case "ssh": {
      const host = ctx.hosts?.find((h) => h.id === c.host_id);
      const parts = [
        host ? host.name : `主机 #${c.host_id ?? "—"}`,
        c.command ?? "",
        host?.conn.sudo ? "sudo" : "",
        c.shutdown ? "关机命令" : "",
        `超时 ${action.timeout_s} 秒`,
      ];
      return parts.filter(Boolean).join(" · ");
    }
    case "http": {
      const secrets = [
        action.url_set ? "地址" : "",
        action.headers_set ? "请求头" : "",
        action.body_template_set ? "正文" : "",
      ].filter(Boolean);
      return [
        c.method ?? "POST",
        expectedStatusText(c.expected_status),
        secrets.length > 0 ? `${secrets.join("、")}已加密` : "",
      ]
        .filter(Boolean)
        .join(" · ");
    }
    case "script":
      return [c.script ?? "—", c.args && c.args.length > 0 ? `参数 ${c.args.join(" ")}` : ""]
        .filter(Boolean)
        .join(" · ");
  }
}

/** 执行来源文案；结构按 action.Trigger，未知类型原样显示。 */
export function triggerText(trigger: ActionTrigger | null | undefined): string {
  if (!trigger || typeof trigger !== "object") return "—";
  switch (trigger.type) {
    case "manual":
      return "手动";
    case "rule":
      return [
        trigger.rule_id ? `规则 #${trigger.rule_id}` : "规则",
        trigger.recovery ? "恢复动作" : "",
      ]
        .filter(Boolean)
        .join(" · ");
    case "plan":
      return trigger.plan_run_id ? `计划运行 #${trigger.plan_run_id}` : "关机计划";
    default:
      return trigger.type || "—";
  }
}

const OK_STATUSES = new Set(["succeeded", "success", "sent", "started"]);
const FAILED_STATUSES = new Set(["failed", "timeout"]);

/** 执行状态的胶囊配色：失败红、成功绿、dry-run 靛蓝、入队蓝、跳过/未知灰。 */
export function runStatusTone(status: string): PillTone {
  if (status === "dry_run") return "indigo";
  if (status === "queued" || status === "running") return "blue";
  if (OK_STATUSES.has(status)) return status === "started" ? "orange" : "green";
  if (FAILED_STATUSES.has(status)) return "red";
  if (status === "unknown") return "yellow";
  return "gray";
}

export function isFailedRun(run: Pick<ActionRun, "status">): boolean {
  return FAILED_STATUSES.has(run.status) || run.status === "unknown";
}

export type RunFilter = "all" | "failed" | "dry_run";

/** Q-C6：后端不能按状态过滤，只在已加载的记录中筛选。 */
export function filterRuns(runs: readonly ActionRun[], filter: RunFilter): ActionRun[] {
  if (filter === "failed") return runs.filter(isFailedRun);
  if (filter === "dry_run") return runs.filter((r) => r.dry_run);
  return [...runs];
}

/** 每个动作最近一次执行（输入按 id 倒序）。 */
export function latestRunByAction(runs: readonly ActionRun[]): Map<number, ActionRun> {
  const out = new Map<number, ActionRun>();
  for (const run of runs) {
    const current = out.get(run.action_id);
    if (!current || run.id > current.id) out.set(run.action_id, run);
  }
  return out;
}

/** 手动执行前的“将要执行”摘要；只含非敏感配置。 */
export function runPreviewLines(
  action: Action,
  ctx: SummaryContext,
): { label: string; value: string }[] {
  const c = action.config;
  const timeout = { label: "超时", value: `${action.timeout_s} 秒` };
  switch (action.type) {
    case "ssh": {
      const host = ctx.hosts?.find((h) => h.id === c.host_id);
      return [
        {
          label: "主机",
          value: host
            ? `${host.name} (${host.address}:${host.conn.port}) · ${host.hostkey_fingerprint ? "指纹已确认" : "指纹未确认，无法执行"}`
            : `主机 #${c.host_id ?? "—"}（不存在）`,
        },
        {
          label: "用户",
          value: host ? `${host.conn.username}${host.conn.sudo ? " · sudo -n" : ""}` : "—",
        },
        { label: "命令", value: c.command ?? "—" },
        {
          ...timeout,
          value: `${action.timeout_s} 秒${c.shutdown ? " · 关机命令：命令被接受后断开记为「已发送」" : ""}`,
        },
      ];
    }
    case "http":
      return [
        {
          label: "方法",
          value: `${c.method ?? "POST"} · ${expectedStatusText(c.expected_status)}`,
        },
        { label: "地址", value: action.url_set ? "已加密保存（不显示）" : "未设置" },
        timeout,
      ];
    case "script":
      return [
        { label: "脚本", value: c.script ?? "—" },
        { label: "参数", value: c.args && c.args.length > 0 ? c.args.join(" ") : "无" },
        timeout,
      ];
    case "notify":
      return [
        {
          label: "渠道",
          value: (c.channel_ids ?? []).map((id) => ctx.channelName?.(id) ?? `#${id}`).join("、"),
        },
        { label: "事件", value: "test（手动通知）" },
      ];
    case "plan":
      return [];
  }
}

/** 候选指纹只在服务端内存保存 5 分钟；界面从本地获取时刻起计算，避免与服务端时钟偏差。 */
export const HOSTKEY_TTL_S = 300;

/** 剩余有效秒数（向上取整，最小 0）。 */
export function hostKeyRemaining(fetchedAtMs: number, nowMs: number): number {
  return Math.max(0, Math.ceil(HOSTKEY_TTL_S - (nowMs - fetchedAtMs) / 1000));
}

/** 请求失败的文案：保留 Retry-After 提示；其余用统一错误映射后的 message。 */
export function failureText(err: unknown): string {
  if (err && typeof err === "object" && "retryAfterSeconds" in err) {
    const wait = (err as { retryAfterSeconds: number | null }).retryAfterSeconds;
    const message = err instanceof Error ? err.message : "操作失败，请重试";
    return wait ? `${message}（请 ${wait} 秒后重试）` : message;
  }
  return err instanceof Error ? err.message : "操作失败，请重试";
}
