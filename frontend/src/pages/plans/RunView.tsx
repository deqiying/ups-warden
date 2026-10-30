import { useState } from "react";
import { Link } from "react-router";
import type { Action } from "../../api/actions.ts";
import { isActivePlanRun } from "../../api/normalize.ts";
import type { Host } from "../../api/hosts.ts";
import type { Device, PlanRun, PlanStep } from "../../api/types.ts";
import { Badge, Banner, Button, List, Pill, Ring, Row, Spinner } from "../../components/index.ts";
import { Icon } from "../../icons/Icon.tsx";
import { useLiveStale, useNow } from "../../live/hooks.ts";
import { clockText } from "../../model/format.ts";
import { countdownFraction, countdownRemaining, stageCount } from "../../model/plan.ts";
import { formatFullDateTime, formatTime } from "../../model/time.ts";
import {
  ACTION_TYPE_COLORS,
  ACTION_TYPE_ICONS,
  ACTION_TYPE_LABELS,
} from "../hosts-actions/model.ts";
import { CancelRunDialog } from "./CancelRunDialog.tsx";
import {
  actionRunPath,
  isProblemStep,
  planRunStatusText,
  planRunTone,
  runStages,
  stageNode,
  stepsDoneText,
  stepStatusView,
  type RunStage,
} from "./model.ts";

export interface RunViewProps {
  run: PlanRun;
  actions: readonly Action[] | undefined;
  hosts: readonly Host[] | undefined;
  devices: readonly Device[] | undefined;
  timeZone: string | undefined;
}

/**
 * 单次运行视图（F6-3）：活动运行由 SSE（sync.plan_runs / plan_progress）驱动，
 * 已结束的运行来自查询 API。倒计时按 countdown_until 与本地时钟计算；
 * 阶段数与步骤只从 steps 推导（Q-C5），不显示触发来源。
 */
export function RunView({ run, actions, hosts, devices, timeZone }: RunViewProps) {
  const stale = useLiveStale();
  const active = isActivePlanRun(run);
  const [cancelOpen, setCancelOpen] = useState(false);
  const device = run.device_id !== null ? devices?.find((d) => d.id === run.device_id) : undefined;
  const stages = runStages(run);

  return (
    <div className="pl-run">
      {active && stale ? (
        <Banner tone="warning" role="status" className="pl-run-stale" title="实时连接已断开">
          显示的是断线前的进度，实际状态可能已变化；重连后自动同步。
        </Banner>
      ) : null}
      <section className="card" aria-label={`运行 #${run.id}`}>
        <div className="pl-run-hero">
          <RunVisual run={run} />
          <div className="pl-run-main">
            <div className="hstack wrap gap-1">
              <Pill
                tone={planRunTone(run)}
                size="sm"
                dot={active}
                breathe={run.status === "countdown"}
              >
                {run.status === "running" && run.current_stage > 0
                  ? `执行中 · 阶段 ${run.current_stage} / ${stageCount(run)}`
                  : planRunStatusText(run)}
              </Pill>
              {run.dry_run && active ? <Badge tone="indigo">Dry-run</Badge> : null}
              <span className="t-footnote faint">
                运行 #{run.id} · {formatFullDateTime(run.started_at, timeZone)} 开始
                {run.ended_at ? ` · ${formatFullDateTime(run.ended_at, timeZone)} 结束` : ""}
              </span>
            </div>
            <div className="t-title2 mt-2">{heroTitle(run)}</div>
            <div className="t-subhead muted mt-2">
              {device
                ? `关联设备 ${device.name}`
                : run.device_id !== null
                  ? `关联设备 #${run.device_id}`
                  : "未关联设备"}
              {run.outage_cycle_id !== null ? ` · 停电周期 #${run.outage_cycle_id}` : ""}
              {` · 步骤 ${stepsDoneText(run)}`}
            </div>
            {active ? (
              <div className="t-footnote faint mt-2">{recoveryNote(run, device)}</div>
            ) : null}
            {run.status === "running" ? <ProgressBar stages={stages} run={run} /> : null}
            {problemCount(run) > 0 ? (
              <div className="t-footnote ink-orange mt-2">
                {problemCount(run)} 个步骤失败、结果未知或等待离线超时；失败不会阻塞后续阶段。
              </div>
            ) : null}
          </div>
          {active ? (
            <div className="pl-run-actions">
              <Button variant="danger-tinted" onClick={() => setCancelOpen(true)}>
                {run.status === "countdown" ? "取消运行…" : "取消未开始的步骤…"}
              </Button>
            </div>
          ) : null}
        </div>
      </section>

      {stages.length > 0 ? (
        <div className="pl-stages mt-6">
          {stages.map((stage) => (
            <RunStageCard
              key={stage.stage}
              run={run}
              stage={stage}
              actions={actions}
              hosts={hosts}
              timeZone={timeZone}
            />
          ))}
        </div>
      ) : (
        <p className="list-caption">此运行没有步骤记录。</p>
      )}
      <p className="list-caption">
        「已发送」只表示命令已被接受，不代表关机完成；SSH 步骤在等待时间内确认离线后记为「已离线」。
        服务中断时执行中的步骤记为「未知」，不会重放。输出与错误详情见对应的动作执行记录。
      </p>

      <CancelRunDialog run={run} open={cancelOpen} onClose={() => setCancelOpen(false)} />
    </div>
  );
}

function heroTitle(run: PlanRun): string {
  switch (run.status) {
    case "countdown":
      return `「${run.plan_name}」即将开始`;
    case "running":
      return run.current_stage > 0
        ? `正在执行阶段 ${run.current_stage}`
        : `「${run.plan_name}」执行中`;
    case "completed":
      return run.dry_run ? "Dry-run 预演已完成" : "全部步骤已结束";
    case "completed_with_errors":
      return "已结束，部分步骤有错误";
    case "cancelled":
      return "运行已取消";
    case "aborted":
      return "服务重启后按配置终止";
    default:
      return planRunStatusText(run);
  }
}

/** 市电恢复说明：使用触发设备配置的 recovery_confirm_s，不写死秒数。 */
function recoveryNote(run: PlanRun, device: Device | undefined): string {
  if (run.outage_cycle_id === null) return "未关联停电周期，市电恢复不会自动取消此运行。";
  const confirm = device ? `（${device.config.recovery_confirm_s} 秒去抖）` : "";
  if (run.status === "countdown") {
    return `触发设备的市电恢复确认后${confirm}自动取消。${run.dry_run ? "" : "本停电周期内只会真实执行一次。"}`;
  }
  return `市电恢复确认后${confirm}按计划的「执行中市电恢复」策略处理；已发出的命令无法撤回。`;
}

function problemCount(run: PlanRun): number {
  return run.steps.filter(isProblemStep).length;
}

function RunVisual({ run }: { run: PlanRun }) {
  const now = useNow(1000);
  const remaining = countdownRemaining(run, now);
  const fraction = countdownFraction(run, now);
  if (remaining !== null) {
    return (
      <Ring
        fraction={fraction ?? 0}
        size={120}
        stroke={9}
        color="var(--orange)"
        label={`倒计时剩余 ${clockText(remaining)}`}
      >
        <div>
          <div className="t-metric pl-cd">{clockText(remaining)}</div>
          <div className="t-caption faint">后开始</div>
        </div>
      </Ring>
    );
  }
  if (run.status === "running") {
    return (
      <span className="pl-run-icon is-running">
        <Spinner />
      </span>
    );
  }
  const icon =
    run.status === "completed"
      ? "check"
      : run.status === "cancelled"
        ? "slash-circle"
        : run.status === "countdown"
          ? "hourglass"
          : "alert";
  return (
    <span className={`pl-run-icon is-${run.status}`}>
      <Icon name={icon} />
    </span>
  );
}

function ProgressBar({ stages, run }: { stages: RunStage[]; run: PlanRun }) {
  return (
    <div className="pl-progress" aria-hidden="true">
      {stages.map((stage) => {
        const node = stageNode(run, stage);
        return <span key={stage.stage} className={`is-${node}`} />;
      })}
    </div>
  );
}

function RunStageCard({
  run,
  stage,
  actions,
  hosts,
  timeZone,
}: {
  run: PlanRun;
  stage: RunStage;
  actions: readonly Action[] | undefined;
  hosts: readonly Host[] | undefined;
  timeZone: string | undefined;
}) {
  const node = stageNode(run, stage);
  const started = stage.steps
    .map((s) => s.started_at)
    .filter((t): t is string => t !== null)
    .sort()[0];
  const ended = stage.steps.every((s) => s.ended_at !== null)
    ? stage.steps
        .map((s) => s.ended_at as string)
        .sort()
        .at(-1)
    : undefined;
  let timing: string;
  if (run.status === "countdown") timing = "等待倒计时结束";
  else if (started && ended)
    timing = `${formatTime(started, timeZone)} – ${formatTime(ended, timeZone)}`;
  else if (started) timing = `${formatTime(started, timeZone)} 开始`;
  else timing = node === "cancelled" ? "未开始即取消" : "尚未开始";

  return (
    <div className="pl-stage">
      <div className="pl-rail">
        <span className={`pl-node is-${node}`}>
          {node === "done" ? (
            <Icon name="check" />
          ) : node === "warn" ? (
            <Icon name="alert" />
          ) : (
            stage.stage
          )}
        </span>
      </div>
      <div className="pl-stage-body">
        <section className="card pl-stage-card" aria-label={`阶段 ${stage.stage}`}>
          <div className="pl-stage-head">
            <h3>阶段 {stage.stage}</h3>
            <span className="pl-timing">{timing}</span>
          </div>
          <List hasIcons flat className="pl-steps">
            {stage.steps.map((step) => (
              <RunStepRow
                key={step.step}
                step={step}
                action={actions?.find((a) => a.id === step.action_id)}
                hosts={hosts}
              />
            ))}
          </List>
        </section>
      </div>
    </div>
  );
}

function RunStepRow({
  step,
  action,
  hosts,
}: {
  step: PlanStep;
  action: Action | undefined;
  hosts: readonly Host[] | undefined;
}) {
  const view = stepStatusView(step);
  const hostId = step.host_id ?? (action?.type === "ssh" ? action.config.host_id : undefined);
  const host = hostId !== undefined ? hosts?.find((h) => h.id === hostId) : undefined;
  const link = actionRunPath(step);
  const meta = [
    action ? ACTION_TYPE_LABELS[action.type] : "",
    host ? host.name : hostId != null ? `主机 #${hostId}` : "",
    step.result.action_status ? `动作结果 ${step.result.action_status}` : "",
  ].filter(Boolean);
  return (
    <Row
      icon={action ? ACTION_TYPE_ICONS[action.type] : "question"}
      iconColor={action ? ACTION_TYPE_COLORS[action.type] : "var(--gray)"}
      title={action?.name ?? `动作 #${step.action_id}`}
      sub={
        <>
          {meta.join(" · ")}
          {step.result.error ? (
            <div className="ink-red pl-step-err">{step.result.error}</div>
          ) : null}
          {step.result.offline_timeout ? (
            <div className="ink-orange">命令已被接受，但在等待时间内未确认主机离线。</div>
          ) : null}
          {link ? (
            <div>
              <Link to={link}>执行记录 #{step.result.action_run_id}</Link>
            </div>
          ) : null}
        </>
      }
    >
      <div className="pl-step-status">
        <Pill tone={view.tone} size="sm" icon={view.icon}>
          {view.label}
        </Pill>
      </div>
    </Row>
  );
}
