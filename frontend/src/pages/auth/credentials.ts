/*
 * 用户名与密码的前端校验，镜像后端 auth.username / auth.validPassword（monitoring-api.md「初始化、登录与用户」）。
 * 只用于即时提示与禁用提交；最终以后端 422 validation_failed 为准。
 */

const USERNAME_PATTERN = /^[A-Za-z0-9._-]{1,64}$/;

/** 用户名 1～64 个 ASCII 字母、数字、`.`、`_`、`-`；后端统一转小写，长度按字节计（全 ASCII 时等于字符数）。 */
export function isValidUsername(name: string): boolean {
  return USERNAME_PATTERN.test(name);
}

/** 与后端一致统一转小写；只处理 ASCII，非法字符原样保留以便提示。 */
export function normalizeUsername(name: string): string {
  return name.replace(/[A-Z]/g, (c) => c.toLowerCase());
}

/** 密码字符数：按 Unicode 码点计，与后端 utf8.RuneCountInString 一致（emoji 计 1 个）。 */
export function passwordLength(password: string): number {
  // 有意按码点而非字素计数：代理对算 1 个，组合字符各算 1 个，与后端规则一致。
  return password.length - (password.match(/[\uD800-\uDBFF][\uDC00-\uDFFF]/g)?.length ?? 0);
}

/** 密码 UTF-8 字节数，后端上限 1024 字节。 */
export function passwordBytes(password: string): number {
  return new TextEncoder().encode(password).length;
}

export const PASSWORD_MIN_CHARS = 8;
export const PASSWORD_MAX_BYTES = 1024;

export interface PasswordChecks {
  minLength: boolean;
  maxBytes: boolean;
  matches: boolean;
}

export function checkPassword(password: string, confirm: string): PasswordChecks {
  return {
    minLength: passwordLength(password) >= PASSWORD_MIN_CHARS,
    maxBytes: passwordBytes(password) <= PASSWORD_MAX_BYTES,
    matches: password !== "" && password === confirm,
  };
}

/** 后端限速默认 15 分钟（Retry-After: 900）；缺失或无法解析时按此值倒计时。 */
export const DEFAULT_LOCK_SECONDS = 900;
