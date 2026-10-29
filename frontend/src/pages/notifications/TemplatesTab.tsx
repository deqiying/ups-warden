import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useLayoutEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router";
import { isApiError } from "../../api/client.ts";
import {
  listTemplates,
  resetTemplate,
  saveTemplate,
  templateKeys,
  type NotifyTemplate,
  type TemplateFormat,
} from "../../api/notifications.ts";
import {
  Badge,
  Banner,
  Button,
  DestructiveDialog,
  List,
  ListCaption,
  ListHeader,
  Row,
  Segmented,
  Spinner,
  useToast,
} from "../../components/index.ts";
import { SplitView } from "../../layout/SplitView.tsx";
import { eventLabel } from "../../model/labels.ts";
import {
  COMMON_VARIABLES,
  FORMAT_LABELS,
  TEMPLATE_FORMATS,
  TEMPLATE_VARIABLES,
  isTemplateFormat,
  renderPreview,
  scanTemplate,
  templateGroups,
  templateIssues,
  variableTitle,
} from "./templateModel.ts";

const FORMAT_OPTIONS = TEMPLATE_FORMATS.map((value) => ({ value, label: FORMAT_LABELS[value] }));

/** 模板列表-编辑器（F4-3）。选中的事件与格式写入 `?event=&format=`。 */
export function TemplatesTab() {
  const [params, setParams] = useSearchParams();
  const query = useQuery({
    queryKey: templateKeys.all,
    queryFn: ({ signal }) => listTemplates(signal),
  });

  const items = query.data;
  const groups = useMemo(() => templateGroups(items ?? []), [items]);

  if (query.isPending) {
    return (
      <div className="card">
        <div className="empty">
          <Spinner />
          <div className="t-subhead muted">正在加载通知模板…</div>
        </div>
      </div>
    );
  }
  if (query.isError && !items) {
    return (
      <Banner
        tone="critical"
        role="alert"
        title="通知模板加载失败"
        actions={
          <Button size="sm" onClick={() => void query.refetch()}>
            重试
          </Button>
        }
      >
        {query.error.message}
      </Banner>
    );
  }

  const all = items ?? [];
  const firstEvent = groups[0]?.events[0] ?? "";
  const wantedEvent = params.get("event");
  const event =
    wantedEvent && all.some((item) => item.event_type === wantedEvent) ? wantedEvent : firstEvent;
  const wantedFormat = params.get("format");
  const format: TemplateFormat = isTemplateFormat(wantedFormat) ? wantedFormat : "text";
  const current = all.find((item) => item.event_type === event && item.format === format);

  const patch = (next: { event?: string; format?: TemplateFormat }) =>
    setParams(
      (prev) => {
        const p = new URLSearchParams(prev);
        if (next.event) p.set("event", next.event);
        if (next.format) p.set("format", next.format);
        return p;
      },
      { replace: true },
    );

  const customizedCount = (type: string) =>
    all.filter((item) => item.event_type === type && item.customized).length;

  const list = (
    <>
      {groups.map((group) => (
        <div key={group.label} className="nt-group">
          <ListHeader>{group.label}</ListHeader>
          <List className="tpl-list" aria-label={group.label}>
            {group.events.map((type) => {
              const count = customizedCount(type);
              return (
                <Row
                  key={type}
                  title={eventLabel(type)}
                  sub={<span className="tpl-key">{type}</span>}
                  selected={type === event}
                  onActivate={() => patch({ event: type })}
                >
                  {count > 0 ? (
                    <Badge tone="blue">
                      {count === 3 ? "已自定义" : `${count} 种格式已自定义`}
                    </Badge>
                  ) : null}
                </Row>
              );
            })}
          </List>
        </div>
      ))}
      <ListCaption>共 {all.length} 个模板：每种事件各有纯文本、HTML、JSON 三种格式。</ListCaption>
    </>
  );

  const detail = current ? (
    <TemplateEditor
      key={`${current.event_type}:${current.format}`}
      template={current}
      onFormatChange={(next) => patch({ format: next })}
    />
  ) : (
    <div className="card card-pad muted">没有可编辑的模板。</div>
  );

  return <SplitView stickyList listLabel="模板列表" list={list} detail={detail} />;
}

// ───────────────────────── 编辑器 ─────────────────────────

interface SaveFailure {
  message: string;
}

function TemplateEditor({
  template,
  onFormatChange,
}: {
  template: NotifyTemplate;
  onFormatChange: (format: TemplateFormat) => void;
}) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const { event_type: eventType, format } = template;

  const [draft, setDraft] = useState(template.body);
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState<SaveFailure | null>(null);
  const [resetOpen, setResetOpen] = useState(false);
  const textRef = useRef<HTMLTextAreaElement>(null);
  const caret = useRef<number | null>(null);

  const dirty = draft !== template.body;
  const segments = useMemo(() => scanTemplate(draft, format), [draft, format]);
  const issues = useMemo(() => templateIssues(segments), [segments]);
  const preview = useMemo(
    () => renderPreview(draft, format, eventType),
    [draft, format, eventType],
  );

  // 编辑区随内容自动增高（不出现内部滚动条），保证与下层高亮层的换行一致；
  // 点按变量后把光标放到插入内容之后（等 React 写回 value 再设置选区）。
  useLayoutEffect(() => {
    const el = textRef.current;
    if (!el) return;
    el.style.height = "auto";
    if (el.scrollHeight > 0) el.style.height = `${el.scrollHeight}px`;
    if (caret.current !== null) {
      el.focus();
      el.setSelectionRange(caret.current, caret.current);
      caret.current = null;
    }
  }, [draft]);

  const insert = (name: string) => {
    const token = `{{${name}}}`;
    const el = textRef.current;
    const start = el?.selectionStart ?? draft.length;
    const end = el?.selectionEnd ?? start;
    caret.current = start + token.length;
    setDraft(draft.slice(0, start) + token + draft.slice(end));
    setFailure(null);
  };

  const save = async () => {
    setSaving(true);
    setFailure(null);
    try {
      await saveTemplate(eventType, format, draft);
      await queryClient.invalidateQueries({ queryKey: templateKeys.all });
      toast.show({ tone: "ok", title: "模板已保存", duration: 3000 });
    } catch (err) {
      // 422 validation_failed 没有 details：未知变量的位置由本地扫描给出，结论以服务端为准。
      setFailure({ message: isApiError(err) ? err.message : "保存失败，请重试" });
    } finally {
      setSaving(false);
    }
  };

  const reset = async () => {
    // 提交 {event_type, format, reset:true}；成功后编辑区回到默认内容。
    const restored = await resetTemplate(eventType, format);
    setDraft(restored.body);
    setFailure(null);
    await queryClient.invalidateQueries({ queryKey: templateKeys.all });
    toast.show({ tone: "ok", title: "已恢复默认模板", duration: 3000 });
  };

  return (
    <>
      <section className="card card-pad">
        <div className="hstack wrap nt-tpl-head">
          <div>
            <h2 className="t-title3">
              {eventLabel(eventType)}
              {template.customized ? <Badge tone="blue">已自定义</Badge> : null}
            </h2>
            <div className="t-footnote muted">全局模板 · Webhook 渠道自带的正文模板优先</div>
          </div>
          <Segmented
            aria-label="模板格式"
            options={FORMAT_OPTIONS}
            value={format}
            onChange={onFormatChange}
          />
        </div>

        <div className="nt-editor mt-4">
          <pre className="nt-hl" aria-hidden="true">
            {segments.map((segment, index) =>
              segment.kind === "text" ? (
                segment.text
              ) : (
                <span
                  key={index}
                  className={segment.kind === "var" ? "v" : "bad"}
                  title={segment.kind === "bad" ? segment.reason : undefined}
                >
                  {segment.text}
                </span>
              ),
            )}
            {"\n"}
          </pre>
          <textarea
            ref={textRef}
            className="nt-ta"
            aria-label="模板内容"
            spellCheck={false}
            value={draft}
            onChange={(event) => {
              setDraft(event.target.value);
              setFailure(null);
            }}
          />
        </div>

        {issues.length > 0 ? (
          <Banner
            tone="critical"
            role="alert"
            title="模板里有无法识别的占位符，保存会被拒绝"
            className="mt-3"
          >
            <ul className="nt-issues">
              {issues.slice(0, 5).map((issue, index) => (
                <li key={index}>
                  第 {issue.line} 行：<span className="mono">{issue.text}</span> — {issue.reason}
                </li>
              ))}
              {issues.length > 5 ? <li>…另有 {issues.length - 5} 处</li> : null}
            </ul>
          </Banner>
        ) : null}
        {format === "json" && issues.length === 0 && !preview.jsonValid ? (
          <Banner tone="warning" role="status" className="mt-3">
            用示例数据渲染后不是合法 JSON：变量按字符串内容转义，请在模板里自行加上双引号。
          </Banner>
        ) : null}
        {failure ? (
          <Banner tone="critical" role="alert" title="保存被拒绝" className="mt-3">
            {failure.message}
            {issues.length === 0
              ? "。请检查占位符语法、JSON 是否合法，以及模板大小（≤ 64 KiB）。"
              : null}
          </Banner>
        ) : null}

        <div className="list-header mt-4">点按插入变量</div>
        <div className="nt-chips">
          {COMMON_VARIABLES.map((name) => (
            <button
              key={name}
              type="button"
              className="var-chip"
              aria-label={`插入变量 ${name}`}
              onClick={() => insert(name)}
            >
              {`{{${name}}}`}
            </button>
          ))}
        </div>
        <details className="nt-all-vars mt-3">
          <summary>全部变量（{TEMPLATE_VARIABLES.length}）</summary>
          <div className="nt-chips mt-2">
            {TEMPLATE_VARIABLES.map((name) => (
              <button
                key={name}
                type="button"
                className="var-chip"
                title={variableTitle(name)}
                aria-label={`插入变量 ${name}（全部变量）`}
                onClick={() => insert(name)}
              >
                {`{{${name}}}`}
              </button>
            ))}
          </div>
        </details>
        <p className="list-caption">
          按相字段必须写相号 [1]～[3]；时长字段加 <span className="mono">_text</span> 显示为「1 小时
          21 分」这样的中文时长。未采集或无效的字段渲染为「—」，不会显示为 0。
          <span className="mono"> {"{{detail}}"} </span>只在关机计划通知中有内容。
        </p>
      </section>

      <div className="list-header mt-6">预览 · 示例数据</div>
      <div className="preview-wrap">
        <Badge tone="indigo">示例</Badge>
        <pre className="preview-msg" aria-label="模板预览（示例）">
          {preview.text}
        </pre>
        <p className="t-footnote muted mt-3">
          使用固定的示例上下文近似渲染（机房 UPS-A），实际内容以发送时为准；未采集、非 good
          或未知的变量显示为「—」。{format === "html" ? "HTML 以源码形式显示。" : null}
        </p>
      </div>

      <div className="hstack mt-6">
        <Button
          variant="plain"
          size="sm"
          icon="undo"
          disabled={!template.customized || saving}
          onClick={() => setResetOpen(true)}
        >
          恢复默认模板
        </Button>
        <span className="spacer" />
        <Button disabled={!dirty || saving} onClick={() => setDraft(template.body)}>
          还原
        </Button>
        <Button
          variant="primary"
          disabled={!dirty || saving}
          aria-busy={saving}
          onClick={() => void save()}
        >
          {saving ? "保存中…" : "保存"}
        </Button>
      </div>

      <DestructiveDialog
        open={resetOpen}
        onClose={() => setResetOpen(false)}
        title="恢复默认模板？"
        message={`「${eventLabel(eventType)} · ${FORMAT_LABELS[format]}」的自定义内容将被删除，改用内置默认模板。`}
        confirmLabel="恢复默认"
        onConfirm={reset}
      />
    </>
  );
}
