import "./plans.css";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Navigate, useNavigate, useParams, useSearchParams } from "react-router";
import {
  queryKeys,
  useActions,
  useDevices,
  useHosts,
  usePlans,
  useTimeZone,
} from "../../api/hooks.ts";
import { deletePlan, getPlanRun, listPlanRuns, type Plan } from "../../api/plans.ts";
import type { PlanRun } from "../../api/types.ts";
import {
  Badge,
  Banner,
  Button,
  DestructiveDialog,
  Empty,
  List,
  ListCaption,
  Row,
  Segmented,
  Spinner,
  type SegmentedOption,
  useToast,
} from "../../components/index.ts";
import { Page } from "../../layout/Page.tsx";
import { useLive, useNow } from "../../live/hooks.ts";
import { clockText } from "../../model/format.ts";
import { countdownRemaining, stageCount, stageNumber } from "../../model/plan.ts";
import { PlanEditor } from "./PlanEditor.tsx";
import { PlanRunsTable } from "./PlanRunsTable.tsx";
import { RunPlanDialog } from "./RunPlanDialog.tsx";
import { RunView } from "./RunView.tsx";
import { planReferences, planStats } from "./model.ts";

type Tab = "config" | "run";

function parseTab(value: string | null): Tab {
  return value === "run" ? "run" : "config";
}

/**
 * 关机计划（F6）：左侧计划列表，右侧为「计划配置 / 当前运行」两个页内视图（`?tab=`），
 * 运行详情由 `?run=<id>` 指定。路由：`/plans`、`/plans/new`、`/plans/:id`。
 * 开发与联调只使用 dry-run、模拟器与本机测试 SSH 服务端，不执行真实关机。
 */
export function PlansPage() {
  const { id: idParam } = useParams();
  const navigate = useNavigate();
  const plansQuery = usePlans();
  const plans = plansQuery.data;
  const isNew = idParam === "new";
  const planId = idParam && !isNew ? Number(idParam) : null;

  // /plans：有计划时默认打开第一个。
  if (idParam === undefined && plans && plans.length > 0) {
    return <Navigate replace to={`/plans/${plans[0].id}`} />;
  }

  let detail;
  if (isNew) {
    detail = <PlanEditor key="new" plan={null} />;
  } else if (plansQuery.isPending) {
    detail = (
      <div className="card">
        <div className="empty">
          <Spinner />
          <div className="t-subhead muted">正在加载计划…</div>
        </div>
      </div>
    );
  } else if (plansQuery.isError && !plans) {
    detail = (
      <Banner
        tone="critical"
        role="alert"
        title="计划加载失败"
        actions={
          <Button size="sm" onClick={() => void plansQuery.refetch()}>
            重试
          </Button>
        }
      >
        {plansQuery.error.message}
      </Banner>
    );
  } else if (planId === null) {
    detail = (
      <div className="card">
        <Empty
          icon="power"
          tone="info"
          title="还没有关机计划"
          action={
            <Button variant="primary" icon="plus" onClick={() => void navigate("/plans/new")}>
              新建计划
            </Button>
          }
        >
          计划按阶段依次执行已保存的 SSH、本地脚本或 HTTP 动作。先在「主机与动作」中准备好动作。
        </Empty>
      </div>
    );
  } else {
    const plan = plans?.find((p) => p.id === planId);
    detail = plan ? (
      <PlanDetail key={plan.id} plan={plan} />
    ) : (
      <div className="card">
        <Empty icon="info" tone="warning" title="计划不存在">
          计划 #{idParam} 可能已被删除。
        </Empty>
      </div>
    );
  }

  return (
    <Page
      title="关机计划"
      subtitle="按阶段依次关闭受控主机。阶段之间串行，同一阶段内的步骤同时执行。计划不会向 UPS 发送任何指令。"
      toolbar={
        <Button variant="primary" size="sm" icon="plus" onClick={() => void navigate("/plans/new")}>
          新建计划
        </Button>
      }
    >
      <div className="split pl-split">
        <div className="sticky-col pl-col">
          <PlanList plans={plans ?? []} selectedId={planId} isNew={isNew} />
          <ListCaption>
            运行开始时冻结计划快照，之后的修改只影响下一次运行。同一计划同时只能有一个运行。
          </ListCaption>
        </div>
        <div className="pl-detail">{detail}</div>
      </div>
    </Page>
  );
}

function PlanList({
  plans,
  selectedId,
  isNew,
}: {
  plans: readonly Plan[];
  selectedId: number | null;
  isNew: boolean;
}) {
  const navigate = useNavigate();
  const actions = useActions().data;
  const runs = useLive((s) => s.data.planRuns);
  if (plans.length === 0 && !isNew) return null;
  return (
    <List hasIcons className="pl-list" aria-label="关机计划">
      {isNew ? <Row icon="plus" iconColor="var(--accent)" title="新建计划" selected /> : null}
      {plans.map((plan) => {
        const stats = planStats(plan);
        const refs = planReferences(plan.id, actions).length;
        const run = runs.find((r) => r.plan_id === plan.id);
        return (
          <Row
            key={plan.id}
            icon="power"
            iconColor={stats.selfShutdown ? "var(--red)" : "var(--indigo)"}
            title={<span className="strong">{plan.name}</span>}
            sub={
              <>
                {stats.stages} 个阶段 · {stats.steps} 个步骤 · {refs > 0 ? "规则触发" : "仅手动"}
                {run ? (
                  <span className="pl-meta">
                    <LiveRunBadge run={run} />
                  </span>
                ) : null}
              </>
            }
            selected={plan.id === selectedId}
            onActivate={() => void navigate(`/plans/${plan.id}`)}
          />
        );
      })}
    </List>
  );
}

/** 列表中的活动运行徽标；倒计时每秒刷新。 */
function LiveRunBadge({ run }: { run: PlanRun }) {
  const now = useNow(1000);
  const remaining = countdownRemaining(run, now);
  const dry = run.dry_run ? "Dry-run · " : "";
  if (remaining !== null) {
    return (
      <Badge tone="orange">
        {dry}倒计时 {clockText(remaining)}
      </Badge>
    );
  }
  return (
    <Badge tone={run.dry_run ? "indigo" : "blue"}>
      {dry}执行中
      {run.status === "running"
        ? ` · 阶段 ${stageNumber(run.current_stage)} / ${stageCount(run)}`
        : ""}
    </Badge>
  );
}

function PlanDetail({ plan }: { plan: Plan }) {
  const [params, setParams] = useSearchParams();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();
  const actions = useActions().data;
  const tab = parseTab(params.get("tab"));
  const runParam = Number(params.get("run")) || null;
  const liveRun = useLive((s) => s.data.planRuns.find((r) => r.plan_id === plan.id) ?? null);
  const [runOpen, setRunOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const refs = planReferences(plan.id, actions);

  const showRun = (runId: number | null) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.set("tab", "run");
        if (runId === null) next.delete("run");
        else next.set("run", String(runId));
        return next;
      },
      { replace: false },
    );
  const setTab = (next: Tab) =>
    setParams(
      (prev) => {
        const p = new URLSearchParams(prev);
        p.set("tab", next);
        if (next === "config") p.delete("run");
        return p;
      },
      { replace: true },
    );

  const tabOptions: SegmentedOption<Tab>[] = [
    { value: "config", label: "计划配置" },
    { value: "run", label: liveRun ? `当前运行 #${liveRun.id}` : "运行详情" },
  ];

  return (
    <>
      <div className="pl-detail-bar">
        <Segmented<Tab> aria-label="计划视图" options={tabOptions} value={tab} onChange={setTab} />
        <div className="hstack gap-1">
          <Button
            size="sm"
            icon="trash"
            disabled={refs.length > 0}
            title={refs.length > 0 ? "被 plan 动作引用，不能删除" : undefined}
            onClick={() => setDeleteOpen(true)}
          >
            删除
          </Button>
          <Button variant="tinted" size="sm" icon="play" onClick={() => setRunOpen(true)}>
            手动执行…
          </Button>
        </div>
      </div>

      {tab === "config" ? (
        <>
          <PlanEditor key={plan.id} plan={plan} />
          <PlanRunsTable planId={plan.id} selectedRunId={runParam} onSelect={(id) => showRun(id)} />
        </>
      ) : (
        <RunPanel plan={plan} runParam={runParam} liveRun={liveRun} onShowRun={showRun} />
      )}

      <RunPlanDialog
        plan={plan}
        open={runOpen}
        onClose={() => setRunOpen(false)}
        onStarted={(runId) => showRun(runId)}
      />
      <DestructiveDialog
        open={deleteOpen}
        onClose={() => setDeleteOpen(false)}
        title={`删除计划「${plan.name}」？`}
        message="删除后无法恢复。"
        confirmLabel="删除计划"
        conflictMessage="计划被 plan 动作引用，或已有运行记录（需保留执行证据），不能删除。"
        onConfirm={async () => {
          await deletePlan(plan.id);
          await queryClient.invalidateQueries({ queryKey: queryKeys.plans });
          toast.show({ tone: "ok", title: "计划已删除", duration: 3000 });
          void navigate("/plans", { replace: true });
        }}
      />
    </>
  );
}

/**
 * 运行视图的数据来源：
 * 1. `?run=` 指定且正是活动运行 → 使用实时存储；
 * 2. `?run=` 指定的其他运行 → `GET /plan-runs/{id}`；
 * 3. 未指定 → 活动运行，否则该计划最近一次运行（运行结束后实时存储移除它，
 *    LiveEffects 使查询失效，这里随之显示终态）。
 */
function RunPanel({
  plan,
  runParam,
  liveRun,
  onShowRun,
}: {
  plan: Plan;
  runParam: number | null;
  liveRun: PlanRun | null;
  onShowRun: (runId: number | null) => void;
}) {
  const timeZone = useTimeZone();
  const actions = useActions().data;
  const hosts = useHosts().data;
  const devices = useDevices().data;
  const showLive = liveRun !== null && (runParam === null || runParam === liveRun.id);

  const latest = useQuery({
    queryKey: [...queryKeys.planRuns, { plan_id: plan.id, latest: true }],
    queryFn: ({ signal }) => listPlanRuns({ plan_id: plan.id, limit: 1 }, signal),
    enabled: runParam === null && liveRun === null,
  });
  const detail = useQuery({
    queryKey: [...queryKeys.planRuns, "detail", runParam],
    queryFn: ({ signal }) => getPlanRun(runParam as number, signal),
    enabled: runParam !== null && !showLive,
  });

  let run: PlanRun | undefined;
  let pending = false;
  let error: Error | null = null;
  let refetch = () => {};
  if (showLive) {
    run = liveRun;
  } else if (runParam !== null) {
    run = detail.data;
    pending = detail.isPending;
    error = detail.error;
    refetch = () => void detail.refetch();
  } else {
    run = latest.data?.items[0];
    pending = latest.isPending;
    error = latest.error;
    refetch = () => void latest.refetch();
  }

  return (
    <>
      {liveRun && !showLive ? (
        <Banner
          tone="info"
          role="status"
          className="pl-run-switch"
          actions={
            <Button size="sm" onClick={() => onShowRun(null)}>
              查看当前运行
            </Button>
          }
        >
          计划当前有进行中的运行 #{liveRun.id}。
        </Banner>
      ) : null}
      {run && run.plan_id === plan.id ? (
        <RunView run={run} actions={actions} hosts={hosts} devices={devices} timeZone={timeZone} />
      ) : pending ? (
        <div className="card">
          <div className="empty">
            <Spinner />
            <div className="t-subhead muted">正在加载运行…</div>
          </div>
        </div>
      ) : error ? (
        <Banner
          tone="critical"
          role="alert"
          title="运行加载失败"
          actions={
            <Button size="sm" onClick={refetch}>
              重试
            </Button>
          }
        >
          {error.message}
        </Banner>
      ) : (
        <div className="card">
          <Empty icon="power" tone="info" title={run ? "运行不属于此计划" : "还没有运行"}>
            {run
              ? "请从运行记录中选择本计划的运行。"
              : "规则触发或手动执行后，倒计时与各阶段进度会显示在这里。"}
          </Empty>
        </div>
      )}
    </>
  );
}
