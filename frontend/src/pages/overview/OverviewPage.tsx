import { useMemo, useState, type CSSProperties } from "react";
import { Link } from "react-router";
import { useDevices, useSourcesByDevice, useTimeZone } from "../../api/hooks.ts";
import type { Device, DeviceState, EventItem, PlanRun, Source } from "../../api/types.ts";
import { Badge } from "../../components/Badge.tsx";
import { Empty } from "../../components/Empty.tsx";
import { FieldValue, Metric } from "../../components/Metric.tsx";
import { Meter } from "../../components/Meter.tsx";
import { Pill } from "../../components/Pill.tsx";
import { Segmented } from "../../components/Segmented.tsx";
import { Select } from "../../components/Select.tsx";
import { Spinner } from "../../components/Spinner.tsx";
import { Icon } from "../../icons/Icon.tsx";
import { Page } from "../../layout/Page.tsx";
import { useConnection, useLive } from "../../live/hooks.ts";
import type { LiveData } from "../../live/reducer.ts";
import {
  heldPowerText,
  isCommLost,
  isOnBattery,
  needsAttention,
  onBatteryElapsed,
  pendingWindow,
  phaseCount,
  powerStatus,
  primarySource,
  protocolLabel,
  relativeTime,
  statusRank,
} from "../../model/device.ts";
import {
  compareAlerts,
  eventDurationSeconds,
  eventIcon,
  SEVERITY_PILL_TONE,
  shortDuration,
  summaryText,
} from "../../model/events.ts";
import { clockText } from "../../model/format.ts";
import { eventLabel, SEVERITY_TEXT } from "../../model/labels.ts";
import {
  countdownFraction,
  countdownRemaining,
  planRunPath,
  runProgressText,
} from "../../model/plan.ts";
import { formatDateHeading, formatDateTime, formatTime } from "../../model/time.ts";
import { useLiveClock } from "../monitor/hooks.ts";
import { CapacityRing, CountdownRing, PowerPill } from "../monitor/shared.tsx";
import { CancelRunDialog } from "../plans/CancelRunDialog.tsx";
import "./overview.css";

type DeviceFilter = "all" | "battery" | "attention";
type DeviceSort = "status" | "name";

const NO_ALERTS: readonly EventItem[] = [];

const selectData = (s: { data: LiveData }) => s.data;

/** 最近一次设备状态时间（页头“hh:mm:ss 更新”）。 */
function latestAt(devices: readonly DeviceState[]): string | null {
  let best: string | null = null;
  for (const d of devices) if (!best || d.at > best) best = d.at;
  return best;
}

/**
 * 总览（F2-1，overview.html）：停电主卡、汇总、设备卡、活动告警。
 * 运行数据全部来自 SSE 实时存储；设备名称、位置、去抖配置与数据源摘要来自查询 API。
 */
export function OverviewPage() {
  const timeZone = useTimeZone();
  const { now, refMs, stale } = useLiveClock();
  const connection = useConnection();
  const data = useLive(selectData);
  const devicesQuery = useDevices();
  const devicesById = useMemo(
    () => new Map((devicesQuery.data ?? []).map((d) => [d.id, d] as const)),
    [devicesQuery.data],
  );
  const liveIds = useMemo(() => data.devices.map((d) => d.device_id), [data.devices]);
  const sourcesQuery = useSourcesByDevice(liveIds);

  const [filter, setFilter] = useState<DeviceFilter>("all");
  const [sort, setSort] = useState<DeviceSort>("status");

  const alertsOf = (id: number) => data.alerts[id] ?? NO_ALERTS;
  const allAlerts = useMemo(
    () => Object.values(data.alerts).flat().sort(compareAlerts),
    [data.alerts],
  );

  const outageDevices = data.devices.filter((d) => d.outage_cycle_id !== null);
  // 与停电设备关联的运行显示在停电主卡内；其余活动运行（手动执行、其他设备）单独列出。
  const outageIds = new Set(outageDevices.map((d) => d.device_id));
  const otherRuns = data.planRuns.filter(
    (r) => r.device_id === null || !outageIds.has(r.device_id),
  );

  const counts = {
    online: data.devices.filter((d) => d.power_state === "online").length,
    pending: data.devices.filter((d) => d.pending_since !== null && !isOnBattery(d)).length,
    battery: data.devices.filter(isOnBattery).length,
    attention: data.devices.filter((d) => needsAttention(d, alertsOf(d.device_id))).length,
    lost: data.devices.filter(isCommLost).length,
    bypass: data.devices.filter((d) => !isCommLost(d) && d.power_state === "bypass").length,
    alerts: allAlerts.length,
    critical: allAlerts.filter((a) => a.severity === "critical").length,
  };
  const longestBattery = Math.max(
    0,
    ...data.devices.filter(isOnBattery).map((d) => onBatteryElapsed(d, refMs) ?? 0),
  );

  const nameOf = (id: number) => devicesById.get(id)?.name ?? `设备 #${id}`;
  const visible = data.devices
    .filter((d) =>
      filter === "battery"
        ? isOnBattery(d)
        : filter === "attention"
          ? needsAttention(d, alertsOf(d.device_id))
          : true,
    )
    .sort((a, b) => {
      if (sort === "status") {
        const rank = statusRank(a, alertsOf(a.device_id)) - statusRank(b, alertsOf(b.device_id));
        if (rank !== 0) return rank;
      }
      return (
        nameOf(a.device_id).localeCompare(nameOf(b.device_id), "zh-CN") || a.device_id - b.device_id
      );
    });

  const updatedAt = latestAt(data.devices);
  const headAside = stale ? (
    <span className="t-footnote ink-orange">
      数据停在 {formatTime(connection.disconnectedAt, timeZone)}
    </span>
  ) : updatedAt ? (
    <span className="t-footnote faint">{formatTime(updatedAt, timeZone)} 更新</span>
  ) : null;

  const toolbar = (
    <Link className="btn btn-icon" to="/devices/new" aria-label="添加设备">
      <Icon name="plus" />
    </Link>
  );

  const loading = data.syncedAt === null;

  return (
    <Page
      title="总览"
      eyebrow={`${formatDateHeading(now, timeZone)}${timeZone ? ` · ${timeZone}` : ""}`}
      headAside={headAside}
      toolbar={toolbar}
    >
      {loading ? (
        <div className="card">
          <Empty title="正在同步实时数据…">
            <Spinner />
          </Empty>
        </div>
      ) : data.devices.length === 0 ? (
        <div className="card">
          <Empty
            icon="ups"
            tone="info"
            title="还没有运行中的设备"
            action={
              <Link className="btn btn-tinted btn-sm" to="/devices/new">
                添加设备
              </Link>
            }
          >
            添加并启用设备后，这里会实时显示供电状态、电池与负载。已停用的设备不在总览中显示。
          </Empty>
        </div>
      ) : (
        <>
          {outageDevices.map((state) => (
            <OutageCard
              key={state.device_id}
              state={state}
              device={devicesById.get(state.device_id)}
              run={data.planRuns.find((r) => r.device_id === state.device_id)}
              now={now}
              refMs={refMs}
              stale={stale}
              timeZone={timeZone}
            />
          ))}
          {otherRuns.map((run) => (
            <PlanStrip
              key={run.id}
              run={run}
              deviceName={run.device_id === null ? null : nameOf(run.device_id)}
              now={now}
              stale={stale}
            />
          ))}

          <section className="section" aria-label="汇总">
            <div className="grid cols-4 keep-2">
              <StatCard
                icon="plug"
                color="var(--green)"
                value={counts.online}
                label={counts.pending > 0 ? `市电供电 · ${counts.pending} 台确认中` : "市电供电"}
              />
              <StatCard
                icon="battery"
                color="var(--orange)"
                value={counts.battery}
                label={
                  counts.battery > 0 ? `电池供电 · ${shortDuration(longestBattery)}` : "电池供电"
                }
              />
              <StatCard
                icon="alert"
                color="var(--yellow-ink)"
                value={counts.attention}
                label={[
                  "需要关注",
                  counts.lost > 0 ? `失联 ${counts.lost}` : "",
                  counts.bypass > 0 ? `旁路 ${counts.bypass}` : "",
                ]
                  .filter(Boolean)
                  .join(" · ")}
              />
              <StatCard
                icon="bell"
                color="var(--red)"
                value={counts.alerts}
                label={counts.critical > 0 ? `活动告警 · ${counts.critical} 严重` : "活动告警"}
              />
            </div>
          </section>

          <section className="section" aria-labelledby="overview-devices">
            <div className="section-head">
              <h2 className="section-title" id="overview-devices">
                设备
              </h2>
              <div className="hstack">
                <Segmented<DeviceFilter>
                  aria-label="设备筛选"
                  value={filter}
                  onChange={setFilter}
                  options={[
                    { value: "all", label: "全部", count: data.devices.length },
                    { value: "battery", label: "电池", count: counts.battery },
                    { value: "attention", label: "需关注", count: counts.attention },
                  ]}
                />
                <Select
                  plain
                  aria-label="设备排序"
                  value={sort}
                  onChange={(v) => setSort(v as DeviceSort)}
                  options={[
                    { value: "status", label: "按状态排序" },
                    { value: "name", label: "按名称排序" },
                  ]}
                />
              </div>
            </div>
            {visible.length === 0 ? (
              <div className="card">
                <Empty
                  icon="check"
                  title={filter === "battery" ? "没有电池供电的设备" : "没有需要关注的设备"}
                />
              </div>
            ) : (
              <div className={stale ? "grid cards is-live-stale" : "grid cards"}>
                {visible.map((state) => (
                  <DeviceCard
                    key={state.device_id}
                    state={state}
                    device={devicesById.get(state.device_id)}
                    source={primarySource(sourcesQuery.data?.[state.device_id])}
                    now={now}
                    refMs={refMs}
                    stale={stale}
                    timeZone={timeZone}
                  />
                ))}
              </div>
            )}
          </section>

          <section className="section" aria-labelledby="overview-alerts">
            <div className="section-head">
              <h2 className="section-title" id="overview-alerts">
                活动告警
              </h2>
              <Link className="section-link" to="/alerts">
                全部告警与事件
                <Icon name="chev-right" />
              </Link>
            </div>
            {allAlerts.length > 0 ? (
              <div className="list has-icons">
                {allAlerts.map((alert) => (
                  <AlertRow
                    key={alert.id}
                    alert={alert}
                    deviceName={nameOf(alert.device_id)}
                    now={now}
                    timeZone={timeZone}
                  />
                ))}
              </div>
            ) : (
              <div className="card">
                <Empty icon="check" title="没有活动告警">
                  {noAlertText(data, nameOf, timeZone)}
                </Empty>
              </div>
            )}
          </section>
        </>
      )}
    </Page>
  );
}

function noAlertText(
  data: LiveData,
  nameOf: (id: number) => string,
  timeZone: string | undefined,
): string {
  const allOnline = data.devices.every((d) => d.power_state === "online" && !isCommLost(d));
  const head = allOnline
    ? `${data.devices.length} 台设备均由市电供电。`
    : `${data.devices.length} 台设备运行中。`;
  // 最近事件只来自本次会话收到的 SSE event 帧，完整记录见「告警与事件」。
  const last = data.recentEvents[0];
  return last
    ? `${head}最近一次事件：${formatDateTime(last.started_at, timeZone)} ${eventLabel(last.type)} · ${nameOf(last.device_id)}`
    : head;
}

function StatCard({
  icon,
  color,
  value,
  label,
}: {
  icon: "plug" | "battery" | "alert" | "bell";
  color: string;
  value: number;
  label: string;
}) {
  const zero = value === 0;
  return (
    <div className="card stat">
      <div className="stat-top">
        <span
          className={zero ? "row-icon is-soft" : "row-icon"}
          style={{ "--c": color } as CSSProperties}
        >
          <Icon name={icon} />
        </span>
      </div>
      <div>
        <div className={zero ? "stat-num faint" : "stat-num"}>{value}</div>
        <div className="t-subhead muted">{label}</div>
      </div>
    </div>
  );
}

interface CardProps {
  state: DeviceState;
  device: Device | undefined;
  now: number;
  refMs: number;
  stale: boolean;
  timeZone: string | undefined;
}

/** 输入 / 输出电压（第 1 相）。 */
function InOut({ state, stale }: { state: DeviceState; stale: boolean }) {
  return (
    <>
      <FieldValue
        fieldKey="input_voltage[1]"
        field={state.fields["input_voltage[1]"]}
        stale={stale}
      />
      {" / "}
      <FieldValue
        fieldKey="output_voltage[1]"
        field={state.fields["output_voltage[1]"]}
        stale={stale}
      />
    </>
  );
}

function DeviceCard({
  state,
  device,
  source,
  now,
  refMs,
  stale,
  timeZone,
}: CardProps & { source: Source | undefined }) {
  const status = powerStatus(state);
  const lost = isCommLost(state);
  const fieldStale = stale || lost;
  const pending = pendingWindow(state, device?.config, now);
  const elapsed = onBatteryElapsed(state, refMs);
  const multiPhase = phaseCount(state.fields, "output_phases") > 1;
  const loadLabel = multiPhase ? "负载 L1" : "负载";
  const tone = status.card === "normal" ? "" : ` tone-${status.card}`;

  const stat = (label: string, key: string, extra?: string) => (
    <div key={key} className={extra}>
      <dt>{label}</dt>
      <dd>
        <FieldValue
          fieldKey={key}
          field={state.fields[key]}
          stale={fieldStale}
          runtimeSource={state.runtime_source}
        />
      </dd>
    </div>
  );

  let stats;
  if (lost) {
    stats = (
      <>
        <div>
          <dt>供电状态</dt>
          <dd className="is-stale" style={{ fontSize: "0.875rem" }}>
            {heldPowerText(state)}
          </dd>
        </div>
        {stat("剩余时间", "runtime_estimate")}
        {stat(loadLabel, "output_load_pct[1]")}
        <div>
          <dt>输入 / 输出</dt>
          <dd>
            <InOut state={state} stale />
          </dd>
        </div>
      </>
    );
  } else if (isOnBattery(state)) {
    stats = (
      <>
        {stat("预计剩余", "runtime_estimate")}
        <div>
          <dt>已放电</dt>
          <dd className={stale ? "is-stale" : undefined}>
            {elapsed === null ? "—" : clockText(elapsed)}
          </dd>
        </div>
        {stat(loadLabel, "output_load_pct[1]")}
        <div>
          <dt>输入 / 输出</dt>
          <dd>
            <InOut state={state} stale={stale} />
          </dd>
        </div>
      </>
    );
  } else {
    stats = (
      <>
        {pending ? (
          <div className="span-2">
            <dt>{pending.kind === "outage" ? "停电确认中" : "市电恢复确认中"}</dt>
            <dd className="pending-meter">
              <Meter fraction={pending.elapsedS / Math.max(1, pending.totalS)} label="确认进度" />
              {pending.elapsedS} / {pending.totalS} 秒
            </dd>
          </div>
        ) : null}
        {stat(loadLabel, "output_load_pct[1]")}
        {pending ? null : stat("温度", "temperature")}
        {pending ? null : stat("剩余时间", "runtime_estimate")}
        <div>
          <dt>输入 / 输出</dt>
          <dd>
            <InOut state={state} stale={stale} />
          </dd>
        </div>
      </>
    );
  }

  const ago = lost ? null : relativeTime(state.at, now);
  return (
    <article className={`card dev${tone}`}>
      <div className="dev-top">
        <div>
          <Link className="dev-link dev-name" to={`/devices/${state.device_id}`}>
            {device?.name ?? `设备 #${state.device_id}`}
          </Link>
          {device?.location ? <div className="dev-loc">{device.location}</div> : null}
        </div>
        <PowerPill state={state} />
      </div>
      <div className="dev-mid">
        <CapacityRing
          state={state}
          stale={fieldStale}
          size={84}
          stroke={7}
          caption={lost ? (relativeTime(state.last_ok_at, now) ?? "电池") : "电池"}
          numClassName="ring-num"
          captionClassName="ring-cap"
        />
        <dl className="dev-stats">{stats}</dl>
      </div>
      <div className="dev-foot">
        <span>
          {source
            ? `${protocolLabel(source)} · ${source.profile_key}${source.status?.failures ? ` · 连续失败 ${source.status.failures} 次` : ""}`
            : "数据源 —"}
        </span>
        {lost ? (
          <span className="ink-orange">最后成功 {formatTime(state.last_ok_at, timeZone)}</span>
        ) : (
          <span>{ago ?? formatDateTime(state.at, timeZone)}</span>
        )}
      </div>
    </article>
  );
}

function PlanRunBody({ run, now, stale }: { run: PlanRun; now: number; stale: boolean }) {
  const remaining = countdownRemaining(run, now);
  const [cancelOpen, setCancelOpen] = useState(false);
  return (
    <div style={{ minWidth: 0 }}>
      <div className="t-subhead strong hstack wrap gap-1">
        关机计划「{run.plan_name}」{run.dry_run ? <Badge tone="indigo">Dry-run</Badge> : null}
      </div>
      <div className="t-footnote muted mt-2">
        {remaining !== null
          ? `倒计时 ${clockText(remaining)} 后开始执行。`
          : `${runProgressText(run)}。`}
        {stale ? "实时连接已断开，状态可能已变化。" : ""}
      </div>
      <div className="hstack mt-3">
        <Link className="btn btn-plain btn-sm" to={planRunPath(run)}>
          查看进度
        </Link>
        {/* 运行列表只含活动运行（countdown / running），因此总能取消。 */}
        <button
          type="button"
          className="btn btn-danger-tinted btn-sm"
          onClick={() => setCancelOpen(true)}
        >
          {run.status === "countdown" ? "取消运行…" : "取消未开始的步骤…"}
        </button>
      </div>
      <CancelRunDialog run={run} open={cancelOpen} onClose={() => setCancelOpen(false)} />
    </div>
  );
}

function PlanRunVisual({ run, now }: { run: PlanRun; now: number }) {
  const remaining = countdownRemaining(run, now);
  const fraction = countdownFraction(run, now);
  if (remaining !== null && fraction !== null) {
    return <CountdownRing fraction={fraction} text={clockText(remaining)} />;
  }
  return (
    <span className="oh-icon" style={{ marginBottom: 0 }}>
      <Icon name="power" />
    </span>
  );
}

/** 不属于停电主卡的活动运行（手动执行或其他设备）。 */
function PlanStrip({
  run,
  deviceName,
  now,
  stale,
}: {
  run: PlanRun;
  deviceName: string | null;
  now: number;
  stale: boolean;
}) {
  return (
    <section className="card plan-strip" aria-label={`关机计划「${run.plan_name}」`}>
      <PlanRunVisual run={run} now={now} />
      <div style={{ minWidth: 0, flex: 1 }}>
        {deviceName ? <div className="eyebrow ink-orange">关联设备 · {deviceName}</div> : null}
        <PlanRunBody run={run} now={now} stale={stale} />
      </div>
    </section>
  );
}

function OutageCard({
  state,
  device,
  run,
  now,
  refMs,
  stale,
  timeZone,
}: CardProps & { run: PlanRun | undefined }) {
  const name = device?.name ?? `设备 #${state.device_id}`;
  const elapsed = onBatteryElapsed(state, refMs);
  const fieldStale = stale || isCommLost(state);
  const pending = pendingWindow(state, device?.config, now);
  return (
    <section className="card outage" aria-label="停电中">
      <div>
        <div className="oh-icon">
          <Icon name="battery" />
        </div>
        <div className="eyebrow ink-orange">停电中 · 停电周期 #{state.outage_cycle_id}</div>
        <h2 className="t-title2">
          <Link to={`/devices/${state.device_id}`} style={{ color: "inherit" }}>
            {name}
          </Link>{" "}
          {isOnBattery(state) ? "正在使用电池供电" : "停电周期进行中"}
        </h2>
        <p className="t-subhead muted mt-2">
          {state.on_battery_since
            ? `${formatTime(state.on_battery_since, timeZone)} 起由电池供电。`
            : null}
          {elapsed !== null ? (
            <>
              已放电 <b className="tnum">{clockText(elapsed)}</b>，计时在服务重启后延续。
            </>
          ) : null}
          {pending?.kind === "recovery"
            ? ` 市电已恢复，确认中 ${pending.elapsedS} / ${pending.totalS} 秒。`
            : null}
          {isCommLost(state) ? " 通讯中断，以下为最后读数。" : null}
        </p>
      </div>
      <div className="oh-metrics">
        <Metric
          fieldKey="battery_capacity_pct"
          field={state.fields.battery_capacity_pct}
          stale={fieldStale}
        />
        <Metric
          label="预计剩余"
          fieldKey="runtime_estimate"
          field={state.fields.runtime_estimate}
          stale={fieldStale}
          runtimeSource={state.runtime_source}
        />
        <Metric
          label="负载"
          fieldKey="output_load_pct[1]"
          field={state.fields["output_load_pct[1]"]}
          stale={fieldStale}
        />
        <Metric
          label="输出电压"
          fieldKey="output_voltage[1]"
          field={state.fields["output_voltage[1]"]}
          stale={fieldStale}
        />
      </div>
      <div className="oh-plan">
        {run ? (
          <>
            <PlanRunVisual run={run} now={now} />
            <PlanRunBody run={run} now={now} stale={stale} />
          </>
        ) : (
          <div className="t-footnote muted">当前没有与该设备关联的关机计划运行。</div>
        )}
      </div>
    </section>
  );
}

function AlertRow({
  alert,
  deviceName,
  now,
  timeZone,
}: {
  alert: EventItem;
  deviceName: string;
  now: number;
  timeZone: string | undefined;
}) {
  const duration = eventDurationSeconds(alert, now);
  const sub = [
    `${formatTime(alert.started_at, timeZone)} 起`,
    duration !== null ? `持续 ${shortDuration(duration)}` : "",
    summaryText(alert.summary),
  ]
    .filter(Boolean)
    .join(" · ");
  return (
    <Link className="row is-link alert-row" to="/alerts">
      <span className={`sev sev-${alert.severity}`}>
        <Icon name={eventIcon(alert.type)} />
      </span>
      <div className="row-main">
        <div className="row-title">
          <b>{eventLabel(alert.type)}</b> · {deviceName}
        </div>
        <div className="row-sub">{sub}</div>
      </div>
      <Pill tone={SEVERITY_PILL_TONE[alert.severity]} size="sm">
        {SEVERITY_TEXT[alert.severity]}
      </Pill>
      <Icon name="chev-right" className="row-chevron" />
    </Link>
  );
}
