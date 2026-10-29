/** 只接受应用内路径作为返回地址，排除协议相对地址（//host）与认证页本身。 */
export function safeReturnPath(from: string | undefined): string {
  if (!from || !from.startsWith("/") || from.startsWith("//")) return "/";
  if (/^\/(login|setup)(?=$|[/?#])/.test(from)) return "/";
  return from;
}

/**
 * 组合输入框的类名。错误边框（is-error）保持到用户再次输入；抖动（is-shaking）每次失败播放一次，
 * 调用方在 animationend 时清除 shaking，以便下次失败重新播放。
 */
export function shakeClass(error: boolean, shaking: boolean): string {
  return ["field-group", error && "is-error", shaking && "is-shaking"].filter(Boolean).join(" ");
}
