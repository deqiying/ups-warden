/*
 * 统一字段目录镜像：与 backend/internal/model/catalog.go 一一对应（key、kind、unit、phased、history、
 * derived、group、enum），另补中文名称。后端没有字段目录接口（Q-C8），catalog.go 变化时必须同步此文件，
 * fields.test.ts 会校验条目数与关键属性。
 */

export type FieldKind = "number" | "bool" | "enum" | "text" | "duration";
export type FieldGroup =
  | "ident"
  | "nominal"
  | "input"
  | "output"
  | "bypass"
  | "battery"
  | "flags"
  | "derived";

export interface FieldSpec {
  key: string;
  kind: FieldKind;
  /** 显示单位；Duration 的线上单位为秒，显示时转换为“x 分 y 秒”，此处为空。 */
  unit: string;
  /** 按相字段：实例键为 `key[1]`～`key[3]`。 */
  phased: boolean;
  /** 是否进入历史曲线（明细与聚合）。 */
  history: boolean;
  /** 由状态引擎派生，不来自 Profile 映射。 */
  derived: boolean;
  group: FieldGroup;
  /** 枚举取值（enum 类型）；映射失败时值为 unknown。 */
  enum: readonly string[];
  label: string;
}

type SpecInput = Omit<FieldSpec, "enum" | "label"> & { enum?: readonly string[] };

const ENUMS: Readonly<Record<string, readonly string[]>> = {
  output_source: ["other", "none", "normal", "bypass", "battery", "booster", "reducer", "unknown"],
  battery_status: ["normal", "low", "depleted", "unknown"],
  ups_mode: [
    "power_on",
    "standby",
    "bypass",
    "line",
    "battery",
    "test",
    "fault",
    "converter",
    "eco",
    "shutdown",
    "unknown",
  ],
  ups_type: ["online", "backup", "unknown"],
  power_state: ["online", "on_battery", "bypass", "fault", "shutdown", "unknown"],
  runtime_source: ["native", "estimated", "none", "unknown"],
  comm_state: ["ok", "lost", "unknown"],
};

/** 字段中文名：取自效果图（device-detail、rules 等），效果图未出现的按同组命名补齐。 */
const LABELS: Readonly<Record<string, string>> = {
  manufacturer: "厂商",
  model: "型号",
  firmware: "固件",
  agent_version: "监控卡版本",
  serial: "序列号",
  name: "设备名称",
  nominal_input_voltage: "额定输入电压",
  nominal_output_voltage: "额定输出电压",
  nominal_battery_voltage: "电池额定电压",
  nominal_input_freq: "额定输入频率",
  nominal_output_freq: "额定输出频率",
  rated_va: "额定容量",
  rated_w: "额定功率",
  input_phases: "输入相数",
  output_phases: "输出相数",
  input_voltage: "输入电压",
  input_current: "输入电流",
  input_power: "输入功率",
  input_freq: "输入频率",
  output_voltage: "输出电压",
  output_current: "输出电流",
  output_power: "输出功率",
  output_freq: "输出频率",
  output_load_pct: "负载",
  output_source: "输出来源",
  bypass_voltage: "旁路电压",
  bypass_current: "旁路电流",
  bypass_power: "旁路功率",
  bypass_freq: "旁路频率",
  battery_status: "电池状态",
  battery_capacity_pct: "电池容量",
  battery_voltage: "电池电压",
  battery_current: "电池电流",
  temperature: "温度",
  native_seconds_on_battery: "设备上报放电时长",
  native_runtime_remaining: "设备上报剩余时间",
  mains_ok: "市电正常",
  low_battery: "低电量",
  overload: "过载",
  shutting_down: "正在关机",
  buzzer_muted: "蜂鸣器静音",
  ups_mode: "UPS 模式",
  ups_type: "UPS 类型",
  self_test_result: "自检结果",
  power_state: "供电状态",
  runtime_source: "剩余时间来源",
  comm_state: "通讯状态",
  on_battery_elapsed: "已放电",
  runtime_estimate: "剩余时间",
};

function buildCatalog(): FieldSpec[] {
  const out: SpecInput[] = [];
  const add = (
    group: FieldGroup,
    kind: FieldKind,
    unit: string,
    phased: boolean,
    history: boolean,
    derived: boolean,
    ...keys: string[]
  ) => {
    for (const key of keys) out.push({ key, kind, unit, phased, history, derived, group });
  };
  add(
    "ident",
    "text",
    "",
    false,
    false,
    false,
    "manufacturer",
    "model",
    "firmware",
    "agent_version",
    "serial",
    "name",
  );
  add(
    "nominal",
    "number",
    "V",
    false,
    false,
    false,
    "nominal_input_voltage",
    "nominal_output_voltage",
    "nominal_battery_voltage",
  );
  add("nominal", "number", "Hz", false, false, false, "nominal_input_freq", "nominal_output_freq");
  add("nominal", "number", "VA", false, false, false, "rated_va");
  add("nominal", "number", "W", false, false, false, "rated_w");
  add("nominal", "number", "", false, false, false, "input_phases", "output_phases");
  for (const group of ["input", "output", "bypass"] as const) {
    add(group, "number", "V", true, true, false, `${group}_voltage`);
    add(group, "number", "A", true, true, false, `${group}_current`);
    add(group, "number", group === "input" ? "W" : "VA", true, true, false, `${group}_power`);
    add(group, "number", "Hz", false, true, false, `${group}_freq`);
  }
  add("output", "number", "%", true, true, false, "output_load_pct");
  add("output", "enum", "", false, false, false, "output_source");
  add("battery", "enum", "", false, false, false, "battery_status");
  add("battery", "number", "%", false, true, false, "battery_capacity_pct");
  add("battery", "number", "V", false, true, false, "battery_voltage");
  add("battery", "number", "A", false, true, false, "battery_current");
  add("battery", "number", "°C", false, true, false, "temperature");
  add(
    "battery",
    "duration",
    "",
    false,
    true,
    false,
    "native_seconds_on_battery",
    "native_runtime_remaining",
  );
  add(
    "flags",
    "bool",
    "",
    false,
    false,
    false,
    "mains_ok",
    "low_battery",
    "overload",
    "shutting_down",
    "buzzer_muted",
  );
  add("flags", "enum", "", false, false, false, "ups_mode", "ups_type");
  // 后端：自检位的枚举含义尚未确定，按文本保留。
  add("flags", "text", "", false, false, false, "self_test_result");
  add("derived", "enum", "", false, false, true, "power_state", "runtime_source", "comm_state");
  add("derived", "duration", "", false, true, true, "on_battery_elapsed", "runtime_estimate");

  return out
    .map((spec) => ({ ...spec, enum: ENUMS[spec.key] ?? [], label: LABELS[spec.key] ?? spec.key }))
    .sort((a, b) => (a.key < b.key ? -1 : a.key > b.key ? 1 : 0));
}

/** 完整目录，按 key 升序（与后端一致）。 */
export const FIELD_CATALOG: readonly FieldSpec[] = buildCatalog();

const BY_KEY: ReadonlyMap<string, FieldSpec> = new Map(FIELD_CATALOG.map((s) => [s.key, s]));

const PHASED_KEY = /^([a-z_]+)\[([1-3])\]$/;

/** 拆分实例键：`input_voltage[2]` → { base: "input_voltage", phase: 2 }；非法格式返回 null。 */
export function splitFieldKey(key: string): { base: string; phase: number } | null {
  if (!key.includes("[")) return key.includes("]") ? null : { base: key, phase: 0 };
  const match = PHASED_KEY.exec(key);
  return match ? { base: match[1], phase: Number(match[2]) } : null;
}

const warned = new Set<string>();

/**
 * 查询字段规格：接受基础键或合法的按相实例键（与后端 Lookup 一致）。
 * 未知键返回 undefined，并在开发模式下提示一次——通常意味着 catalog.go 已变化而镜像未同步。
 */
export function lookupField(key: string): FieldSpec | undefined {
  const parts = splitFieldKey(key);
  const spec = parts ? BY_KEY.get(parts.base) : undefined;
  if (!spec || (parts && parts.phase !== 0 && !spec.phased)) {
    if (import.meta.env.DEV && !warned.has(key)) {
      warned.add(key);
      console.warn(`未知统一字段：${key}（model/fields.ts 可能需要与 catalog.go 同步）`);
    }
    return undefined;
  }
  return spec;
}

/** 字段显示名；按相实例追加相号（如“输入电压 L2”），未知键原样返回。 */
export function fieldLabel(key: string): string {
  const spec = lookupField(key);
  if (!spec) return key;
  const phase = splitFieldKey(key)?.phase ?? 0;
  return phase > 0 ? `${spec.label} L${phase}` : spec.label;
}

/** 按相字段的实例键：`phaseKeys("input_voltage", 3)` → `[1]`～`[3]`。 */
export function phaseKeys(base: string, phases: number): string[] {
  const count = Math.min(Math.max(Math.trunc(phases), 1), 3);
  return Array.from({ length: count }, (_, i) => `${base}[${i + 1}]`);
}
