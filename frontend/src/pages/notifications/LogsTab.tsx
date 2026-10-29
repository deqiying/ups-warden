import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { useSearchParams } from "react-router";
import { channelKeys, listChannels, listNotifyLogs } from "../../api/notifications.ts";
import { queryKeys, useTimeZone } from "../../api/hooks.ts";
import type { NotificationResult } from "../../api/types.ts";
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
  type SelectOption,
} from "../../components/index.ts";
import { Icon } from "../../icons/Icon.tsx";
import { NA_TEXT } from "../../model/format.ts";
import { formatFullDateTime } from "../../model/time.ts";
import {
  durationMsText,
  eventColumnText,
  isDefaultLogFilters,
  logQuery,
  parseLogFilters,
  withLogFilters,
  DEFAULT_LOG_FILTERS,
  type LogFilters,
  type LogRange,
  type ResultFilter,
} from "./logs.ts";

/** 每页条数（契约默认 50，范围 1～200）。 */
const PAGE_SIZE = 50;

const RESULT_OPTIONS: SegmentedOption<ResultFilter>[] = [
  { value: "all", label: "全部" },
  { value: "failed", label: "失败" },
  { value: "ok", label: "成功" },
];

const RANGE_OPTIONS: SelectOption[] = [
  { value: "24h", label: "最近 24 小时" },
  { value: "7d", label: "最近 7 天" },
  { value: "30d", label: "最近 30 天" },
  { value: "all", label: "全部时间" },
];

/** 发送记录（F4-4）：筛选写入 query，游标“加载更多”，不显示总数，也没有“重试中”状态。 */
export function LogsTab() {
  const [params, setParams] = useSearchParams();
  const filters = useMemo(() => parseLogFilters(params), [params]);
  const timeZone = useTimeZone();

  const patchFilters = (patch: Partial<LogFilters>) =>
    setParams((prev) => withLogFilters(prev, patch), { replace: true });

  const channelsQuery = useQuery({
    queryKey: [...channelKeys.all, "list"],
    queryFn: ({ signal }) => listChannels(signal),
  });
  const channels = channelsQuery.data;

  const query = useInfiniteQuery({
    // 以 notifyLogs 为前缀：notify_failed 实时帧到达时整体失效并重新读取。
    queryKey: [...queryKeys.notifyLogs, filters],
    queryFn: ({ pageParam, signal }) =>
      listNotifyLogs(
        {
          limit: PAGE_SIZE,
          before: pageParam > 0 ? pageParam : undefined,
          // from 在请求时换算，避免把“当前时间”放进查询键导致不断产生新查询。
          ...logQuery(filters, Date.now()),
        },
        signal,
      ),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.next_before > 0 ? last.next_before : undefined),
  });

  const items = useMemo(() => query.data?.pages.flatMap((p) => p.items ?? []) ?? [], [query.data]);
  const channelName = (id: number) => channels?.find((c) => c.id === id)?.name ?? `渠道 #${id}`;

  const channelOptions: SelectOption[] = [
    { value: "", label: "全部渠道" },
    ...(channels ?? []).map((c) => ({ value: String(c.id), label: c.name })),
  ];
  // 渠道列表未加载（或渠道已删除）时，保证当前选中值始终有对应选项。
  if (filters.channel !== null && !channels?.some((c) => c.id === filters.channel)) {
    channelOptions.push({ value: String(filters.channel), label: `渠道 #${filters.channel}` });
  }

  const isDefault = isDefaultLogFilters(filters);

  let body;
  if (query.isPending) {
    body = (
      <div className="card">
        <div className="empty">
          <Spinner />
          <div className="t-subhead muted">正在加载发送记录…</div>
        </div>
      </div>
    );
  } else if (query.isError && !query.data) {
    body = (
      <Banner
        tone="critical"
        role="alert"
        title="发送记录加载失败"
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
        <Empty
          icon="info"
          tone="info"
          title="没有匹配的发送记录"
          action={
            isDefault ? undefined : (
              <Button size="sm" className="mt-2" onClick={() => patchFilters(DEFAULT_LOG_FILTERS)}>
                清除筛选
              </Button>
            )
          }
        >
          {isDefault ? "所选时间范围内还没有发送记录。" : "调整或清除筛选条件后重试。"}
        </Empty>
      </div>
    );
  } else {
    body = (
      <div className="card nt-logs-card">
        <Table className="logs" caption="发送记录">
          <thead>
            <tr>
              <th>完成时间</th>
              <th>渠道</th>
              <th className="col-hide">目标</th>
              <th>事件</th>
              <th>结果</th>
              <th className="num">尝试</th>
              <th className="num col-hide">耗时</th>
              <th className="col-hide">内容摘要（已脱敏）</th>
            </tr>
          </thead>
          <tbody>
            {items.map((item) => (
              <LogRow
                key={item.id}
                item={item}
                channel={channelName(item.channel_id)}
                timeZone={timeZone}
              />
            ))}
          </tbody>
        </Table>
        <div className="nt-load-more">
          {query.isFetchNextPageError ? (
            <div className="nt-load-more-error">
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
            <span className="t-footnote faint">没有更多匹配的记录</span>
          )}
        </div>
      </div>
    );
  }

  return (
    <div className="nt-logs">
      <div className="nt-filters">
        <label className="nt-filter-field">
          <span className="lbl">渠道</span>
          <Select
            aria-label="渠道"
            options={channelOptions}
            value={filters.channel === null ? "" : String(filters.channel)}
            onChange={(value) => patchFilters({ channel: value ? Number(value) : null })}
          />
        </label>
        <Segmented
          aria-label="结果筛选"
          options={RESULT_OPTIONS}
          value={filters.result}
          onChange={(result) => patchFilters({ result })}
        />
        <label className="nt-filter-field">
          <Icon name="calendar" className="faint" />
          <Select
            aria-label="时间范围"
            options={RANGE_OPTIONS}
            value={filters.range}
            onChange={(range) => patchFilters({ range: range as LogRange })}
          />
        </label>
        <div className="spacer" />
        {isDefault ? null : (
          <Button variant="plain" size="sm" onClick={() => patchFilters(DEFAULT_LOG_FILTERS)}>
            清除筛选
          </Button>
        )}
      </div>
      {body}
      <p className="list-caption">
        每个接收目标一条记录。耗时包含重试等待，不含排队时间。超时或取消不能证明接收端一定没有收到。时间按「设置
        › 通用 › 时区」显示{timeZone ? `（${timeZone}）` : ""}；记录默认保留 365 天。
      </p>
    </div>
  );
}

function LogRow({
  item,
  channel,
  timeZone,
}: {
  item: NotificationResult;
  channel: string;
  timeZone: string | undefined;
}) {
  const noEvent = item.event_id === null;
  return (
    <tr>
      <td className="tnum">{formatFullDateTime(item.at, timeZone)}</td>
      <td>{channel}</td>
      <td className="col-hide mono">{item.target || NA_TEXT}</td>
      <td>
        {noEvent ? (
          <span title="测试消息、条件规则与关机计划的通知没有关联的设备事件">
            {eventColumnText(item.event_id)}
          </span>
        ) : (
          eventColumnText(item.event_id)
        )}
      </td>
      <td>
        {item.ok ? (
          <Pill tone="green" size="sm">
            成功
          </Pill>
        ) : (
          <Pill tone="red" size="sm">
            失败{item.error ? ` · ${item.error}` : ""}
          </Pill>
        )}{" "}
        {item.template_fallback ? (
          <span title="自定义模板渲染失败，已改用默认模板">
            <Badge tone="yellow">默认模板</Badge>
          </span>
        ) : null}
      </td>
      <td className="num">{item.attempts}</td>
      <td className="num col-hide">{durationMsText(item.duration_ms)}</td>
      <td className="sum col-hide">{item.summary}</td>
    </tr>
  );
}
