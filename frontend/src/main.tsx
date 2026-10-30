import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./styles/tokens.css";
import "./styles/components.css";
import "./styles/controls.css";
import "./styles/overlays.css";
import "./styles/app.css";
import "./styles/auth.css";
import App from "./App.tsx";
import { createAppServices } from "./app/services.ts";
import { initTheme } from "./pages/settings/theme.ts";

// 外观主题是本机偏好（localStorage），首屏渲染前恢复，避免先按系统主题闪一下。
initTheme();

const root = document.getElementById("root");
if (!root) {
  throw new Error("index.html 缺少 #root 挂载节点");
}

// 会话、实时连接与查询缓存在整个页面生命周期内只创建一次。
const services = createAppServices();

createRoot(root).render(
  <StrictMode>
    <App services={services} />
  </StrictMode>,
);
