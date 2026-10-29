/*
 * 通知模板的纯函数：变量目录、占位符扫描（镜像后端 notify.Render 的语法）、示例上下文预览。
 *
 * 后端没有预览接口（Q-C4）：这里用固定示例上下文近似渲染，并在界面标注“示例”；
 * 保存时以后端 422 为准。非 good / 未知字段一律渲染为「—」，绝不显示为 0。
 */
import type { NotifyTemplate, TemplateFormat } from "../../api/notifications.ts";
import { FIELD_CATALOG, fieldLabel } from "../../model/fields.ts";
import { NA_TEXT, durationText } from "../../model/format.ts";

// ───────────────────────── 事件与分组 ─────────────────────────

/** 15 种统一模型事件（后端 templateEvents 的顺序）。 */
export const DEVICE_EVENT_TYPES: readonly string[] = [
  "mains_lost",
  "mains_restored",
  "battery_low",
  "battery_low_cleared",
  "battery_depleted",
  "comm_lost",
  "comm_restored",
  "overload",
  "overload_cleared",
  "on_bypass",
  "bypass_cleared",
  "ups_fault",
  "ups_shutting_down",
  "temperature_high",
  "self_test_result",
];

/** 测试消息、条件规则与关机计划的通知上下文。 */
export const OTHER_EVENT_TYPES: readonly string[] = [
  "test",
  "rule_triggered",
  "rule_recovered",
  "plan_started",
  "plan_step_failed",
  "plan_cancelled",
  "plan_completed",
  "plan_resumed",
  "plan_aborted",
];

const PLAN_EVENTS = new Set(OTHER_EVENT_TYPES.filter((type) => type.startsWith("plan_")));

export interface TemplateGroup {
  label: string;
  events: string[];
}

/** 按效果图分组；接口返回了本表之外的新事件时并入第二组，不丢项。 */
export function templateGroups(items: readonly NotifyTemplate[]): TemplateGroup[] {
  const present = new Set(items.map((item) => item.event_type));
  const known = new Set([...DEVICE_EVENT_TYPES, ...OTHER_EVENT_TYPES]);
  const extra = [...present].filter((type) => !known.has(type));
  const only = (types: readonly string[]) => types.filter((type) => present.has(type));
  return [
    { label: "设备事件", events: only(DEVICE_EVENT_TYPES) },
    { label: "测试、规则与关机计划", events: [...only(OTHER_EVENT_TYPES), ...extra] },
  ].filter((group) => group.events.length > 0);
}

export const TEMPLATE_FORMATS: readonly TemplateFormat[] = ["text", "html", "json"];

export const FORMAT_LABELS: Readonly<Record<TemplateFormat, string>> = {
  text: "纯文本",
  html: "HTML",
  json: "JSON",
};

export function isTemplateFormat(value: string | null): value is TemplateFormat {
  return value === "text" || value === "html" || value === "json";
}

// ───────────────────────── 变量目录 ─────────────────────────

const BASE_VARIABLES = [
  "device.name",
  "device.id",
  "event.type",
  "event.severity",
  "time",
  "detail",
];

/**
 * 后端 notify.Variables 暴露的全部变量：基础变量 + 统一字段实例键（按相字段只有 `[1]`～`[3]`）
 * + 每个 Duration 字段的 `_text` 变体。统一字段目录由 model/fields.ts 镜像。
 */
function buildVariables(): string[] {
  const out = [...BASE_VARIABLES];
  for (const spec of FIELD_CATALOG) {
    const keys = spec.phased ? [1, 2, 3].map((phase) => `${spec.key}[${phase}]`) : [spec.key];
    out.push(...keys);
    if (spec.kind === "duration") out.push(`${spec.key}_text`);
  }
  return out;
}

export const TEMPLATE_VARIABLES: readonly string[] = buildVariables();
const KNOWN_VARIABLES: ReadonlySet<string> = new Set(TEMPLATE_VARIABLES);

/** 效果图“点按插入变量”里的常用变量。 */
export const COMMON_VARIABLES: readonly string[] = [
  "device.name",
  "event.type",
  "event.severity",
  "time",
  "battery_capacity_pct",
  "runtime_estimate_text",
  "on_battery_elapsed_text",
  "output_load_pct[1]",
  "input_voltage[1]",
  "detail",
];

const BASE_TITLES: Readonly<Record<string, string>> = {
  "device.name": "设备名称",
  "device.id": "设备 ID",
  "event.type": "事件类型",
  "event.severity": "事件严重度",
  time: "事件时间",
  detail: "补充说明（仅关机计划通知）",
};

/** 变量 chip 的中文提示：基础变量用固定文案，统一字段取目录名称（`_text` 变体加“（中文时长）”）。 */
export function variableTitle(name: string): string {
  const base = BASE_TITLES[name];
  if (base) return base;
  if (name.endsWith("_text")) return `${fieldLabel(name.slice(0, -"_text".length))}（中文时长）`;
  return fieldLabel(name);
}

export function isKnownVariable(name: string): boolean {
  return KNOWN_VARIABLES.has(name);
}

// ───────────────────────── 占位符扫描 ─────────────────────────

export type TemplateSegment =
  | { kind: "text"; text: string }
  | { kind: "var"; text: string; name: string }
  | { kind: "bad"; text: string; reason: string; line: number; unknown?: boolean };

/** 与后端 validTemplatePath 一致：`a.b[1]` 形式，段内字母 / 下划线 / 数字（不得以数字开头）。 */
function wellFormedPath(path: string): boolean {
  if (path === "") return false;
  return path.split(".").every((segment) => /^[A-Za-z_][A-Za-z0-9_]*(\[[1-3]\])?$/.test(segment));
}

function badReason(name: string): string {
  if (name === "") return "占位符不能为空";
  if (/\s/.test(name)) return "占位符内不能有空白";
  return `${name} 不是可用变量`;
}

/**
 * 按后端 Render 的规则拆分模板：严格的 `{{path}}`，JSON 允许嵌套对象以 `}}` 结尾。
 * 无效占位符以 `bad` 段保留原文，便于高亮与定位（line 为 1 起的行号）。
 */
export function scanTemplate(body: string, format: TemplateFormat): TemplateSegment[] {
  const segments: TemplateSegment[] = [];
  let rest = body;
  let consumed = "";
  const lineAt = () => consumed.split("\n").length;
  const pushText = (text: string) => {
    if (text === "") return;
    segments.push({ kind: "text", text });
    consumed += text;
  };

  while (rest.length > 0) {
    const start = rest.indexOf("{{");
    const end = rest.indexOf("}}");
    // 非 JSON 模板里孤立的 }} 是语法错误；JSON 的嵌套对象可合法以 }} 结尾。
    if (format !== "json" && end >= 0 && (start < 0 || end < start)) {
      pushText(rest.slice(0, end));
      segments.push({ kind: "bad", text: "}}", reason: "多余的 }}", line: lineAt() });
      consumed += "}}";
      rest = rest.slice(end + 2);
      continue;
    }
    if (start < 0) {
      pushText(rest);
      break;
    }
    pushText(rest.slice(0, start));
    const tail = rest.slice(start + 2);
    const close = tail.indexOf("}}");
    if (close < 0) {
      const text = rest.slice(start);
      segments.push({ kind: "bad", text, reason: "缺少结束的 }}", line: lineAt() });
      consumed += text;
      break;
    }
    const name = tail.slice(0, close);
    const text = `{{${name}}}`;
    if (isKnownVariable(name)) {
      segments.push({ kind: "var", text, name });
    } else {
      segments.push({
        kind: "bad",
        text,
        reason: badReason(name),
        line: lineAt(),
        unknown: wellFormedPath(name),
      });
    }
    consumed += text;
    rest = tail.slice(close + 2);
  }
  return segments;
}

export interface TemplateIssue {
  line: number;
  text: string;
  reason: string;
}

export function templateIssues(segments: readonly TemplateSegment[]): TemplateIssue[] {
  return segments.flatMap((segment) =>
    segment.kind === "bad"
      ? [{ line: segment.line, text: segment.text, reason: segment.reason }]
      : [],
  );
}

// ───────────────────────── 示例预览 ─────────────────────────

const SAMPLE_SEVERITY: Readonly<Record<string, string>> = {
  mains_lost: "critical",
  battery_low: "critical",
  battery_low_cleared: "critical",
  battery_depleted: "critical",
  ups_fault: "critical",
  ups_shutting_down: "critical",
  comm_lost: "warning",
  comm_restored: "warning",
  overload: "warning",
  overload_cleared: "warning",
  on_bypass: "warning",
  bypass_cleared: "warning",
  temperature_high: "warning",
  rule_triggered: "warning",
  plan_step_failed: "warning",
  plan_aborted: "warning",
};

/**
 * 示例上下文：只给出少量 good 字段，其余（含 `input_voltage[1]`）一律按非 good 处理，
 * 渲染为「—」。这样预览能同时演示“有数据”和“无数据”两种渲染。
 */
const SAMPLE_GOOD_FIELDS: Readonly<Record<string, number>> = {
  battery_capacity_pct: 89,
  "output_load_pct[1]": 27,
  runtime_estimate: 4860,
  on_battery_elapsed: 750,
};
const SAMPLE_DURATION_FIELDS = new Set(["runtime_estimate", "on_battery_elapsed"]);

/** 示例里的取值；未知或非 good 返回「—」。 */
export function sampleValue(name: string, eventType: string): string {
  switch (name) {
    case "device.name":
      return "机房 UPS-A";
    case "device.id":
      return "1";
    case "event.type":
      return eventType;
    case "event.severity":
      return SAMPLE_SEVERITY[eventType] ?? "info";
    case "time":
      return "2026-10-27T14:11:12+08:00";
    case "detail":
      // 与后端一致：仅关机计划通知填充，其他事件渲染为空串。
      return PLAN_EVENTS.has(eventType) ? "计划「机房关机」· 运行 #18" : "";
  }
  if (name in SAMPLE_GOOD_FIELDS) return String(SAMPLE_GOOD_FIELDS[name]);
  if (name.endsWith("_text")) {
    const base = name.slice(0, -"_text".length);
    if (SAMPLE_DURATION_FIELDS.has(base)) return durationText(SAMPLE_GOOD_FIELDS[base]);
  }
  return NA_TEXT;
}

function escapeHtml(text: string): string {
  return text
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&#34;")
    .replaceAll("'", "&#39;");
}

/** 按格式转义变量值：JSON 只转义字符串内容，HTML 只转义变量值，纯文本原样。 */
function escapeValue(text: string, format: TemplateFormat): string {
  if (format === "json") return JSON.stringify(text).slice(1, -1);
  if (format === "html") return escapeHtml(text);
  return text;
}

export interface TemplatePreview {
  text: string;
  issues: TemplateIssue[];
  /** JSON 格式下，示例渲染结果是否是合法 JSON（后端保存时同样校验）。 */
  jsonValid: boolean;
}

/** 用示例上下文近似渲染；无效占位符保持原文并进入 issues。 */
export function renderPreview(
  body: string,
  format: TemplateFormat,
  eventType: string,
): TemplatePreview {
  const segments = scanTemplate(body, format);
  const text = segments
    .map((segment) => {
      if (segment.kind === "var") return escapeValue(sampleValue(segment.name, eventType), format);
      // 格式正确但不存在的变量：预览渲染为「—」（保存时后端会以 422 拒绝，问题列表另行提示）。
      if (segment.kind === "bad" && segment.unknown) return escapeValue(NA_TEXT, format);
      return segment.text;
    })
    .join("");
  let jsonValid = true;
  if (format === "json") {
    try {
      JSON.parse(text);
    } catch {
      jsonValid = false;
    }
  }
  return { text, issues: templateIssues(segments), jsonValid };
}
