import type { ReactNode } from "react";
import type { DeviceState, FieldMap } from "../../api/types.ts";
import { FieldValue } from "../../components/Metric.tsx";
import { Pill, type PillTone } from "../../components/Pill.tsx";
import { ReasonTip } from "../../components/ReasonTip.tsx";
import { fieldLabel, lookupField, phaseKeys } from "../../model/fields.ts";
import { clockText, formatField } from "../../model/format.ts";
import { onBatteryElapsed, phaseCount } from "../../model/device.ts";

interface FieldItem {
  key: string;
  label?: string;
}

interface LiveProps {
  state: DeviceState;
  /** 实时连接断开：数值保留但置灰。 */
  stale: boolean;
  /** 放电计时的参考时刻（断线时停在断开时刻）。 */
  refMs: number;
}

/** 字段网格：标签在上、值在下；非 good 字段显示「—」并在下方给出原因。 */
function FieldGrid({
  fields,
  items,
  stale,
  runtimeSource,
  children,
}: {
  fields: FieldMap;
  items: readonly FieldItem[];
  stale: boolean;
  runtimeSource?: string;
  /** 排在字段前的自定义项（如本地计算的放电计时）。 */
  children?: ReactNode;
}) {
  return (
    <dl className="fields mt-2">
      {children}
      {items.map(({ key, label }) => (
        <div className="fld" key={key}>
          <dt>{label ?? fieldLabel(key)}</dt>
          <dd
            className={
              lookupField(key)?.kind === "text" || lookupField(key)?.kind === "enum"
                ? "is-text"
                : undefined
            }
          >
            <FieldValue
              fieldKey={key}
              field={fields[key]}
              stale={stale}
              runtimeSource={runtimeSource}
              reasonMode="inline"
            />
          </dd>
        </div>
      ))}
    </dl>
  );
}

/** 按相表格：列数按相数展开；非按相字段只在 L1 列显示。键整体缺失的行不显示。 */
function PhaseTable({
  fields,
  rows,
  phases,
  stale,
}: {
  fields: FieldMap;
  rows: readonly string[];
  phases: number;
  stale: boolean;
}) {
  const visible = rows.filter((base) => {
    const spec = lookupField(base);
    const keys = spec?.phased ? phaseKeys(base, phases) : [base];
    return keys.some((k) => fields[k] !== undefined);
  });
  if (visible.length === 0)
    return <p className="card-body t-footnote muted">设备未提供这些字段。</p>;
  const cols = Array.from({ length: phases }, (_, i) => i + 1);
  return (
    <table className="phase-table">
      <thead>
        <tr>
          <th scope="col">
            <span className="sr-only">字段</span>
          </th>
          {cols.map((p) => (
            <th key={p} scope="col" className="num">
              L{p}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {visible.map((base) => {
          const spec = lookupField(base);
          return (
            <tr key={base}>
              <th scope="row">{spec?.label ?? base}</th>
              {cols.map((p) => {
                const key = spec?.phased ? `${base}[${p}]` : base;
                return (
                  <td key={p} className="num">
                    {spec?.phased || p === 1 ? (
                      <FieldValue fieldKey={key} field={fields[key]} stale={stale} />
                    ) : null}
                  </td>
                );
              })}
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

/** 状态标志：true 为告警语义的标志（低电量、过载、正在关机）用橙色，市电正常 true 为绿色。 */
const FLAG_TONES: Readonly<Record<string, { yes: PillTone; no: PillTone }>> = {
  mains_ok: { yes: "green", no: "orange" },
  low_battery: { yes: "orange", no: "gray" },
  overload: { yes: "orange", no: "gray" },
  shutting_down: { yes: "red", no: "gray" },
  buzzer_muted: { yes: "gray", no: "gray" },
};

function FlagValue({
  fieldKey,
  fields,
  stale,
}: {
  fieldKey: string;
  fields: FieldMap;
  stale: boolean;
}) {
  const field = fields[fieldKey];
  const display = formatField(fieldKey, field, { stale });
  if (display.kind === "na") {
    return (
      <ReasonTip reason={display.reason} label={`${fieldLabel(fieldKey)}无数据`} className="na" />
    );
  }
  const tones = FLAG_TONES[fieldKey];
  if (!tones || !field)
    return <span className={display.stale ? "is-stale" : "muted"}>{display.text}</span>;
  return (
    <Pill size="sm" tone={display.stale ? "gray" : field.bool ? tones.yes : tones.no}>
      {display.text}
    </Pill>
  );
}

const BATTERY_ITEMS: readonly FieldItem[] = [
  { key: "battery_capacity_pct", label: "容量" },
  { key: "battery_voltage", label: "电压" },
  { key: "temperature", label: "温度" },
  { key: "runtime_estimate", label: "剩余时间" },
  { key: "battery_current", label: "电池电流" },
  { key: "native_runtime_remaining", label: "设备上报剩余" },
];

const IDENTITY_ITEMS: readonly FieldItem[] = [
  { key: "model" },
  { key: "manufacturer" },
  { key: "firmware" },
  { key: "serial" },
  { key: "agent_version" },
  { key: "rated_va" },
  { key: "rated_w" },
  { key: "nominal_input_voltage" },
  { key: "nominal_output_voltage" },
  { key: "nominal_input_freq" },
  { key: "nominal_output_freq" },
  { key: "nominal_battery_voltage" },
];

const FLAG_KEYS = [
  "mains_ok",
  "low_battery",
  "overload",
  "shutting_down",
  "buzzer_muted",
  "ups_type",
  "self_test_result",
];

/** 设备详情「实时」（F2-2）：电池、输出、输入与旁路、状态标志、额定与身份。 */
export function LiveTab({ state, stale, refMs }: LiveProps) {
  const { fields } = state;
  const outPhases = phaseCount(fields, "output_phases");
  const inPhases = phaseCount(fields, "input_phases");
  const elapsed = onBatteryElapsed(state, refMs);

  return (
    <div className="mt-4">
      <div className="grid cols-2">
        <section className="card">
          <div className="card-head">
            <h3 className="card-title">电池</h3>
            <span className="card-sub">
              状态：
              <FieldValue fieldKey="battery_status" field={fields.battery_status} stale={stale} />
            </span>
          </div>
          <FieldGrid
            fields={fields}
            items={BATTERY_ITEMS}
            stale={stale}
            runtimeSource={state.runtime_source}
          >
            <div className="fld">
              <dt>已放电</dt>
              <dd>
                {elapsed !== null ? (
                  <span className={stale ? "is-stale" : undefined}>{clockText(elapsed)}</span>
                ) : (
                  <FieldValue
                    fieldKey="on_battery_elapsed"
                    field={fields.on_battery_elapsed}
                    stale={stale}
                    reasonMode="inline"
                  />
                )}
              </dd>
            </div>
          </FieldGrid>
        </section>

        <section className="card">
          <div className="card-head">
            <h3 className="card-title">输出</h3>
            <span className="card-sub">
              来源：
              <FieldValue fieldKey="output_source" field={fields.output_source} stale={stale} />
            </span>
          </div>
          {outPhases > 1 ? (
            <>
              <PhaseTable
                fields={fields}
                rows={[
                  "output_voltage",
                  "output_current",
                  "output_power",
                  "output_load_pct",
                  "output_freq",
                ]}
                phases={outPhases}
                stale={stale}
              />
              <FieldGrid fields={fields} items={[{ key: "ups_mode" }]} stale={stale} />
            </>
          ) : (
            <FieldGrid
              fields={fields}
              stale={stale}
              items={[
                { key: "output_voltage[1]", label: "电压" },
                { key: "output_freq", label: "频率" },
                { key: "output_load_pct[1]", label: "负载" },
                { key: "output_power[1]", label: "功率" },
                { key: "output_current[1]", label: "电流" },
                { key: "ups_mode" },
              ]}
            />
          )}
        </section>

        <section className="card">
          <div className="card-head">
            <h3 className="card-title">输入与旁路</h3>
            <span className="card-sub">{inPhases > 1 ? `${inPhases} 相` : "单相 · 仅 L1"}</span>
          </div>
          <PhaseTable
            fields={fields}
            rows={[
              "input_voltage",
              "input_current",
              "input_power",
              "input_freq",
              "bypass_voltage",
              "bypass_current",
              "bypass_power",
              "bypass_freq",
            ]}
            phases={inPhases}
            stale={stale}
          />
        </section>

        <section className="card">
          <div className="card-head">
            <h3 className="card-title">状态标志</h3>
          </div>
          <div className="flag-grid mt-2">
            {FLAG_KEYS.map((key) => (
              <div className="flag" key={key}>
                <span>{fieldLabel(key)}</span>
                <FlagValue fieldKey={key} fields={fields} stale={stale} />
              </div>
            ))}
          </div>
        </section>
      </div>

      <section className="card mt-4">
        <div className="card-head">
          <h3 className="card-title">额定与身份</h3>
          <span className="card-sub">来自数据源身份信息</span>
        </div>
        <FieldGrid fields={fields} items={IDENTITY_ITEMS} stale={stale} />
      </section>
    </div>
  );
}
