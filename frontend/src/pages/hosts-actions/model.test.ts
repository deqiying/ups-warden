import { describe, expect, it } from "vite-plus/test";
import { emptyActionDraft, type ActionDraft } from "../../api/actions.ts";
import { draftFromHost, emptyHostDraft, type HostDraft } from "../../api/hosts.ts";
import { wireAction, wireActionRun, wireHost } from "../../test/fixtures.ts";
import {
  filterRuns,
  hostKeyRemaining,
  HOSTKEY_TTL_S,
  latestRunByAction,
  triggerText,
  validateActionDraft,
  validateHostDraft,
} from "./model.ts";

const validHost = (overrides: Partial<HostDraft> = {}): HostDraft => ({
  ...emptyHostDraft(),
  name: "NAS-01",
  address: "nas-01.lan",
  username: "admin",
  password: "pw",
  ...overrides,
});

describe("validateHostDraft", () => {
  it("合法的新建草稿无错误", () => {
    expect(validateHostDraft(validHost())).toEqual({});
  });

  it("名称：必填；按 UTF-8 字节限制 128（43 个汉字 = 129 字节报错）", () => {
    expect(validateHostDraft(validHost({ name: "  " })).name).toBeDefined();
    expect(validateHostDraft(validHost({ name: "汉".repeat(42) })).name).toBeUndefined();
    expect(validateHostDraft(validHost({ name: "汉".repeat(43) })).name).toMatch(/128 字节/);
    expect(validateHostDraft(validHost({ name: "a\u0000" })).name).toMatch(/空字符/);
  });

  it("地址：必填；不含协议 / 路径 / 用户名 / 空白", () => {
    expect(validateHostDraft(validHost({ address: "" })).address).toBeDefined();
    for (const bad of ["ssh://nas", "nas/path", "root@nas", "na s"]) {
      expect(validateHostDraft(validHost({ address: bad })).address).toBeDefined();
    }
    expect(validateHostDraft(validHost({ address: "192.168.1.5" })).address).toBeUndefined();
  });

  it("端口：1～65535 的整数，NaN 报错；TCP 在线检测端口同样校验，ICMP 不校验", () => {
    for (const bad of [0, 65536, 1.5, Number.NaN]) {
      expect(validateHostDraft(validHost({ port: bad })).port).toBeDefined();
    }
    expect(validateHostDraft(validHost({ port: 65535 })).port).toBeUndefined();
    expect(validateHostDraft(validHost({ onlinePort: 0 })).onlinePort).toBeDefined();
    expect(
      validateHostDraft(validHost({ onlineType: "icmp", onlinePort: Number.NaN })).onlinePort,
    ).toBeUndefined();
  });

  it("用户名：必填、不超过 128 字节、不含换行或 NUL", () => {
    expect(validateHostDraft(validHost({ username: " " })).username).toBeDefined();
    expect(validateHostDraft(validHost({ username: "a".repeat(129) })).username).toMatch(/128/);
    expect(validateHostDraft(validHost({ username: "a\nb" })).username).toMatch(/换行/);
  });

  it("新建：密码方式必须填密码，私钥方式必须填私钥", () => {
    expect(validateHostDraft(validHost({ password: "" })).password).toBeDefined();
    const key = validHost({ authMethod: "private_key", password: "" });
    expect(validateHostDraft(key).privateKey).toBeDefined();
    expect(validateHostDraft({ ...key, privateKey: "KEY" }).privateKey).toBeUndefined();
  });

  it("编辑：认证方式不变且已设置凭据时可留空；切换认证方式后必须重新填写", () => {
    const original = wireHost({ password_set: true });
    expect(validateHostDraft({ ...draftFromHost(original) }, original)).toEqual({});

    const switched = { ...draftFromHost(original), authMethod: "private_key" as const };
    expect(validateHostDraft(switched, original).privateKey).toBeDefined();

    // 原本就没有设置密码（异常数据）时，留空仍需提示。
    const unset = wireHost({ password_set: false });
    expect(validateHostDraft(draftFromHost(unset), unset).password).toBeDefined();
  });
});

const validAction = (type: ActionDraft["type"], overrides: Partial<ActionDraft> = {}) => ({
  ...emptyActionDraft(type),
  name: "动作",
  ...overrides,
});

describe("validateActionDraft", () => {
  it("名称与超时：必填、1～3600 秒整数", () => {
    const base = validAction("plan", { planId: 1 });
    expect(validateActionDraft(base)).toEqual({});
    expect(validateActionDraft({ ...base, name: "" }).name).toBeDefined();
    for (const bad of [0, 3601, Number.NaN, 1.5]) {
      expect(validateActionDraft({ ...base, timeoutS: bad }).timeoutS).toBeDefined();
    }
  });

  it("notify：至少 1 个、最多 64 个渠道", () => {
    expect(validateActionDraft(validAction("notify")).channelIds).toBeDefined();
    const ids = (n: number) => Array.from({ length: n }, (_, i) => i + 1);
    expect(validateActionDraft(validAction("notify", { channelIds: ids(64) }))).toEqual({});
    expect(
      validateActionDraft(validAction("notify", { channelIds: ids(65) })).channelIds,
    ).toBeDefined();
  });

  it("http：新建必须填地址；编辑已设置地址时可留空", () => {
    expect(validateActionDraft(validAction("http")).url).toBeDefined();
    const original = wireAction({ type: "http", url_set: true, config: {} });
    expect(validateActionDraft(validAction("http"), original)).toEqual({});
    // 原动作不是 HTTP 时不存在可保留的地址。
    expect(validateActionDraft(validAction("http"), wireAction()).url).toBeDefined();
  });

  it("http：地址必须是 http(s)，不含内嵌凭据、片段与模板变量", () => {
    const url = (value: string) => validateActionDraft(validAction("http", { url: value })).url;
    expect(url("https://hook.example/x?a=1")).toBeUndefined();
    expect(url("ftp://hook.example")).toBeDefined();
    expect(url("hook.example")).toBeDefined();
    expect(url("https://user:pw@hook.example")).toMatch(/用户名或密码/);
    expect(url("https://hook.example/#frag")).toMatch(/片段/);
    expect(url("https://hook.example/{{event}}")).toMatch(/模板变量/);
  });

  it("http：Header 文本与期望状态码校验", () => {
    const base = { url: "https://hook.example" };
    expect(
      validateActionDraft(validAction("http", { ...base, headersText: "Host: x" })).headers,
    ).toBeDefined();
    expect(
      validateActionDraft(validAction("http", { ...base, expectedStatus: [199] })).expectedStatus,
    ).toBeDefined();
    expect(
      validateActionDraft(validAction("http", { ...base, expectedStatus: [200, 599] })),
    ).toEqual({});
  });

  it("script：必须是脚本目录内的相对路径", () => {
    const script = (value: string) => validateActionDraft(validAction("script", { script: value }));
    expect(script("").script).toBeDefined();
    expect(script("backup/run.sh")).toEqual({});
    for (const bad of ["/etc/passwd", "..\\x", "a/../b", "C:x"]) {
      expect(script(bad).script).toBeDefined();
    }
  });

  it("ssh：必须选主机并填命令（命令不超过 65536）", () => {
    const errors = validateActionDraft(validAction("ssh"));
    expect(errors.hostId).toBeDefined();
    expect(errors.command).toBeDefined();
    expect(validateActionDraft(validAction("ssh", { hostId: 1, command: "poweroff" }))).toEqual({});
    expect(
      validateActionDraft(validAction("ssh", { hostId: 1, command: "x".repeat(65537) })).command,
    ).toBeDefined();
  });

  it("plan：必须选计划", () => {
    expect(validateActionDraft(validAction("plan")).planId).toBeDefined();
  });
});

describe("filterRuns / latestRunByAction", () => {
  const runs = [
    wireActionRun({ id: 5, action_id: 1, status: "failed", dry_run: false }),
    wireActionRun({ id: 4, action_id: 2, status: "dry_run", dry_run: true }),
    wireActionRun({ id: 3, action_id: 1, status: "timeout", dry_run: false }),
    wireActionRun({ id: 2, action_id: 2, status: "unknown", dry_run: false }),
    wireActionRun({ id: 1, action_id: 1, status: "succeeded", dry_run: false }),
  ];

  it("全部：返回副本，不改变原数组", () => {
    const all = filterRuns(runs, "all");
    expect(all.map((r) => r.id)).toEqual([5, 4, 3, 2, 1]);
    expect(all).not.toBe(runs);
  });

  it("失败：failed / timeout / unknown", () => {
    expect(filterRuns(runs, "failed").map((r) => r.id)).toEqual([5, 3, 2]);
  });

  it("dry-run：只看 dry_run 标记", () => {
    expect(filterRuns(runs, "dry_run").map((r) => r.id)).toEqual([4]);
  });

  it("每个动作取 id 最大的一次，与输入顺序无关", () => {
    const latest = latestRunByAction(runs);
    expect(latest.get(1)?.id).toBe(5);
    expect(latest.get(2)?.id).toBe(4);
    expect(latest.size).toBe(2);
    expect(latestRunByAction([...runs].reverse()).get(1)?.id).toBe(5);
    expect(latestRunByAction([]).size).toBe(0);
  });
});

describe("hostKeyRemaining", () => {
  it("刚获取时为 300 秒，随时间递减并向上取整", () => {
    expect(HOSTKEY_TTL_S).toBe(300);
    expect(hostKeyRemaining(1_000, 1_000)).toBe(300);
    expect(hostKeyRemaining(1_000, 1_001)).toBe(300);
    expect(hostKeyRemaining(1_000, 2_000)).toBe(299);
    expect(hostKeyRemaining(0, 299_001)).toBe(1);
  });

  it("到期及之后为 0，不为负", () => {
    expect(hostKeyRemaining(0, 300_000)).toBe(0);
    expect(hostKeyRemaining(0, 900_000)).toBe(0);
  });
});

describe("triggerText", () => {
  it("手动 / 规则 / 计划", () => {
    expect(triggerText({ type: "manual", user_id: 1 })).toBe("手动");
    expect(triggerText({ type: "rule", rule_id: 7 })).toBe("规则 #7");
    expect(triggerText({ type: "rule", rule_id: 7, recovery: true })).toBe("规则 #7 · 恢复动作");
    expect(triggerText({ type: "rule" })).toBe("规则");
    expect(triggerText({ type: "plan", plan_run_id: 9 })).toBe("计划运行 #9");
    expect(triggerText({ type: "plan" })).toBe("关机计划");
  });

  it("未知类型原样显示；缺失或非对象显示 —", () => {
    expect(triggerText({ type: "scheduler" })).toBe("scheduler");
    expect(triggerText({ type: "" })).toBe("—");
    expect(triggerText(null)).toBe("—");
    expect(triggerText(undefined)).toBe("—");
    expect(triggerText("manual" as never)).toBe("—");
  });
});
