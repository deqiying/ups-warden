# upssim 联调剧本

前提：已按 [SKILL.md](../SKILL.md) 第 3 节完成第 1～5 步，即守护进程运行在临时状态目录，临时 `ups-warden` 已初始化并登录，模拟设备已接入为数据源。参数细节见[协议驱动与本机模拟器](../../../../docs/guides/protocol-drivers.md#多设备守护进程)。

每个剧本都按“切换 → 轮询核对 → 清理”执行。下文的“实测参考”是在 ups-warden 默认设备配置（`outage_confirm_s=10`、`recovery_confirm_s=60`、`comm_failures=3`、`battery_low_pct=30`）、ET1K SNMP、`poll_ms=1000` 下，于 2026-10-29 在 Windows 本机测得的耗时。它只用来设置轮询超时的上限（建议取 2 倍余量），不要当作固定 sleep。阈值和去抖的含义见[采集、状态与历史](../../../../docs/guides/collection-state-history.md)。

## 公共片段

以下片段只连 loopback，凭据都是虚构测试值。不要把 `$TOKEN`、`$CSRF` 和 Cookie 文件内容打印出来。

```bash
W="<会话临时目录>/e2e"; mkdir -p "$W"
export UPSSIM_STATE_DIR="$W/sim"; SIM=.cache/build/upssim
API=http://127.0.0.1:18080/api/v1; J="$W/cookies"

# 临时 ups-warden（宿主支持后台任务时用后台任务运行；否则与后续步骤放在同一脚本中，用 trap 清理）
(cd backend && mise x -- go build -o "$W/" ./cmd/ups-warden)
"$W/ups-warden" --data-dir "$W/data" --listen 127.0.0.1:18080 2>"$W/warden.err" &
for _ in $(seq 50); do curl -sf http://127.0.0.1:18080/healthz >/dev/null && break; sleep 0.2; done
TOKEN=$(sed -n 's/.*setup token: \([^ ]*\).*/\1/p' "$W/warden.err" | tr -d '\r')
curl -sf -X POST "$API/setup" -H 'Content-Type: application/json' \
  -d "{\"setup_token\":\"$TOKEN\",\"username\":\"admin\",\"password\":\"sim-test-pass\"}" >/dev/null
CSRF=$(curl -sf -c "$J" -X POST "$API/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"sim-test-pass"}' | jq -r .csrf_token)
call() { curl -sf -b "$J" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' "$@"; }

# 模拟设备 → ups-warden 设备 + SNMP 数据源
$SIM add r1 --et1k --ip 127.0.0.5 --json >"$W/r1.json"
PORT=$(jq -r '.endpoints.snmp' "$W/r1.json" | cut -d: -f2)
DEV=$(call -X POST "$API/devices" -d '{"name":"sim-r1"}' | jq -r .id)
call -X POST "$API/devices/$DEV/sources" -d "{\"protocol\":\"snmp\",\"host\":\"127.0.0.5\",\"port\":$PORT,\"profile_key\":\"et1k-snmp\",\"params\":{\"version\":\"2c\"},\"community\":\"sim-public\",\"poll_ms\":1000}" >/dev/null

# 按时间顺序列出事件；带 (active) 的是仍在活动的告警
events() { call "$API/events?device_id=$DEV&page_size=100" | jq -r '[.items[] | .type + (if .active then "(active)" else "" end)] | reverse | join(" ")'; }
# 轮询直到事件列表出现 $1，最多等 $2 秒
wait_event() { for _ in $(seq "$2"); do events | grep -qw "$1" && return 0; sleep 1; done; echo "超时未见 $1：$(events)"; return 1; }
```

接入 Modbus 时数据源为 `"protocol":"modbus","params":{}`，接入 AYI9 时为 `"protocol":"ayi9-json","params":{}`；端口取 `.endpoints.modbus` / `.endpoints.ayi9`，Profile 取 `.profiles.*`。

清理（所有剧本通用）：

```bash
$SIM remove r1          # 删除本次创建的设备
$SIM daemon stop        # 仅当守护进程是本次启动的
kill %1                 # 停止临时 ups-warden（或停止对应的宿主后台任务）
```

事件名：`mains_lost`、`mains_restored`、`battery_low`、`battery_low_cleared`、`comm_lost`、`comm_restored`、`battery_depleted`。

## R1 停电 → 低电 → 恢复

目的：验证停电确认、低电告警，以及市电恢复和低电清除。

```bash
$SIM set r1 --mode battery;               wait_event mains_lost 25
$SIM set r1 --mode low;                   wait_event battery_low 15
$SIM set r1 --mode mains --capacity 100;  wait_event mains_restored 130 && wait_event battery_low_cleared 10
```

预期：`mains_lost` → `battery_low` → `mains_restored`、`battery_low_cleared`。快照中 `fields.output_source.str` 在电池期间为 `battery`，市电时为 `normal`。

实测参考：`battery` 后约 10～12 秒出现 `mains_lost`；`low` 后 1～数秒出现 `battery_low`；`mains --capacity 100` 后约 60 秒同时出现 `mains_restored` 和 `battery_low_cleared`。

注意：只执行 `--mode mains` 而不带 `--capacity` 时，模拟器的低电标志要等电量回充超过 20% 才清除，ups-warden 还要求电量回到清除阈值以上，因此 `battery_low_cleared` 会明显晚于 `mains_restored`。具体耗时取决于 `--discharge-minutes`（默认 30 分钟回充满）。

## R2 5 秒闪断

目的：验证去抖窗口能吸收短暂断电。

```bash
$SIM set r1 --flash      # 只能在市电模式下执行
sleep 15                 # 这里要证明“没有事件”，因此需要等过完整的确认窗口
events                   # 不应出现 mains_lost
```

预期：不产生 `mains_lost`，界面不出现停电告警。实测参考：5 秒闪断期间和之后均没有事件。

## R3 失联与恢复

目的：验证通信丢失后的 hold 语义和恢复。

```bash
$SIM set r1 --link down;  wait_event comm_lost 25
$SIM set r1 --link up;    wait_event comm_restored 45
```

预期：`comm_lost` → `comm_restored`。失联期间模拟器的供电状态照常演进；ups-warden 的观测字段降为 stale，不能把它当作停电。

实测参考：`link down` 后约 10 秒出现 `comm_lost`；`link up` 后第 7 秒出现 `comm_restored`。采集失败的重试退避为 1/2/4/8/16/30 秒，最坏要等约 30 秒才会重新采集。

如需验证“停电期间失联”，先执行 R1 的 `--mode battery` 并等到 `mains_lost`，再执行本剧本。

## R4 多设备总览

目的：让总览和设备列表同时呈现市电、电池、低电三种状态。

```bash
$SIM add ups-a --et1k --ip 127.0.0.2 --json
$SIM add ups-b --et1k --ip 127.0.0.3 --mode battery --json
$SIM add ups-c --et1k --ip 127.0.0.4 --mode low --json
$SIM list
```

把三台设备分别按公共片段接入 ups-warden（每台一个 ups-warden 设备、一个数据源）。预期：ups-b 产生 `mains_lost`，ups-c 产生 `mains_lost` 和 `battery_low`，ups-a 没有事件；`GET /api/v1/snapshots` 和 SSE `sync` 中三台设备的状态与上述一致。实测参考：接入约 15 秒后，ups-b 已有 `mains_lost`；ups-c 先出现 `battery_low`，确认窗口过后出现 `mains_lost`；ups-a 没有事件；`/snapshots` 中三台设备的 `output_source` 分别为 `normal`、`battery`、`battery`。SSE `sync` 未单独核对。

## R5 AYI9 错误码

目的：验证 AYI9 密码错误（103）的提示。

```bash
$SIM add ayi --et1k --ip 127.0.0.6 --ayi9-error-code 103 --json >"$W/ayi.json"
APORT=$(jq -r '.endpoints.ayi9' "$W/ayi.json" | cut -d: -f2)
call -o /dev/null -w '%{http_code}\n' -X POST "$API/sources/test" \
  -d "{\"protocol\":\"ayi9-json\",\"host\":\"127.0.0.6\",\"port\":$APORT,\"profile_key\":\"et1k-ayi9-json\",\"params\":{}}"
$SIM set ayi --ayi9-error-code 0    # 恢复正常响应
```

预期：连接测试返回 502 和错误码 `source_password_wrong`（`curl -f` 会让该调用返回非零退出码，以 HTTP 状态码为准）。如果作为已保存的数据源采集，列表中数据源的 `status.error_code` 为 `password_wrong`。实测参考：连接测试返回 502 / `source_password_wrong`；执行 `--ayi9-error-code 0` 后，同一请求返回 200。已保存数据源的 `status.error_code` 未单独核对。

## R6 关机计划联调（dry-run）

目的：用模拟停电驱动关机计划，只在 dry-run 下观察倒计时和进度。

1. 按[规则、动作与受控主机](../../../../docs/reference/rules-actions-hosts-api.md)和[关机计划](../../../../docs/reference/shutdown-plans-api.md)创建计划、`type:"plan"` 动作，以及引用该动作的规则。新建规则默认 dry-run，不要关闭。受控主机只用本机测试服务端，不填真实主机。
2. 执行 R1 的 `--mode battery`，规则命中后计划进入倒计时。通过 SSE `plan_progress` 或 `GET /api/v1/plan-runs` 观察。
3. 在倒计时期间执行 `--mode mains --capacity 100`。去抖确认的 `mains_restored` 出现后，倒计时中的运行会被取消。

预期：运行为 dry-run，步骤只写 `dry_run` 预览，不连接主机，也不发送计划通知。市电恢复确认后，倒计时中的运行变为 `cancelled`。实测参考：本剧本尚未与 ups-warden 联调实测；计划倒计时默认 60 秒，与 `recovery_confirm_s=60` 相当，需要观察取消时应先把 `countdown_s` 调大。
