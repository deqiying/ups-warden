import "./alerts.css";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Fragment, useMemo } from "react";
import { Link, useSearchParams } from "react-router";
import { listEvents } from "../../api/events.ts";
import { queryKeys, useDevices, useTimeZone } from "../../api/hooks.ts";
import type { Device, DeviceState, EventItem, PlanRun } from "../../api/types.ts";
import {
  Banner,
  Button,
  Empty,
  Metric,
  Pill,
  Segmented,
  Select,
  Spinner,
  Table,
  type SegmentedOption,
  type SelectOption,
} from "../../components/index.ts";
import { Icon } from "../../icons/Icon.tsx";
import { Page } from "../../layout/Page.tsx";
import { activeAlertCount } from "../../layout/selectors.ts";
import { shallowArrayEqual, useLive, useNow } from "../../live/hooks.ts";
import {
  COMM_LOST_POLICY_TEXT,
  deviceName,
  heldPowerText,
  relativeTime,
} from "../../model/device.ts";
import {
  EVENT_TYPE_GROUPS,
  SEVERITY_PILL_TONE,
  compareAlerts,
  eventDurationSeconds,
  eventIcon,
  shortDuration,
  summaryText,
} from "../../model/events.ts";
import { NA_TEXT, clockText } from "../../model/format.ts";
import { PLAN_RUN_STATUS_LABELS, SEVERITY_TEXT, eventLabel } from "../../model/labels.ts";
import { formatTime } from "../../model/time.ts";
import { useLiveClock } from "../monitor/hooks.ts";
import {
  DEFAULT_FILTERS,
  clockOrDate,
  groupByDay,
  isDefaultFilters,
  parseFilters,
  parseView,
  rangeFrom,
  withFilters,
  withView,
  type AlertsView,
  type HistoryFilters,
  type RangeFilter,
  type SeverityFilter,
} from "./helpers.ts";

/** 历史事件每页条数（契约默认 50，范围 1～200）。 */
const PAGE_SIZE = 50;

const VIEW_SUBTITLE = "告警是仍在持续的事件，条件解除后自动结束，无需手动确认。";

// ───────────────────────── 页面骨架与视图切换 ─────────────────────────

/** 页头右侧的视图切换；活动告警数为 0 时不显示角标。 */
function ViewSwitch({
  view,
  count,
  onChange,
  label,
}: {
  view: AlertsView;
  count: number;
  onChange: (view: AlertsView) => void;
  label: string;
}) {
  const options: SegmentedOption<AlertsView>[] = [
    { value: "active", label: "活动告警", count: count > 0 ? count : undefined },
    { value: "history", label: "历史事件" },
  ];
  return (
    <Segmented size="lg" aria-label={label} value={view} onChange={onChange} options={options} />
  );
}

/**
 * 告警与事件（F2-6）。
 * - 活动告警：完全来自实时存储（SSE sync / alert 替换），不提供确认按钮；
 * - 历史事件：`GET /events` 游标分页，筛选条件写入 query string。
 */
export function AlertsPage() {
  const [params, setParams] = useSearchParams();
  const view = parseView(params);
  const filters = useMemo(() => parseFilters(params), [params]);
  const alertCount = useLive(activeAlertCount);

  const setView = (next: AlertsView) => setParams((prev) => withView(prev, next));
  // 筛选变化用 replace，避免每次调整都占用一条浏览器历史。
  const patchFilters = (patch: Partial<HistoryFilters>) =>
    setParams((prev) => withFilters(prev, patch), { replace: true });

  return (
    <Page
      title="告警与事件"
      subtitle={VIEW_SUBTITLE}
      headAside={<ViewSwitch view={view} count={alertCount} onChange={setView} label="页面视图" />}
    >
      <div className="alerts-page">
        {/* Page 在窄屏会隐藏 headAside，因此内容区放一份仅窄屏显示的切换控件。 */}
        <div className="alerts-view-narrow">
          <ViewSwitch view={view} count={alertCount} onChange={setView} label="页面视图（窄屏）" />
        </div>
        {view === "active" ? (
          <ActiveAlerts onShowHistory={() => setView("history")} />
        ) : (
          <HistoryEvents
            filters={filters}
            onChange={patchFilters}
            onClear={() => patchFilters(DEFAULT_FILTERS)}
          />
        )}
      </div>
    </Page>
  );
}

// ───────────────────────── 活动告警 ─────────────────────────

/** 事件开始时间：当天显示“今天 14:11:12”，其余显示“10月26日 22:13”。 */
function startedText(iso: string, nowMs: number, timeZone?: string): string {
  const text = clockOrDate(iso, nowMs, timeZone);
  return /^\d{2}:\d{2}:\d{2}$/.test(text) ? `今天 ${text}` : text;
}

function ActiveAlerts({ onShowHistory }: { onShowHistory: () => void }) {
  const timeZone = useTimeZone();
  const devices = useDevices().data;
  const alertsByDevice = useLive((s) => s.data.alerts);
  const states = useLive((s) => s.data.devices, shallowArrayEqual);
  const planRuns = useLive((s) => s.data.planRuns, shallowArrayEqual);
  const failureCount = useLive((s) => s.data.notifyFailures.length);
  const syncedAt = useLive((s) => s.data.syncedAt);
  // 断线时“已持续”停在断开时刻（refMs），不把断流期间当作连续观测；倒计时仍走本地时钟。
  const { now, refMs, stale } = useLiveClock();

  const alerts = useMemo(
    () => Object.values(alertsByDevice).flat().sort(compareAlerts),
    [alertsByDevice],
  );
  // 主卡只放第一条严重告警；没有 critical 时全部进入“需要关注”。
  const hero = alerts[0]?.severity === "critical" ? alerts[0] : undefined;
  const others = hero ? alerts.slice(1) : alerts;

  const stateOf = (deviceId: number): DeviceState | undefined =>
    states.find((s) => s.device_id === deviceId);
  const deviceOf = (deviceId: number): Device | undefined =>
    devices?.find((d) => d.id === deviceId);

  let body;
  if (syncedAt === null) {
    body = (
      <div className="card">
        <div className="empty">
          <Spinner />
          <div className="t-subhead muted">正在同步实时数据…</div>
        </div>
      </div>
    );
  } else if (alerts.length === 0) {
    body = (
      <div className="card">
        <Empty
          icon="check"
          tone="ok"
          title="一切正常"
          action={
            <Button size="sm" className="mt-2" onClick={onShowHistory}>
              查看历史事件
            </Button>
          }
        >
          {states.length} 台设备，没有活动告警。
          {stale ? "实时连接已断开，以上为断线前最后一次同步的结果。" : ""}
        </Empty>
      </div>
    );
  } else {
    body = (
      <>
        {hero ? (
          <HeroCard
            alert={hero}
            device={deviceOf(hero.device_id)}
            deviceLabel={deviceName(devices, hero.device_id)}
            state={stateOf(hero.device_id)}
            planRun={planRuns.find((r) => r.device_id === hero.device_id)}
            now={now}
            refMs={refMs}
            stale={stale}
            timeZone={timeZone}
          />
        ) : null}
        {others.length > 0 ? (
          <>
            <div className={hero ? "section-head mt-6" : "section-head"}>
              <h2 className="section-title">需要关注</h2>
              <span className="t-footnote faint">按严重度与开始时间排序</span>
            </div>
            <div className="grid cols-3">
              {others.map((alert) => (
                <AlertCard
                  key={alert.id}
                  alert={alert}
                  device={deviceOf(alert.device_id)}
                  deviceLabel={deviceName(devices, alert.device_id)}
                  state={stateOf(alert.device_id)}
                  now={now}
                  timeZone={timeZone}
                />
              ))}
            </div>
          </>
        ) : null}
        <p className="list-caption">
          告警由事件开始，条件解除时自动结束并在「历史事件」中留存。断线重连后会重新同步全部活动告警。
        </p>
      </>
    );
  }

  return (
    <>
      {failureCount > 0 ? (
        <Banner
          tone="warning"
          role="status"
          className="alerts-notify-banner"
          actions={
            <Link className="btn btn-tinted btn-sm" to="/notifications?tab=logs">
              查看发送记录
            </Link>
          }
        >
          最近有 {failureCount} 条通知发送失败
        </Banner>
      ) : null}
      {body}
    </>
  );
}

interface HeroCardProps {
  alert: EventItem;
  device: Device | undefined;
  deviceLabel: string;
  state: DeviceState | undefined;
  planRun: PlanRun | undefined;
  now: number;
  /** “已持续”的参考时刻，断线时冻结。 */
  refMs: number;
  stale: boolean;
  timeZone: string | undefined;
}

/** 严重告警主卡：事件、已持续时长、设备实时指标与关联的关机计划运行。 */
function HeroCard({
  alert,
  device,
  deviceLabel,
  state,
  planRun,
  now,
  refMs,
  stale,
  timeZone,
}: HeroCardProps) {
  const started = Date.parse(alert.started_at);
  const elapsed = Number.isNaN(started) ? null : Math.max(0, (refMs - started) / 1000);
  const fields = state?.fields;
  const meta = [
    device?.location || null,
    `${startedText(alert.started_at, now, timeZone)} 开始`,
  ].filter(Boolean);

  return (
    <section className="card alert-hero" aria-label="严重告警">
      <div className="hero-top">
        <span className="sev sev-critical">
          <Icon name={eventIcon(alert.type)} />
        </span>
        <div className="hero-main">
          <div className="hstack wrap">
            <span className="t-title2">{eventLabel(alert.type)}</span>
            <Pill tone={SEVERITY_PILL_TONE[alert.severity]} size="sm">
              {SEVERITY_TEXT[alert.severity]}
            </Pill>
          </div>
          <div className="t-subhead muted mt-2">
            <Link to={`/devices/${alert.device_id}`}>{deviceLabel}</Link>
            {meta.map((text) => ` · ${text}`).join("")}
          </div>
        </div>
        <div className="hero-elapsed">
          <div className="t-caption muted">已持续</div>
          <div className={stale ? "t-metric tnum is-stale" : "t-metric tnum"}>
            {elapsed === null ? NA_TEXT : clockText(elapsed)}
          </div>
        </div>
      </div>
      <div className="hero-metrics">
        <Metric
          fieldKey="battery_capacity_pct"
          label="电池容量"
          field={fields?.["battery_capacity_pct"]}
          stale={stale}
        />
        <Metric
          fieldKey="runtime_estimate"
          field={fields?.["runtime_estimate"]}
          runtimeSource={state?.runtime_source}
          stale={stale}
        />
        <Metric
          fieldKey="output_load_pct[1]"
          label="负载"
          field={fields?.["output_load_pct[1]"]}
          stale={stale}
        />
        <Metric
          fieldKey="input_voltage[1]"
          label="输入电压"
          field={fields?.["input_voltage[1]"]}
          stale={stale}
        />
      </div>
      {planRun ? <PlanRunLine run={planRun} now={now} stale={stale} /> : null}
    </section>
  );
}

/** 主卡下方的关机计划运行：倒计时显示剩余 mm:ss，执行中显示当前阶段。 */
function PlanRunLine({ run, now, stale }: { run: PlanRun; now: number; stale: boolean }) {
  const until = run.countdown_until ? Date.parse(run.countdown_until) : NaN;
  let status;
  if (run.status === "countdown" && !Number.isNaN(until)) {
    const remaining = Math.max(0, Math.ceil((until - now) / 1000));
    status = (
      <>
        {PLAN_RUN_STATUS_LABELS.countdown}：
        <b className="tnum ink-orange">{clockText(remaining)}</b> 后开始
      </>
    );
  } else if (run.status === "running") {
    // current_stage 为从 0 开始的阶段序号。
    status = `${PLAN_RUN_STATUS_LABELS.running} · 第 ${run.current_stage + 1} 阶段`;
  } else {
    status = PLAN_RUN_STATUS_LABELS[run.status];
  }
  return (
    <div className="alert-plan">
      <span className="alert-plan-icon">
        <Icon name="power" />
      </span>
      <div className="alert-plan-main">
        <div className="hstack wrap">
          <span className="alert-plan-title">关机计划「{run.plan_name}」</span>
          {run.dry_run ? (
            <Pill tone="indigo" size="sm">
              预演
            </Pill>
          ) : null}
        </div>
        <div className="alert-plan-line">
          {status} · <Link to={`/plans/${run.plan_id}?tab=run`}>查看运行</Link>
          {stale ? <span className="ink-orange"> · 实时连接已断开，状态可能已变化</span> : null}
        </div>
      </div>
    </div>
  );
}

interface AlertCardProps {
  alert: EventItem;
  device: Device | undefined;
  deviceLabel: string;
  state: DeviceState | undefined;
  now: number;
  timeZone: string | undefined;
}

/** “需要关注”卡片：通讯中断类显示失联信息，其它类型显示事件摘要。 */
function AlertCard({ alert, device, deviceLabel, state, now, timeZone }: AlertCardProps) {
  const relative = relativeTime(alert.started_at, now);
  const startedAbs = clockOrDate(alert.started_at, now, timeZone);
  const summary = summaryText(alert.summary);
  const lostPolicy = device?.config.comm_lost_policy;

  return (
    <article className="card alert-card">
      <div className="alert-head">
        <span className={`sev sev-${alert.severity}`}>
          <Icon name={eventIcon(alert.type)} />
        </span>
        <div className="row-main">
          <div className="t-headline">{eventLabel(alert.type)}</div>
          <div className="t-footnote muted">{deviceLabel}</div>
        </div>
        <Pill tone={SEVERITY_PILL_TONE[alert.severity]} size="sm">
          {SEVERITY_TEXT[alert.severity]}
        </Pill>
      </div>
      <div className="card-body">
        <dl className="kv">
          <dt>开始</dt>
          <dd>{relative ? `${startedAbs} · ${relative}` : startedAbs}</dd>
          {alert.type === "comm_lost" ? (
            <>
              <dt>最后成功采集</dt>
              <dd>{state?.last_ok_at ? clockOrDate(state.last_ok_at, now, timeZone) : NA_TEXT}</dd>
              <dt>失联策略</dt>
              <dd>{lostPolicy ? COMM_LOST_POLICY_TEXT[lostPolicy] : NA_TEXT}</dd>
              <dt>供电状态</dt>
              <dd>{state ? heldPowerText(state) : NA_TEXT}</dd>
            </>
          ) : summary ? (
            <>
              <dt>摘要</dt>
              <dd>{summary}</dd>
            </>
          ) : null}
        </dl>
      </div>
      <div className="alert-foot">
        <Link to={`/devices/${alert.device_id}`}>查看设备</Link>
      </div>
    </article>
  );
}

// ───────────────────────── 历史事件 ─────────────────────────

const SEVERITY_OPTIONS: SegmentedOption<SeverityFilter>[] = [
  { value: "all", label: "全部" },
  { value: "critical", label: "严重" },
  { value: "warning", label: "警告" },
  { value: "info", label: "信息" },
];

const RANGE_OPTIONS: SelectOption[] = [
  { value: "24h", label: "最近 24 小时" },
  { value: "7d", label: "最近 7 天" },
  { value: "30d", label: "最近 30 天" },
  { value: "all", label: "全部时间" },
];

/** 类型筛选为单选（Q-C3）：按供电、电池等分组，分组标题为不可选项。 */
const TYPE_OPTIONS: SelectOption[] = [
  { value: "", label: "全部类型" },
  ...EVENT_TYPE_GROUPS.flatMap((group) => [
    { value: `group:${group.label}`, label: `— ${group.label} —`, disabled: true },
    ...group.types.map((type) => ({ value: type, label: eventLabel(type) })),
  ]),
];

interface HistoryEventsProps {
  filters: HistoryFilters;
  onChange: (patch: Partial<HistoryFilters>) => void;
  onClear: () => void;
}

function HistoryEvents({ filters, onChange, onClear }: HistoryEventsProps) {
  const timeZone = useTimeZone();
  const devices = useDevices().data;
  const now = useNow(30_000);
  const isDefault = isDefaultFilters(filters);

  const query = useInfiniteQuery({
    // 以 events 为前缀：SSE sync 时整体失效并重新读取。
    queryKey: [...queryKeys.events, "history", filters],
    queryFn: ({ pageParam, signal }) =>
      listEvents(
        {
          limit: PAGE_SIZE,
          before: pageParam > 0 ? pageParam : undefined,
          device_id: filters.device ?? undefined,
          type: filters.type ?? undefined,
          severity: filters.severity === "all" ? undefined : filters.severity,
          // from 在请求时换算，避免把“当前时间”放进查询键导致不断产生新查询。
          from: rangeFrom(filters.range, Date.now()),
        },
        signal,
      ),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.next_before > 0 ? last.next_before : undefined),
  });

  const items = useMemo(() => query.data?.pages.flatMap((p) => p.items) ?? [], [query.data]);
  const groups = useMemo(() => groupByDay(items, now, timeZone), [items, now, timeZone]);

  const deviceOptions: SelectOption[] = [
    { value: "", label: "全部设备" },
    ...(devices ?? []).map((d) => ({ value: String(d.id), label: d.name })),
  ];
  // 设备列表尚未加载（或设备已删除）时，保证当前选中值始终有对应选项。
  if (filters.device !== null && !devices?.some((d) => d.id === filters.device)) {
    deviceOptions.push({ value: String(filters.device), label: `设备 #${filters.device}` });
  }

  let body;
  if (query.isPending) {
    body = (
      <div className="card">
        <div className="empty">
          <Spinner />
          <div className="t-subhead muted">正在加载历史事件…</div>
        </div>
      </div>
    );
  } else if (query.isError && !query.data) {
    body = (
      <Banner
        tone="critical"
        role="alert"
        title="历史事件加载失败"
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
          title="没有匹配的事件"
          action={
            isDefault ? undefined : (
              <Button size="sm" className="mt-2" onClick={onClear}>
                清除筛选
              </Button>
            )
          }
        >
          {isDefault ? "所选时间范围内还没有事件记录。" : "调整或清除筛选条件后重试。"}
        </Empty>
      </div>
    );
  } else {
    body = (
      <div className="card">
        <Table sticky className="ev" caption="历史事件">
          <thead>
            <tr>
              <th className="shrink">
                <span className="sr-only">严重度</span>
              </th>
              <th>事件</th>
              <th>设备</th>
              <th className="col-time">开始</th>
              <th className="col-time">结束</th>
              <th className="col-dur">持续</th>
              <th className="col-sum">摘要</th>
            </tr>
          </thead>
          <tbody>
            {groups.map((group) => (
              <Fragment key={group.key}>
                <tr className="grp">
                  <td colSpan={7}>{group.heading}</td>
                </tr>
                {group.items.map((event) => (
                  <EventRow
                    key={event.id}
                    event={event}
                    deviceLabel={deviceName(devices, event.device_id)}
                    now={now}
                    timeZone={timeZone}
                  />
                ))}
              </Fragment>
            ))}
          </tbody>
        </Table>
        <div className="load-more">
          {query.isFetchNextPageError ? (
            <div className="load-more-error">
              <span className="ink-red">加载更早的事件失败：{query.error?.message}</span>
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
              {query.isFetchingNextPage ? "正在加载…" : "加载更早的事件"}
            </Button>
          ) : (
            <span className="t-footnote faint">没有更多匹配的事件</span>
          )}
        </div>
      </div>
    );
  }

  return (
    <>
      <div className="filters">
        <label className="filter-field">
          <span className="lbl">设备</span>
          <Select
            aria-label="设备"
            options={deviceOptions}
            value={filters.device === null ? "" : String(filters.device)}
            onChange={(value) => onChange({ device: value ? Number(value) : null })}
          />
        </label>
        <label className="filter-field">
          <span className="lbl">类型</span>
          <Select
            aria-label="类型"
            options={TYPE_OPTIONS}
            value={filters.type ?? ""}
            onChange={(value) => onChange({ type: value || null })}
          />
        </label>
        <Segmented
          aria-label="严重度筛选"
          options={SEVERITY_OPTIONS}
          value={filters.severity}
          onChange={(severity) => onChange({ severity })}
        />
        <label className="filter-field">
          <Icon name="calendar" className="faint" />
          <Select
            aria-label="时间范围"
            options={RANGE_OPTIONS}
            value={filters.range}
            onChange={(range) => onChange({ range: range as RangeFilter })}
          />
        </label>
        <div className="spacer" />
        {isDefault ? null : (
          <Button variant="plain" size="sm" onClick={onClear}>
            清除筛选
          </Button>
        )}
      </div>
      {body}
      <p className="list-caption">
        时间按「设置 › 通用 › 时区」显示{timeZone ? `（${timeZone}）` : ""}；服务端以 UTC
        保存。每次加载 {PAGE_SIZE} 条，按 next_before 继续向前翻页。
      </p>
    </>
  );
}

interface EventRowProps {
  event: EventItem;
  deviceLabel: string;
  now: number;
  timeZone: string | undefined;
}

function EventRow({ event, deviceLabel, now, timeZone }: EventRowProps) {
  const duration = eventDurationSeconds(event, now);
  const summary = summaryText(event.summary);

  let ended;
  if (event.active) {
    ended = (
      <Pill tone={SEVERITY_PILL_TONE[event.severity]} size="sm">
        进行中
      </Pill>
    );
  } else if (event.cleared_at) {
    // 以开始时间为基准：与开始同一天只显示时分秒，跨天带上日期，避免产生歧义。
    ended = clockOrDate(event.cleared_at, Date.parse(event.started_at), timeZone);
  } else {
    ended = <span className="faint">{NA_TEXT}</span>;
  }

  return (
    <tr>
      <td>
        <span className={`sev sev-${event.severity}`}>
          <Icon name={eventIcon(event.type)} />
          <span className="sr-only">{SEVERITY_TEXT[event.severity]}</span>
        </span>
      </td>
      <td>
        <span className="ev-name">{eventLabel(event.type)}</span>
      </td>
      <td>{deviceLabel}</td>
      <td className="col-time">{formatTime(event.started_at, timeZone)}</td>
      <td className="col-time">{ended}</td>
      <td className="col-dur muted">
        {duration === null ? <span className="faint">{NA_TEXT}</span> : shortDuration(duration)}
      </td>
      <td className="col-sum muted">{summary}</td>
    </tr>
  );
}
