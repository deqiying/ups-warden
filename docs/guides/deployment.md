# 部署指南

[返回文档导航](../README.md)

M8 发布产物：三平台单文件二进制（内嵌 WebUI）、多阶段 Dockerfile、[compose.yaml](../../compose.yaml) 示例与 [systemd unit](../../deploy/ups-warden.service) 示例。本机已验证三平台交叉编译和 Windows 二进制启动/优雅退出；**Linux 实机、Docker 镜像构建与运行尚未验证**（开发机无 Docker/WSL）。WebUI 已随二进制托管（F0～F9，本机验证；真机与 Linux 实机验收未完成）：首次访问 `/setup` 用启动日志中的 setup token 创建管理员，之后在界面管理设备、规则、计划与通知。

## 1. 构建发布二进制

在仓库根目录执行：

```bash
VERSION=v0.8.0 mise run backend:release
```

先构建并嵌入前端，再以 `CGO_ENABLED=0`、`-trimpath -ldflags "-s -w -X main.version=$VERSION"` 输出：

| 平台 | 产物 |
|---|---|
| linux/amd64 | `.cache/build/release/ups-warden-linux-amd64` |
| linux/arm64 | `.cache/build/release/ups-warden-linux-arm64` |
| windows/amd64 | `.cache/build/release/ups-warden-windows-amd64.exe` |

未设置 `VERSION` 时取 `git describe --tags --always --dirty`，失败为 `dev`。`ups-warden --version` 只输出版本号并退出，不读取数据目录；启动日志 `HTTP 服务启动` 也带 `version`。

## 2. 启动参数

| 参数 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `--listen` | `UPS_WARDEN_LISTEN` | `127.0.0.1:8080` | HTTP 监听地址；局域网访问改为 `0.0.0.0:8080` 并配合防火墙 |
| `--data-dir` | `UPS_WARDEN_DATA_DIR` | `./data` | 数据目录（数据库、`master.key`、目录锁、默认脚本目录） |
| `--log-level` | `UPS_WARDEN_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `--log-file` | `UPS_WARDEN_LOG_FILE` | 空（stderr） | 滚动日志文件，20 MB、最多 5 份备份 |
| `--log-format` | `UPS_WARDEN_LOG_FORMAT` | `text` | `text` 或 `json` |
| `--script-dir` | `UPS_WARDEN_SCRIPT_DIR` | `<data-dir>/scripts` | 脚本动作目录，由管理员放置脚本 |
| `--version` | — | — | 输出版本号并退出 |
| — | `UPS_WARDEN_MASTER_KEY` | 空 | Base64 编码的 32 字节主密钥，优先于 `master.key` 文件且不写回 |

优先级：命令行 > 非空环境变量 > 默认值。保留期、设备默认阈值、时区、语言和 HTTP 动作护栏属于运行期设置，通过[系统设置 API](../reference/settings-api.md) 修改，不是启动参数。

首次启动且无管理员时，一次性 setup token 只写到主进程 stderr（即使配置了 `--log-file`）。systemd 下用 `journalctl -u ups-warden`、Docker 下用 `docker compose logs ups-warden` 获取，**不要公开这些日志**。初始化与登录流程见[监控 API](../reference/monitoring-api.md)。

## 3. Linux + systemd

```bash
sudo install -m 0755 ups-warden-linux-amd64 /usr/local/bin/ups-warden
sudo useradd --system --no-create-home --shell /usr/sbin/nologin ups-warden
sudo install -m 0644 deploy/ups-warden.service /etc/systemd/system/ups-warden.service
sudo systemctl daemon-reload
sudo systemctl enable --now ups-warden
```

- unit 使用 `StateDirectory=ups-warden`，数据目录为 `/var/lib/ups-warden`（0700，属主为服务用户）。
- `TimeoutStopSec=45s`：收到 SIGTERM 后最坏依次等待 HTTP 关闭 10 秒、在途动作自然完成 10 秒（超时取消并记录）、历史落盘 10 秒，再关闭通知与数据库。通常数百毫秒内退出；不要把超时调到 30 秒以下，否则 systemd 可能在保存结果前 SIGKILL。
- 需要本机作为关机计划最后一步时，脚本动作以服务用户身份运行，需由管理员自行授予关机权限（例如受限 sudoers 规则）；示例 unit 未做此授权。

## 4. Docker

```bash
docker compose up -d --build
docker compose logs ups-warden    # 获取初始化 token
```

[Dockerfile](../../Dockerfile) 为三阶段构建：Node 构建前端并复制到 embed 目录 → Go 按 `TARGETOS/TARGETARCH` 交叉编译（可用 `docker buildx build --platform linux/arm64`）→ `alpine:3` 运行镜像，非 root（uid 10001），`/data` 卷，默认 `UPS_WARDEN_LISTEN=0.0.0.0:8080`、`UPS_WARDEN_DATA_DIR=/data`。版本号经 `--build-arg VERSION=...` 注入。

- compose 设置 `stop_grace_period: 45s`，原因同 systemd 的 `TimeoutStopSec`；Docker 默认 10 秒不够。
- **容器无法关闭自身所在宿主机**。宿主机作为关机目标时，请把宿主机登记为受控主机并使用 SSH 动作；脚本动作只在容器内执行。
- ICMP 在线检测：非 root 进程使用非特权 ping socket（依赖容器内 `net.ipv4.ping_group_range`，Docker 默认放开）。非 root 进程即使 `cap_add: NET_RAW` 也没有有效 capability；若 ping socket 不可用，优先改用 TCP 在线检测，或按 compose 注释以 root 运行并添加 `NET_RAW`。
- UPS 网卡通常在局域网中，桥接网络可直接访问；Docker 不需要额外端口映射来访问设备。

## 5. 安全与备份

- **主密钥**：`master.key`（或 `UPS_WARDEN_MASTER_KEY`）加密数据源、渠道、动作与主机凭据。丢失后已有凭据无法解密，服务拒绝启动且不会自动清空。备份时停止服务，复制完整数据目录（含 WAL）；使用环境密钥时另行安全保存。不要删除密钥让服务重新生成。
- **Linux 权限**：`master.key` 权限宽于 0600 时拒绝启动。
- **Windows 权限**：文件 0600 不提供保护，启动日志会警告。请将数据目录 ACL 限制为服务账号与管理员，例如：

  ```powershell
  icacls C:\ProgramData\ups-warden /inheritance:r /grant:r "SYSTEM:(OI)(CI)F" "Administrators:(OI)(CI)F"
  ```

  以其他账号运行服务时，把该账号加入授权。ICMP raw socket 在 Windows 需要管理员权限，普通账号请用 TCP 在线检测。
- **HTTP 动作护栏**默认开启，阻止 HTTP 动作访问任何 UPS 数据源主机；关闭需显式确认并写审计，见[系统设置 API](../reference/settings-api.md)。
- 默认只监听 127.0.0.1，服务本身不提供 TLS。开放到局域网时只在可信网段监听并配合防火墙；写请求的 Origin 必须与请求自身的 scheme/Host 一致且不信任 `X-Forwarded-*`，因此终止 TLS 的反向代理部署未适配也未验证。
- 设备 Profile 的编写与校验见 [Profile 与归一化](profiles.md)；自定义 Profile 经 API 导入，不需要重新构建。
