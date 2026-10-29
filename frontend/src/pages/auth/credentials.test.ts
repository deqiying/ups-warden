import { describe, expect, it } from "vite-plus/test";
import {
  checkPassword,
  isValidUsername,
  normalizeUsername,
  passwordBytes,
  passwordLength,
} from "./credentials.ts";

describe("isValidUsername", () => {
  it("空串非法", () => {
    expect(isValidUsername("")).toBe(false);
  });

  it("长度上限为 64：64 个字符合法，65 个字符非法", () => {
    expect(isValidUsername("a".repeat(64))).toBe(true);
    expect(isValidUsername("a".repeat(65))).toBe(false);
  });

  it("含空格、中文、@ 非法", () => {
    expect(isValidUsername("ad min")).toBe(false);
    expect(isValidUsername(" admin")).toBe(false);
    expect(isValidUsername("管理员")).toBe(false);
    expect(isValidUsername("admin@example")).toBe(false);
  });

  it("`.` `_` `-`、数字与大写字母合法", () => {
    expect(isValidUsername("a.b_c-d")).toBe(true);
    expect(isValidUsername("...")).toBe(true);
    expect(isValidUsername("user01")).toBe(true);
    expect(isValidUsername("Admin")).toBe(true);
  });
});

describe("normalizeUsername", () => {
  it("ASCII 大写转小写", () => {
    expect(normalizeUsername("AdMiN.User_1")).toBe("admin.user_1");
  });

  it("只处理 ASCII：非 ASCII 字符（含大写非 ASCII 字母）原样保留", () => {
    expect(normalizeUsername("ÄB管理")).toBe("Äb管理");
  });
});

describe("passwordLength", () => {
  it("按 Unicode 码点计：emoji 计 1", () => {
    expect(passwordLength("😀")).toBe(1);
    expect(passwordLength("a😀b")).toBe(3);
    expect(passwordLength("")).toBe(0);
  });

  it("7 个 emoji 不足 8，8 个满足", () => {
    expect(checkPassword("😀".repeat(7), "").minLength).toBe(false);
    expect(checkPassword("😀".repeat(8), "").minLength).toBe(true);
  });
});

describe("passwordBytes", () => {
  it("按 UTF-8 字节计：ASCII 1 字节、中文 3 字节、emoji 4 字节", () => {
    expect(passwordBytes("abc")).toBe(3);
    expect(passwordBytes("中文")).toBe(6);
    expect(passwordBytes("中")).toBe(3);
    expect(passwordBytes("😀")).toBe(4);
  });
});

describe("checkPassword", () => {
  it("minLength 边界：7 个字符不足，8 个字符满足", () => {
    expect(checkPassword("a".repeat(7), "").minLength).toBe(false);
    expect(checkPassword("a".repeat(8), "").minLength).toBe(true);
  });

  it("maxBytes 边界：1024 字节满足，1025 字节超限", () => {
    expect(checkPassword("a".repeat(1024), "").maxBytes).toBe(true);
    expect(checkPassword("a".repeat(1025), "").maxBytes).toBe(false);
  });

  it("maxBytes 按字节而非字符计：342 个中文 = 1026 字节超限，341 个 = 1023 字节满足", () => {
    expect(checkPassword("中".repeat(341), "").maxBytes).toBe(true);
    expect(checkPassword("中".repeat(342), "").maxBytes).toBe(false);
  });

  it("matches：两者均为空串为 false", () => {
    expect(checkPassword("", "").matches).toBe(false);
  });

  it("matches：不一致为 false，一致为 true", () => {
    expect(checkPassword("password1", "password2").matches).toBe(false);
    expect(checkPassword("password1", "").matches).toBe(false);
    expect(checkPassword("password1", "password1").matches).toBe(true);
  });
});
