import { renderToString } from "react-dom/server";
import { describe, expect, it } from "vite-plus/test";
import App from "./App.tsx";

// 冒烟测试：验证 TypeScript、React 与 vp test 的链路可用。
describe("App", () => {
  it("渲染项目名称", () => {
    expect(renderToString(<App />)).toContain("UPS Warden");
  });
});
