import { useQueryClient } from "@tanstack/react-query";
import { useId, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router";
import type { Action } from "../../api/actions.ts";
import {
  queryKeys,
  useActions,
  useChannels,
  useDevices,
  useHosts,
  usePlans,
  useTimeZone,
} from "../../api/hooks.ts";
import {
  buildRuleCreateBody,
  buildRuleUpdateBody,
  createRule,
  deleteRule,
  draftFromRule,
  emptyGroupDraft,
  emptyRuleDraft,
  RULE_LIMITS,
  updateRule,
  validateRuleDraft,
  validateTag,
  type Rule,
  type RuleDraft,
  type RuleTemplate,
  type ScopeType,
  type TriggerType,
} from "../../api/rules.ts";
import {
  Banner,
  Button,
  ChipSelect,
  DestructiveDialog,
  Dialog,
  Input,
  List,
  Row,
  Segmented,
  Select,
  Stepper,
  Switch,
  TokenInput,
  useToast,
} from "../../components/index.ts";
import { Icon } from "../../icons/Icon.tsx";
import { EVENT_SEVERITY, EVENT_TYPE_GROUPS } from "../../model/events.ts";
import { durationText } from "../../model/format.ts";
import { eventLabel, SEVERITY_TEXT } from "../../model/labels.ts";
import { formatFullDateTime } from "../../model/time.ts";
import {
  ACTION_TYPE_COLORS,
  ACTION_TYPE_ICONS,
  ACTION_TYPE_LABELS,
  actionSummary,
  failureText,
} from "../hosts-actions/model.ts";
import { ConditionBuilder } from "./ConditionBuilder.tsx";
import { actionEffectText, matchedDevices } from "./model.ts";
import { latestFired, useRuleLogs } from "./logs.ts";
import { RuleLogs } from "./RuleLogs.tsx";

const SCOPE_OPTIONS = [
  { value: "device" as const, label: "指定设备" },
  { value: "tags" as const, label: "按标签" },
  { value: "all" as const, label: "所有设备" },
];

const TRIGGER_OPTIONS = [
  { value: "event" as const, label: "事件" },
  { value: "condition" as const, label: "条件" },
];

const ALL_EVENTS = EVENT_TYPE_GROUPS.flatMap((g) => g.types);

/**
 * 规则编辑器（F7-1～F7-5、F7-7）。新建走 POST（默认 dry-run），编辑走 PUT 且只提交修改过的顶层字段。
 * 关闭 dry-run 时先弹确认框，确认后才改变草稿；重新开启不需确认（Q-P2，后端无确认字段）。
 * 前端只做结构性校验，跨资源错误以后端 422 message 为准；保存成功后显示响应中的 warnings。
 */
export function RuleEditor({
  rule,
  template = null,
}: {
  rule: Rule | null;
  /** 从模板新建时的模板；模板默认停用且 dry-run，动作留空需显式选择。 */
  template?: RuleTemplate | null;
}) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();
  const ids = useId();
  const [draft, setDraft] = useState<RuleDraft>(() => {
    if (rule) return draftFromRule(rule);
    if (template) return draftFromRule(template.rule);
    return emptyRuleDraft();
  });
  const [submitted, setSubmitted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const [warnings, setWarnings] = useState<string[]>([]);
  const [dryOffOpen, setDryOffOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  const actionsQuery = useActions();
  const actions = actionsQuery.data;
  const devices = useDevices().data;
  const plans = usePlans().data;
  const channels = useChannels().data;
  const hosts = useHosts().data;

  const actionById = useMemo(() => new Map((actions ?? []).map((a) => [a.id, a])), [actions]);
  const actionTypes = useMemo(
    () => (actions ? new Map(actions.map((a) => [a.id, a.type as string])) : undefined),
    [actions],
  );
  const requiredActionType = !rule && template ? template.suggested_action_type : null;
  const errors = validateRuleDraft(draft, { actionTypes, requiredActionType });
  const hasErrors = Object.keys(errors).length > 0;
  const showErr = (key: string) => (submitted ? errors[key] : undefined);

  const updateBody = rule ? buildRuleUpdateBody(rule, draft) : null;
  const dirty = rule ? Object.keys(updateBody ?? {}).length > 0 : true;

  const set = <K extends keyof RuleDraft>(key: K, value: RuleDraft[K]) => {
    setDraft((d) => ({ ...d, [key]: value }));
    setFailure(null);
  };

  const planName = (id: number) => plans?.find((p) => p.id === id)?.name;
  const channelName = (id: number) => channels?.find((c) => c.id === id)?.name ?? `渠道 #${id}`;

  const changeTrigger = (type: TriggerType) =>
    setDraft((d) => ({
      ...d,
      triggerType: type,
      // 首次切到条件时给出一个空条件组，减少一次点击；已有内容保留，切回后仍可恢复。
      groups: type === "condition" && d.groups.length === 0 ? [emptyGroupDraft()] : d.groups,
    }));

  const toggleDryRun = (next: boolean) => {
    if (next) set("dryRun", true);
    else setDryOffOpen(true);
  };

  const reset = () => {
    if (rule) setDraft(draftFromRule(rule));
    setSubmitted(false);
    setFailure(null);
  };

  const save = async () => {
    setSubmitted(true);
    if (hasErrors) return;
    setBusy(true);
    setFailure(null);
    try {
      if (rule) {
        if (!dirty) return;
        const saved = await updateRule(rule.id, updateBody ?? {});
        await queryClient.invalidateQueries({ queryKey: queryKeys.rules });
        setDraft(draftFromRule(saved.rule));
        setSubmitted(false);
        setWarnings(saved.warnings);
        toast.show({
          tone: "ok",
          title: "规则已保存",
          message: "修改作用范围或触发条件会开始新的计算实例，旧日志保留。",
          duration: 4000,
        });
      } else {
        const saved = await createRule(buildRuleCreateBody(draft));
        await queryClient.invalidateQueries({ queryKey: queryKeys.rules });
        toast.show({
          tone: saved.warnings.length > 0 ? "warning" : "ok",
          title: "规则已创建",
          message: saved.warnings.join("；") || undefined,
          duration: saved.warnings.length > 0 ? 8000 : 3000,
        });
        void navigate(`/rules/${saved.rule.id}`, { replace: true });
      }
    } catch (err) {
      // 422 validation_failed（message 给出原因，含设备或动作不存在）。
      setFailure(failureText(err));
    } finally {
      setBusy(false);
    }
  };

  const matched = matchedDevices(draft, devices);
  const effects = draft.actionIds.map((id) => actionEffectText(actionById.get(id), id, planName));
  const recoveryEffects = draft.recoveryActionIds.map((id) =>
    actionEffectText(actionById.get(id), id, planName),
  );
  const isEvent = draft.triggerType === "event";

  return (
    <div className="ru-editor">
      <section className="card" aria-label="规则配置">
        <div className="card-pad ru-head">
          <div className="ru-head-main">
            <div className="eyebrow">
              {rule
                ? `${rule.trigger.type === "event" ? "事件规则" : "条件规则"} · #${rule.id}`
                : template
                  ? `从模板创建 · ${template.rule.name}`
                  : "新建规则"}
            </div>
            <label className="ru-name-label" htmlFor={`${ids}-name`}>
              规则名称
            </label>
            <Input
              id={`${ids}-name`}
              className="ru-name"
              value={draft.name}
              invalid={Boolean(showErr("name"))}
              placeholder="如：停电与恢复通知"
              autoComplete="off"
              onChange={(e) => set("name", e.target.value)}
            />
            {showErr("name") ? <div className="ru-err">{errors.name}</div> : null}
            {rule ? <LastFired ruleId={rule.id} /> : null}
          </div>
          <div className="ru-toggles">
            <label>
              启用
              <Switch
                aria-label="启用"
                checked={draft.enabled}
                onChange={(v) => set("enabled", v)}
              />
            </label>
            <label>
              Dry-run
              <Switch
                aria-label="Dry-run"
                tone="indigo"
                checked={draft.dryRun}
                onChange={toggleDryRun}
              />
            </label>
          </div>
        </div>
        {draft.dryRun ? (
          <Banner tone="indigo" className="ru-mode" title="Dry-run">
            命中时只保存执行记录与脱敏预览：不连接主机、不发送通知、不启动脚本。
          </Banner>
        ) : (
          <Banner tone="warning" className="ru-mode" title="真实执行">
            {effects.length > 0 ? `命中后会${effects.join("、")}。` : "命中后会执行所选动作。"}
            重新开启 dry-run 不需要确认。
          </Banner>
        )}
        {!draft.enabled ? (
          <Banner tone="neutral" className="ru-mode" title="已停用">
            停用的规则不参与评估。启停保留已处理过的触发，不会重放。
          </Banner>
        ) : null}

        <div className="ru-sec">
          <div className="ru-sec-title">
            <h3>作用范围</h3>
            <span className="t-footnote faint">只评估已启用的设备</span>
          </div>
          <Segmented<ScopeType>
            aria-label="作用范围"
            options={SCOPE_OPTIONS}
            value={draft.scopeType}
            onChange={(v) => set("scopeType", v)}
          />
          <div className="mt-3">
            {draft.scopeType === "device" ? (
              <ChipSelect
                aria-label="指定设备"
                values={draft.deviceIds}
                onChange={(v) => set("deviceIds", v)}
                options={(devices ?? []).map((d) => ({
                  value: d.id,
                  label: d.enabled ? d.name : `${d.name}（已停用）`,
                }))}
                max={RULE_LIMITS.devicesMax}
                addLabel="添加设备…"
                missingLabel={(id) => `设备 #${id}（已删除）`}
              />
            ) : draft.scopeType === "tags" ? (
              <TokenInput
                aria-label="设备标签"
                values={draft.tags}
                onChange={(v) => set("tags", v)}
                addLabel="添加标签"
                placeholder="输入标签后回车"
                max={RULE_LIMITS.tagsMax}
                validate={validateTag}
              />
            ) : null}
            <p className="t-footnote muted mt-2">
              {draft.scopeType === "all"
                ? `包括之后新增的设备 · 当前 ${matched.length} 台已启用`
                : draft.scopeType === "tags"
                  ? `匹配任一标签 · 当前 ${matched.length} 台已启用${
                      matched.length > 0 ? `：${matched.map((d) => d.name).join("、")}` : ""
                    }`
                  : `当前 ${matched.length} 台已启用`}
            </p>
          </div>
          {showErr("scope") ? (
            <div className="ru-err" role="alert">
              {errors.scope}
            </div>
          ) : null}
        </div>

        <div className="ru-sec">
          <div className="ru-sec-title">
            <h3>{isEvent ? "触发事件" : "触发条件"}</h3>
            <Segmented<TriggerType>
              aria-label="触发方式"
              options={TRIGGER_OPTIONS}
              value={draft.triggerType}
              onChange={changeTrigger}
            />
          </div>
          {isEvent ? (
            <>
              <EventPicker values={draft.events} onChange={(v) => set("events", v)} />
              <p className="list-caption">
                恢复动作会与对应的解除事件配对执行，例如「市电中断」与「市电恢复」。多个同时活动的事件分别配对。
              </p>
            </>
          ) : (
            <ConditionBuilder
              groups={draft.groups}
              onChange={(v) => set("groups", v)}
              errorFor={showErr}
            />
          )}
          {showErr("trigger") ? (
            <div className="ru-err" role="alert">
              {errors.trigger}
            </div>
          ) : null}
        </div>

        <div className="ru-sec">
          <div className="ru-sec-title">
            <h3>{isEvent ? "事件发生时执行" : "命中时执行"}</h3>
            <span className="t-footnote faint">同时派发，不等待前一个完成</span>
          </div>
          {requiredActionType === "plan" ? (
            <Banner tone="warning" className="ru-plan-hint" icon="power">
              需要选择一个「启动关机计划」动作。模板不会自动挑选计划。
            </Banner>
          ) : null}
          <ActionRefList
            label="命中动作"
            ids={draft.actionIds}
            onChange={(v) => set("actionIds", v)}
            actions={actions}
            actionById={actionById}
            actionsLoaded={actions !== undefined}
            summaryContext={{ hosts, channelName, planName: (id) => planName(id) ?? `#${id}` }}
            error={showErr("actions")}
          />
          <div className="ru-sec-title mt-6">
            <h3>{isEvent ? "事件解除时执行" : "恢复时执行"}</h3>
            <span className="t-footnote faint">
              {isEvent ? "不受冷却时间限制" : "所有条件组都确定不成立后"}
            </span>
          </div>
          <ActionRefList
            label="恢复动作"
            ids={draft.recoveryActionIds}
            onChange={(v) => set("recoveryActionIds", v)}
            actions={actions}
            actionById={actionById}
            actionsLoaded={actions !== undefined}
            summaryContext={{ hosts, channelName, planName: (id) => planName(id) ?? `#${id}` }}
            error={showErr("recovery")}
          />
          {actionsQuery.isError ? (
            <Banner tone="warning" role="status" className="mt-3" title="动作列表加载失败">
              无法显示动作详情，也无法检查动作是否存在。{actionsQuery.error.message}
            </Banner>
          ) : null}
        </div>

        <div className="ru-sec ru-cooldown">
          <div>
            <h3 className="t-subhead strong">冷却时间</h3>
            <div className="t-footnote muted">
              {isEvent
                ? "0 表示每次事件都执行。冷却期内的新触发记为「跳过」，冷却结束后不补发。"
                : "冷却期内的新触发记为「跳过」，冷却结束后不补发。"}
              {draft.cooldownS >= 60 ? ` 当前 ${durationText(draft.cooldownS)}。` : ""}
            </div>
            {showErr("cooldown") ? <div className="ru-err">{errors.cooldown}</div> : null}
          </div>
          <Stepper
            aria-label="冷却时间（秒）"
            value={draft.cooldownS}
            min={0}
            max={RULE_LIMITS.secondsMax}
            step={10}
            unit="秒"
            onChange={(v) => set("cooldownS", v)}
          />
        </div>

        {warnings.length > 0 ? (
          <Banner tone="warning" role="status" className="ru-mode" title="保存提醒">
            {warnings.join("；")}
          </Banner>
        ) : null}
        {failure ? (
          <Banner tone="critical" role="alert" title="未能保存" className="ru-mode">
            {failure}
          </Banner>
        ) : null}
        {submitted && hasErrors ? (
          <div className="ru-err ru-save-err" role="alert">
            还有未通过校验的项目，请按提示修改后再保存。
          </div>
        ) : null}
        <div className="ru-sec ru-save">
          {rule ? (
            <Button
              variant="danger-tinted"
              size="sm"
              icon="trash"
              className="ru-delete"
              onClick={() => setDeleteOpen(true)}
            >
              删除规则
            </Button>
          ) : null}
          {rule ? (
            <Button disabled={!dirty || busy} onClick={reset}>
              还原
            </Button>
          ) : (
            <Button disabled={busy} onClick={() => void navigate("/rules")}>
              取消
            </Button>
          )}
          <Button
            variant="primary"
            disabled={!dirty || busy}
            aria-busy={busy}
            onClick={() => void save()}
          >
            {busy ? "正在保存…" : rule ? "保存" : "创建规则"}
          </Button>
        </div>
      </section>

      {rule ? <RuleLogs ruleId={rule.id} /> : null}

      <Dialog
        open={dryOffOpen}
        onClose={() => setDryOffOpen(false)}
        icon="power"
        tone="warning"
        title="让这条规则真实执行？"
        message={
          <>
            「{draft.name.trim() || "未命名规则"}」下次命中时将
            {effects.length > 0 ? effects.join("、") : "执行所选动作"}
            {recoveryEffects.length > 0 ? `；恢复时将${recoveryEffects.join("、")}` : ""}
            。已经处理过的触发不会重放。保存后生效。
          </>
        }
        actions={
          <>
            <Button onClick={() => setDryOffOpen(false)}>保持 dry-run</Button>
            <Button
              variant="danger"
              onClick={() => {
                set("dryRun", false);
                setDryOffOpen(false);
              }}
            >
              真实执行
            </Button>
          </>
        }
      />
      {rule ? (
        <DestructiveDialog
          open={deleteOpen}
          onClose={() => setDeleteOpen(false)}
          title={`删除规则「${rule.name}」？`}
          message="删除后无法恢复。"
          confirmLabel="删除规则"
          conflictMessage="规则已有评估日志（需保留执行证据），不能删除。可以改为停用。"
          onDisableInstead={() => {
            setDeleteOpen(false);
            void (async () => {
              try {
                await updateRule(rule.id, { enabled: false });
                await queryClient.invalidateQueries({ queryKey: queryKeys.rules });
                setDraft((d) => ({ ...d, enabled: false }));
                toast.show({ tone: "ok", title: "规则已停用", duration: 3000 });
              } catch (err) {
                toast.show({ tone: "critical", title: "停用失败", message: failureText(err) });
              }
            })();
          }}
          onConfirm={async () => {
            await deleteRule(rule.id);
            await queryClient.invalidateQueries({ queryKey: queryKeys.rules });
            toast.show({ tone: "ok", title: "规则已删除", duration: 3000 });
            void navigate("/rules", { replace: true });
          }}
        />
      ) : null}
    </div>
  );
}

/** 编辑器头部的最近触发：取已加载评估日志中最新的 fired（契约没有该字段）。 */
function LastFired({ ruleId }: { ruleId: number }) {
  const timeZone = useTimeZone();
  const devices = useDevices().data;
  const logs = useRuleLogs(ruleId);
  const hit = latestFired(logs.data?.pages);
  if (!hit) return null;
  const device = devices?.find((d) => d.id === hit.device_id)?.name ?? `设备 #${hit.device_id}`;
  return (
    <div className="t-footnote muted mt-2">
      最近触发 {formatFullDateTime(hit.at, timeZone)}（{device}）
    </div>
  );
}

/** 事件多选：15 种统一模型事件，按供电、电池、负载与环境、通讯分组顺序排列。 */
function EventPicker({
  values,
  onChange,
}: {
  values: string[];
  onChange: (next: string[]) => void;
}) {
  const full = values.length >= RULE_LIMITS.eventsMax;
  return (
    <div className="ru-events" role="group" aria-label="触发事件">
      {ALL_EVENTS.map((type) => {
        const on = values.includes(type);
        const severity = EVENT_SEVERITY[type];
        return (
          <button
            key={type}
            type="button"
            className={on ? "ru-ev is-on" : "ru-ev"}
            aria-pressed={on}
            disabled={!on && full}
            onClick={() => onChange(on ? values.filter((v) => v !== type) : [...values, type])}
          >
            <span className={on ? "check is-on" : "check"}>
              {on ? <Icon name="check" /> : null}
            </span>
            {eventLabel(type)}
            {severity ? <span className="faint ru-ev-sev">{SEVERITY_TEXT[severity]}</span> : null}
          </button>
        );
      })}
    </div>
  );
}

function ActionRefList({
  label,
  ids,
  onChange,
  actions,
  actionById,
  actionsLoaded,
  summaryContext,
  error,
}: {
  label: string;
  ids: number[];
  onChange: (next: number[]) => void;
  actions: readonly Action[] | undefined;
  actionById: ReadonlyMap<number, Action>;
  actionsLoaded: boolean;
  summaryContext: Parameters<typeof actionSummary>[1];
  error: string | undefined;
}) {
  const candidates = (actions ?? []).filter((a) => !ids.includes(a.id));
  const canAdd = ids.length < RULE_LIMITS.actionsMax;
  return (
    <>
      {ids.length > 0 ? (
        <List hasIcons className="ru-acts" aria-label={label}>
          {ids.map((id) => {
            const action = actionById.get(id);
            const name = action?.name ?? `动作 #${id}`;
            return (
              <Row
                key={id}
                icon={action ? ACTION_TYPE_ICONS[action.type] : "question"}
                iconColor={action ? ACTION_TYPE_COLORS[action.type] : "var(--gray)"}
                title={name}
                sub={
                  action ? (
                    `${ACTION_TYPE_LABELS[action.type]} · ${actionSummary(action, summaryContext)}`
                  ) : actionsLoaded ? (
                    <span className="ink-red">动作不存在（可能已删除），请移除</span>
                  ) : (
                    "正在加载动作…"
                  )
                }
              >
                {action?.type === "plan" && action.config.plan_id ? (
                  <Link className="t-footnote nowrap" to={`/plans/${action.config.plan_id}`}>
                    查看计划
                  </Link>
                ) : null}
                <Button
                  size="sm"
                  variant="plain"
                  iconOnly
                  icon="minus"
                  aria-label={`从${label}移除「${name}」`}
                  onClick={() => onChange(ids.filter((x) => x !== id))}
                />
              </Row>
            );
          })}
        </List>
      ) : (
        <p className="t-footnote faint ru-acts-empty">尚未选择动作。</p>
      )}
      {canAdd ? (
        <Select
          className="ru-act-add"
          aria-label={`添加${label}`}
          options={[
            {
              value: "",
              label: candidates.length > 0 ? "添加动作…" : "没有可添加的动作",
            },
            ...candidates.map((a) => ({
              value: String(a.id),
              label: `${ACTION_TYPE_LABELS[a.type]} · ${a.name}`,
            })),
          ]}
          value=""
          disabled={candidates.length === 0}
          onChange={(v) => {
            if (v) onChange([...ids, Number(v)]);
          }}
        />
      ) : null}
      {error ? (
        <div className="ru-err" role="alert">
          {error}
        </div>
      ) : null}
    </>
  );
}
