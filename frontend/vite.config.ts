import react from "@vitejs/plugin-react";
import { defineConfig, lazyPlugins } from "vite-plus";

// 开发时把后端接口代理到本地 Go 服务，默认地址与后端 -listen 默认值一致。
const backendOrigin = process.env.UPS_WARDEN_BACKEND ?? "http://127.0.0.1:8080";

export default defineConfig({
  plugins: lazyPlugins(() => [react()]),
  server: {
    proxy: {
      "/api": { target: backendOrigin },
      "/healthz": { target: backendOrigin },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  test: {
    include: ["src/**/*.test.{ts,tsx}"],
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
