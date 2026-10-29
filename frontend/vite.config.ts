import react from "@vitejs/plugin-react";
import { defineConfig, lazyPlugins } from "vite-plus";

// 开发时把后端接口代理到本地 Go 服务，默认地址与后端 -listen 默认值一致。
const backendOrigin = process.env.UPS_WARDEN_BACKEND ?? "http://127.0.0.1:8080";

export default defineConfig({
  plugins: lazyPlugins(() => [react()]),
  server: {
    proxy: {
      "/api": {
        target: backendOrigin,
        configure(proxy) {
          // 后端重启或崩溃时，代理不会结束已开始的流式响应，SSE 会一直挂起而不触发 error。
          // 上游响应未完整结束（complete=false）就关闭时，主动断开下游，让前端按退避重连。
          proxy.on("proxyRes", (proxyRes, _req, res) => {
            proxyRes.on("close", () => {
              if (!proxyRes.complete) res.destroy();
            });
          });
        },
      },
      "/healthz": { target: backendOrigin },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  test: {
    include: ["src/**/*.test.{ts,tsx}"],
    // 组件测试需要 DOM；纯函数测试同样在 happy-dom 下运行，不影响结果。
    environment: "happy-dom",
    setupFiles: ["src/test/setup.ts"],
  },
  fmt: {
    ignorePatterns: ["dist/**"],
  },
  lint: {
    plugins: ["react", "typescript", "oxc"],
    ignorePatterns: ["dist/**"],
    rules: {
      "react/rules-of-hooks": "error",
      "react/only-export-components": ["warn", { allowConstantExport: true }],
      "vite-plus/prefer-vite-plus-imports": "error",
    },
    options: {
      typeAware: true,
      typeCheck: true,
    },
    jsPlugins: [{ name: "vite-plus", specifier: "vite-plus/oxlint-plugin" }],
  },
});
