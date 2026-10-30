import { useInfiniteQuery } from "@tanstack/react-query";
import { Fragment, useMemo } from "react";
import { useSearchParams } from "react-router";
import { listActionRuns, type ActionRun } from "../../api/actions.ts";
import { queryKeys, useActions, useTimeZone } from "../../api/hooks.ts";
import {
  Badge,
  Banner,
  Button,
  Empty,
  Pill,
  Segmented,
  Select,
  Spinner,
  Table,
  type SegmentedOption,
} from "../../components/index.ts";
import { NA_TEXT } from "../../model/format.ts";
import { actionRunStatusLabel } from "../../model/labels.ts";
import { formatFullDateTime } from "../../model/time.ts";
import { durationMsText } from "../notifications/logs.ts";
import { filterRuns, runStatusTone, triggerText, type RunFilter } from "./model.ts";

const PAGE_SIZE = 50;

const FILTER_OPTIONS: SegmentedOption<RunFilter>[] = [
  { value: "all", label: "全部" },
  { value: "failed", label: "失败" },
  { value: "dry_run", label: "Dry-run" },
];

function parseFilter(value: string | null): RunFilter {
  return value === "failed" || value === "dry_run" ? value : "all";
}

/**
 * 执行记录（F5-6）：`GET /action-runs?action_id=` 游标分页，可展开 stdout / stderr。
 * 状态筛选只作用于已加载的记录（Q-C6：后端不支持按状态过滤）。
 */
export function RunsTab() {
  const [params, setParams] = useSearchParams();
  const timeZone = useTimeZone();
  const actions = useActions().data;
  const actionId = Number(params.get("action")) || null;
  const filter = parseFilter(params.get("filter"));
  const openId = Number(params.get("open")) || null;

  const patch = (changes: Record<string, string | null>) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        for (const [key, value] of Object.entries(changes)) {
          if (value === null) next.delete(key);
          else next.set(key, value);
        }
        return next;
      },
      { replace: true },
    );

  const query = useInfiniteQuery({
    queryKey: [...queryKeys.actionRuns, { action_id: actionId }],
    queryFn: ({ pageParam, signal }) =>
      listActionRuns(
        {
          limit: PAGE_SIZE,
          before: pageParam > 0 ? pageParam : undefined,
          action_id: actionId ?? undefined,
        },
        signal,
      ),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.next_before > 0 ? last.next_before : undefined),
  });

  const loaded = useMemo(() => query.data?.pages.flatMap((p) => p.items ?? []) ?? [], [query.data]);
  const items = filterRuns(loaded, filter);
  const actionName = (id: number) => actions?.find((a) => a.id === id)?.name ?? `动作 #${id}`;

  const actionOptions = [
    { value: "", label: "全部动作" },
    ...(actions ?? []).map((a) => ({ value: String(a.id), label: a.name })),
  ];
  if (actionId !== null && !actions?.some((a) => a.id === actionId)) {
    actionOptions.push({ value: String(actionId), label: `动作 #${actionId}` });
  }

  let body;
  if (query.isPending) {
    body = (
      <div className="card">
        <div className="empty">
          <Spinner />
          <div className="t-subhead muted">正在加载执行记录…</div>
        </div>
      </div>
    );
  } else if (query.isError && !query.data) {
    body = (
      <Banner
        tone="critical"
        role="alert"
        title="执行记录加载失败"
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
      <div className="card ha-runs-card">
        {items.length === 0 ? (
          <Empty icon="info" tone="info" title="没有匹配的执行记录">
            {filter === "all"
              ? "动作执行后会在这里留下记录。"
              : "已加载的记录中没有匹配项，可加载更早的记录后再筛选。"}
          </Empty>
        ) : (
          <Table className="ha-runs" caption="动作执行记录">
            <thead>
              <tr>
                <th>时间</th>
                <th>动作</th>
                <th className="ha-col-hide">触发</th>
                <th>结果</th>
                <th className="num ha-col-hide">耗时</th>
              </tr>
            </thead>
            <tbody>
              {items.map((run) => (
                <RunRow
                  key={run.id}
                  run={run}
                  name={actionName(run.action_id)}
                  open={run.id === openId}
                  timeZone={timeZone}
                  onToggle={() => patch({ open: run.id === openId ? null : String(run.id) })}
                />
              ))}
            </tbody>
          </Table>
        )}
        <div className="ha-load-more">
          {query.isFetchNextPageError ? (
            <>
              <span className="ink-red">加载更早的记录失败：{query.error?.message}</span>
              <Button size="sm" onClick={() => void query.fetchNextPage()}>
                重试
              </Button>
            </>
          ) : query.hasNextPage ? (
            <Button
              size="sm"
              disabled={query.isFetchingNextPage}
              onClick={() => void query.fetchNextPage()}
            >
              {query.isFetchingNextPage ? "正在加载…" : "加载更早的记录"}
            </Button>
          ) : (
            <span className="t-footnote faint">没有更早的记录</span>
          )}
        </div>
      </div>
    );
  }

  return (
    <>
      <div className="ha-filters">
        <Select
          aria-label="动作"
          options={actionOptions}
          value={actionId === null ? "" : String(actionId)}
          onChange={(v) => patch({ action: v || null, open: null })}
        />
        <Segmented<RunFilter>
          aria-label="结果筛选"
          options={FILTER_OPTIONS}
          value={filter}
          onChange={(v) => patch({ filter: v === "all" ? null : v })}
        />
        {filter !== "all" ? (
          <span className="t-footnote faint">只筛选已加载的 {loaded.length} 条记录</span>
        ) : null}
      </div>
      {body}
      <p className="list-caption">
        「已入队」只表示进入通知队列，实际送达见通知发送记录。服务重启时仍在执行的记录标为「未知」，不会自动重放。stdout
        / stderr 各保留前 4 KiB，已按已知凭据脱敏。
      </p>
    </>
  );
}

function RunRow({
  run,
  name,
  open,
  timeZone,
  onToggle,
}: {
  run: ActionRun;
  name: string;
  open: boolean;
  timeZone: string | undefined;
  onToggle: () => void;
}) {
  const hasDetail = Boolean(run.stdout || run.stderr || run.error || run.exit_code !== null);
  const result = [
    actionRunStatusLabel(run.status),
    run.exit_code !== null && run.exit_code !== 0 ? `退出码 ${run.exit_code}` : "",
  ]
    .filter(Boolean)
    .join(" · ");
  return (
    <Fragment>
      <tr className={open ? "is-open" : undefined}>
        <td className="tnum nowrap">{formatFullDateTime(run.started_at, timeZone)}</td>
        <td>
          {hasDetail ? (
            <button type="button" className="ha-link-btn" aria-expanded={open} onClick={onToggle}>
              {name}
            </button>
          ) : (
            name
          )}
        </td>
        <td className="ha-col-hide muted">
          {triggerText(run.trigger)}
          {run.dry_run ? " · dry-run" : ""}
        </td>
        <td>
          <Pill tone={runStatusTone(run.status)} size="sm">
            {result}
          </Pill>{" "}
          {run.dry_run && run.status !== "dry_run" ? <Badge tone="indigo">Dry-run</Badge> : null}
        </td>
        <td className="num ha-col-hide">
          {run.ended_at ? durationMsText(run.duration_ms) : NA_TEXT}
        </td>
      </tr>
      {open && hasDetail ? (
        <tr className="ha-run-detail">
          <td colSpan={5}>
            {run.error ? (
              <div className="t-footnote ink-red ha-run-error" role="note">
                {run.error}
              </div>
            ) : null}
            <div className="ha-detail-grid">
              <div>
                <div className="lbl">stdout（前 4 KiB）</div>
                <pre className="code-block">{run.stdout || "（空）"}</pre>
              </div>
              <div>
                <div className="lbl">stderr</div>
                <pre className="code-block ink-red">{run.stderr || "（空）"}</pre>
              </div>
            </div>
            <div className="t-caption faint mt-3">
              运行 #{run.id}
              {run.exit_code !== null ? ` · 退出码 ${run.exit_code}` : ""}
              {run.trigger?.plan_run_id ? ` · 计划运行 #${run.trigger.plan_run_id}` : ""}
            </div>
          </td>
        </tr>
      ) : null}
    </Fragment>
  );
}
