import { describe, expect, it } from "vite-plus/test";
import { wireHost } from "../test/fixtures.ts";
import {
  buildHostCreateBody,
  buildHostUpdateBody,
  draftFromHost,
  emptyHostDraft,
  hostKeyWillReset,
  type Host,
  type HostDraft,
} from "./hosts.ts";

const passwordHost = (): Host => wireHost({ password_set: true });
const keyHost = (): Host =>
  wireHost({
    conn: { port: 22, username: "root", auth_method: "private_key", sudo: false },
    password_set: false,
    private_key_set: true,
    passphrase_set: true,
  });

/** 以已保存主机为基线构造“未修改”的草稿，再叠加修改。 */
const draftOf = (host: Host, overrides: Partial<HostDraft> = {}): HostDraft => ({
  ...draftFromHost(host),
  ...overrides,
});

describe("emptyHostDraft / draftFromHost", () => {
  it("新建默认值：SSH 22、密码认证、TCP 在线检测、凭据全空", () => {
    expect(emptyHostDraft()).toEqual({
      name: "",
      address: "",
      kind: "linux",
      port: 22,
      username: "",
      authMethod: "password",
      sudo: false,
      onlineType: "tcp",
      onlinePort: 22,
      password: "",
      privateKey: "",
      passphrase: "",
      clearSecrets: [],
    });
  });

  it("编辑草稿凭据永不预填，且不含任何 *_set 以外的凭据信息", () => {
    const draft = draftFromHost(keyHost());
    expect(draft.password).toBe("");
    expect(draft.privateKey).toBe("");
    expect(draft.passphrase).toBe("");
    expect(draft.clearSecrets).toEqual([]);
    expect(draft.authMethod).toBe("private_key");
    expect(draft.username).toBe("root");
  });

  it("ICMP 检测（port=0）时在线端口回落为 SSH 端口，便于切回 TCP", () => {
    const host = wireHost({
      conn: { port: 2222, username: "admin", auth_method: "password", sudo: true },
      online_check: { type: "icmp", port: 0 },
    });
    const draft = draftFromHost(host);
    expect(draft.onlineType).toBe("icmp");
    expect(draft.onlinePort).toBe(2222);
    expect(draft.sudo).toBe(true);
  });
});

describe("buildHostCreateBody", () => {
  it("密码方式：名称 / 地址 / 用户名 trim，只提交密码，不含私钥与 clear_secrets", () => {
    const body = buildHostCreateBody({
      ...emptyHostDraft(),
      name: "  NAS-01 ",
      address: " nas-01.lan ",
      kind: "nas",
      username: " admin ",
      password: "p@ss",
      // 另一种方式残留的输入不应提交。
      privateKey: "STALE-KEY",
      passphrase: "STALE-PP",
    });
    expect(body).toStrictEqual({
      name: "NAS-01",
      address: "nas-01.lan",
      kind: "nas",
      conn: { port: 22, username: "admin", auth_method: "password", sudo: false },
      online_check: { type: "tcp", port: 22 },
      password: "p@ss",
    });
  });

  it("私钥方式：提交私钥与口令，不提交残留的密码；口令留空则省略", () => {
    const base = {
      ...emptyHostDraft(),
      name: "esxi",
      address: "10.0.0.2",
      username: "root",
      authMethod: "private_key" as const,
      password: "STALE-PW",
      privateKey: "-----BEGIN KEY-----",
    };
    const noPassphrase = buildHostCreateBody(base);
    expect(noPassphrase).toMatchObject({ private_key: "-----BEGIN KEY-----" });
    expect(noPassphrase).not.toHaveProperty("password");
    expect(noPassphrase).not.toHaveProperty("passphrase");

    const withPassphrase = buildHostCreateBody({ ...base, passphrase: "pp" });
    expect(withPassphrase).toMatchObject({ private_key: "-----BEGIN KEY-----", passphrase: "pp" });
  });

  it("创建体永不含 clear_secrets；ICMP 检测端口提交 0", () => {
    const body = buildHostCreateBody({
      ...emptyHostDraft(),
      name: "h",
      address: "h.lan",
      username: "u",
      password: "x",
      onlineType: "icmp",
      onlinePort: 8080,
      clearSecrets: ["passphrase"],
    });
    expect(body).not.toHaveProperty("clear_secrets");
    expect(body.online_check).toEqual({ type: "icmp", port: 0 });
  });
});

describe("buildHostUpdateBody：部分更新", () => {
  it("未修改时为空对象（保存按钮据此禁用）", () => {
    expect(buildHostUpdateBody(passwordHost(), draftOf(passwordHost()))).toStrictEqual({});
    expect(buildHostUpdateBody(keyHost(), draftOf(keyHost()))).toStrictEqual({});
  });

  it("名称 / 地址 / 用户名仅首尾空白差异不算修改", () => {
    const host = passwordHost();
    const body = buildHostUpdateBody(
      host,
      draftOf(host, { name: " NAS-01 ", address: " nas-01.lan ", username: " admin " }),
    );
    expect(body).toStrictEqual({});
  });

  it("凭据留空 = 不提交：改名称时不带 password / private_key / passphrase", () => {
    const body = buildHostUpdateBody(passwordHost(), draftOf(passwordHost(), { name: "新名称" }));
    expect(body).toStrictEqual({ name: "新名称" });
  });

  it("只填新密码时只提交 password", () => {
    const body = buildHostUpdateBody(
      passwordHost(),
      draftOf(passwordHost(), { password: "new-pw" }),
    );
    expect(body).toStrictEqual({ password: "new-pw" });
  });

  it("conn 只含变化的子字段", () => {
    const host = passwordHost();
    expect(buildHostUpdateBody(host, draftOf(host, { port: 2222 }))).toStrictEqual({
      conn: { port: 2222 },
    });
    expect(buildHostUpdateBody(host, draftOf(host, { sudo: true }))).toStrictEqual({
      conn: { sudo: true },
    });
    expect(
      buildHostUpdateBody(host, draftOf(host, { username: "ops", port: 2200, sudo: true })),
    ).toStrictEqual({ conn: { port: 2200, username: "ops", sudo: true } });
  });

  it("类型 / 地址变化只提交各自字段", () => {
    const host = passwordHost();
    expect(
      buildHostUpdateBody(host, draftOf(host, { kind: "linux", address: "10.0.0.9" })),
    ).toStrictEqual({ kind: "linux", address: "10.0.0.9" });
  });

  it("在线检测一旦变化即整体提交 online_check", () => {
    const host = passwordHost();
    expect(buildHostUpdateBody(host, draftOf(host, { onlinePort: 5000 }))).toStrictEqual({
      online_check: { type: "tcp", port: 5000 },
    });
    expect(buildHostUpdateBody(host, draftOf(host, { onlineType: "icmp" }))).toStrictEqual({
      online_check: { type: "icmp", port: 0 },
    });
  });

  it("原为 ICMP 且后端残留端口时，ICMP 不算修改", () => {
    const host = wireHost({ online_check: { type: "icmp", port: 22 } });
    expect(buildHostUpdateBody(host, draftOf(host))).toStrictEqual({});
  });

  it("ICMP 下修改在线端口输入框不产生修改", () => {
    const host = wireHost({ online_check: { type: "icmp", port: 0 } });
    expect(buildHostUpdateBody(host, draftOf(host, { onlinePort: 9999 }))).toStrictEqual({});
  });
});

describe("buildHostUpdateBody：clear_secrets", () => {
  it("私钥方式下可清除私钥口令", () => {
    const host = keyHost();
    const body = buildHostUpdateBody(host, draftOf(host, { clearSecrets: ["passphrase"] }));
    expect(body).toStrictEqual({ clear_secrets: ["passphrase"] });
  });

  it("clear_secrets 只可能含 passphrase：password / private_key 被丢弃", () => {
    const host = keyHost();
    const body = buildHostUpdateBody(
      host,
      draftOf(host, { clearSecrets: ["password", "private_key", "passphrase"] }),
    );
    expect(body.clear_secrets).toEqual(["passphrase"]);

    const onlyRequired = buildHostUpdateBody(
      host,
      draftOf(host, { clearSecrets: ["password", "private_key"] }),
    );
    expect(onlyRequired).not.toHaveProperty("clear_secrets");
  });

  it("密码方式下不提交任何 clear_secrets", () => {
    const host = passwordHost();
    const body = buildHostUpdateBody(
      host,
      draftOf(host, { clearSecrets: ["passphrase", "password"] }),
    );
    expect(body).toStrictEqual({});
  });

  it("切换认证方式时丢弃 clear_secrets，并只提交新方式的凭据", () => {
    // 密码 → 私钥：clearSecrets 即使残留也被丢弃。
    const toKey = buildHostUpdateBody(
      passwordHost(),
      draftOf(passwordHost(), {
        authMethod: "private_key",
        privateKey: "NEW-KEY",
        password: "STALE-PW",
        clearSecrets: ["passphrase"],
      }),
    );
    expect(toKey).toStrictEqual({
      conn: { auth_method: "private_key" },
      private_key: "NEW-KEY",
    });

    // 私钥 → 密码。
    const toPassword = buildHostUpdateBody(
      keyHost(),
      draftOf(keyHost(), {
        authMethod: "password",
        password: "NEW-PW",
        privateKey: "STALE-KEY",
        passphrase: "STALE-PP",
        clearSecrets: ["passphrase"],
      }),
    );
    expect(toPassword).toStrictEqual({
      conn: { auth_method: "password" },
      password: "NEW-PW",
    });
  });

  it("同一凭据既有新值又要求清除属于调用方错误，直接抛错而不是猜测", () => {
    const host = keyHost();
    expect(() =>
      buildHostUpdateBody(host, draftOf(host, { passphrase: "x", clearSecrets: ["passphrase"] })),
    ).toThrow(/不能同时设置新值和清除/);
  });
});

describe("hostKeyWillReset", () => {
  const confirmed = () => wireHost({ hostkey_fingerprint: "SHA256:abc" });

  it("指纹未确认时无需提示", () => {
    const host = wireHost({ hostkey_fingerprint: "" });
    expect(hostKeyWillReset(host, draftOf(host, { address: "other.lan", port: 2222 }))).toBe(false);
  });

  it("地址或 SSH 端口变化会清除已确认的指纹", () => {
    const host = confirmed();
    expect(hostKeyWillReset(host, draftOf(host, { address: "other.lan" }))).toBe(true);
    expect(hostKeyWillReset(host, draftOf(host, { port: 2222 }))).toBe(true);
  });

  it("其他字段变化或地址仅有空白差异不会清除", () => {
    const host = confirmed();
    expect(hostKeyWillReset(host, draftOf(host))).toBe(false);
    expect(hostKeyWillReset(host, draftOf(host, { address: ` ${host.address} ` }))).toBe(false);
    expect(
      hostKeyWillReset(host, draftOf(host, { username: "ops", sudo: true, onlinePort: 80 })),
    ).toBe(false);
  });
});
