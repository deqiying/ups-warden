import { useQuery } from "@tanstack/react-query";
import { useId, useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import { getHistory } from "../../api/devices.ts";
import { listEvents } from "../../api/events.ts";
import { queryKeys, useTimeZone } from "../../api/hooks.ts";
import type { FieldMap } from "../../api/types.ts";
import { Banner } from "../../components/Banner.tsx";
import { Button } from "../../components/Button.tsx";
import { Empty } from "../../components/Empty.tsx";
import { Segmented } from "../../components/Segmented.tsx";
import { Spinner } from "../../components/Spinner.tsx";
import { fieldLabel } from "../../model/fields.ts";
import { formatNumber } from "../../model/format.ts";
import { formatTime } from "../../model/time.ts";
import { HistoryChart } from "./HistoryChart.tsx";
import {
  buildSeries,
  DEFAULT_HISTORY_FIELDS,
  groupByUnit,
  HISTORY_RANGES,
  historyFieldOptions,
  historyUnitLabel,
  MAX_HISTORY_FIELDS,
  MAX_POINTS,
  outageIntervals,
  sanitizeHistoryFields,
  seriesStats,
  type HistoryRange,
} from "./history.ts";

const OUTAGE_EVENT_LIMIT = 100;

function isRange(value: string | null): value is HistoryRange {
  return HISTORY_RANGES.some((r) => r.value === value);
}

/** 设备详情「历史」（F2-3）：时间范围、字段选择、自写 SVG 折线与停电区间阴影。 */
export function HistoryTab({ deviceId, fields }: { deviceId: number; fields: FieldMap }) {
  const timeZone = useTimeZone();
  const [params, setParams] = useSearchParams();
  const range: HistoryRange = isRange(params.get("range"))
    ? (params.get("range") as HistoryRange)
    : "1h";
  const rangeMs = HISTORY_RANGES.find((r) => r.value === range)?.ms ?? 3600_000;
  const options = useMemo(() => historyFieldOptions(fields), [fields]);
  const rawFields = params.get("fields");
  const selected = useMemo(() => {
    const parsed = rawFields ? sanitizeHistoryFields(rawFields.split(","), options) : [];
    return parsed.length > 0 ? parsed : [...DEFAULT_HISTORY_FIELDS];
  }, [rawFields, options]);

  // 查询区间的终点：切换范围、字段或点“刷新”时更新，保持查询键稳定。
  const [anchor, setAnchor] = useState(() => Date.now());
  const from = anchor - rangeMs;
  const fromIso = new Date(from).toISOString();
  const toIso = new Date(anchor).toISOString();

  const update = (next: { range?: HistoryRange; fields?: string[] }) => {
    const p = new URLSearchParams(params);
    if (next.range) p.set("range", next.range);
    if (next.fields) p.set("fields", next.fields.join(","));
    setParams(p, { replace: true });
    setAnchor(Date.now());
  };

  const history = useQuery({
    queryKey: [...queryKeys.history, deviceId, fromIso, toIso, selected],
    queryFn: ({ signal }) =>
      getHistory(
        deviceId,
        { from: fromIso, to: toIso, fields: selected, resolution: "auto", maxPoints: MAX_POINTS },
        signal,
      ),
  });

  // 停电区间：mains_lost 可能早于 from 开始，因此不按 from 过滤，只限制 to 后在前端求交集。
  const outageEvents = useQuery({
    queryKey: [...queryKeys.events, "outage", deviceId, toIso],
    queryFn: async ({ signal }) => {
      const [lost, restored] = await Promise.all([
        listEvents(
          { device_id: deviceId, type: "mains_lost", to: toIso, limit: OUTAGE_EVENT_LIMIT },
          signal,
        ),
        listEvents(
          {
            device_id: deviceId,
            type: "mains_restored",
            from: fromIso,
            to: toIso,
            limit: OUTAGE_EVENT_LIMIT,
          },
          signal,
        ),
      ]);
      return { lost: lost.items, restored: restored.items };
    },
  });

  const series = useMemo(
    () => (history.data ? buildSeries(history.data, selected, rangeMs) : []),
    [history.data, selected, rangeMs],
  );
  const outages = useMemo(
    () =>
      outageEvents.data
        ? outageIntervals(outageEvents.data.lost, outageEvents.data.restored, from, anchor, anchor)
        : [],
    [outageEvents.data, from, anchor],
  );
  const hasData = series.some((s) => s.segments.length > 0);
  const band = history.data?.resolution === "1m";
  const rangeLabel = HISTORY_RANGES.find((r) => r.value === range)?.label ?? "";

  return (
    <div className="mt-4">
      <section className="card">
        <div className="card-head" style={{ flexWrap: "wrap" }}>
          <div>
            <h3 className="card-title">历史曲线</h3>
            <div className="card-sub">
              过去 {rangeLabel} ·{" "}
              {history.data
                ? history.data.resolution === "raw"
                  ? "原始样本"
                  : "1 分钟聚合（min / max / avg）"
                : "加载中"}
              {" · "}截至 {formatTime(anchor, timeZone)}
            </div>
          </div>
          <div className="hstack wrap">
            <Segmented<HistoryRange>
              aria-label="时间范围"
              value={range}
              onChange={(value) => update({ range: value })}
              options={HISTORY_RANGES.map((r) => ({ value: r.value, label: r.label }))}
            />
            <FieldPicker
              options={options}
              selected={selected}
              onChange={(next) => update({ fields: next })}
            />
            <Button size="sm" icon="refresh" onClick={() => setAnchor(Date.now())}>
              刷新
            </Button>
          </div>
        </div>
        <div className="chart-wrap">
          {history.isPending ? (
            <Empty title="正在加载历史数据…">
              <Spinner />
            </Empty>
          ) : history.isError ? (
            <Banner
              tone="critical"
              title="历史数据加载失败"
              role="alert"
              actions={
                <Button size="sm" onClick={() => void history.refetch()}>
                  重试
                </Button>
              }
            >
              {history.error.message}
            </Banner>
          ) : !hasData ? (
            <Empty icon="chart" tone="info" title="该时间范围内没有历史数据">
              只记录质量正常的数值；设备停用、通讯中断或服务停机期间没有采样。
            </Empty>
          ) : (
            <>
              <div className="legend mt-3">
                {series.map((s) => (
                  <span key={s.key}>
                    <i style={{ background: s.color }} />
                    {s.label}
                    {s.unit ? ` ${historyUnitLabel(s.unit)}` : ""}
                  </span>
                ))}
                {outages.length > 0 ? (
                  <span>
                    <i className="legend-outage" />
                    停电区间
                  </span>
                ) : null}
              </div>
              {groupByUnit(series).map((group) => (
                <HistoryChart
                  key={group[0].unit}
                  series={group}
                  from={from}
                  to={anchor}
                  band={band}
                  outages={outages}
                  timeZone={timeZone}
                  label={`过去 ${rangeLabel}：${group
                    .map((s) => {
                      const st = seriesStats(s);
                      return st
                        ? `${s.label} ${formatNumber(st.min, s.unit)}～${formatNumber(st.max, s.unit)} ${historyUnitLabel(s.unit)}`
                        : `${s.label} 无数据`;
                    })
                    .join("；")}`}
                />
              ))}
            </>
          )}
          {outageEvents.isError ? (
            <p className="t-footnote muted">停电区间加载失败，图中未标出停电时段。</p>
          ) : null}
        </div>
      </section>
      {hasData ? (
        <div className="grid cols-3 mt-4">
          {series.map((s) => {
            const st = seriesStats(s);
            const unit = historyUnitLabel(s.unit);
            return (
              <section key={s.key} className="card card-pad">
                <div className="metric-label">
                  {s.label} · {rangeLabel}
                </div>
                {st ? (
                  <>
                    <div className="metric-value mt-2">
                      {formatNumber(st.min, s.unit)} – {formatNumber(st.max, s.unit)}
                      {unit ? <small>{unit}</small> : null}
                    </div>
                    <div className="t-footnote muted mt-2">
                      平均 {formatNumber(st.avg, s.unit)}
                      {unit}
                    </div>
                  </>
                ) : (
                  <div className="metric-value na mt-2">—</div>
                )}
              </section>
            );
          })}
        </div>
      ) : null}
      <p className="list-caption">
        范围不超过 6 小时且在明细保留期内时使用原始样本，否则使用 1 分钟聚合；超过 {MAX_POINTS}{" "}
        点时由服务端降采样。只记录质量正常的数值，通讯中断与服务停机期间不连线。
      </p>
    </div>
  );
}

/** 字段选择：只列 History 字段的实例，最多选 {@link MAX_HISTORY_FIELDS} 个。 */
function FieldPicker({
  options,
  selected,
  onChange,
}: {
  options: readonly string[];
  selected: readonly string[];
  onChange: (next: string[]) => void;
}) {
  const [open, setOpen] = useState(false);
  const id = useId();
  const toggle = (key: string) => {
    if (selected.includes(key)) {
      if (selected.length > 1) onChange(selected.filter((k) => k !== key));
    } else if (selected.length < MAX_HISTORY_FIELDS) {
      onChange([...selected, key]);
    }
  };
  return (
    <div className="field-picker">
      <Button size="sm" aria-expanded={open} aria-controls={id} onClick={() => setOpen((v) => !v)}>
        字段（{selected.length}）
      </Button>
      {open ? (
        <fieldset
          id={id}
          className="popover field-picker-pop"
          onKeyDown={(event) => {
            if (event.key === "Escape") setOpen(false);
          }}
        >
          <legend className="sr-only">历史字段，最多 {MAX_HISTORY_FIELDS} 个</legend>
          {options.map((key) => {
            const checked = selected.includes(key);
            const disabled =
              (!checked && selected.length >= MAX_HISTORY_FIELDS) ||
              (checked && selected.length === 1);
            return (
              <label key={key} className="menu-item">
                <input
                  type="checkbox"
                  checked={checked}
                  disabled={disabled}
                  onChange={() => toggle(key)}
                />
                {fieldLabel(key)}
                <span className="spacer" />
                <span className="faint mono t-caption">{key}</span>
              </label>
            );
          })}
          <div className="hstack mt-2" style={{ justifyContent: "flex-end" }}>
            <Button size="sm" variant="plain" onClick={() => setOpen(false)}>
              完成
            </Button>
          </div>
        </fieldset>
      ) : null}
    </div>
  );
}
