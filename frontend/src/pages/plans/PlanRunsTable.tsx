import { useInfiniteQuery } from "@tanstack/react-query";
import { queryKeys, useDevices, useTimeZone } from "../../api/hooks.ts";
import { listPlanRuns } from "../../api/plans.ts";
import { Banner, Button, Pill, Spinner, Table } from "../../components/index.ts";
import { formatFullDateTime } from "../../model/time.ts";
import { planRunStatusText, planRunTone, stepsDoneText } from "./model.ts";

const PAGE_SIZE = 20;

/**
 * 运行记录（F6-5）：`GET /plan-runs?plan_id=` 游标分页，按 ID 倒序。
 * 运行结束（plan_progress 终态）或断线重连时 LiveEffects 使 plan-runs 前缀失效，表格随之刷新。
 * 触发来源结构未写入契约（Q-C5），这里只显示关联设备与停电周期。
 */
export function PlanRunsTable({
  planId,
  selectedRunId,
  onSelect,
}: {
  planId: number;
  selectedRunId: number | null;
  onSelect: (runId: number) => void;
}) {
  const timeZone = useTimeZone();
  const devices = useDevices().data;
  const query = useInfiniteQuery({
    queryKey: [...queryKeys.planRuns, { plan_id: planId }],
    queryFn: ({ pageParam, signal }) =>
      listPlanRuns(
        { plan_id: planId, limit: PAGE_SIZE, before: pageParam > 0 ? pageParam : undefined },
        signal,
      ),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.next_before > 0 ? last.next_before : undefined),
  });
  const runs = query.data?.pages.flatMap((p) => p.items) ?? [];
  const deviceName = (id: number) => devices?.find((d) => d.id === id)?.name ?? `设备 #${id}`;

  let body;
  if (query.isPending) {
    body = (
      <div className="empty">
        <Spinner />
        <div className="t-subhead muted">正在加载运行记录…</div>
      </div>
    );
  } else if (query.isError && !query.data) {
    body = (
      <div className="card-pad">
        <Banner
          tone="critical"
          role="alert"
          title="运行记录加载失败"
          actions={
            <Button size="sm" onClick={() => void query.refetch()}>
              重试
            </Button>
          }
        >
          {query.error.message}
        </Banner>
      </div>
    );
  } else if (runs.length === 0) {
    body = (
      <div className="card-pad t-subhead muted">
        还没有运行记录。手动执行或规则触发后会显示在这里。
      </div>
    );
  } else {
    body = (
      <>
        <div className="pl-runs-wrap">
          <Table className="pl-runs" caption="计划运行记录">
            <thead>
              <tr>
                <th>运行</th>
                <th>开始</th>
                <th>关联</th>
                <th>结果</th>
                <th className="num">步骤</th>
              </tr>
            </thead>
            <tbody>
              {runs.map((run) => (
                <tr key={run.id} className={run.id === selectedRunId ? "is-selected" : undefined}>
                  <td>
                    <button
                      type="button"
                      className="pl-link-btn"
                      aria-current={run.id === selectedRunId ? "true" : undefined}
                      onClick={() => onSelect(run.id)}
                    >
                      #{run.id}
                    </button>
                  </td>
                  <td className="tnum">{formatFullDateTime(run.started_at, timeZone)}</td>
                  <td className="muted">
                    {run.device_id !== null ? deviceName(run.device_id) : "未关联设备"}
                    {run.outage_cycle_id !== null ? ` · 周期 #${run.outage_cycle_id}` : ""}
                  </td>
                  <td>
                    <Pill tone={planRunTone(run)} size="sm">
                      {planRunStatusText(run)}
                    </Pill>
                  </td>
                  <td className="num tnum">{stepsDoneText(run)}</td>
                </tr>
              ))}
            </tbody>
          </Table>
        </div>
        <div className="pl-load-more">
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
      </>
    );
  }

  return (
    <section className="card mt-6" aria-label="运行记录">
      <div className="card-head">
        <div>
          <h3 className="card-title">运行记录</h3>
          <div className="card-sub">每个步骤对应一条动作执行记录，输出与错误详情见执行记录</div>
        </div>
      </div>
      {body}
    </section>
  );
}
