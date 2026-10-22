# 开发指南

[返回文档导航](../README.md)

本文适用于已实现并通过本机验证的 M0～M7 后端，以及前端脚手架。M4 提供认证/审计/监控 API/SSE/静态 SPA，M5 提供通知，M6 提供规则联动、动作与受控主机，M7 提供关机计划编排；M2 真机 CP-1、CP-2 双方联调待验收，CP-3 关机编排评审未完成。M8 系统发布未实现，本轮到 M7 停止。主服务会采集启用的数据源并评估启用规则，开发测试只能使用独立临时数据库 + loopback 模拟器，不访问真实 data。模块语义见[采集、状态与历史](collection-state-history.md)，HTTP 契约见[监控](../reference/monitoring-api.md)、[通知](../reference/notifications-api.md)、[规则、动作与受控主机](../reference/rules-actions-hosts-api.md)、[关机计划](../reference/shutdown-plans-api.md)。新规则默认 dry-run，联动需要显式选择动作和通知渠道。

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

新终端，从**仓库根目录**执行；将占位路径替换为新建的独立临时目录（Windows / Git Bash 可用 `E:/...` 绝对路径），不要使用已有真实设备配置的 `data/`：

```bash
cd backend
mise x -- go run ./cmd/ups-warden --data-dir /path/to/temporary/ups-warden
```

默认监听 `127.0.0.1:8080`。后端创建数据目录、获取独占文件锁、加载主密钥、迁移 SQLite，校验 Profile/渠道/动作/计划/规则配置并装配服务，恢复状态/历史后自动采集启用源，然后开放 `/healthz`、`/api/v1` 和静态 SPA。空数据库不会连接 UPS；已有启用源和规则会运行。通知队列不在启动时重放；未完成动作标为 unknown，规则保留已消费边沿；未结束的关机计划运行按 `on_restart` 继续或终止，中断时执行中的步骤记为 unknown 且不重放。直接 `go run` 未构建 embed dist 时 WebUI 返回 503；`mise run backend:build` 构建并嵌入当前前端占位页，不代表业务 UI 已完成。离线映射契约见 [Profile 与归一化](profiles.md)。

### 启动配置

定义与优先级来自 [config.go](../../backend/internal/config/config.go)：**显式命令行参数 > 非空环境变量 > 默认值**。环境变量为空时回退默认值。相对路径以进程工作目录为基准；在 `backend/` 运行时请显式传入 `--data-dir ../data`。

| 参数 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `--listen` | `UPS_WARDEN_LISTEN` | `127.0.0.1:8080` | HTTP 监听地址；端口 0 可申请临时端口 |
| `--data-dir` | `UPS_WARDEN_DATA_DIR` | `./data` | 数据目录；推荐统一使用仓库根目录 `data/` |
| `--log-level` | `UPS_WARDEN_LOG_LEVEL` | `info` | 常用 `debug`、`info`、`warn`、`error`；由 `slog.Level.UnmarshalText` 解析 |
| `--log-file` | `UPS_WARDEN_LOG_FILE` | 空 | 空时写 stderr；指定后写滚动文件，20 MB、最多 5 份备份 |
| `--log-format` | `UPS_WARDEN_LOG_FORMAT` | `text` | `text` 或 `json` |
| `--script-dir` | `UPS_WARDEN_SCRIPT_DIR` | `<data-dir>/scripts` | M6 脚本动作目录；由管理员放置脚本，服务不自动创建或在线编辑 |

例如在 **`backend/`** 目录启用调试日志并更改端口：

```bash
mise x -- go run ./cmd/ups-warden --data-dir /path/to/temporary/ups-warden --listen 127.0.0.1:8081 --log-level debug
```

- 已知敏感属性键和 `secret.String` 自动掩码；不得把原始凭据插入日志消息、任意结构体或错误字符串中。采集层保留安全的 AYI9 错误码，不传播原始包装文本；API 内部错误记录类型、SQLite 数字码和调用位置，panic 记录类型与堆栈，不输出原始值。
- 无效参数、日志文件打不开、目录锁冲突、主密钥错误、数据库迁移失败、渠道/动作/计划/规则配置损坏、监听失败或关闭失败均使进程非零退出。API 在构造时要求 DB、Cipher、Auth、Collector、History、Bus、Runtime、Notify、Actions、Plans 全部提供，不支持缺失模块的静默降级。
- 入口处理 `os.Interrupt` / `SIGTERM`，HTTP 优雅关闭最多等待 10 秒，超时强制关闭连接；请求头超时为 5 秒。后台任务必须响应 context 取消；错误或 panic 以 1～30 秒指数退避重启，正常返回不重启，HTTP 异常退出则结束进程。
- TLS、脚本编辑等 P1 参数尚未实现。

### 数据目录与主密钥

- `ups-warden.lock`：操作系统独占文件锁；第二实例立即报“数据目录已被占用”。进程退出自动释放，**不要手工删除仍被持有的锁文件**。目录应位于本地磁盘，不依赖网络共享文件系统的锁语义。
- `ups-warden.db`：SQLite WAL，`synchronous=FULL`、`busy_timeout=5000`、`foreign_keys=ON`；写池 1 条连接、只读池最多 4 条连接。启动校验版本与迁移历史，在事务内执行未应用的 embed 前向迁移，不重放已应用脚本到参考库或逐字比对完整 DDL。初始迁移包含 25 张表；建表不等于相关业务模块已实现。已有迁移不可改写，后续修改使用新编号脚本。迁移检查不代替数据库损坏检查，也不承诺兼容手工改表。
- `0002_notify_results.sql` 为通知日志增量增加接收目标、尝试次数及模板回退标记，保留旧记录。通知关闭在总线/数据库关闭之前完成，取消在途请求并记录待发送项取消结果；不承诺重启重放或 exactly-once。
- M6 沿用已有 rule_state/rule_logs/actions/action_runs/hosts 表，无新增迁移。M7 沿用已有 plans/plan_runs/plan_run_steps 表及其周期唯一、单活动运行索引，同样无新增迁移。关闭先停采集与状态，再排空规则队列、停止计划协程（不写运行终态）、取消动作并保存结果，最后关闭历史/通知与数据库。动作与计划步骤 write-ahead、规则边沿恢复与不重放语义见 M6、M7 契约。
- `master.key`：默认生成并保存原始 32 字节密钥，Linux 文件权限为 `0600`。也可用 `UPS_WARDEN_MASTER_KEY` 提供 Base64 编码的 32 字节密钥，优先于文件且不会写回文件。该变量不是命令行参数，避免密钥出现在命令历史或进程参数中。
- 凭据使用 AES-256-GCM，以 `表名.列名` 为 AAD；启动会检查已有加密凭据。错误密钥、损坏文件或解密失败均不会被静默清空。请恢复正确密钥备份；数据源及通知渠道 API 可保存加密凭据，但密钥错误导致无法启动时不能靠 HTTP 接口恢复，前端也尚无凭据管理页面。
- **Windows 的 `0600` 不提供 Unix 权限保护**：请用数据目录 ACL 限制为服务账号可访问，启动日志也会提示。不要依赖 Git 忽略规则作为访问控制。
- 备份时先停止服务，再复制完整数据目录（包括可能存在的 WAL 文件）；使用环境密钥时另行安全备份该密钥。丢失密钥将无法解密已有凭据，不要删除密钥后让服务重新生成。

### 健康检查

路由与响应定义见 [server.go](../../backend/internal/server/server.go)：

| 请求 | 当前行为 |
|---|---|
| `GET /healthz` | HTTP 200，`Content-Type: application/json`，响应体 `{"status":"ok"}`（末尾换行） |
| `POST /healthz` | HTTP 405；当前方法路由不接受 POST |
| `/api/v1/...` | 已注册 M4 监控/管理、M5 通知、M6 规则/动作/主机与 M7 关机计划 API；除初始化状态/初始化/登录外均需管理员会话，未知 API 也先认证、不回退 SPA |

健康检查无需鉴权，只证明进程能处理 HTTP 请求，**不表示 UPS 连通或业务就绪**。占位前端没有调用它；需要单独检查。

启动后端后，可在另一终端访问本机服务（这不是 UPS 网卡地址）：

```bash
curl --fail http://127.0.0.1:8080/healthz
```

### 初始化、会话与管理 API

完整请求、响应、分页、错误码与 SSE 格式以[监控 API 与 SSE 契约](../reference/monitoring-api.md)为准（CP-2 后端联调基线，双方联调待验收）。无用户时主进程仅向 stderr 输出一次 setup token，不写数据库、轮转日志或 HTTP 响应；令牌只存内存，重启重新生成，首个管理员创建成功后失效。即使使用 `--log-file`，也应从 stderr 安全获取初始化令牌，不要公开终端日志。

先调用 `GET /api/v1/setup/status`，按契约 `POST /api/v1/setup` 创建首个管理员，再 `POST /api/v1/auth/login` 获得会话 Cookie 与 CSRF token。P0 只有管理员，用户 CRUD 已实现；密码使用 argon2id。认证后的非 GET/HEAD 请求携带 `X-CSRF-Token`；所有写请求（含初始化/登录）若有 Origin，必须匹配请求 scheme/Host。前端通过同源代理访问，不启用跨域 CORS，也不信任 `X-Forwarded-*`。

Cookie `ups_warden_session` 为 HttpOnly、SameSite=Strict、Path=/，直接 HTTPS 下 Secure；登录时固定为 7 天绝对期限，不滚动续发，服务端另执行 24 小时 idle 期限。SSE 每 15 秒心跳重新认证并维持服务端活跃时间，仍受撤销和绝对期限约束；每订阅 256 条缓冲，溢出独立信号立即断开不排空，每帧写期限 5 秒，断线重连后等待全量 sync。

设备/源 CRUD 与启停、Profile 管理、只读 test/raw、快照/历史/事件告警/审计查询已实现。源凭据省略或空串表示保留，`clear_secrets` 显式清除，协议变更清旧凭据；响应仅 `community_set/password_set`。配置变更暂停并排空相关采集 actor、Flush 历史，配置和审计同事务，再恢复运行；503 / `configuration_saved_runtime_error` 表示配置已保存但恢复失败，读取配置后修正重试，不要误判为回滚。test 显式只读连接指定设备，raw 只读取最近内存诊断缓存；开发只指定 loopback 模拟器。

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

修改环境变量后需重启开发服务器。代理只负责转发已实现 API；认证与 Origin 校验要求保持同源，不能以伪造转发头绕过。开发代理不等于生产环境部署配置。

## 4. 检查、测试与构建

完成第 1 节工具链安装后，在**仓库根目录**执行：

```bash
mise run backend:check
mise run backend:build
```

`backend:check` 使用 Bash / Git Bash，依次检查 `gofmt -l .`（有输出即失败）、`go vet ./...`、`go test ./...`。`backend:build` 依赖 `webui:build`，先执行 `pnpm --dir frontend build`，再由 `scripts/build-webui.mjs` 校验并安全复制产物至 `backend/internal/webui/dist`，保留占位文件、拒绝符号链接/隐藏文件/特殊文件；当前 index、favicon 与 JS/CSS 共 4 文件。随后 `CGO_ENABLED=0` 构建含 embed SPA 的本机二进制，输出 `.cache/build/`（Windows 为 `ups-warden.exe`）；dist 产物不提交。

需要单独运行时，在 **`backend/`** 执行：

```bash
mise x -- gofmt -l .
mise x -- go vet ./...
mise x -- go test ./...
```

后端测试覆盖配置、日志与脱敏、主密钥、目录锁、SQLite、app 生命周期及包依赖边界；M1 覆盖 Profile、归一化与 60 组 ET1K 离线回归；M2 覆盖三协议与只读护栏；M3 覆盖采集/状态/事件/历史、重启与事务边界。时间逻辑使用 `testing/synctest` 或注入场景时钟。Linux 交叉编译不能替代 Linux 实机运行验证。

M4 测试覆盖 auth/audit/API/webui 与 app 集成，包括初始化并发、会话与限速、CSRF/Origin、凭据不回显、配置审计/动态运行协调、Profile 引用、监控查询及 SSE/静态托管边界。后端全量检查、无缓存测试、重复回归、JSON fuzz、前端 check/test/build、内嵌构建和 Windows 临时数据库 + 三协议 loopback 冒烟已通过，详细证据见[开发计划 M4 实施记录](../planning/backend-plan.md)。CP-1 真机、CP-2 双方联调、Linux 实机、race 与容量验收仍未完成。

M5 测试覆盖模板/转义/回退、Webhook 签名与 Telegram 多目标/代理/429、队列边界与精确重试时序、通知日志/失败 SSE、凭据保护、审计回滚及 app 重启/取消。测试只使用临时数据库、内存 HTTP handler 和 loopback 假服务器；未使用真实通知渠道。详见[通知契约](../reference/notifications-api.md)和[开发计划 M5 实施记录](../planning/backend-plan.md)。

M6 测试覆盖两层规则条件、持续时长/冷却/恢复/质量/重启去重，动作互斥、write-ahead、dry-run、HTTP UPS 主机护栏、脚本实路径与 Windows 进程树取消、loopback SSH 指纹/认证/断连、主机 API 凭据保护与审计。模拟器集成验证停电确认后两秒内送达假 Webhook，dry-run 只有日志无外发。详见 [M6 契约](../reference/rules-actions-hosts-api.md)与开发计划。未新增依赖或数据库迁移；真实主机和 ICMP 权限环境未验收。

M7 测试覆盖计划校验、阶段串行/阶段内并行、单步超时、失败不阻塞、dry-run 无副作用、单活动运行与停电周期幂等、倒计时与执行中的市电恢复、执行中/阶段延迟中注入重启不重放、resume/abort、自关机前置标记，以及 API 的确认、冲突码、审计、引用保护和 SSE。app 集成以 loopback 模拟器、本机假 SSH 主机、假 HTTP 受控端和假 Webhook 验证 AC-05 计划部分、AC-06、AC-07。详见[关机计划契约](../reference/shutdown-plans-api.md)。未执行真实关机，CP-3 评审未完成。

本机 `upssim` 与显式 `upsprobe` 的参数、安全边界及示例见[协议驱动与本机模拟器](protocol-drivers.md)。主服务会自动采集启用数据源；勿用含真实设备配置的数据目录做开发冒烟。

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
- `build` 执行 `tsc -b && vp build`，产物位于 `frontend/dist/`；单独 pnpm build 不复制/嵌入，根目录 `mise run webui:build` 完成安全复制，`mise run backend:build` 再构建内嵌二进制。
- 当前没有统一前后端发布任务、文档构建或专用链接检查脚本。仅修改文档时核对路径、相对链接、参数和示例，不为验证文档访问真实设备。

## 5. 常见排查与安全边界

| 现象 | 优先检查 |
|---|---|
| 工具命令缺失或版本不符 | 在根目录执行 `mise install`，再使用 `mise x --`；不要绕过 `GOTOOLCHAIN=local` 自动下载 Go |
| 后端无法启动 | 查看 stderr 或日志文件；检查端口、配置、目录权限、是否有另一实例持锁，以及主密钥和数据库是否完整 |
| 前端代理报连接错误 | 确认后端已启动，检查 `UPS_WARDEN_BACKEND` 是否与 `-listen` 一致且非空 |
| `/api/v1` 返回 401/403/404 | 检查完整路由、会话 Cookie、CSRF 与 Origin；未知 API 不回退 SPA，健康检查不代表已登录 |
| 后端根路径返回 503 提示页 | embed dist 未构建；在根目录执行 `mise run backend:build`，或单独启动前端开发服务器；构建后仍仅为占位页 |

完整设备安全规则见 [AGENTS.md](../../AGENTS.md)。自动化测试只用临时目录、回环服务、模拟器或脱敏录制数据，不需要真实 UPS。实际设备地址、community、密码和未脱敏数据不得写入文档或 Git 跟踪文件；本地敏感数据统一放仓库根目录 `/data/`（已在 [.gitignore](../../.gitignore) 中忽略）。额外忽略 `backend/data/` 仅防止误用默认目录时泄漏，不改变推荐位置。
