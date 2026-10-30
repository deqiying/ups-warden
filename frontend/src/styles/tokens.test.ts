/// <reference types="node" />
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, it } from "vite-plus/test";

// vitest 对 CSS 的 ?raw 导入返回空串，因此直接读文件；测试根目录即 frontend/。
const css = readFileSync(resolve(process.cwd(), "src/styles/tokens.css"), "utf8");

// F9-3 对比度回归（frontend-plan Q-D5）：文字 token 在常见底色上须达到 WCAG AA（普通文字 4.5:1）。
// 直接解析 tokens.css，避免“改了 CSS 却没人发现对比度又掉下去”。

type RGB = [number, number, number];

/** 取出以 selector 开头的第一个规则块里的自定义属性。 */
function block(selector: string): Map<string, string> {
  const start = css.indexOf(`${selector} {`);
  if (start < 0) throw new Error(`tokens.css 缺少规则块 ${selector}`);
  const body = css.slice(css.indexOf("{", start) + 1, css.indexOf("}", start));
  const map = new Map<string, string>();
  for (const m of body.matchAll(/--([\w-]+):\s*([^;]+);/g)) map.set(m[1]!, m[2]!.trim());
  return map;
}

/** 解析 #rrggbb 或 rgba(r, g, b, a)；返回 RGB 与不透明度。 */
function parse(value: string): { rgb: RGB; a: number } {
  const hex = value.match(/^#([0-9a-f]{6})$/i);
  if (hex) {
    const n = parseInt(hex[1]!, 16);
    return { rgb: [(n >> 16) & 255, (n >> 8) & 255, n & 255], a: 1 };
  }
  const m = value.match(/^rgba?\(([^)]+)\)$/);
  if (!m) throw new Error(`无法解析颜色 ${value}`);
  const p = m[1]!.split(",").map((s) => Number(s.trim()));
  return { rgb: [p[0]!, p[1]!, p[2]!], a: p[3] ?? 1 };
}

/** 把（可能半透明的）前景叠到不透明底色上。 */
function over(fg: string, base: RGB): RGB {
  const { rgb, a } = parse(fg);
  return rgb.map((v, i) => v * a + base[i]! * (1 - a)) as RGB;
}

function luminance([r, g, b]: RGB): number {
  const f = (v: number) => {
    const s = v / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
}

function contrast(a: RGB, b: RGB): number {
  const [x, y] = [luminance(a), luminance(b)];
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
}

const AA = 4.5;

function checkTheme(name: string, tokens: Map<string, string>) {
  const t = (key: string) => {
    const v = tokens.get(key);
    if (!v) throw new Error(`${name} 缺少 --${key}`);
    return v;
  };
  const solid = (key: string) => parse(t(key)).rgb;

  // 页面里实际出现的底色：卡片、页面底、侧栏材质与填充（分段控件、输入框）叠在页面底上。
  const bg = solid("bg");
  const surfaces: Record<string, RGB> = {
    surface: solid("surface"),
    bg,
    "surface-2": solid("surface-2"),
    sidebar: over(t("material-sidebar"), bg),
    "fill on bg": over(t("fill"), bg),
    "fill on surface": over(t("fill"), solid("surface")),
  };

  describe(`${name}：文字 token 对常见底色`, () => {
    for (const key of ["label", "label-2", "label-3"]) {
      for (const [surfaceName, surface] of Object.entries(surfaces)) {
        // 效果图里 label-3 只用于次要文字，仍按 AA 要求；填充底上的 label-3 是最差情形。
        it(`--${key} 在 ${surfaceName} 上 ≥ ${AA}:1`, () => {
          expect(contrast(solid(key), surface)).toBeGreaterThanOrEqual(AA);
        });
      }
    }
  });

  describe(`${name}：状态 ink 对自身 tint`, () => {
    const pairs: [string, string][] = [
      ["green-ink", "green-tint"],
      ["orange-ink", "orange-tint"],
      ["red-ink", "red-tint"],
      ["yellow-ink", "yellow-tint"],
      ["indigo-ink", "indigo-tint"],
      ["gray-ink", "gray-tint"],
      ["link", "accent-tint"],
    ];
    for (const [ink, tint] of pairs) {
      it(`--${ink} 在 --${tint} 上 ≥ ${AA}:1`, () => {
        const base = solid("surface");
        expect(contrast(solid(ink), over(t(tint), base))).toBeGreaterThanOrEqual(AA);
      });
    }
  });

  it("白色文字在 --accent-solid（主按钮、侧栏当前项）上 ≥ 4.5:1", () => {
    expect(contrast([255, 255, 255], solid("accent-solid"))).toBeGreaterThanOrEqual(AA);
  });
}

checkTheme("浅色", block(":root"));
checkTheme(
  "深色",
  // 深色块只覆盖差异项；其余沿用浅色（如 --link 在深色块里自带，这里合并后检查）。
  new Map([...block(":root"), ...block(':root[data-theme="dark"]')]),
);

describe("label 层级", () => {
  it("浅色：label-2 比 label-3 更深，保持主次顺序", () => {
    const t = block(":root");
    expect(luminance(parse(t.get("label-2")!).rgb)).toBeLessThan(
      luminance(parse(t.get("label-3")!).rgb),
    );
  });
  it("深色：label-2 比 label-3 更亮", () => {
    const t = block(':root[data-theme="dark"]');
    expect(luminance(parse(t.get("label-2")!).rgb)).toBeGreaterThan(
      luminance(parse(t.get("label-3")!).rgb),
    );
  });
});
