import { useQuery } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router";
import { listActionRuns, type Action, type ActionRun } from "../../api/actions.ts";
import {
  queryKeys,
  useActions,
  useChannels,
  useHosts,
  usePlans,
  useSettings,
  useTimeZone,
} from "../../api/hooks.ts";
import { Banner, Button, Empty, ListCaption, Spinner } from "../../components/index.ts";
import { Icon } from "../../icons/Icon.tsx";
import { actionRunStatusLabel } from "../../model/labels.ts";
import { formatDateTime } from "../../model/time.ts";
import { ActionSheet } from "./ActionSheet.tsx";
import {
  ACTION_TYPE_COLORS,
  ACTION_TYPE_ICONS,
  ACTION_TYPE_LABELS,
  ACTION_TYPE_ORDER,
  actionSummary,
  isFailedRun,
  latestRunByAction,
} from "./model.ts";
import { RunActionSheet } from "./RunActionSheet.tsx";

const NEW = "new";

/** 动作（F5-4、F5-5）：按类型分组；`?edit=<id|new>` 打开编辑，`?run=<id>` 打开手动执行。 */
export function ActionsTab() {
  const [params, setParams] = useSearchParams();
  const actionsQuery = useActions();
  const hosts = useHosts().data;
  const channels = useChannels().data;
  const plans = usePlans().data;
  const guardOn = useSettings().data?.http_action_guard_ups_hosts;
  const timeZone = useTimeZone();
  // 最近一页执行记录，用于每个动作的“最近执行”；超出这一页的动作不显示，而不是显示“从未运行”。
  const recentRuns = useQuery({
    queryKey: [...queryKeys.actionRuns, "recent"],
    queryFn: ({ signal }) => listActionRuns({ limit: 200 }, signal),
  });
  const latest = latestRunByAction(recentRuns.data?.items ?? []);

  const patch = (key: "edit" | "run", value: string | null) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete("edit");
        next.delete("run");
        if (value !== null) next.set(key, value);
        return next;
      },
      { replace: true },
    );

  if (actionsQuery.isPending) {
    return (
      <div className="card">
        <div className="empty">
          <Spinner />
          <div className="t-subhead muted">正在加载动作…</div>
        </div>
      </div>
    );
  }
  if (actionsQuery.isError && !actionsQuery.data) {
    return (
      <Banner
        tone="critical"
        role="alert"
        title="动作加载失败"
        actions={
          <Button size="sm" onClick={() => void actionsQuery.refetch()}>
            重试
          </Button>
        }
      >
        {actionsQuery.error.message}
      </Banner>
    );
  }

  const actions = actionsQuery.data ?? [];
  const ctx = {
    hosts,
    channelName: (id: number) => channels?.find((c) => c.id === id)?.name ?? `渠道 #${id}`,
    planName: (id: number) => plans?.find((p) => p.id === id)?.name ?? `#${id}`,
  };
  const editParam = params.get("edit");
  const editing = actions.find((a) => String(a.id) === editParam);
  const running = actions.find((a) => String(a.id) === params.get("run") && a.type !== "plan");

  return (
    <>
      {guardOn !== undefined ? (
        <Banner
          tone="neutral"
          icon="shield"
          title={guardOn ? "HTTP 护栏已开启" : "HTTP 护栏已关闭"}
          className="ha-guard"
        >
          {guardOn
            ? "HTTP 动作不能指向任何已配置的 UPS 数据源地址，保存和每次执行时都会检查。"
            : "HTTP 动作可以指向 UPS 数据源地址，请确认不会误操作 UPS 网卡。"}{" "}
          <Link to="/settings/security">在设置中管理</Link>
        </Banner>
      ) : null}

      {actions.length === 0 ? (
        <div className="card">
          <Empty
            icon="bolt"
            tone="info"
            title="还没有动作"
            action={
              <Button
                variant="primary"
                size="sm"
                className="mt-2"
                onClick={() => patch("edit", NEW)}
              >
                新建动作
              </Button>
            }
          >
            动作是规则与关机计划可调用的最小单元：通知、HTTP、本地脚本、SSH 命令或启动计划。
          </Empty>
        </div>
      ) : (
        ACTION_TYPE_ORDER.map((type) => {
          const group = actions.filter((a) => a.type === type);
          if (group.length === 0) return null;
          return (
            <section key={type} aria-label={ACTION_TYPE_LABELS[type]}>
              <h2 className="ha-group-title">
                <Icon name={ACTION_TYPE_ICONS[type]} />
                {ACTION_TYPE_LABELS[type]}
                <span className="faint">{group.length}</span>
              </h2>
              <div className="list has-icons ha-big-icons">
                {group.map((action) => (
                  <ActionRow
                    key={action.id}
                    action={action}
                    summary={actionSummary(action, ctx)}
                    last={latest.get(action.id)}
                    timeZone={timeZone}
                    onEdit={() => patch("edit", String(action.id))}
                    onRun={() => patch("run", String(action.id))}
                  />
                ))}
              </div>
            </section>
          );
        })
      )}
      <ListCaption>
        脚本只能从服务端脚本目录中选择，不能在线创建或编辑。脚本只继承 PATH 等必要环境变量，并注入
        UPS_DEVICE_NAME、UPS_EVENT、UPS_DRY_RUN 等上下文。
      </ListCaption>

      {editParam === NEW ? (
        <ActionSheet key="new" onClose={() => patch("edit", null)} />
      ) : editing ? (
        <ActionSheet key={editing.id} action={editing} onClose={() => patch("edit", null)} />
      ) : null}
      {running ? (
        <RunActionSheet key={running.id} action={running} onClose={() => patch("run", null)} />
      ) : null}
    </>
  );
}

function ActionRow({
  action,
  summary,
  last,
  timeZone,
  onEdit,
  onRun,
}: {
  action: Action;
  summary: string;
  last: ActionRun | undefined;
  timeZone: string | undefined;
  onEdit: () => void;
  onRun: () => void;
}) {
  const failed = last ? isFailedRun(last) : false;
  return (
    <div className="row ha-row">
      <span className="row-icon" style={{ ["--c" as string]: ACTION_TYPE_COLORS[action.type] }}>
        <Icon name={ACTION_TYPE_ICONS[action.type]} />
      </span>
      <div className="row-main">
        <div className="row-title strong">{action.name}</div>
        <div className="row-sub">{summary}</div>
      </div>
      <div className={failed ? "ha-col-last t-footnote ink-red" : "ha-col-last t-footnote faint"}>
        {last
          ? `${formatDateTime(last.started_at, timeZone)} · ${actionRunStatusLabel(last.status)}`
          : ""}
      </div>
      <div className="ha-col-end">
        <Button size="sm" onClick={onEdit} aria-label={`编辑动作「${action.name}」`}>
          编辑
        </Button>
        {action.type === "plan" ? (
          <Link
            className="btn btn-gray btn-sm"
            to={action.config.plan_id ? `/plans/${action.config.plan_id}` : "/plans"}
          >
            前往计划
          </Link>
        ) : (
          <Button size="sm" onClick={onRun} aria-label={`运行动作「${action.name}」`}>
            运行…
          </Button>
        )}
      </div>
    </div>
  );
}
