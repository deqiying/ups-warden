import { describe, expect, it } from "vite-plus/test";
import type { Device, ProfileSummary, Source } from "../../api/types.ts";
import { settings } from "../../test/fixtures.ts";
import {
  buildDevicePatch,
  buildSourceCreate,
  buildSourcePatch,
  buildSourceTest,
  defaultProfile,
  deviceDraftFrom,
  emptySourceDraft,
  sourceDraftFrom,
  switchProtocol,
  validHost,
  validateConfig,
  validateDeviceDraft,
  validateSourceDraft,
} from "./model.ts";

const PROFILES: ProfileSummary[] = [
  { key: "ayi9-json-generic", name: "AYI9", protocol: "ayi9-json", version: 1, builtin: true },
  { key: "et1k-modbus", name: "ET1K Modbus", protocol: "modbus", version: 1, builtin: true },
  { key: "et1k-snmp", name: "ET1K SNMP", protocol: "snmp", version: 1, builtin: true },
  { key: "rfc1628-generic", name: "RFC 1628", protocol: "snmp", version: 1, builtin: true },
];

const DEVICE: Device = {
  id: 3,
  name: "机房 UPS-A",
  note: "",
  location: "1F",
  tags: ["核心"],
  enabled: true,
  config: settings().device_defaults,
};

const AYI9_SOURCE: Source = {
  id: 9,
  device_id: 3,
  protocol: "ayi9-json",
  host: "127.0.0.1",
  port: 23900,
  params: {},
  profile_key: "ayi9-json-generic",
  profile_version: 1,
  poll_ms: 3000,
  timeout_ms: 1000,
  retries: 0,
  enabled: true,
  password_set: true,
  community_set: false,
  status: null,
};

describe("数据源草稿", () => {
  it("按协议选默认端口与通用 Profile", () => {
    expect(defaultProfile(PROFILES, "snmp")?.key).toBe("rfc1628-generic");
    const draft = emptySourceDraft("ayi9-json", PROFILES);
    expect(draft.port).toBe(2390);
    expect(draft.profileKey).toBe("ayi9-json-generic");
    const modbus = switchProtocol({ ...draft, host: "10.0.0.2", secret: "x" }, "modbus", PROFILES);
    expect(modbus).toMatchObject({
      port: 502,
      profileKey: "et1k-modbus",
      host: "10.0.0.2",
      secret: "",
    });
  });

  it("测试请求不带时序字段，空凭据不提交", () => {
    const draft = { ...emptySourceDraft("snmp", PROFILES), host: " 127.0.0.1 " };
    expect(buildSourceTest(draft)).toEqual({
      protocol: "snmp",
      host: "127.0.0.1",
      port: 161,
      params: { version: "2c" },
      profile_key: "rfc1628-generic",
      profile_version: 1,
    });
    const created = buildSourceCreate({ ...draft, secret: "private" });
    expect(created).toMatchObject({ community: "private", poll_ms: 3000, timeout_ms: 1000 });
    expect(created).not.toHaveProperty("password");
  });

  it("Modbus unit_id 留空时省略，沿用 Profile", () => {
    const draft = { ...emptySourceDraft("modbus", PROFILES), host: "h" };
    expect(buildSourceTest(draft).params).toEqual({});
    expect(buildSourceTest({ ...draft, unitId: 1 }).params).toEqual({ unit_id: 1 });
  });

  it("编辑只提交修改的字段；凭据留空保留、清除走 clear_secrets", () => {
    const draft = sourceDraftFrom(AYI9_SOURCE);
    expect(buildSourcePatch(AYI9_SOURCE, draft)).toEqual({});
    expect(buildSourcePatch(AYI9_SOURCE, { ...draft, port: 2390 })).toEqual({ port: 2390 });
    expect(buildSourcePatch(AYI9_SOURCE, { ...draft, secret: "new" })).toEqual({ password: "new" });
    expect(buildSourcePatch(AYI9_SOURCE, { ...draft, clearSecret: true })).toEqual({
      clear_secrets: ["password"],
    });
    expect(() =>
      buildSourcePatch(AYI9_SOURCE, { ...draft, clearSecret: true, secret: "x" }),
    ).toThrow();
  });

  it("切换协议时提交协议、参数与 Profile，不提交 clear_secrets", () => {
    const draft = switchProtocol(sourceDraftFrom(AYI9_SOURCE), "snmp", PROFILES);
    const patch = buildSourcePatch(AYI9_SOURCE, { ...draft, clearSecret: true });
    expect(patch).toMatchObject({
      protocol: "snmp",
      port: 161,
      params: { version: "2c" },
      profile_key: "rfc1628-generic",
      profile_version: 1,
    });
    expect(patch).not.toHaveProperty("clear_secrets");
  });

  it("校验主机、端口与时序范围", () => {
    expect(validHost("ups-a.lan")).toBe(true);
    expect(validHost("192.168.1.2")).toBe(true);
    expect(validHost("http://ups")).toBe(false);
    expect(validHost("ups:161")).toBe(false);
    const errors = validateSourceDraft({
      ...emptySourceDraft("snmp", PROFILES),
      host: "",
      port: 70000,
      pollMs: 100,
    });
    expect(Object.keys(errors).sort()).toEqual(["host", "pollMs", "port"]);
    expect(
      validateSourceDraft({ ...emptySourceDraft("snmp", PROFILES), host: "h", pollMs: 100 }, false),
    ).toEqual({});
  });
});

describe("设备草稿", () => {
  it("只提交修改的字段，config 任一子字段变化时整体提交", () => {
    const draft = deviceDraftFrom(DEVICE);
    expect(buildDevicePatch(DEVICE, draft)).toEqual({});
    expect(buildDevicePatch(DEVICE, { ...draft, name: " 新名称 " })).toEqual({ name: "新名称" });
    const config = { ...draft.config, battery_low_pct: 25 };
    expect(buildDevicePatch(DEVICE, { ...draft, config })).toEqual({ config });
  });

  it("名称必填且按字节限长", () => {
    const draft = deviceDraftFrom(DEVICE);
    expect(validateDeviceDraft({ ...draft, name: "  " }).name).toBeTruthy();
    expect(validateDeviceDraft({ ...draft, name: "中".repeat(43) }).name).toBeTruthy();
    expect(validateDeviceDraft(draft)).toEqual({});
  });

  it("阈值校验与后端 Config.Validate 一致", () => {
    const c = settings().device_defaults;
    expect(validateConfig(c)).toEqual({});
    expect(validateConfig({ ...c, battery_low_pct: 98, battery_hysteresis_pct: 5 })).toHaveProperty(
      "battery_hysteresis_pct",
    );
    expect(validateConfig({ ...c, load_hysteresis_pct: 95, overload_pct: 90 })).toHaveProperty(
      "load_hysteresis_pct",
    );
    expect(
      validateConfig({
        ...c,
        comm_lost_policy: "assume_on_battery_after",
        assume_on_battery_after_s: 0,
      }),
    ).toHaveProperty("assume_on_battery_after_s");
  });
});
