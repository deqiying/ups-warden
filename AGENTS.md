# UPS Warden 项目级协作规范

本文档适用于本仓库及其所有子目录。更具体的目录级 `AGENTS.md`（如后续新增）优先于本文档；用户的明确要求优先于本文档和其他项目文档。

## 项目现状

后端已完成 **M0/M1**，**M2 只读驱动及 M3 采集调度、状态、事件与历史已实现并通过本机验证，CP-1 真机验收待完成**。主服务会自动采集数据库中启用的数据源，开发测试必须使用临时数据库与本机模拟器，不得直接使用本地真实设备配置启动测试。前端仍为脚手架；业务 API、认证、规则、通知、关机编排尚未实现，不要把规划或模拟器验证描述为已完成真实设备验收。

README 视觉与文案调整使用 `.agents/skills/beautify-github-readme` 技能。

## 目录结构

```text
backend/    Go 后端（module: ups-warden），入口 cmd/ups-warden，内部包放 internal/
frontend/   React 19 + TypeScript 7 + Vite+ 前端
.cache/     mise 管理的 Go / Node / npm / pnpm 缓存（不提交）
mise.toml   项目工具链与缓存目录的唯一来源
```

## 工具链与缓存

- Go、Node.js、pnpm 版本只由根目录 `mise.toml` 管理。执行项目命令前先通过 `mise install` 安装，再在已激活 mise 的 shell 中运行，或使用 `mise x -- <命令>`。
- 所有工具缓存统一放在仓库根目录 `.cache/`（`GOPATH`、`GOMODCACHE`、`GOCACHE`、npm cache、pnpm store / cache、Node 编译缓存），不得改回用户全局目录，也不得在 `backend/`、`frontend/` 下另建缓存目录。
- `GOTOOLCHAIN=local`：不允许 go 命令自动下载其他 Go 版本，需要升级时修改 `mise.toml`。
- 前端依赖只用 pnpm 管理；Vite、Vitest、Oxlint、Oxfmt 统一通过 Vite+（`vp`）调用，不要单独引入同类工具。
- 禁止未经批准安装额外工具。项目确实需要且 mise 支持的工具可通过 mise 安装；需要纳入团队基线时同步更新 `mise.toml`。

## 常用命令

```bash
# 后端（在 backend/ 下执行）
go run ./cmd/ups-warden --data-dir ../data # 启动服务，数据统一放根目录 data/
go test ./...
go vet ./...
gofmt -l .

# 后端统一检查与本机构建（在仓库根目录执行）
mise run backend:check
mise run backend:build

# 前端（在 frontend/ 下执行）
pnpm install
pnpm dev                          # 开发服务器，/api 与 /healthz 代理到后端
pnpm check                        # 格式、lint、类型检查
pnpm test
pnpm build
```

## 设备安全（强制）

开发和测试中会直接连接真实 UPS 网卡，探测阶段已发生过误触出厂重置和关机指令的事故。

- 驱动只能发送只读请求：SNMP 只用 GET / GETNEXT / GETBULK；Modbus 只用 FC03 / FC04；`ayi9-json`（AYI9 云监控卡 JSON API，默认 TCP 2390）只发 `QAD`、`RTD`。
- 禁止向真实设备发送 SNMP SET、Modbus 写功能码、`ayi9-json` 的 `SON` / `SOFF` / `BZON` / `BZOFF` / `new_pass` / `host` 等指令。
- 禁止访问设备的 HTTP WebUI 路径，禁止端口扫描、OID 爆破或 `cmd` 字典探测。
- 自动化测试使用协议模拟器或录制数据，不依赖真实设备。
- 设备 IP、community、密码等不得写入 Git 跟踪文件，本地运行数据放在 `/data/`（已忽略）。

## 架构约束

- 采集链路分为三层：驱动（协议原始数据）→ Profile（字段映射）→ 归一化（统一数据模型）。存储、事件、规则、通知、动作、API 和 WebUI 只使用统一模型，不得引用 OID、寄存器地址或 QAD / RTD 索引。
- 型号差异放在 Profile 中描述，驱动代码不包含具体型号逻辑。
- 不为当前任务之外的假设性需求增加抽象、配置项或扩展点。

## 代码与文档规范

- 代码注释、文档和提交说明使用简体中文；类型名、函数名、协议名、配置键等技术标识保留原文。
- 公开 API 写文档注释，说明用途和关键契约；非显而易见的逻辑说明设计意图、边界条件和失败语义，不复述语法。
- 错误不得被静默吞掉；错误信息保留诊断上下文，且不包含凭据。
- 文本文件使用 UTF-8（无 BOM）和 LF（由 `.gitattributes`、`.editorconfig` 约束）。

## 验证与提交

- 后端改动至少执行 `gofmt -l .`（无输出）、`go vet ./...`、`go test ./...`。
- 前端改动至少执行 `pnpm check` 和 `pnpm test`；涉及构建配置时追加 `pnpm build`。
- 只报告实际执行过的检查。提交前检查 `git status --short`，只暂存当前任务的文件，不使用 `git add -f` 提交缓存或构建产物；未明确要求时不执行 push。
