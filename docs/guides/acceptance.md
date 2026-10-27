# 验收与压测

[返回文档导航](../README.md)

M8 把需求第 15.2 节 AC-01～11 中可自动化的部分集中为一条命令，并提供 NFR-01～03 的模拟器压测。全部用例只使用临时数据目录、loopback 模拟器、假 Webhook/SSH/HTTP 受控端，**不访问真实设备、渠道或主机**；结果不能替代 CP-1 / CP-4 真机验收。

## 1. 运行

在仓库根目录执行：

```bash
mise run backend:acceptance   # AC 自动化套件，约 30 秒
mise run backend:load         # 32 / 100 台压测，约 3 分钟
```

`backend:load` 设置 `UPS_WARDEN_LOAD=32,100` 运行 `TestLoad`；也可在 `backend/` 下用 `UPS_WARDEN_LOAD=3 mise x -- go test ./internal/acceptance -run '^TestLoad$' -v` 快速试跑。未设置该变量时 `go test ./...` 跳过压测。

## 2. AC 映射

| AC | 自动化用例 | 覆盖范围 |
|---|---|---|
| AC-01 ET1K 三协议与面板一致 | — | 需真机；CP-1 已确认三协议读数互相一致，与设备本体面板的独立比对留 CP-4；离线回归见 [Profile 指南](profiles.md) 的 ET1K fixture |
| AC-02 RFC 1628 通用 Profile | `TestAcceptanceProtocolsAndReadOnly` | 经 API 接入 SNMP 模拟器，标准字段值正确，未实现字段为 `unsupported` |
| AC-03 自定义 Profile 接入新寄存器表 | `TestAcceptanceProtocolsAndReadOnly` | 经 API 导入自定义 Modbus Profile（FC04 新寄存器表），不改代码采集成功 |
| AC-04 5 s 闪断 | `TestAcceptanceOutageNotificationAndCommLoss`、`TestLoopbackPipelineFlashLostRestart` | 短于确认时长的停电不产生 `mains_lost`、不触发规则 |
| AC-05 持续停电 | `TestAcceptanceOutageNotificationAndCommLoss`（通知）、`TestM7OutagePlanStagesOfflineWaitAndNotifications`（计划） | 确认后 2 s 内送达 Webhook；计划倒计时、阶段串行/并行、等待离线、进度与日志 |
| AC-06 倒计时期间恢复 | `TestM7CountdownCancelledByMainsRestored` | 计划自动取消并通知，步骤不执行 |
| AC-07 执行中重启 | `TestM7RestartResumeAndAbort` | 已完成步骤不重放，按 `on_restart` 继续或终止 |
| AC-08 停电期间失联（hold） | `TestAcceptanceOutageNotificationAndCommLoss` | 产生 `comm_lost`，放电计时继续，时长条件照常触发 |
| AC-09 dry-run | `TestM6OutageNotificationAndDryRun` | 只有日志，无外发 |
| AC-10 AYI9 密码错误 | `TestAcceptanceProtocolsAndReadOnly` | 连接测试返回密码错误 103，不回显密码 |
| AC-11 抓包审计 | `TestAcceptanceProtocolsAndReadOnly`（模拟器部分） | 模拟器记录服务发出的全部请求，三协议均为白名单只读请求；真实网卡抓包仍需 CP-4 |

`internal/acceptance` 为经 HTTP API 的黑盒用例（真实时间 + 可跳变的模拟器场景时钟）；AC-05 计划部分、AC-06、AC-07、AC-09 依赖可控时钟，保留在 `internal/app` 集成测试中，由同一 mise 任务运行。

## 3. 压测方法

`TestLoad` 把模拟器放在重新执行的子进程中，使其 CPU/内存不计入被测服务；每台模拟设备监听独立 loopback 地址（`127.0.x.y`，避免同 IP 串行采集互相影响）。服务端经 API 创建设备，SNMP / Modbus / AYI9 轮换、3 s 轮询，并配置“全部设备 `mains_lost` → notify → 假 Webhook”规则。阶段：

1. 等待全部设备采集正常，预热至 20 s。
2. 空闲 30 s（无 SSE 订阅者）：先 `debug.FreeOSMemory()` 排除初始化/登录时 argon2id 的一次性峰值，再每秒采样 Go 运行时内存（总量减已归还堆），记录进程 CPU 时间。
3. SSE 15 s：统计成功采集的 `device_state` 帧从采集完成（`at`）到客户端收到的延迟。
4. 全部设备同时停电：统计 `mains_lost` 事件 `at`（去抖确认）到 Webhook 收到的延迟。

断言：32 台以内内存 ≤ 128 MiB；SSE p99 ≤ 1 s；每台设备都确认停电并送达通知；通知最大延迟 ≤ 2 s。CPU 只报告不断言，因 NFR-02 以 4 核 ARM64 为参考。

## 4. 实测结果（2026-10-25）

环境：Windows amd64 开发机，16 逻辑核，`GOMAXPROCS=16`，Go 1.27.2，均为本机 loopback。

| 设备数 | 空闲 CPU（单核 / 全部核心） | Go 内存峰值 | 堆对象峰值 | SSE 延迟 p50 / p99 / max（样本） | 停电通知 | 通知延迟 p50 / p99 / max |
|---|---|---|---|---|---|---|
| 32 | 3.12% / 0.195% | 29.2 MiB | 5.8 MiB | 1 / 1 / 2 ms（304） | 32/32 确认并送达 | 2 / 3 / 3 ms |
| 100 | 4.89% / 0.306% | 39.5 MiB | 12.5 MiB | 1 / 1 / 3 ms（956） | 100/100 确认并送达 | 2 / 4 / 4 ms |

说明与边界：

- 32 台与 100 台均满足 NFR-01 容量、NFR-02 内存与 NFR-03 时效。空闲 CPU 在本机约为一个核心的 3%～5%（全部核心的 0.2%～0.3%）；4 核 ARM64 上是否 < 2% 需在目标硬件复测。
- 内存为 Go 运行时统计，不含操作系统对可执行映像等的计数，与 RSS 可能有差异。
- 每次初始化或登录执行一次 argon2id（m=64 MiB），会造成短时约 64 MiB 的堆峰值；稳态内存不受影响，但并发登录时需要考虑。
- 全部设备同时停电时共享一个 notify 动作，未出现 `skipped_concurrent`，100 次运行均为 `queued` 并全部送达。
- 未覆盖：长时间（天级）运行、历史表满容量下的查询与清理性能、真实网络时延、Linux 与 ARM64 实机。
