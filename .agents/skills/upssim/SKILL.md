---
name: upssim
description: >-
  Use when UPS Warden development, frontend/backend integration, acceptance or debugging needs
  UPS device data without real hardware: starting or stopping the upssim daemon, adding simulated
  UPS devices, switching mains/battery/low/link-down/flash states, choosing loopback IPs and ports,
  or wiring simulated endpoints into a temporary ups-warden instance. Do not use against real
  devices, for CP-1/CP-4 hardware acceptance, or with production data directories.
---

# upssim 联调

联调、测试或排查需要 UPS 设备数据时，用 `upssim` 多设备守护进程在 loopback 上模拟设备，再接入**临时数据目录**的 `ups-warden`。不得改为往数据库写假数据、手写 mock，或连接真实设备“试一下”。

命令、参数、默认值、模式语义、错误码和退出码只在[协议驱动与本机模拟器](../../../docs/guides/protocol-drivers.md#多设备守护进程)中维护，本技能不重复列出；不确定时先查该文档或执行 `upssim <子命令> --help`。常用联调剧本见 [references/recipes.md](references/recipes.md)。

## 1. 安全红线

以下规则优先于后面的工作流：

- 只操作 loopback 模拟设备。真实设备的 IP、community、密码不得用作 upssim 参数，也不得写入数据源配置。
- `ups-warden` 只用临时 `--data-dir`，不用仓库的 `data/` 或任何含真实设备配置的目录。
- 规则保持 dry-run；手动执行计划必须显式传 `"dry_run": true`。不执行真实动作或关机。
- 不按 PID 结束进程。未经用户明确要求，不停止、不删除不是本次创建的守护进程或设备。
- 端口被占用时换端口或用 `0`，不结束占用端口的其他进程。
- 不输出、不记录 setup token、会话 Cookie、CSRF token 和控制面 token。
- 模拟器结果只能表述为“本机模拟器验证”，不能写成真机、CP-1～CP-4 或双方联调验收。

## 2. 隔离

- agent 自己发起的联调使用独立状态目录：会话临时目录下的 `upssim/`（没有会话临时目录时用 `mktemp -d`），通过 `--state-dir` 或 `UPSSIM_STATE_DIR` 指定，不干扰开发者在默认状态目录中运行的守护进程。
- 只有用户明确要求操作他当前在用的模拟器时，才使用默认状态目录，并先用 `daemon status`、`list` 确认现状再修改。
- 不靠删除 `devices.json`、`control.json` 等状态文件来“修复”问题；需要重置时换一个新的临时状态目录。

## 3. 标准工作流

在仓库根目录执行。下面的 `$SIM` 指构建产物 `.cache/build/upssim`（Windows 为 `upssim.exe`，Git Bash 中可省略扩展名）；也可以用 `mise run sim -- <参数>`，它每次都会先构建。

```bash
export UPSSIM_STATE_DIR="<会话临时目录>/upssim"
SIM=.cache/build/upssim
```

1. **准备**：`mise install`、`mise run sim:build`。确认要接入的 `ups-warden` 使用临时数据目录；需要自己启动时，构建 `./cmd/ups-warden`，以 `--data-dir <临时目录> --listen 127.0.0.1:<端口>` 运行，并把 stderr 重定向到临时文件（首次初始化的 setup token 只出现在 stderr，读取后不得回显）。
2. **启动守护进程**：先 `$SIM daemon status`。退出码 3 表示未运行，这时执行 `$SIM daemon start`，并记下守护进程是本次启动的，清理时据此判断。
   - 如果输出提示“当前终端所在的 Windows Job 不允许子进程脱离”，或已知宿主会在每条命令结束时结束其子进程树，就改用宿主的后台任务机制运行前台 `$SIM daemon run`，之后的命令照常执行。
   - 宿主不支持后台任务时，把启动、操作、核对和清理写进同一条命令（脚本），用 `trap` 保证退出时执行 `daemon stop`。临时 `ups-warden` 同样处理。
3. **规划设备**：按要验证的 Profile 选择 `--et1k` 或标准模式（见第 4 节）。多台设备各用一个 loopback IP（`127.0.0.2`、`127.0.0.3`……）。数据源配置需要在守护进程重启后继续有效时，显式指定端口；否则用默认的 `0`，并以实际输出为准。
4. **创建设备**：`$SIM add <name> [--et1k] --ip 127.0.0.N --json`。端点从 `.endpoints.snmp|modbus|ayi9`（`IP:端口`）读取，推荐 Profile 从 `.profiles.*` 读取（`null` 表示该协议没有通用内置 Profile）。不要自己推测端口。
5. **接入 ups-warden**：按[监控 API 契约](../../../docs/reference/monitoring-api.md)依次执行 `POST /api/v1/setup`、`POST /api/v1/auth/login`（之后的写请求带 Cookie 和 `X-CSRF-Token`）、`POST /api/v1/devices`、`POST /api/v1/devices/{id}/sources`。数据源字段为 `protocol`、`host`、`port`、`profile_key`、`params`、`poll_ms`，凭据只用模拟器的虚构值（SNMP community 默认 `sim-public`，AYI9 密码默认空）。添加后轮询 `GET /api/v1/devices/{id}/snapshot`，等快照中已有采集字段（例如 `fields.output_source.str` 不为空）后再开始切换状态。数据源刚添加时接口可能已返回 200，但字段还是空的。
6. **驱动并核对**：用 `$SIM set <name> ...` 切换状态，然后轮询 `GET /api/v1/events?device_id=<id>`、`GET /api/v1/devices/{id}/snapshot`，或订阅 `GET /api/v1/stream`，直到观察到预期事件或超时。等待以实际观察结果为准，不能用固定 sleep 代替核对。超时上限参考 recipes.md 中的实测时序，并结合设备的去抖配置。
7. **清理**：`$SIM remove` 删除本次创建的设备；守护进程如果是本次启动的，执行 `$SIM daemon stop`（由宿主后台任务托管的 `daemon run` 也用这条命令停止）；停止临时 `ups-warden`，删除临时目录。不是本次创建的设备和守护进程保持原样。

## 4. 决策规则

| 需要 | 做法 |
|---|---|
| 验证 ET1K 内置 Profile（`et1k-snmp`、`et1k-modbus`、`et1k-ayi9-json`） | `add --et1k`，只能单相 |
| 验证 `rfc1628-generic`、`ayi9-json-generic` | 标准模式（不带 `--et1k`）。Modbus 没有通用内置 Profile，需要先在 ups-warden 中创建自定义 Profile |
| 三相字段 | 标准模式加 `--phases 3` |
| 多台设备 | 每台使用不同的 loopback IP，避免 ups-warden 对同一 IP 串行采集 |
| 可复现的时间轴（自动化、回放） | `add --scenario FILE` 或 `set --scenario FILE` |
| 交互式控制 | `set --mode` / `--link` / `--flash` / `--capacity` |
| 低电后快速恢复并清除低电 | `set --mode mains --capacity 100` |
| AYI9 密码错误 | `add` 或 `set` 加 `--ayi9-error-code 103` |
| 改监听地址、端口、ET1K、相数或凭据 | `remove` 后重新 `add` |
| `address_in_use` | 换端口或用 `0` |
| `address_conflict` | 换端口；或者确认占用该端口的模拟设备不再需要后再 `remove` |
| 旁路、故障、过载 | 当前不支持。如实说明，不用其他手段伪造 |

## 5. 失败处理

- **退出码 2**：参数错误，按提示修正。`set` 提示“需 remove 后重新 add”时按第 4 节处理。
- **退出码 3**：守护进程未运行。如果本次并没有执行 `daemon stop`，多半是宿主结束了 Job 内的守护进程，按第 3 节第 2 步改用后台任务或单条命令，不要反复重试 `daemon start`。
- **`daemon start` 超时或提前退出**：错误信息里已附带日志末尾；必要时读状态目录下的 `upssim.log`，或用前台 `daemon run` 排查。
- **状态目录已有守护进程**：说明另一个会话或开发者在用。不要停止它，换一个临时状态目录。
- **设备 `status=failed`**：重启恢复时原端口不可用。`remove` 后重新 `add`。
- **`persist_failed`**：运行期修改已生效但没有落盘。如实报告，不要删除状态文件。
- **ups-warden 没有产生事件**：先用 `$SIM show <name> --json` 确认模拟器侧的状态已经改变，再检查数据源 `status`（`failures`、`error_code`）和设备去抖配置。不要直接修改数据库。

## 6. 汇报口径

汇报中写明：

- 状态目录（临时路径）、设备名、协议端点和使用的 Profile；
- 执行过的状态切换及其时间；
- 实际观察到的事件和字段，以及从切换到出现事件的耗时；
- 清理结果（设备、守护进程、临时 ups-warden 和目录）；
- 结论标注“本机模拟器验证”，并列出未覆盖的部分。

不在汇报中出现 token、Cookie 或任何凭据。
