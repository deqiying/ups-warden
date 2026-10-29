import { describe, expect, it } from "vite-plus/test";

import { changedFields, confirmBody, secretFields } from "./patch.ts";

describe("changedFields", () => {
  it("只返回发生变化的键", () => {
    const original = { name: "a", enabled: true, port: 22 };
    const draft = { name: "b", enabled: true, port: 22 };
    expect(changedFields(original, draft)).toEqual({ name: "b" });
  });

  it("无变化时返回空对象", () => {
    expect(changedFields({ a: 1, b: "x" }, { a: 1, b: "x" })).toEqual({});
  });

  it("对象与数组按 JSON 内容比较，而非引用", () => {
    const original = { tags: ["a", "b"], cfg: { x: 1 } };
    expect(changedFields(original, { tags: ["a", "b"], cfg: { x: 1 } })).toEqual({});
    expect(changedFields(original, { tags: ["a", "c"], cfg: { x: 1 } })).toEqual({
      tags: ["a", "c"],
    });
    expect(changedFields(original, { tags: ["a", "b"], cfg: { x: 2 } })).toEqual({ cfg: { x: 2 } });
  });

  it("键顺序不同的对象视为修改", () => {
    const original = { cfg: { a: 1, b: 2 } };
    const draft = { cfg: { b: 2, a: 1 } };
    expect(changedFields(original, draft)).toEqual({ cfg: { b: 2, a: 1 } });
  });

  it("对象与 null 之间的变化被识别", () => {
    const original: { v: { a: number } | null } = { v: null };
    expect(changedFields(original, { v: { a: 1 } })).toEqual({ v: { a: 1 } });
    expect(changedFields({ v: { a: 1 } as { a: number } | null }, { v: null })).toEqual({
      v: null,
    });
  });

  it("基础类型按严格相等比较，0 与空串、false 视为不同", () => {
    expect(changedFields({ a: 0 as number | string }, { a: "" })).toEqual({ a: "" });
    expect(changedFields({ a: false as boolean | number }, { a: 0 })).toEqual({ a: 0 });
  });

  it("keys 限定参与比较的范围", () => {
    const original = { a: 1, b: 1, c: 1 };
    const draft = { a: 2, b: 2, c: 2 };
    expect(changedFields(original, draft, ["a", "c"])).toEqual({ a: 2, c: 2 });
    expect(changedFields(original, draft, [])).toEqual({});
  });

  it("变化为 undefined 的键会出现在结果中", () => {
    const original: { a?: number } = { a: 1 };
    const draft: { a?: number } = { a: undefined };
    const out = changedFields(original, draft);
    expect("a" in out).toBe(true);
    expect(out.a).toBeUndefined();
  });
});

describe("secretFields", () => {
  it("非空值原样提交", () => {
    expect(secretFields({ password: "p", token: "t" })).toEqual({ password: "p", token: "t" });
  });

  it("空串、undefined、null、空对象省略", () => {
    expect(
      secretFields({ password: "", token: undefined, key: null, headers: {}, community: "c" }),
    ).toEqual({ community: "c" });
  });

  it("非空对象与数组原样提交", () => {
    expect(secretFields({ headers: { Authorization: "x" }, list: [] })).toEqual({
      headers: { Authorization: "x" },
      list: [],
    });
  });

  it("0 与 false 不是留空", () => {
    expect(secretFields({ a: 0, b: false })).toEqual({ a: 0, b: false });
  });

  it("clear 去重后放入 clear_secrets", () => {
    expect(secretFields({ password: "" }, ["password", "token", "password"])).toEqual({
      clear_secrets: ["password", "token"],
    });
  });

  it("同一键既有新值又要求清除时抛错", () => {
    expect(() => secretFields({ password: "new" }, ["password"])).toThrow(/password/);
  });

  it("留空的键可以同时出现在 clear 中", () => {
    expect(secretFields({ password: "", token: "t" }, ["password"])).toEqual({
      token: "t",
      clear_secrets: ["password"],
    });
  });

  it("没有 clear 时不出现 clear_secrets 键", () => {
    expect("clear_secrets" in secretFields({ a: "1" })).toBe(false);
    expect("clear_secrets" in secretFields({ a: "1" }, [])).toBe(false);
    expect("clear_secrets" in secretFields({})).toBe(false);
  });
});

describe("confirmBody", () => {
  it("严格等于 { confirm: true }", () => {
    expect(confirmBody()).toStrictEqual({ confirm: true });
  });

  it("每次返回新对象", () => {
    expect(confirmBody()).not.toBe(confirmBody());
  });
});
