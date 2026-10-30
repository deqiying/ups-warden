import { useQueryClient } from "@tanstack/react-query";
import { useId, useMemo, useState } from "react";
import { useNavigate } from "react-router";
import type { Action } from "../../api/actions.ts";
import { queryKeys, useActions, useChannels, useHosts } from "../../api/hooks.ts";
import type { Host } from "../../api/hosts.ts";
import {
  buildPlanCreateBody,
  buildPlanUpdateBody,
  createPlan,
  draftFromPlan,
  emptyPlanDraft,
  emptyStageDraft,
  PLAN_LIMITS,
  updatePlan,
  validatePlanDraft,
  type OnMainsRestored,
  type OnRestart,
  type Plan,
  type PlanDraft,
  type StageDraft,
} from "../../api/plans.ts";
import {
  Badge,
  Banner,
  Button,
  ChipSelect,
  FormRow,
  Input,
  List,
  Row,
  Segmented,
  Select,
  Stepper,
  useToast,
} from "../../components/index.ts";
import { Icon } from "../../icons/Icon.tsx";
import {
  ACTION_TYPE_COLORS,
  ACTION_TYPE_ICONS,
  ACTION_TYPE_LABELS,
  actionSummary,
  failureText,
} from "../hosts-actions/model.ts";
import { planReferences, stepActions } from "./model.ts";

const MAINS_OPTIONS = [
  { value: "cancel_pending" as const, label: "取消未开始的步骤" },
  { value: "continue" as const, label: "继续执行" },
];

const RESTART_OPTIONS = [
  { value: "resume" as const, label: "继续剩余步骤" },
  { value: "abort" as const, label: "终止" },
];

/**
 * 计划配置编辑（F6-1）。新建走 POST，编辑走 PUT 且只提交修改过的顶层字段
 * （stages / notify_channel_ids 修改时整体提交）。前端只做结构性校验，
 * 跨资源语义（动作已删除、渠道停用等）以后端 422 的 message 为准。
 * 运行开始时冻结计划快照，保存只影响之后的运行。
 */
export function PlanEditor({ plan }: { plan: Plan | null }) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();
  const ids = useId();
  const [draft, setDraft] = useState<PlanDraft>(() =>
    plan ? draftFromPlan(plan) : emptyPlanDraft(),
  );
  const [submitted, setSubmitted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const actionsQuery = useActions();
  const actions = actionsQuery.data;
  const hosts = useHosts().data;
  const channels = useChannels().data;

  const actionById = useMemo(() => new Map((actions ?? []).map((a) => [a.id, a])), [actions]);
  const actionTypes = useMemo(
    () => (actions ? new Map(actions.map((a) => [a.id, a.type as string])) : undefined),
    [actions],
  );
  const errors = validatePlanDraft(draft, actionTypes);
  const hasErrors = Object.keys(errors).length > 0;
  const showErr = (key: string) => (submitted ? errors[key] : undefined);

  const updateBody = plan ? buildPlanUpdateBody(plan, draft) : null;
  const dirty = plan ? Object.keys(updateBody ?? {}).length > 0 : true;

  const set = <K extends keyof PlanDraft>(key: K, value: PlanDraft[K]) => {
    setDraft((d) => ({ ...d, [key]: value }));
    setFailure(null);
  };
  const setStage = (key: number, patch: Partial<StageDraft>) =>
    set(
      "stages",
      draft.stages.map((s) => (s.key === key ? { ...s, ...patch } : s)),
    );

  const selfIndex = draft.stages.findIndex((s) => s.selfShutdown);
  const hasSelf = selfIndex >= 0;
  const usedIds = new Set(draft.stages.flatMap((s) => s.actionIds));
  const candidates = stepActions(actions).filter((a) => !usedIds.has(a.id));

  const addStage = () => {
    const next = [...draft.stages];
    // 普通阶段总是插在「关闭本机」之前。
    next.splice(hasSelf ? selfIndex : next.length, 0, emptyStageDraft());
    set("stages", next);
  };
  const addSelfStage = () => set("stages", [...draft.stages, emptyStageDraft(true)]);
  const removeStage = (key: number) =>
    set(
      "stages",
      draft.stages.filter((s) => s.key !== key),
    );
  const moveUp = (index: number) => {
    if (index <= 0) return;
    const next = [...draft.stages];
    [next[index - 1], next[index]] = [next[index], next[index - 1]];
    set("stages", next);
  };

  const save = async () => {
    setSubmitted(true);
    if (hasErrors) return;
    setBusy(true);
    setFailure(null);
    try {
      if (plan) {
        if (!dirty) return;
        const saved = await updatePlan(plan.id, updateBody ?? {});
        setDraft(draftFromPlan(saved));
        setSubmitted(false);
        await queryClient.invalidateQueries({ queryKey: queryKeys.plans });
        toast.show({
          tone: "ok",
          title: "计划已保存",
          message: "进行中的运行使用开始时冻结的快照，修改只影响之后的运行。",
          duration: 4000,
        });
      } else {
        const saved = await createPlan(buildPlanCreateBody(draft));
        await queryClient.invalidateQueries({ queryKey: queryKeys.plans });
        toast.show({ tone: "ok", title: "计划已创建", duration: 3000 });
        void navigate(`/plans/${saved.id}`, { replace: true });
      }
    } catch (err) {
      // 422 validation_failed（message 给出原因）；409 resource_conflict（名称重复等）。
      setFailure(failureText(err));
    } finally {
      setBusy(false);
    }
  };

  const refs = plan ? planReferences(plan.id, actions) : [];
  const normalCount = draft.stages.filter((s) => !s.selfShutdown).length;

  return (
    <div className="pl-editor">
      <section className="card">
        <div className="card-pad pl-head">
          <div className="eyebrow">{plan ? `计划 · #${plan.id}` : "新建计划"}</div>
          <label className="pl-name-label" htmlFor={`${ids}-name`}>
            计划名称
          </label>
          <Input
            id={`${ids}-name`}
            className="pl-name"
            value={draft.name}
            invalid={Boolean(showErr("name"))}
            placeholder="如：机房关机"
            autoComplete="off"
            onChange={(e) => set("name", e.target.value)}
          />
          {showErr("name") ? <div className="pl-err">{errors.name}</div> : null}
          {plan ? (
            <div className="t-footnote muted mt-2">
              {refs.length > 0
                ? `被 ${refs.map((a) => `「${a.name}」`).join("、")} 动作引用，规则通过它们触发本计划`
                : "未被 plan 动作引用，只能手动执行"}
            </div>
          ) : null}
        </div>
        <List flat>
          <FormRow
            label="倒计时"
            description="触发后先等待，期间可取消；触发设备的市电恢复确认后总会取消"
          >
            <Stepper
              aria-label="倒计时（秒）"
              value={draft.countdownS}
              min={0}
              max={PLAN_LIMITS.secondsMax}
              unit="秒"
              onChange={(v) => set("countdownS", v)}
            />
          </FormRow>
          <FormRow
            label="执行中市电恢复"
            description="以触发设备去抖确认的「市电恢复」为准；已发出的命令不可撤回"
          >
            <Segmented<OnMainsRestored>
              aria-label="执行中市电恢复"
              options={MAINS_OPTIONS}
              value={draft.onMainsRestored}
              onChange={(v) => set("onMainsRestored", v)}
            />
          </FormRow>
          <FormRow label="服务重启后" description="中断时正在执行的步骤记为「未知」，不会重放">
            <Segmented<OnRestart>
              aria-label="服务重启后"
              options={RESTART_OPTIONS}
              value={draft.onRestart}
              onChange={(v) => set("onRestart", v)}
            />
          </FormRow>
          <FormRow
            label="计划通知"
            description={`开始、步骤失败、取消、完成时发送；dry-run 不发送。最多 ${PLAN_LIMITS.channelsMax} 个启用的渠道`}
          >
            <ChipSelect
              aria-label="计划通知渠道"
              values={draft.notifyChannelIds}
              onChange={(v) => set("notifyChannelIds", v)}
              options={(channels ?? []).map((c) => ({
                value: c.id,
                label: c.enabled ? c.name : `${c.name}（已停用）`,
                disabled: !c.enabled,
              }))}
              max={PLAN_LIMITS.channelsMax}
              addLabel="添加渠道…"
              missingLabel={(id) => `渠道 #${id}`}
            />
          </FormRow>
        </List>
        {showErr("countdown") || showErr("channels") ? (
          <div className="card-pad pl-err">{errors.countdown ?? errors.channels}</div>
        ) : null}
      </section>

      <div className="section-head mt-6">
        <h2 className="section-title">阶段</h2>
        <span className="t-footnote faint">
          {draft.stages.length} / {PLAN_LIMITS.stagesMax} · 阶段之间串行，同一阶段内的步骤同时执行
        </span>
      </div>
      {actionsQuery.isError ? (
        <Banner tone="warning" role="status" className="mt-3" title="动作列表加载失败">
          无法显示步骤详情，也无法检查步骤类型。{actionsQuery.error.message}
        </Banner>
      ) : null}
      {showErr("stages") ? (
        <div className="pl-err" role="alert">
          {errors.stages}
        </div>
      ) : null}
      <div className="pl-stages mt-3">
        {/* 「关闭本机」只能是最后一个阶段，因此普通阶段的序号就是 index + 1。 */}
        {draft.stages.map((stage, index) => (
          <StageEditor
            key={stage.key}
            stage={stage}
            title={stage.selfShutdown ? "最后 · 关闭本机" : `阶段 ${index + 1}`}
            nodeLabel={stage.selfShutdown ? null : String(index + 1)}
            error={showErr(`stage-${index}`)}
            actionById={actionById}
            actionsLoaded={actions !== undefined}
            hosts={hosts}
            candidates={candidates}
            canRemove={draft.stages.length > 1}
            canMoveUp={index > 0 && !stage.selfShutdown}
            onChange={(patch) => setStage(stage.key, patch)}
            onRemove={() => removeStage(stage.key)}
            onMoveUp={() => moveUp(index)}
          />
        ))}
      </div>
      <div className="pl-stage-adds">
        <Button
          variant="plain"
          size="sm"
          icon="plus"
          disabled={draft.stages.length >= PLAN_LIMITS.stagesMax}
          onClick={addStage}
        >
          {hasSelf ? "在本机关机前添加阶段" : "添加阶段"}
        </Button>
        {!hasSelf ? (
          <Button
            variant="plain"
            size="sm"
            icon="power"
            disabled={draft.stages.length >= PLAN_LIMITS.stagesMax || normalCount === 0}
            onClick={addSelfStage}
          >
            添加「关闭本机」阶段
          </Button>
        ) : null}
      </div>

      {failure ? (
        <Banner tone="critical" role="alert" title="未能保存" className="mt-4">
          {failure}
        </Banner>
      ) : null}
      {submitted && hasErrors ? (
        <div className="pl-err mt-3" role="alert">
          还有未通过校验的项目，请按提示修改后再保存。
        </div>
      ) : null}
      <div className="pl-save mt-6">
        {plan ? (
          <Button
            disabled={!dirty || busy}
            onClick={() => {
              setDraft(draftFromPlan(plan));
              setSubmitted(false);
              setFailure(null);
            }}
          >
            还原
          </Button>
        ) : (
          <Button disabled={busy} onClick={() => void navigate("/plans")}>
            取消
          </Button>
        )}
        <Button
          variant="primary"
          disabled={!dirty || busy}
          aria-busy={busy}
          onClick={() => void save()}
        >
          {busy ? "正在保存…" : plan ? "保存" : "创建计划"}
        </Button>
      </div>
    </div>
  );
}

function StageEditor({
  stage,
  title,
  nodeLabel,
  error,
  actionById,
  actionsLoaded,
  hosts,
  candidates,
  canRemove,
  canMoveUp,
  onChange,
  onRemove,
  onMoveUp,
}: {
  stage: StageDraft;
  title: string;
  /** 时间线节点文字；自关机阶段显示电源图标。 */
  nodeLabel: string | null;
  error: string | undefined;
  actionById: ReadonlyMap<number, Action>;
  actionsLoaded: boolean;
  hosts: readonly Host[] | undefined;
  candidates: readonly Action[];
  canRemove: boolean;
  canMoveUp: boolean;
  onChange: (patch: Partial<StageDraft>) => void;
  onRemove: () => void;
  onMoveUp: () => void;
}) {
  const self = stage.selfShutdown;
  const maxSteps = self ? 1 : PLAN_LIMITS.stepsMax;
  const canAdd = stage.actionIds.length < maxSteps;
  const hasSsh = stage.actionIds.some((id) => actionById.get(id)?.type === "ssh");
  const options = [
    { value: "", label: candidates.length > 0 ? "添加步骤…" : "没有可添加的动作" },
    ...candidates.map((a) => ({
      value: String(a.id),
      label: `${ACTION_TYPE_LABELS[a.type]} · ${a.name}`,
    })),
  ];

  return (
    <div className="pl-stage">
      <div className="pl-rail">
        <span className={self ? "pl-node is-self" : "pl-node"}>
          {nodeLabel ?? <Icon name="power" />}
        </span>
      </div>
      <div className="pl-stage-body">
        <section
          className={self ? "card pl-stage-card pl-self-card" : "card pl-stage-card"}
          aria-label={title}
        >
          <div className="pl-stage-head">
            <h3>{title}</h3>
            {canMoveUp ? (
              <Button
                size="sm"
                iconOnly
                icon="arrow-up"
                aria-label={`上移${title}`}
                onClick={onMoveUp}
              />
            ) : null}
            {canRemove ? (
              <Button
                size="sm"
                iconOnly
                icon="trash"
                aria-label={`删除${title}`}
                onClick={onRemove}
              />
            ) : null}
          </div>
          <div className="pl-params">
            <label className="pl-param">
              <span>
                <Icon name="hourglass" />
                开始前等待
              </span>
              <Stepper
                aria-label={`${title}开始前等待（秒）`}
                value={stage.delayS}
                min={0}
                max={PLAN_LIMITS.secondsMax}
                unit="秒"
                onChange={(v) => onChange({ delayS: v })}
              />
            </label>
            <label className="pl-param">
              <span>
                <Icon name="clock" />
                单步超时
              </span>
              <Stepper
                aria-label={`${title}单步超时（秒）`}
                value={stage.stepTimeoutS}
                min={1}
                max={PLAN_LIMITS.secondsMax}
                unit="秒"
                onChange={(v) => onChange({ stepTimeoutS: v })}
              />
            </label>
            {!self ? (
              <label className="pl-param">
                <span>
                  <Icon name="wifi-off" />
                  等待离线
                </span>
                <Stepper
                  aria-label={`${title}等待离线（秒）`}
                  value={stage.offlineWaitS}
                  min={0}
                  max={PLAN_LIMITS.secondsMax}
                  unit="秒"
                  onChange={(v) => onChange({ offlineWaitS: v })}
                />
              </label>
            ) : null}
          </div>
          {self ? (
            <div className="pl-self-note">
              自关机阶段必须是最后一个阶段、只含一个步骤且不等待离线。执行前先把运行记为结束，再关闭
              UPS Warden 所在主机；本机重启后不会继续本计划。
            </div>
          ) : null}
          {stage.actionIds.length > 0 ? (
            <List hasIcons flat className="pl-steps">
              {stage.actionIds.map((id) => (
                <StepRow
                  key={id}
                  id={id}
                  action={actionById.get(id)}
                  actionsLoaded={actionsLoaded}
                  hosts={hosts}
                  onRemove={() => onChange({ actionIds: stage.actionIds.filter((x) => x !== id) })}
                />
              ))}
            </List>
          ) : null}
          <div className="pl-stage-foot">
            {canAdd ? (
              <Select
                aria-label={`为${title}添加步骤`}
                options={options}
                value=""
                disabled={candidates.length === 0}
                onChange={(v) => {
                  if (v) onChange({ actionIds: [...stage.actionIds, Number(v)] });
                }}
              />
            ) : null}
            <span className="spacer" />
            <span>
              {stage.actionIds.length} / {maxSteps}
              {self ? "" : " · 同时执行"}
              {!self && hasSsh && stage.offlineWaitS > 0 ? " · 只对 SSH 步骤等待离线" : ""}
            </span>
          </div>
          {error ? (
            <div className="pl-err pl-stage-err" role="alert">
              {error}
            </div>
          ) : null}
        </section>
      </div>
    </div>
  );
}

function StepRow({
  id,
  action,
  actionsLoaded,
  hosts,
  onRemove,
}: {
  id: number;
  action: Action | undefined;
  actionsLoaded: boolean;
  hosts: readonly Host[] | undefined;
  onRemove: () => void;
}) {
  const name = action?.name ?? `动作 #${id}`;
  const host =
    action?.type === "ssh" ? hosts?.find((h) => h.id === action.config.host_id) : undefined;
  return (
    <Row
      icon={action ? ACTION_TYPE_ICONS[action.type] : "question"}
      iconColor={action ? ACTION_TYPE_COLORS[action.type] : "var(--gray)"}
      title={name}
      sub={
        action ? (
          `${ACTION_TYPE_LABELS[action.type]} · ${actionSummary(action, { hosts })}`
        ) : actionsLoaded ? (
          <span className="ink-red">动作不存在（可能已删除），请移除该步骤</span>
        ) : (
          "正在加载动作…"
        )
      }
    >
      {host ? (
        host.hostkey_fingerprint ? (
          <Badge tone="green" icon="fingerprint">
            指纹已确认
          </Badge>
        ) : (
          <Badge tone="orange" icon="fingerprint">
            指纹未确认
          </Badge>
        )
      ) : null}
      <Button
        size="sm"
        variant="plain"
        iconOnly
        icon="x"
        aria-label={`移除步骤「${name}」`}
        onClick={onRemove}
      />
    </Row>
  );
}
