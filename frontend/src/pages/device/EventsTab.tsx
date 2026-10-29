import { useInfiniteQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { listEvents } from "../../api/events.ts";
import { queryKeys, useTimeZone } from "../../api/hooks.ts";
import { Banner } from "../../components/Banner.tsx";
import { Button } from "../../components/Button.tsx";
import { Empty } from "../../components/Empty.tsx";
import { Pill } from "../../components/Pill.tsx";
import { Spinner } from "../../components/Spinner.tsx";
import { Icon } from "../../icons/Icon.tsx";
import { useNow } from "../../live/hooks.ts";
import { eventDurationSeconds, eventIcon, shortDuration, summaryText } from "../../model/events.ts";
import { eventLabel, SEVERITY_TEXT } from "../../model/labels.ts";
import { formatDateTime, formatTime } from "../../model/time.ts";

const PAGE_SIZE = 50;

/** 设备详情「事件」（F2-4）：`GET /events?device_id=` 游标分页，“加载更多”向前翻页。 */
export function EventsTab({ deviceId }: { deviceId: number }) {
  const timeZone = useTimeZone();
  const now = useNow(30_000);
  const query = useInfiniteQuery({
    queryKey: [...queryKeys.events, "device", deviceId],
    queryFn: ({ pageParam, signal }) =>
      listEvents({ device_id: deviceId, limit: PAGE_SIZE, before: pageParam }, signal),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (last) => (last.next_before ? last.next_before : undefined),
  });

  if (query.isPending) {
    return (
      <div className="card mt-4">
        <Empty title="正在加载事件…">
          <Spinner />
        </Empty>
      </div>
    );
  }
  // 只有首屏失败才整页显示错误；加载更多失败时保留已加载的列表（TanStack 此时同样处于 error 状态）。
  if (!query.data) {
    return (
      <Banner
        className="mt-4"
        tone="critical"
        role="alert"
        title="事件加载失败"
        actions={
          <Button size="sm" onClick={() => void query.refetch()}>
            重试
          </Button>
        }
      >
        {query.error?.message}
      </Banner>
    );
  }

  const events = query.data.pages.flatMap((p) => p.items);
  return (
    <div className="mt-4">
      {events.length === 0 ? (
        <div className="card">
          <Empty icon="check" title="该设备还没有事件" />
        </div>
      ) : (
        <div className="list has-icons">
          {events.map((event) => {
            const duration = eventDurationSeconds(event, now);
            const sub = [formatDateTime(event.started_at, timeZone), summaryText(event.summary)]
              .filter(Boolean)
              .join(" · ");
            return (
              <div className="row" key={event.id}>
                <span className={`sev sev-${event.severity}`}>
                  <Icon name={eventIcon(event.type)} label={SEVERITY_TEXT[event.severity]} />
                </span>
                <div className="row-main">
                  <div className="row-title">
                    <b>{eventLabel(event.type)}</b>{" "}
                    {event.active ? (
                      <Pill tone="red" size="sm">
                        活动
                      </Pill>
                    ) : null}
                  </div>
                  <div className="row-sub">{sub}</div>
                </div>
                <span className="t-footnote faint nowrap">
                  {event.active
                    ? duration !== null
                      ? shortDuration(duration)
                      : "进行中"
                    : event.cleared_at
                      ? `${formatTime(event.cleared_at, timeZone).slice(0, 5)} 结束`
                      : "—"}
                </span>
              </div>
            );
          })}
        </div>
      )}
      <div className="hstack mt-3" style={{ justifyContent: "center" }}>
        {query.hasNextPage ? (
          <Button
            size="sm"
            disabled={query.isFetchingNextPage}
            onClick={() => void query.fetchNextPage()}
          >
            {query.isFetchingNextPage ? "正在加载…" : "加载更早的事件"}
          </Button>
        ) : events.length > 0 ? (
          <span className="t-footnote faint">没有更早的事件</span>
        ) : null}
        <Link className="btn btn-plain btn-sm" to={`/alerts?view=history&device=${deviceId}`}>
          在「告警与事件」中查看
        </Link>
      </div>
      {query.isFetchNextPageError ? (
        <p className="t-footnote ink-red mt-2" role="alert">
          加载更多失败：{query.error.message}
        </p>
      ) : null}
    </div>
  );
}
