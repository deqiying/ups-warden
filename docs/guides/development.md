# 开发指南

[返回文档导航](../README.md)

本文适用于当前脚手架，说明已有的运行入口、配置与检查方式。业务目标见[需求草案](../planning/requirements.md)，不能把其中的部署方案或 API 列表作为当前能力使用。

## 1. 工具链与工作目录

- 先安装 mise；Go、Node.js、pnpm 的版本与缓存位置以根目录 [mise.toml](../../mise.toml) 为唯一来源，不在本指南重复固定版本号。
- 前端依赖使用 pnpm，Vite、测试、lint、格式化统一通过 Vite+（`vp`）调用；脚本见 [package.json](../../frontend/package.json)，catalog 见 [pnpm-workspace.yaml](../../frontend/pnpm-workspace.yaml)。
- Go 使用 `GOTOOLCHAIN=local`，不会按 `go.mod` 自动下载其他 Go 版本。工具链升级需同步检查 [go.mod](../../backend/go.mod) 与前端版本约束。
- 缓存统一在仓库根目录 `.cache/`；不要将缓存改到用户全局目录或在前后端下另建缓存目录。

以下命令面向 Bash / Git Bash。先在**仓库根目录**安装声明的工具链：

```bash
mise install
```

随后在 `frontend/` 安装依赖：

```bash
cd frontend
mise x -- pnpm install
```

其余章节各自注明起始目录，不要把不同章节的 `cd` 连续粘贴进同一终端。已激活 mise 的 shell 可以省略 `mise x --`。

## 2. 启动后端

新终端，从**仓库根目录**执行：

```bash
cd backend
mise x -- go run ./cmd/ups-warden
```

默认监听 `127.0.0.1:8080`。当前后端不连接 UPS、不创建业务数据库，也不托管前端页面。

### 启动配置

定义与优先级来自 [main.go](../../backend/cmd/ups-warden/main.go)：**显式命令行参数 > 非空环境变量 > 默认值**。环境变量为空时回退默认值，不能把空值当作有效配置。

| 参数 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `-listen` | `UPS_WARDEN_LISTEN` | `127.0.0.1:8080` | HTTP 监听地址 |
| `-log-level` | `UPS_WARDEN_LOG_LEVEL` | `info` | 日志级别；常用值为 `debug`、`info`、`warn`、`error`，由 `slog.Level.UnmarshalText` 解析 |

例如在 **`backend/`** 目录启用调试日志并更改端口：

```bash
mise x -- go run ./cmd/ups-warden -listen 127.0.0.1:8081 -log-level debug
```

- 日志以文本格式写入 stderr；当前没有日志文件或滚动配置。
- 无效日志级别、监听失败或关闭失败会记录错误并以非零状态退出。
- 入口处理 `os.Interrupt` / `SIGTERM`，优雅关闭最多等待 10 秒；HTTP 读取请求头超时为 5 秒。
- 规划中的 `--data-dir`、`--log-file`、`--log-format`、`--script-dir`、TLS 等参数尚未实现。

### 健康检查

路由与响应定义见 [server.go](../../backend/internal/server/server.go)：

| 请求 | 当前行为 |
|---|---|
| `GET /healthz` | HTTP 200，`Content-Type: application/json`，响应体 `{"status":"ok"}`（末尾换行） |
| `POST /healthz` | HTTP 405；当前方法路由不接受 POST |
| `/api/v1/...` | 尚未注册，当前返回 HTTP 404 |

健康检查无需鉴权，只证明进程能处理 HTTP 请求，**不表示 UPS 连通或业务就绪**。占位前端没有调用它；需要单独检查。

启动后端后，可在另一终端访问本机服务（这不是 UPS 网卡地址）：

```bash
curl --fail http://127.0.0.1:8080/healthz
```

## 3. 启动前端与开发代理

另一终端，从**仓库根目录**执行：

```bash
cd frontend
mise x -- pnpm dev
```

浏览器打开开发服务器在终端输出的地址。当前 [App.tsx](../../frontend/src/App.tsx) 只渲染项目名称和“业务页面尚未实现”的提示，没有设备管理或实时状态功能。

[vite.config.ts](../../frontend/vite.config.ts) 为开发服务器配置了 `/api` 和 `/healthz` 代理，默认目标为 `http://127.0.0.1:8080`。`UPS_WARDEN_BACKEND` 在启动开发服务器时读取；未设置时使用默认目标，**空字符串不会回退默认值**，不要设置为空。

如果后端改为 `127.0.0.1:8081`，在 **`frontend/`** 目录同步指定代理目标并启动：

```bash
UPS_WARDEN_BACKEND=http://127.0.0.1:8081 mise x -- pnpm dev
```

修改环境变量后需重启开发服务器。代理只提供转发，不会创建尚未实现的 `/api/v1` 端点；这些配置也不等于生产环境部署配置。

## 4. 检查、测试与构建

完成第 1 节的工具链与依赖安装后执行。以下是维护命令，不是本次文档整理已运行这些命令的声明。

在 **`backend/`** 目录：

```bash
mise x -- gofmt -l .
mise x -- go vet ./...
mise x -- go test ./...
mise x -- go build -o bin/ups-warden ./cmd/ups-warden
```

`gofmt -l .` 预期无输出。后端测试见 [server_test.go](../../backend/internal/server/server_test.go)，目前只检查健康响应和 POST 被拒绝；没有协议模拟器、采集或关机编排测试。

在 **`frontend/`** 目录：

```bash
mise x -- pnpm check
mise x -- pnpm test
mise x -- pnpm build
```

- `check` 调用 `vp check`，覆盖格式、lint 与配置启用的类型检查。
- `test` 调用 `vp test run`；现有 [App.test.tsx](../../frontend/src/App.test.tsx) 使用服务端渲染检查项目名称，不是浏览器端到端测试。
- `build` 执行 `tsc -b && vp build`，产物位于 `frontend/dist/`；不会复制到后端或嵌入 Go 二进制。
- 当前没有统一 mise 构建 tasks、文档构建或专用链接检查脚本。仅修改文档时应核对路径、相对链接、参数和示例，不为验证文档访问真实设备。

## 5. 常见排查与安全边界

| 现象 | 优先检查 |
|---|---|
| 工具命令缺失或版本不符 | 在根目录执行 `mise install`，再使用 `mise x --`；不要绕过 `GOTOOLCHAIN=local` 自动下载 Go |
| 后端无法启动 | 查看 stderr；检查监听地址是否被占用，以及日志级别能否解析 |
| 前端代理报连接错误 | 确认后端已启动，检查 `UPS_WARDEN_BACKEND` 是否与 `-listen` 一致且非空 |
| `/api/v1` 返回 404 | 当前尚无业务 API；先用 `/healthz` 检查本机后端 |
| 后端根路径没有 WebUI | 前端尚未内嵌；开发阶段需单独启动前端开发服务器 |

完整设备安全规则见 [AGENTS.md](../../AGENTS.md)。自动化测试只用模拟器或脱敏录制数据；当前脚手架不需要真实 UPS。实际设备地址、community、密码和未脱敏数据不得写入文档或 Git 跟踪文件；本地敏感数据放仓库根目录 `/data/`（已在 [.gitignore](../../.gitignore) 中忽略），不是 `backend/data/` 或 `frontend/data/`。
