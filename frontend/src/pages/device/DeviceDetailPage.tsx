import { Link, useParams, useSearchParams } from "react-router";
import { isApiError } from "../../api/client.ts";
import { useDevice, useDeviceSources, useTimeZone } from "../../api/hooks.ts";
import type { Device, DeviceState, PlanRun } from "../../api/types.ts";
import { Badge } from "../../components/Badge.tsx";
import { Banner } from "../../components/Banner.tsx";
import { Button } from "../../components/Button.tsx";
import { Empty } from "../../components/Empty.tsx";
import { Metric } from "../../components/Metric.tsx";
import { Segmented } from "../../components/Segmented.tsx";
import { Spinner } from "../../components/Spinner.tsx";
import { Icon } from "../../icons/Icon.tsx";
import { Page } from "../../layout/Page.tsx";
import { useLive } from "../../live/hooks.ts";
import {
  COMM_LOST_POLICY_TEXT,
  heldPowerText,
  isCommLost,
  isOnBattery,
  onBatteryElapsed,
  pendingWindow,
  phaseCount,
  powerStatus,
  primarySource,
  relativeTime,
} from "../../model/device.ts";
import { clockText, fieldText } from "../../model/format.ts";
import { countdownRemaining, planRunPath, runProgressText } from "../../model/plan.ts";
import { formatTime } from "../../model/time.ts";
import { useLiveClock } from "../monitor/hooks.ts";
import { CapacityRing, PowerPill } from "../monitor/shared.tsx";
import { EventsTab } from "./EventsTab.tsx";
import { HistoryTab } from "./HistoryTab.tsx";
import { LiveTab } from "./LiveTab.tsx";
import { SourcesTab } from "./SourcesTab.tsx";
import "./device.css";

type Tab = "live" | "history" | "events" | "sources";
const TABS: readonly { value: Tab; label: string }[] = [
  { value: "live", label: "实时数据" },
  { value: "history", label: "历史" },
  { value: "events", label: "事件" },
  { value: "sources", label: "数据源与诊断" },
];

function isTab(value: string | null): value is Tab {
  return TABS.some((t) => t.value === value);
}

const EMPTY_FIELDS = {};

/** “ET1K · 1000 VA / 900 W · 单相”：只拼接 good 字段，缺失的部分省略。 */
function identityLine(state: DeviceState | null): string {
  if (!state) return "";
  const f = state.fields;
  const good = (key: string) => (f[key]?.quality === "good" ? fieldText(key, f[key]) : "");
  const rating = [good("rated_va"), good("rated_w")].filter(Boolean).join(" / ");
  const phases = f.output_phases?.quality === "good" ? phaseCount(f, "output_phases") : 0;
  return [good("model"), rating, phases === 0 ? "" : phases > 1 ? `${phases} 相` : "单相"]
    .filter(Boolean)
    .join(" · ");
}

/**
 * 设备详情（F2-2～F2-5，device-detail.html）：hero、失联与计划横幅、四个页内视图。
 * 页内视图写入 `?tab=`；运行数据来自 SSE 实时存储，设备记录与数据源来自查询 API。
 */
export function DeviceDetailPage() {
  const params = useParams();
  const deviceId = Number(params.id);
  const valid = Number.isInteger(deviceId) && deviceId > 0;
  const [search, setSearch] = useSearchParams();
  const tab: Tab = isTab(search.get("tab")) ? (search.get("tab") as Tab) : "live";
  const setTab = (next: Tab) => {
    const p = new URLSearchParams(search);
    p.set("tab", next);
    // 切换视图时去掉其他视图的参数，避免 URL 越积越多。
    if (next !== "history") {
      p.delete("range");
      p.delete("fields");
    }
    setSearch(p, { replace: true });
  };

  const timeZone = useTimeZone();
  const { now, refMs, stale } = useLiveClock();
  const device = useDevice(deviceId);
  const state = useLive((s) => s.data.devices.find((d) => d.device_id === deviceId) ?? null);
  const run = useLive((s) => s.data.planRuns.find((r) => r.device_id === deviceId) ?? null);
  const synced = useLive((s) => s.data.syncedAt !== null);

  const notFound =
    !valid || (device.isError && isApiError(device.error) && device.error.status === 404);
  const name = device.data?.name ?? (notFound ? "设备不存在" : `设备 #${valid ? deviceId : ""}`);

  if (notFound) {
    return (
      <Page title="设备不存在" back={{ to: "/", label: "总览" }}>
        <div className="card">
          <Empty
            icon="question"
            tone="info"
            title="找不到这台设备"
            action={
              <Link className="btn btn-gray btn-sm" to="/devices">
                返回设备列表
              </Link>
            }
          >
            设备可能已被删除，或链接中的设备编号有误。
          </Empty>
        </div>
      </Page>
    );
  }

  return (
    <Page
      title={name}
      eyebrow="设备"
      back={{ to: "/", label: "总览" }}
      toolbar={
        device.data ? (
          <Link className="btn btn-gray btn-sm" to={`/devices?edit=${deviceId}`}>
            <Icon name="pencil" />
            编辑
          </Link>
        ) : null
      }
      headAside={
        state && tab === "live" ? (
          <span className="t-footnote faint">
            schema v{state.schema_version} · {formatTime(state.at, timeZone)} 采集
          </span>
        ) : null
      }
    >
      {device.isError ? (
        <Banner
          tone="critical"
          role="alert"
          title="设备信息加载失败"
          style={{ marginBottom: 16 }}
          actions={
            <Button size="sm" onClick={() => void device.refetch()}>
              重试
            </Button>
          }
        >
          {device.error.message}
        </Banner>
      ) : null}

      {state ? (
        <Hero state={state} device={device.data} stale={stale} refMs={refMs} now={now} />
      ) : (
        <NoLiveState synced={synced} device={device.data} />
      )}

      {state && isCommLost(state) ? (
        <LostBanner
          state={state}
          device={device.data}
          deviceId={deviceId}
          now={now}
          timeZone={timeZone}
          onTest={() => setTab("sources")}
        />
      ) : null}
      {run ? <PlanBanner run={run} now={now} stale={stale} /> : null}

      <div className="tab-bar mt-6">
        <Segmented<Tab>
          size="lg"
          aria-label="设备视图"
          value={tab}
          onChange={setTab}
          options={[...TABS]}
        />
      </div>

      {tab === "live" ? (
        state ? (
          <LiveTab state={state} stale={stale} refMs={refMs} />
        ) : (
          <div className="card mt-4">
            <Empty icon="info" tone="info" title="没有实时数据">
              设备停用或尚未完成首次采集时没有运行快照；可在「历史」「事件」中查看已有记录。
            </Empty>
          </div>
        )
      ) : null}
      {tab === "history" ? (
        <HistoryTab deviceId={deviceId} fields={state?.fields ?? EMPTY_FIELDS} />
      ) : null}
      {tab === "events" ? <EventsTab deviceId={deviceId} /> : null}
      {tab === "sources" ? <SourcesTab deviceId={deviceId} /> : null}
    </Page>
  );
}

function NoLiveState({ synced, device }: { synced: boolean; device: Device | undefined }) {
  if (!synced) {
    return (
      <div className="card">
        <Empty title="正在同步实时数据…">
          <Spinner />
        </Empty>
      </div>
    );
  }
  return (
    <Banner
      tone="neutral"
      icon="slash-circle"
      title={device && !device.enabled ? "设备已停用" : "暂无运行快照"}
    >
      {device && !device.enabled
        ? "停用的设备不采集数据，也不参与状态判断；历史与事件仍可查看。"
        : "设备尚未完成首次采集，或没有启用的数据源。"}
    </Banner>
  );
}

function Hero({
  state,
  device,
  stale,
  refMs,
  now,
}: {
  state: DeviceState;
  device: Device | undefined;
  stale: boolean;
  refMs: number;
  now: number;
}) {
  const status = powerStatus(state);
  const lost = isCommLost(state);
  const fieldStale = stale || lost;
  const elapsed = onBatteryElapsed(state, refMs);
  const pending = pendingWindow(state, device?.config, now);
  const identity = identityLine(state);
  const suffix = lost
    ? heldPowerText(state)
    : isOnBattery(state) && elapsed !== null
      ? clockText(elapsed)
      : pending
        ? `${pending.elapsedS} / ${pending.totalS} 秒`
        : undefined;
  const tone = status.card === "normal" ? "" : ` tone-${status.card}`;

  return (
    <section className={`card dd-hero${tone}`} aria-label="设备概况">
      <CapacityRing
        state={state}
        stale={fieldStale}
        size={148}
        stroke={12}
        caption="电池容量"
        numClassName="dd-hero-num"
        captionClassName="t-caption faint mt-2"
      />
      <div style={{ minWidth: 0 }}>
        <div className="dd-hero-top">
          <div className="t-subhead muted hstack wrap gap-3">
            {device?.location ? (
              <span className="hstack gap-1">
                <Icon name="pin" />
                {device.location}
              </span>
            ) : null}
            {identity ? <span>{identity}</span> : null}
            {device?.note ? <span>{device.note}</span> : null}
          </div>
          <div className="dd-hero-side">
            <PowerPill state={state} suffix={suffix} />
            {device && device.tags.length > 0 ? (
              <div className="tags">
                {device.tags.map((tag) => (
                  <Badge key={tag} tone="outline">
                    {tag}
                  </Badge>
                ))}
              </div>
            ) : null}
          </div>
        </div>
        <div className="dd-hero-metrics">
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
            label="输出"
            fieldKey="output_voltage[1]"
            field={state.fields["output_voltage[1]"]}
            stale={fieldStale}
          />
          <Metric
            label="输入"
            fieldKey="input_voltage[1]"
            field={state.fields["input_voltage[1]"]}
            stale={fieldStale}
          />
        </div>
      </div>
    </section>
  );
}

function LostBanner({
  state,
  device,
  deviceId,
  now,
  timeZone,
  onTest,
}: {
  state: DeviceState;
  device: Device | undefined;
  deviceId: number;
  now: number;
  timeZone: string | undefined;
  onTest: () => void;
}) {
  const sources = useDeviceSources(deviceId);
  const failures = primarySource(sources.data)?.status?.failures ?? 0;
  const ago = relativeTime(state.last_ok_at, now);
  const policy = device ? COMM_LOST_POLICY_TEXT[device.config.comm_lost_policy] : null;
  return (
    <Banner
      tone="warning"
      icon="wifi-off"
      role="status"
      className="mt-4"
      title={failures > 0 ? `通讯中断 · 已连续 ${failures} 次采集失败` : "通讯中断"}
      actions={
        <Button size="sm" onClick={onTest}>
          测试连接
        </Button>
      }
    >
      {state.last_ok_at
        ? `最后成功采集于 ${formatTime(state.last_ok_at, timeZone)}${ago ? `（${ago}）` : ""}。`
        : "尚未成功采集。"}
      下方数值均为该时刻的旧值，已置灰。
      {policy ? `失联策略：${policy}。` : ""}
    </Banner>
  );
}

function PlanBanner({ run, now, stale }: { run: PlanRun; now: number; stale: boolean }) {
  const remaining = countdownRemaining(run, now);
  return (
    <Banner
      icon="power"
      className="mt-4"
      title={
        <>
          关机计划「{run.plan_name}」
          {remaining !== null ? `正在倒计时 ${clockText(remaining)}` : "正在执行"}
          {run.dry_run ? (
            <>
              {" "}
              <Badge tone="indigo">Dry-run</Badge>
            </>
          ) : null}
        </>
      }
      actions={
        <Link className="btn btn-tinted btn-sm" to={planRunPath(run)}>
          查看进度
        </Link>
      }
    >
      {runProgressText(run)}
      {run.outage_cycle_id !== null ? ` · 停电周期 #${run.outage_cycle_id}` : ""}
      {stale ? " · 实时连接已断开，状态可能已变化" : ""}
    </Banner>
  );
}
