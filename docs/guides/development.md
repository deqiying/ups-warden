# 开发指南

[返回文档导航](../README.md)

本文适用于已完成 M0/M1、已实现 M2 只读驱动与本机模拟器的后端，以及前端脚手架，说明已有运行入口、配置与检查方式。M2 真机 CP-1 待验收；采集调度、规则、通知与关机编排尚未实现，业务目标见[需求草案](../planning/requirements.md)。

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
mise x -- go run ./cmd/ups-warden --data-dir ../data
```

默认监听 `127.0.0.1:8080`。后端会创建数据目录、获取独占文件锁、加载主密钥、迁移 SQLite，并校验五个内置 Profile，然后启动 HTTP；不连接 UPS，也不托管前端页面。离线数据模型和映射契约见 [Profile 与归一化](profiles.md)。

### 启动配置

定义与优先级来自 [config.go](../../backend/internal/config/config.go)：**显式命令行参数 > 非空环境变量 > 默认值**。环境变量为空时回退默认值。相对路径以进程工作目录为基准；在 `backend/` 运行时请显式传入 `--data-dir ../data`。

| 参数 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `--listen` | `UPS_WARDEN_LISTEN` | `127.0.0.1:8080` | HTTP 监听地址；端口 0 可申请临时端口 |
| `--data-dir` | `UPS_WARDEN_DATA_DIR` | `./data` | 数据目录；推荐统一使用仓库根目录 `data/` |
| `--log-level` | `UPS_WARDEN_LOG_LEVEL` | `info` | 常用 `debug`、`info`、`warn`、`error`；由 `slog.Level.UnmarshalText` 解析 |
| `--log-file` | `UPS_WARDEN_LOG_FILE` | 空 | 空时写 stderr；指定后写滚动文件，20 MB、最多 5 份备份 |
| `--log-format` | `UPS_WARDEN_LOG_FORMAT` | `text` | `text` 或 `json` |
| `--script-dir` | `UPS_WARDEN_SCRIPT_DIR` | `<data-dir>/scripts` | 仅解析目录配置，脚本执行在 M6 实现；当前不创建该目录 |

例如在 **`backend/`** 目录启用调试日志并更改端口：

```bash
mise x -- go run ./cmd/ups-warden --data-dir ../data --listen 127.0.0.1:8081 --log-level debug
```

- 已知敏感属性键和 `secret.String` 自动掩码；不得把原始凭据插入日志消息、任意结构体或错误字符串中。协议错误和外部 URL 清洗随后续模块实现。
- 无效参数、日志文件打不开、目录锁冲突、主密钥错误、数据库迁移失败、监听失败或关闭失败均使进程非零退出；日志文件模式下初始化失败仍可从 stderr 排查。
- 入口处理 `os.Interrupt` / `SIGTERM`，HTTP 优雅关闭最多等待 10 秒，超时强制关闭连接；请求头超时为 5 秒。后台任务必须响应 context 取消；错误或 panic 以 1～30 秒指数退避重启，正常返回不重启，HTTP 异常退出则结束进程。
- TLS、脚本编辑等 P1 参数尚未实现。

### 数据目录与主密钥

- `ups-warden.lock`：操作系统独占文件锁；第二实例立即报“数据目录已被占用”。进程退出自动释放，**不要手工删除仍被持有的锁文件**。目录应位于本地磁盘，不依赖网络共享文件系统的锁语义。
- `ups-warden.db`：SQLite WAL，`synchronous=FULL`、`busy_timeout=5000`、`foreign_keys=ON`；写池 1 条连接、只读池最多 4 条连接。启动执行 embed 前向迁移，初始化 25 张表；建表不等于相关业务模块已实现。已有迁移不可改写，后续修改使用新编号脚本。
- `master.key`：默认生成并保存原始 32 字节密钥，Linux 文件权限为 `0600`。也可用 `UPS_WARDEN_MASTER_KEY` 提供 Base64 编码的 32 字节密钥，优先于文件且不会写回文件。该变量不是命令行参数，避免密钥出现在命令历史或进程参数中。
- 凭据使用 AES-256-GCM，以 `表名.列名` 为 AAD；启动会检查已有加密凭据。错误密钥、损坏文件或解密失败均不会被静默清空。请恢复正确密钥备份；凭据管理界面尚未实现，当前不能通过 WebUI 重新填写。
- **Windows 的 `0600` 不提供 Unix 权限保护**：请用数据目录 ACL 限制为服务账号可访问，启动日志也会提示。不要依赖 Git 忽略规则作为访问控制。
- 备份时先停止服务，再复制完整数据目录（包括可能存在的 WAL 文件）；使用环境密钥时另行安全备份该密钥。丢失密钥将无法解密已有凭据，不要删除密钥后让服务重新生成。

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

完成第 1 节工具链安装后，在**仓库根目录**执行：

```bash
mise run backend:check
mise run backend:build
```

`backend:check` 使用 Bash / Git Bash，依次检查 `gofmt -l .`（有输出即失败）、`go vet ./...`、`go test ./...`。`backend:build` 使用 `CGO_ENABLED=0` 构建本机二进制，输出到 `.cache/build/`（Windows 为 `ups-warden.exe`），尚不包含 WebUI。

需要单独运行时，在 **`backend/`** 执行：

```bash
mise x -- gofmt -l .
mise x -- go vet ./...
mise x -- go test ./...
```

后端测试覆盖配置、日志与脱敏、主密钥、目录锁、SQLite、app 生命周期及包依赖边界；M1 覆盖 Profile、归一化与 60 组 ET1K 离线回归；M2 覆盖三协议编码器、发送护栏、取消/重连、部分错误、模拟器场景和三协议归一化一致性。时间逻辑使用 `testing/synctest` 或注入场景时钟。仍无采集调度或关机编排测试。Linux 交叉编译不能替代 Linux 实机运行验证。

本机 `upssim` 与显式 `upsprobe` 的参数、安全边界及示例见[协议驱动与本机模拟器](protocol-drivers.md)。主服务仍不主动采集设备。

在仓库根目录可单独执行短时 fuzz；所有输入均离线，不连接设备：

```bash
mise x -- go -C backend test ./internal/profile -run '^$' -fuzz '^FuzzParseReadPlan$' -fuzztime=15s
mise x -- go -C backend test ./internal/normalize -run '^$' -fuzz '^FuzzNormalizeAYI9$' -fuzztime=15s
```

在 **`frontend/`** 目录：

```bash
mise x -- pnpm check
mise x -- pnpm test
mise x -- pnpm build
```

- `check` 调用 `vp check`，覆盖格式、lint 与配置启用的类型检查。
- `test` 调用 `vp test run`；现有 [App.test.tsx](../../frontend/src/App.test.tsx) 使用服务端渲染检查项目名称，不是浏览器端到端测试。
- `build` 执行 `tsc -b && vp build`，产物位于 `frontend/dist/`；不会复制到后端或嵌入 Go 二进制。
- 当前没有统一前后端发布任务、文档构建或专用链接检查脚本。仅修改文档时核对路径、相对链接、参数和示例，不为验证文档访问真实设备。

## 5. 常见排查与安全边界

| 现象 | 优先检查 |
|---|---|
| 工具命令缺失或版本不符 | 在根目录执行 `mise install`，再使用 `mise x --`；不要绕过 `GOTOOLCHAIN=local` 自动下载 Go |
| 后端无法启动 | 查看 stderr 或日志文件；检查端口、配置、目录权限、是否有另一实例持锁，以及主密钥和数据库是否完整 |
| 前端代理报连接错误 | 确认后端已启动，检查 `UPS_WARDEN_BACKEND` 是否与 `-listen` 一致且非空 |
| `/api/v1` 返回 404 | 当前尚无业务 API；先用 `/healthz` 检查本机后端 |
| 后端根路径没有 WebUI | 前端尚未内嵌；开发阶段需单独启动前端开发服务器 |

完整设备安全规则见 [AGENTS.md](../../AGENTS.md)。自动化测试只用临时目录、回环服务、模拟器或脱敏录制数据，不需要真实 UPS。实际设备地址、community、密码和未脱敏数据不得写入文档或 Git 跟踪文件；本地敏感数据统一放仓库根目录 `/data/`（已在 [.gitignore](../../.gitignore) 中忽略）。额外忽略 `backend/data/` 仅防止误用默认目录时泄漏，不改变推荐位置。
