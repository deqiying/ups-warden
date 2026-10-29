/*
 * 设备与数据源编辑的纯逻辑：表单草稿、请求体构造与前端结构性校验（F3）。
 * 校验只覆盖契约写明的范围（monitoring-api.md「设备」「数据源」、state.Config.Validate），
 * 语义错误仍以后端 422 为准；请求体遵循“只提交修改的字段、凭据只写”的约定（api/patch.ts）。
 */
import { changedFields } from "../../api/patch.ts";
import type {
  Device,
  DeviceConfig,
  DeviceInput,
  ProfileSummary,
  Source,
  SourceInput,
  SourceProtocol,
} from "../../api/types.ts";
import type { IconName } from "../../icons/icons.ts";

export interface ProtocolMeta {
  value: SourceProtocol;
  label: string;
  defaultPort: number;
  description: string;
  icon: IconName;
  color: string;
  /** 该协议的只写凭据字段；Modbus 无凭据。 */
  secret: "community" | "password" | null;
  secretLabel: string;
}

export const PROTOCOLS: readonly ProtocolMeta[] = [
  {
    value: "snmp",
    label: "SNMP",
    defaultPort: 161,
    description: "v1 / v2c · UDP 161。适用于大多数 UPS 网卡，内置 RFC 1628 通用映射。",
    icon: "antenna",
    color: "var(--accent)",
    secret: "community",
    secretLabel: "Community",
  },
  {
    value: "modbus",
    label: "Modbus TCP",
    defaultPort: 502,
    description: "TCP 502 · 只读 FC03 / FC04。设备没有身份信息，需要手动选择 Profile。",
    icon: "server",
    color: "var(--indigo)",
    secret: null,
    secretLabel: "",
  },
  {
    value: "ayi9-json",
    label: "AYI9 云监控卡 JSON API",
    defaultPort: 2390,
    description: "TCP 2390 · 仅 QAD / RTD。网卡「集成设置」页显示 TCP:2390, JSON format。",
    icon: "globe",
    color: "var(--teal)",
    secret: "password",
    secretLabel: "密码",
  },
];

export function protocolMeta(protocol: SourceProtocol): ProtocolMeta {
  return PROTOCOLS.find((p) => p.value === protocol) ?? PROTOCOLS[0];
}

/** 各协议首选的通用 Profile；不存在时取该协议的第一个 Profile。 */
const PREFERRED_PROFILE: Readonly<Record<SourceProtocol, string>> = {
  snmp: "rfc1628-generic",
  modbus: "et1k-modbus",
  "ayi9-json": "ayi9-json-generic",
};

export function profilesFor(
  profiles: readonly ProfileSummary[] | undefined,
  protocol: SourceProtocol,
): ProfileSummary[] {
  return (profiles ?? []).filter((p) => p.protocol === protocol);
}

export function defaultProfile(
  profiles: readonly ProfileSummary[] | undefined,
  protocol: SourceProtocol,
): ProfileSummary | undefined {
  const candidates = profilesFor(profiles, protocol);
  return candidates.find((p) => p.key === PREFERRED_PROFILE[protocol]) ?? candidates[0];
}

/** 数据源表单草稿。凭据只写：`secret` 永不预填，留空表示保留原值。 */
export interface SourceDraft {
  protocol: SourceProtocol;
  host: string;
  port: number;
  snmpVersion: "2c" | "1";
  /** Modbus unit_id；null 表示沿用 Profile 中的值。 */
  unitId: number | null;
  profileKey: string;
  profileVersion: number;
  pollMs: number;
  timeoutMs: number;
  retries: number;
  enabled: boolean;
  secret: string;
  /** 显式清除已保存的凭据（与新值互斥）。 */
  clearSecret: boolean;
}

/** 后端默认值：poll 3000、timeout 1000、retries 0（monitoring-api.md）。 */
export function emptySourceDraft(
  protocol: SourceProtocol,
  profiles?: readonly ProfileSummary[],
): SourceDraft {
  const profile = defaultProfile(profiles, protocol);
  return {
    protocol,
    host: "",
    port: protocolMeta(protocol).defaultPort,
    snmpVersion: "2c",
    unitId: null,
    profileKey: profile?.key ?? "",
    profileVersion: profile?.version ?? 1,
    pollMs: 3000,
    timeoutMs: 1000,
    retries: 0,
    enabled: true,
    secret: "",
    clearSecret: false,
  };
}

/** 切换协议：端口回到新协议默认值、Profile 换成新协议的默认 Profile，旧协议参数与凭据输入清空。 */
export function switchProtocol(
  draft: SourceDraft,
  protocol: SourceProtocol,
  profiles?: readonly ProfileSummary[],
): SourceDraft {
  if (draft.protocol === protocol) return draft;
  const fresh = emptySourceDraft(protocol, profiles);
  return {
    ...fresh,
    host: draft.host,
    pollMs: draft.pollMs,
    timeoutMs: draft.timeoutMs,
    retries: draft.retries,
    enabled: draft.enabled,
  };
}

export function sourceDraftFrom(source: Source): SourceDraft {
  const version = source.params?.version;
  const unitId = source.params?.unit_id;
  return {
    protocol: source.protocol,
    host: source.host,
    port: source.port,
    snmpVersion: version === "1" ? "1" : "2c",
    unitId: typeof unitId === "number" ? unitId : null,
    profileKey: source.profile_key,
    profileVersion: source.profile_version,
    pollMs: source.poll_ms,
    timeoutMs: source.timeout_ms,
    retries: source.retries,
    enabled: source.enabled,
    secret: "",
    clearSecret: false,
  };
}

/** 协议参数：SNMP `{version}`、Modbus `{unit_id}`（省略沿用 Profile）、AYI9 `{}`。 */
export function sourceParams(draft: SourceDraft): Record<string, unknown> {
  switch (draft.protocol) {
    case "snmp":
      return { version: draft.snmpVersion };
    case "modbus":
      return draft.unitId === null ? {} : { unit_id: draft.unitId };
    default:
      return {};
  }
}

function secretPart(draft: SourceDraft): Pick<SourceInput, "community" | "password"> {
  const field = protocolMeta(draft.protocol).secret;
  if (!field || draft.secret === "") return {};
  return { [field]: draft.secret };
}

/** 未保存配置的连接测试：不带时序字段，由后端按 5 秒总限额探测。 */
export function buildSourceTest(draft: SourceDraft): SourceInput {
  return {
    protocol: draft.protocol,
    host: draft.host.trim(),
    port: draft.port,
    params: sourceParams(draft),
    profile_key: draft.profileKey,
    profile_version: draft.profileVersion,
    ...secretPart(draft),
  };
}

/** 新建数据源的完整请求体。 */
export function buildSourceCreate(draft: SourceDraft): SourceInput {
  return {
    ...buildSourceTest(draft),
    poll_ms: draft.pollMs,
    timeout_ms: draft.timeoutMs,
    retries: draft.retries,
    enabled: draft.enabled,
  };
}

/**
 * 编辑数据源的 PATCH 请求体：只含修改的字段。
 * - 新凭据只在非空时提交；`clearSecret` 时提交 `clear_secrets`，二者互斥（UI 保证，这里再兜底抛错）。
 * - 切换协议时后端会清除旧协议凭据，此时不提交 clear_secrets（清除键必须属于新协议）。
 */
export function buildSourcePatch(original: Source, draft: SourceDraft): SourceInput {
  const before = sourceDraftFrom(original);
  const out: SourceInput = {};
  const protocolChanged = draft.protocol !== before.protocol;
  if (protocolChanged) out.protocol = draft.protocol;
  if (draft.host.trim() !== before.host) out.host = draft.host.trim();
  if (draft.port !== before.port) out.port = draft.port;
  const params = sourceParams(draft);
  if (protocolChanged || JSON.stringify(params) !== JSON.stringify(sourceParams(before))) {
    out.params = params;
  }
  if (protocolChanged || draft.profileKey !== before.profileKey) out.profile_key = draft.profileKey;
  if (protocolChanged || draft.profileVersion !== before.profileVersion) {
    out.profile_version = draft.profileVersion;
  }
  if (draft.pollMs !== before.pollMs) out.poll_ms = draft.pollMs;
  if (draft.timeoutMs !== before.timeoutMs) out.timeout_ms = draft.timeoutMs;
  if (draft.retries !== before.retries) out.retries = draft.retries;
  if (draft.enabled !== before.enabled) out.enabled = draft.enabled;

  const field = protocolMeta(draft.protocol).secret;
  if (draft.clearSecret && draft.secret !== "") {
    throw new Error("凭据不能同时设置新值和清除");
  }
  Object.assign(out, secretPart(draft));
  if (field && draft.clearSecret && !protocolChanged) out.clear_secrets = [field];
  return out;
}

/** 已保存凭据的状态：只读 `*_set`。 */
export function secretIsSet(source: Source | undefined, protocol: SourceProtocol): boolean {
  if (!source || source.protocol !== protocol) return false;
  if (protocol === "snmp") return source.community_set;
  if (protocol === "ayi9-json") return source.password_set;
  return false;
}

export type FieldErrors<K extends string> = Partial<Record<K, string>>;

const byteLength = (text: string) => new TextEncoder().encode(text).length;

// 与后端 validHost 一致：IP 或 DNS 名称，不含 scheme、端口、路径或空白。
const HOST_LABEL = /^[A-Za-z0-9-]{1,63}$/;
const IPV6 = /^[0-9A-Fa-f:.]+$/;

export function validHost(host: string): boolean {
  if (host.length < 1 || host.length > 253 || /[/\\@%?#\s]/.test(host)) return false;
  if (host.includes(":")) return IPV6.test(host);
  return host
    .split(".")
    .every((label) => HOST_LABEL.test(label) && !label.startsWith("-") && !label.endsWith("-"));
}

export type SourceField =
  | "host"
  | "port"
  | "unitId"
  | "profile"
  | "pollMs"
  | "timeoutMs"
  | "retries";

/** 连接字段的结构性校验；`timing=false` 时跳过轮询 / 超时 / 重试（向导测试步骤用）。 */
export function validateSourceDraft(draft: SourceDraft, timing = true): FieldErrors<SourceField> {
  const errors: FieldErrors<SourceField> = {};
  const host = draft.host.trim();
  if (!host) errors.host = "请填写主机地址";
  else if (!validHost(host)) errors.host = "只填写 IP 或主机名，不含 http://、端口或路径";
  if (!Number.isInteger(draft.port) || draft.port < 1 || draft.port > 65535) {
    errors.port = "端口范围 1～65535";
  }
  if (draft.protocol === "modbus" && draft.unitId !== null) {
    if (!Number.isInteger(draft.unitId) || draft.unitId < 0 || draft.unitId > 255) {
      errors.unitId = "Unit ID 范围 0～255";
    }
  }
  if (!draft.profileKey) errors.profile = "请选择 Profile";
  if (timing) {
    if (draft.pollMs < 200 || draft.pollMs > 3_600_000) errors.pollMs = "轮询间隔 0.2～3600 秒";
    if (draft.timeoutMs < 1 || draft.timeoutMs > 60_000) errors.timeoutMs = "超时 1～60000 毫秒";
    if (!Number.isInteger(draft.retries) || draft.retries < 0 || draft.retries > 10) {
      errors.retries = "重试 0～10 次";
    }
  }
  return errors;
}

/** 设备基本信息草稿。 */
export interface DeviceDraft {
  name: string;
  note: string;
  location: string;
  tags: string[];
  config: DeviceConfig;
}

export function deviceDraftFrom(device: Device): DeviceDraft {
  return {
    name: device.name,
    note: device.note,
    location: device.location,
    tags: [...device.tags],
    config: { ...device.config },
  };
}

export type DeviceField = "name" | "note" | "location" | "tags";

export function validateDeviceDraft(draft: DeviceDraft): FieldErrors<DeviceField> {
  const errors: FieldErrors<DeviceField> = {};
  const name = draft.name.trim();
  if (!name) errors.name = "请填写名称";
  else if (byteLength(name) > 128) errors.name = "名称最多 128 字节";
  if (byteLength(draft.note) > 4096) errors.note = "备注最多 4096 字节";
  if (byteLength(draft.location) > 256) errors.location = "位置最多 256 字节";
  if (draft.tags.length > 32) errors.tags = "最多 32 个标签";
  return errors;
}

/** TokenInput 的单个标签校验：1～64 字节。 */
export function validateTag(tag: string): string | null {
  return byteLength(tag) > 64 ? "标签最多 64 字节" : null;
}

/** 新建设备：名称、备注、位置、标签与完整 config（向导第 4 步已预填 device_defaults）。 */
export function buildDeviceCreate(draft: DeviceDraft): DeviceInput {
  return {
    name: draft.name.trim(),
    note: draft.note,
    location: draft.location.trim(),
    tags: draft.tags,
    config: draft.config,
  };
}

/** 编辑设备：只提交修改的字段；config 有任一子字段变化时整体提交。 */
export function buildDevicePatch(original: Device, draft: DeviceDraft): DeviceInput {
  const next: DeviceDraft = {
    ...draft,
    name: draft.name.trim(),
    location: draft.location.trim(),
  };
  return changedFields(deviceDraftFrom(original), next) as DeviceInput;
}

export type ConfigField = keyof DeviceConfig;

/** 与 state.Config.Validate 一致的结构性校验，给出就地提示。 */
export function validateConfig(c: DeviceConfig): FieldErrors<ConfigField> {
  const errors: FieldErrors<ConfigField> = {};
  if (c.outage_confirm_s < 0 || c.outage_confirm_s > 86400) {
    errors.outage_confirm_s = "0～86400 秒";
  }
  if (c.recovery_confirm_s < 0 || c.recovery_confirm_s > 86400) {
    errors.recovery_confirm_s = "0～86400 秒";
  }
  if (c.input_voltage_threshold < 0 || c.input_voltage_threshold > 10000) {
    errors.input_voltage_threshold = "0～10000 V";
  }
  if (!Number.isInteger(c.comm_failures) || c.comm_failures < 1 || c.comm_failures > 1000) {
    errors.comm_failures = "1～1000 次";
  }
  if (c.battery_low_pct < 0 || c.battery_low_pct > 100) errors.battery_low_pct = "0～100%";
  if (c.battery_hysteresis_pct < 0 || c.battery_low_pct + c.battery_hysteresis_pct > 100) {
    errors.battery_hysteresis_pct = "低电量阈值加回差不能超过 100%";
  }
  if (c.overload_pct < 0 || c.overload_pct > 100) errors.overload_pct = "0～100%";
  if (c.load_hysteresis_pct < 0 || c.load_hysteresis_pct > c.overload_pct) {
    errors.load_hysteresis_pct = "回差不能超过过载阈值";
  }
  if (c.comm_lost_policy === "assume_on_battery_after") {
    if (c.assume_on_battery_after_s <= 0 || c.assume_on_battery_after_s > 86400 * 365) {
      errors.assume_on_battery_after_s = "须大于 0 秒";
    }
  }
  return errors;
}

export const hasErrors = (errors: object) => Object.keys(errors).length > 0;

/** 设备列表筛选。 */
export type DeviceFilter = "all" | "enabled" | "disabled";

export function filterDevices(devices: readonly Device[], filter: DeviceFilter): Device[] {
  if (filter === "enabled") return devices.filter((d) => d.enabled);
  if (filter === "disabled") return devices.filter((d) => !d.enabled);
  return [...devices];
}
