import { useInfiniteQuery } from "@tanstack/react-query";
import { Fragment, useMemo, useState } from "react";
import { auditDiffRows, listAudit, MASKED_TEXT, type AuditEntry } from "../../api/audit.ts";
import { useTimeZone } from "../../api/hooks.ts";
import { Banner, Button, Empty, Pill, Spinner, Table } from "../../components/index.ts";
import { useNow } from "../../live/hooks.ts";
import { formatTime } from "../../model/time.ts";
import { settingsKeys, useUsers } from "./hooks.ts";
import { auditKindLabel, auditResultView, auditTargetText, groupAuditByDay } from "./model.ts";
import { PaneHead } from "./shared.tsx";

/** 每页条数（契约默认 50，范围 1～200）。 */
const PAGE_SIZE = 50;

/** 展开后的字段级差异。敏感字段只显示“已修改”，绝不显示值。 */
function DiffView({ diff }: { diff: unknown }) {
  const rows = auditDiffRows(diff);
  if (rows.length === 0) return <div className="t-footnote muted">没有字段级差异。</div>;
  return (
    <div className="diff" role="group" aria-label="字段差异">
      {rows.map((row) =>
        row.masked ? (
          <Fragment key={row.field}>
            <span className="k">{row.field}</span>
            <span />
            <span />
            <span className="a is-masked">{MASKED_TEXT}</span>
          </Fragment>
        ) : (
          <Fragment key={row.field}>
            <span className="k">{row.field}</span>
            <span className="b">{row.before ?? "—"}</span>
            <span className="arr" aria-hidden="true">
              →
            </span>
            <span className="a">{row.after ?? "—"}</span>
          </Fragment>
        ),
      )}
    </div>
  );
}

/**
 * 审计日志（F8-5）：`GET /audit` 只分页（limit / before 游标，“加载更多”），没有筛选，
 * 契约没有 total，因此不显示总数。点击一行展开字段级 diff。
 */
export function AuditSection() {
  const timeZone = useTimeZone();
  const users = useUsers();
  const [open, setOpen] = useState<ReadonlySet<number>>(new Set());

  const query = useInfiniteQuery({
    queryKey: settingsKeys.audit,
    queryFn: ({ pageParam, signal }) =>
      listAudit({ limit: PAGE_SIZE, before: pageParam > 0 ? pageParam : undefined }, signal),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.next_before > 0 ? last.next_before : undefined),
  });

  const items = useMemo(() => query.data?.pages.flatMap((p) => p.items) ?? [], [query.data]);
  // “今天 / 昨天”分组标题随时间推移才会变化，每分钟更新一次即可。
  const now = useNow(60_000);
  const groups = useMemo(() => groupAuditByDay(items, timeZone, now), [items, timeZone, now]);
  const userName = (id: number) => users.data?.find((u) => u.id === id)?.username;

  const toggle = (id: number) =>
    setOpen((prev) => {
      const next = new Set(prev);
      if (!next.delete(id)) next.add(id);
      return next;
    });

  let body;
  if (query.isPending) {
    body = (
      <div className="card">
        <div className="empty">
          <Spinner />
          <div className="t-subhead muted">正在加载审计日志…</div>
        </div>
      </div>
    );
  } else if (query.isError && !query.data) {
    body = (
      <Banner
        tone="critical"
        role="alert"
        title="审计日志加载失败"
        actions={
          <Button size="sm" onClick={() => void query.refetch()}>
            重试
          </Button>
        }
      >
        {query.error.message}
      </Banner>
    );
  } else if (items.length === 0) {
    body = (
      <div className="card">
        <Empty icon="doc" tone="info" title="还没有审计记录">
          登录、设置与配置变更都会记录在这里。
        </Empty>
      </div>
    );
  } else {
    body = (
      <div className="card audit-card">
        <Table className="audit" caption="审计日志">
          <thead>
            <tr>
              <th>时间</th>
              <th>用户</th>
              <th>操作</th>
              <th>对象</th>
              <th>结果</th>
              <th className="col-hide">来源</th>
            </tr>
          </thead>
          <tbody>
            {groups.map((group) => (
              <Fragment key={group.key}>
                <tr className="grp">
                  <td colSpan={6}>{group.label}</td>
                </tr>
                {group.items.map((entry) => (
                  <AuditRows
                    key={entry.id}
                    entry={entry}
                    timeZone={timeZone}
                    open={open.has(entry.id)}
                    onToggle={() => toggle(entry.id)}
                    userName={userName}
                  />
                ))}
              </Fragment>
            ))}
          </tbody>
        </Table>
        <div className="audit-more">
          {query.isFetchNextPageError ? (
            <div className="audit-more-error">
              <span className="ink-red">加载更早的记录失败：{query.error?.message}</span>
              <Button size="sm" onClick={() => void query.fetchNextPage()}>
                重试
              </Button>
            </div>
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
    <section aria-label="审计日志">
      <PaneHead title="审计日志" />
      {body}
      <p className="list-caption">
        按时间倒序。密码、令牌等敏感字段只显示「{MASKED_TEXT}」，不记录内容。审计记录按「数据保留 ›
        日志与记录」的期限保存。
      </p>
    </section>
  );
}

function AuditRows({
  entry,
  timeZone,
  open,
  onToggle,
  userName,
}: {
  entry: AuditEntry;
  timeZone: string | undefined;
  open: boolean;
  onToggle: () => void;
  userName: (id: number) => string | undefined;
}) {
  const result = auditResultView(entry.result);
  const user = entry.user_id === null ? null : (userName(entry.user_id) ?? `#${entry.user_id}`);
  return (
    <>
      <tr className={open ? "is-open" : undefined} onClick={onToggle}>
        <td className="tnum">{formatTime(entry.at, timeZone)}</td>
        <td className={user === null ? "faint" : undefined}>{user ?? "—"}</td>
        <td>
          <button
            type="button"
            className="audit-toggle"
            aria-expanded={open}
            aria-label={`${auditKindLabel(entry.kind)}，${open ? "收起" : "展开"}差异`}
          >
            {auditKindLabel(entry.kind)}
          </button>
          <span className="kind">{entry.kind}</span>
        </td>
        <td>{auditTargetText(entry, userName)}</td>
        <td>
          <Pill tone={result.tone} size="sm">
            {result.label}
          </Pill>
        </td>
        <td className="col-hide mono">{entry.ip || "—"}</td>
      </tr>
      {open ? (
        <tr className="detail">
          <td colSpan={6}>
            <DiffView diff={entry.diff} />
          </td>
        </tr>
      ) : null}
    </>
  );
}
