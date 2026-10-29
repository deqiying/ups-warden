import { cleanup } from "@testing-library/react";
import { afterEach } from "vite-plus/test";

// 未启用 globals，Testing Library 不会自动清理；每个用例后卸载已渲染的组件。
afterEach(() => {
  cleanup();
});
