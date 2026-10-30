import { Link } from "react-router";
import { useDevices, useTimeZone } from "../../api/hooks.ts";
import { Banner, Button, Empty, Pill, Spinner, Table } from "../../components/index.ts";
import { Icon } from "../../icons/Icon.tsx";
import { NA_TEXT } from "../../model/format.ts";
import { formatFullDateTime } from "../../model/time.ts";
import { actionRunLink, useRuleLogs } from "./logs.ts";
import { LOG_KIND_TONES, logKindLabel, logReasonText, logValuesText } from "./model.ts";

export function RuleLogs({ ruleId }: { ruleId: number }) {
  const timeZone = useTimeZone();
  const devices = useDevices().data;
  const query = useRuleLogs(ruleId);
  const items = query.data?.pages.flatMap((p) => p.items) ?? [];
  const deviceName = (id: number) => devices?.find((d) => d.id === id)?.name ?? `设备 #${id}`;

  let body;
  if (query.isPending) {
    body = (
      <div className="empty">
        <Spinner />
        <div className="t-subhead muted">正在加载评估日志…</div>
      </div>
    );
  } else if (query.isError && !query.data) {
    body = (
      <Banner
        tone="critical"
        role="alert"
        title="评估日志加载失败"
        className="mt-3"
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
      <Empty icon="list" tone="info" title="还没有评估记录">
        规则命中、恢复、冷却跳过或字段质量变化时会在这里留下记录。
      </Empty>
    );
  } else {
    body = (
      <div className="ru-logs-wrap mt-3">
        <Table className="ru-logs" caption="规则评估日志">
          <thead>
            <tr>
              <th>时间</th>
              <th>结果</th>
              <th>设备</th>
              <th>依据</th>
              <th>动作</th>
            </tr>
          </thead>
          <tbody>
            {items.map((log) => {
              const values = logValuesText(log.values);
              return (
                <tr key={log.id}>
                  <td className="tnum nowrap">{formatFullDateTime(log.at, timeZone)}</td>
                  <td className="nowrap">
                    <Pill size="sm" tone={LOG_KIND_TONES[log.kind] ?? "gray"}>
                      {logKindLabel(log.kind)}
                    </Pill>
                  </td>
                  <td className="nowrap">{deviceName(log.device_id)}</td>
                  <td className="muted">
                    {logReasonText(log.kind, log.reason)}
                    {values ? <div className="t-footnote faint">{values}</div> : null}
                  </td>
                  <td>
                    {log.action_run_ids.length > 0 ? (
                      <span className="ru-run-links">
                        {log.action_run_ids.map((id) => (
                          <Link key={id} to={actionRunLink(id)}>
                            #{id}
                          </Link>
                        ))}
                      </span>
                    ) : (
                      <span className="faint">{NA_TEXT}</span>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </Table>
        {query.hasNextPage ? (
          <div className="ru-more">
            <Button
              size="sm"
              disabled={query.isFetchingNextPage}
              onClick={() => void query.fetchNextPage()}
            >
              {query.isFetchingNextPage ? "正在加载…" : "加载更多"}
            </Button>
          </div>
        ) : null}
      </div>
    );
  }

  return (
    <section className="card mt-4" aria-label="评估日志">
      <div className="card-head">
        <div>
          <h3 className="card-title">评估日志</h3>
          <div className="card-sub">命中、恢复、跳过与质量变化；动作结果见执行记录</div>
        </div>
        <div className="hstack gap-1">
          <Button
            size="sm"
            variant="plain"
            icon="refresh"
            disabled={query.isFetching}
            onClick={() => void query.refetch()}
          >
            刷新
          </Button>
          <Link className="section-link" to="/hosts-actions?tab=runs">
            执行记录
            <Icon name="chev-right" />
          </Link>
        </div>
      </div>
      <div className="card-pad ru-logs-body">{body}</div>
    </section>
  );
}
