/*
 * 设备向导与编辑浮层共用的表单片段：连接字段、阈值与去抖、连接测试结果。
 * 凭据输入框永不预填，只显示“已设置 / 未设置”（来自 `*_set`）。
 */
import { useId, type ReactNode } from "react";
import { normalizeFields } from "../../api/normalize.ts";
import type { DeviceConfig, ProbeResult, ProfileSummary, Source } from "../../api/types.ts";
import { Banner } from "../../components/Banner.tsx";
import { Input } from "../../components/Input.tsx";
import { FormRow } from "../../components/FormRow.tsx";
import { Metric } from "../../components/Metric.tsx";
import { Segmented } from "../../components/Segmented.tsx";
import { Select } from "../../components/Select.tsx";
import { Stepper } from "../../components/Stepper.tsx";
import { Switch } from "../../components/Switch.tsx";
import { Icon } from "../../icons/Icon.tsx";
import {
  profilesFor,
  protocolMeta,
  secretIsSet,
  type ConfigField,
  type FieldErrors,
  type SourceDraft,
  type SourceField,
} from "./model.ts";

/** 表单网格中的一项：左标签、右控件，下方说明或错误。 */
export function GridField({
  label,
  htmlFor,
  hint,
  error,
  children,
}: {
  label: ReactNode;
  htmlFor?: string;
  hint?: ReactNode;
  error?: string;
  children: ReactNode;
}) {
  return (
    <>
      {htmlFor ? (
        <label htmlFor={htmlFor}>{label}</label>
      ) : (
        <span className="form-label">{label}</span>
      )}
      <div>
        {children}
        {error ? (
          <div className="hint is-error" role="alert">
            {error}
          </div>
        ) : hint ? (
          <div className="hint">{hint}</div>
        ) : null}
      </div>
    </>
  );
}

/** 数字输入（端口、Unit ID 等）：空串视为 NaN，交给校验函数给出提示。 */
function NumberInput({
  id,
  value,
  onChange,
  invalid,
  className,
  placeholder,
}: {
  id: string;
  value: number | null;
  onChange: (next: number | null) => void;
  invalid?: boolean;
  className?: string;
  placeholder?: string;
}) {
  return (
    <Input
      id={id}
      mono
      inputMode="numeric"
      className={className}
      invalid={invalid}
      placeholder={placeholder}
      value={value === null || Number.isNaN(value) ? "" : String(value)}
      onChange={(event) => {
        const text = event.target.value.trim();
        onChange(text === "" ? null : /^\d+$/.test(text) ? Number(text) : Number.NaN);
      }}
    />
  );
}

export interface SourceFieldsProps {
  draft: SourceDraft;
  onChange: (next: SourceDraft) => void;
  profiles: readonly ProfileSummary[] | undefined;
  errors: FieldErrors<SourceField>;
  /** 编辑已有数据源：用于显示凭据“已设置 / 未设置”与清除开关。新建时省略。 */
  saved?: Source;
  /** 是否显示 Profile 选择（向导在测试步骤里选择）。 */
  showProfile?: boolean;
  /** 是否显示轮询 / 超时 / 重试。 */
  showTiming?: boolean;
}

/** 连接字段：主机、端口、协议参数、凭据，以及可选的 Profile 与时序。协议在外部选择。 */
export function SourceFields({
  draft,
  onChange,
  profiles,
  errors,
  saved,
  showProfile = true,
  showTiming = true,
}: SourceFieldsProps) {
  const ids = useId();
  const meta = protocolMeta(draft.protocol);
  const set = <K extends keyof SourceDraft>(key: K, value: SourceDraft[K]) =>
    onChange({ ...draft, [key]: value });
  const candidates = profilesFor(profiles, draft.protocol);
  const isSet = secretIsSet(saved, draft.protocol);
  const editing = saved !== undefined;
  const protocolChanged = editing && saved.protocol !== draft.protocol;

  const secretHint = (() => {
    if (draft.protocol === "snmp") {
      return editing && !protocolChanged
        ? `${isSet ? "已设置" : "未设置（驱动默认 public）"} · 留空表示保留原值`
        : "加密保存，之后只显示「已设置」。留空时驱动使用默认值 public。";
    }
    return editing && !protocolChanged
      ? `${isSet ? "已设置" : "未设置（使用空密码）"} · 留空表示保留原值`
      : "加密保存，之后只显示「已设置」。留空表示设备未设置访问密码。";
  })();

  return (
    <div className="form-grid">
      <GridField label="主机" htmlFor={`${ids}-host`} error={errors.host}>
        <Input
          id={`${ids}-host`}
          mono
          autoComplete="off"
          spellCheck={false}
          placeholder="192.168.1.20 或 ups-a.lan"
          value={draft.host}
          invalid={Boolean(errors.host)}
          onChange={(e) => set("host", e.target.value)}
        />
      </GridField>
      <GridField label="端口" htmlFor={`${ids}-port`} error={errors.port}>
        <NumberInput
          id={`${ids}-port`}
          className="field-short"
          value={draft.port}
          invalid={Boolean(errors.port)}
          onChange={(v) => set("port", v ?? Number.NaN)}
        />
      </GridField>
      {draft.protocol === "snmp" ? (
        <GridField label="SNMP 版本">
          <Segmented<"2c" | "1">
            aria-label="SNMP 版本"
            value={draft.snmpVersion}
            onChange={(v) => set("snmpVersion", v)}
            options={[
              { value: "2c", label: "v2c" },
              { value: "1", label: "v1" },
            ]}
          />
        </GridField>
      ) : null}
      {draft.protocol === "modbus" ? (
        <GridField
          label="Unit ID"
          htmlFor={`${ids}-unit`}
          error={errors.unitId}
          hint="留空沿用 Profile 中的 unit_id"
        >
          <NumberInput
            id={`${ids}-unit`}
            className="field-short"
            value={draft.unitId}
            placeholder="沿用 Profile"
            invalid={Boolean(errors.unitId)}
            onChange={(v) => set("unitId", v)}
          />
        </GridField>
      ) : null}
      {meta.secret ? (
        <GridField label={meta.secretLabel} htmlFor={`${ids}-secret`} hint={secretHint}>
          <div className="hstack wrap gap-3">
            <Input
              id={`${ids}-secret`}
              type="password"
              className="field-mid"
              autoComplete="new-password"
              spellCheck={false}
              value={draft.secret}
              disabled={draft.clearSecret}
              placeholder={editing && isSet && !protocolChanged ? "已设置 · 留空保留" : ""}
              onChange={(e) => set("secret", e.target.value)}
            />
            {editing && isSet && !protocolChanged ? (
              <label className="hstack gap-1 t-footnote">
                <input
                  type="checkbox"
                  checked={draft.clearSecret}
                  onChange={(e) =>
                    onChange({ ...draft, clearSecret: e.target.checked, secret: "" })
                  }
                />
                清除已保存的{meta.secretLabel}
              </label>
            ) : null}
          </div>
        </GridField>
      ) : null}
      {showProfile ? (
        <GridField label="Profile" htmlFor={`${ids}-profile`} error={errors.profile}>
          <Select
            id={`${ids}-profile`}
            value={draft.profileKey}
            onChange={(key) => {
              const p = candidates.find((c) => c.key === key);
              onChange({ ...draft, profileKey: key, profileVersion: p?.version ?? 1 });
            }}
            options={[
              ...(draft.profileKey && !candidates.some((c) => c.key === draft.profileKey)
                ? [{ value: draft.profileKey, label: `${draft.profileKey}（不可用）` }]
                : []),
              ...(draft.profileKey ? [] : [{ value: "", label: "请选择…" }]),
              ...candidates.map((p) => ({
                value: p.key,
                label: `${p.key} · v${p.version}${p.builtin ? "" : "（自定义）"}`,
              })),
            ]}
          />
        </GridField>
      ) : null}
      {showTiming ? (
        <>
          <GridField label="轮询间隔" error={errors.pollMs}>
            <Stepper
              aria-label="轮询间隔（秒）"
              value={draft.pollMs / 1000}
              step={0.5}
              min={0.2}
              max={3600}
              unit="秒"
              onChange={(v) => set("pollMs", Math.round(v * 1000))}
            />
          </GridField>
          <GridField label="单次超时" error={errors.timeoutMs}>
            <Stepper
              aria-label="单次超时（毫秒）"
              value={draft.timeoutMs}
              step={100}
              min={1}
              max={60000}
              unit="毫秒"
              onChange={(v) => set("timeoutMs", v)}
            />
          </GridField>
          <GridField label="重试次数" error={errors.retries}>
            <Stepper
              aria-label="重试次数"
              value={draft.retries}
              min={0}
              max={10}
              unit="次"
              onChange={(v) => set("retries", v)}
            />
          </GridField>
        </>
      ) : null}
    </div>
  );
}

const POLICIES: readonly {
  value: DeviceConfig["comm_lost_policy"];
  title: string;
  description: (c: DeviceConfig) => string;
}[] = [
  {
    value: "hold",
    title: "保持（推荐）",
    description: () => "沿用最后确认的供电状态；如在电池模式，放电计时继续。",
  },
  {
    value: "assume_on_battery_after",
    title: "失联一段时间后视为停电",
    description: (c) => `从最后成功时间起算，当前 ${c.assume_on_battery_after_s} 秒。`,
  },
  {
    value: "ignore",
    title: "仅告警",
    description: () => "显示为未知，不参与关机判定。",
  },
];

/** 阈值与去抖（设备 config）。保存时整体提交。 */
export function ThresholdFields({
  config,
  onChange,
  errors,
}: {
  config: DeviceConfig;
  onChange: (next: DeviceConfig) => void;
  errors: FieldErrors<ConfigField>;
}) {
  const policyId = useId();
  const set = <K extends keyof DeviceConfig>(key: K, value: DeviceConfig[K]) =>
    onChange({ ...config, [key]: value });
  const err = (key: ConfigField) =>
    errors[key] ? (
      <div className="hint is-error" role="alert">
        {errors[key]}
      </div>
    ) : null;
  return (
    <div className="grid cols-2">
      <div>
        <div className="list-header">停电判定</div>
        <div className="list">
          <FormRow label="停电确认时长" description="短于此时长的闪断不产生事件">
            <Stepper
              aria-label="停电确认时长（秒）"
              value={config.outage_confirm_s}
              min={0}
              max={86400}
              unit="秒"
              onChange={(v) => set("outage_confirm_s", v)}
            />
            {err("outage_confirm_s")}
          </FormRow>
          <FormRow label="市电恢复确认" description="恢复后持续该时长才结束停电周期">
            <Stepper
              aria-label="市电恢复确认（秒）"
              value={config.recovery_confirm_s}
              min={0}
              max={86400}
              unit="秒"
              onChange={(v) => set("recovery_confirm_s", v)}
            />
            {err("recovery_confirm_s")}
          </FormRow>
          <FormRow label="输入电压阈值" description="无显式状态时，低于此值视为断电">
            <Stepper
              aria-label="输入电压阈值（V）"
              value={config.input_voltage_threshold}
              min={0}
              max={10000}
              unit="V"
              onChange={(v) => set("input_voltage_threshold", v)}
            />
            {err("input_voltage_threshold")}
          </FormRow>
        </div>
        <div className="list-header mt-4">告警阈值</div>
        <div className="list">
          <FormRow
            label="低电量"
            description={`回差 ${config.battery_hysteresis_pct}%：回升到 ${config.battery_low_pct + config.battery_hysteresis_pct}% 才解除`}
          >
            <Stepper
              aria-label="低电量阈值（%）"
              value={config.battery_low_pct}
              min={0}
              max={100}
              unit="%"
              onChange={(v) => set("battery_low_pct", v)}
            />
            {err("battery_low_pct")}
          </FormRow>
          <FormRow label="低电量回差">
            <Stepper
              aria-label="低电量回差（%）"
              value={config.battery_hysteresis_pct}
              min={0}
              max={100}
              unit="%"
              onChange={(v) => set("battery_hysteresis_pct", v)}
            />
            {err("battery_hysteresis_pct")}
          </FormRow>
          <FormRow
            label="过载"
            description={`回差 ${config.load_hysteresis_pct}%：降到 ${config.overload_pct - config.load_hysteresis_pct}% 才解除`}
          >
            <Stepper
              aria-label="过载阈值（%）"
              value={config.overload_pct}
              min={0}
              max={100}
              unit="%"
              onChange={(v) => set("overload_pct", v)}
            />
            {err("overload_pct")}
          </FormRow>
          <FormRow label="过载回差">
            <Stepper
              aria-label="过载回差（%）"
              value={config.load_hysteresis_pct}
              min={0}
              max={100}
              unit="%"
              onChange={(v) => set("load_hysteresis_pct", v)}
            />
            {err("load_hysteresis_pct")}
          </FormRow>
        </div>
      </div>
      <div>
        <div className="list-header">通讯中断</div>
        <div className="list">
          <FormRow label="判定为失联" description="连续失败次数">
            <Stepper
              aria-label="判定为失联的连续失败次数"
              value={config.comm_failures}
              min={1}
              max={1000}
              unit="次"
              onChange={(v) => set("comm_failures", v)}
            />
            {err("comm_failures")}
          </FormRow>
        </div>
        <div className="list-header mt-4" id={policyId}>
          失联期间如何处理供电状态
        </div>
        <div className="policy" role="radiogroup" aria-labelledby={policyId}>
          {POLICIES.map((p) => {
            const on = config.comm_lost_policy === p.value;
            return (
              <button
                key={p.value}
                type="button"
                role="radio"
                aria-checked={on}
                className={on ? "opt is-on" : "opt"}
                onClick={() => set("comm_lost_policy", p.value)}
              >
                <span className={on ? "check is-on" : "check"} aria-hidden="true">
                  {on ? <Icon name="check" /> : null}
                </span>
                <span>
                  <span className="t-subhead strong" style={{ display: "block" }}>
                    {p.title}
                  </span>
                  <span className="t-footnote muted" style={{ display: "block" }}>
                    {p.description(config)}
                  </span>
                </span>
              </button>
            );
          })}
        </div>
        {config.comm_lost_policy === "assume_on_battery_after" ? (
          <div className="list mt-3">
            <FormRow label="视为停电前等待">
              <Stepper
                aria-label="失联后视为停电的等待时长（秒）"
                value={config.assume_on_battery_after_s}
                min={1}
                max={86400 * 365}
                unit="秒"
                onChange={(v) => set("assume_on_battery_after_s", v)}
              />
              {err("assume_on_battery_after_s")}
            </FormRow>
          </div>
        ) : null}
      </div>
    </div>
  );
}

const PREVIEW_FIELDS: readonly { key: string; label: string }[] = [
  { key: "battery_capacity_pct", label: "电池容量" },
  { key: "input_voltage[1]", label: "输入电压" },
  { key: "output_load_pct[1]", label: "负载" },
  { key: "runtime_estimate", label: "剩余时间" },
];

/** 设备身份文本：空串表示设备未提供。 */
function IdentityRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="row form-row">
      <span className="row-main">{label}</span>
      <span className={value ? "row-value" : "row-value na"}>{value || "— 未提供"}</span>
    </div>
  );
}

/**
 * 连接测试成功的结果：设备身份、推荐 Profile（可选）、按所测 Profile 归一化的读数预览。
 * 预览只经 FieldValue 显示，非 good 字段为「—」加原因。
 */
export function ProbeResultView({
  result,
  testedProfile,
  recommendations,
}: {
  result: ProbeResult;
  testedProfile: string;
  /** 推荐列表区域（由调用方渲染选择交互）；省略则不显示。 */
  recommendations?: ReactNode;
}) {
  const fields = normalizeFields(result.snapshot?.fields);
  const info = result.device_info;
  return (
    <>
      <div className={recommendations ? "grid cols-2 mt-4" : "mt-4"}>
        <div>
          <div className="list-header">设备身份</div>
          <div className="list">
            <IdentityRow label="型号" value={info?.model ?? ""} />
            <IdentityRow label="厂商" value={info?.manufacturer ?? ""} />
            <IdentityRow label="固件" value={info?.firmware ?? ""} />
            <IdentityRow label="Agent" value={info?.agent_version ?? ""} />
          </div>
        </div>
        {recommendations ? <div>{recommendations}</div> : null}
      </div>
      <div className="list-header mt-6">按 {testedProfile} 归一化的读数</div>
      <div className="probe-preview">
        {PREVIEW_FIELDS.map((f) => (
          <Metric key={f.key} label={f.label} fieldKey={f.key} field={fields[f.key]} />
        ))}
      </div>
      <p className="list-caption">
        原始数据与字段追踪可在保存后于设备详情「数据源与诊断」中查看；测试结果不写入历史。
      </p>
    </>
  );
}

/** 只读说明横幅。 */
export function ReadOnlyBanner() {
  return (
    <Banner tone="neutral" icon="shield" title="完全只读">
      无论 Profile 如何配置，UPS Warden 都不会向 UPS 发送写入、控制或 WebUI 请求，也不会扫描端口。
    </Banner>
  );
}

/** 数据源启用开关（编辑浮层）。 */
export function SourceEnabledRow({
  enabled,
  onChange,
}: {
  enabled: boolean;
  onChange: (next: boolean) => void;
}) {
  const id = useId();
  return (
    <div className="list">
      <FormRow label="启用数据源" htmlFor={id} description="每台设备最多一个启用的数据源">
        <Switch id={id} checked={enabled} onChange={onChange} />
      </FormRow>
    </div>
  );
}
