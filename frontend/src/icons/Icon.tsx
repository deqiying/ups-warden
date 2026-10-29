import { ICON_PATHS, type IconName } from "./icons.ts";

export type { IconName } from "./icons.ts";

export interface IconProps {
  name: IconName;
  /** 额外类名；默认 `.i` 的 1em 描边样式始终保留。 */
  className?: string;
  /**
   * 图标单独承载含义时必须提供（例如只有图标的按钮外层未写 aria-label 时）。
   * 省略时图标视为装饰，对辅助技术隐藏——此时旁边必须有可读文字。
   */
  label?: string;
}

/** 引用 {@link IconSprite} 中的 symbol；应用根部必须渲染一次 IconSprite。 */
export function Icon({ name, className, label }: IconProps) {
  const cls = className ? `i ${className}` : "i";
  if (label) {
    return (
      <svg className={cls} role="img" aria-label={label}>
        <use href={`#i-${name}`} />
      </svg>
    );
  }
  return (
    <svg className={cls} aria-hidden="true" focusable="false">
      <use href={`#i-${name}`} />
    </svg>
  );
}

// 路径为仓库内固定常量（非用户输入），可以安全地作为 SVG 标记注入。
const SPRITE = Object.entries(ICON_PATHS)
  .map(([id, body]) => `<symbol id="i-${id}" viewBox="0 0 24 24">${body}</symbol>`)
  .join("");

/** 隐藏的 SVG symbol 精灵，整个应用只渲染一次。 */
export function IconSprite() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      aria-hidden="true"
      style={{ position: "absolute", width: 0, height: 0, overflow: "hidden" }}
      dangerouslySetInnerHTML={{ __html: SPRITE }}
    />
  );
}
