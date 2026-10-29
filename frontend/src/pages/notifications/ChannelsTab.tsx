import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type CSSProperties } from "react";
import { useNavigate, useSearchParams } from "react-router";
import { isApiError, isConfigSavedRuntimeError } from "../../api/client.ts";
import {
  buildEnabledBody,
  channelKeys,
  deleteChannel,
  listChannels,
  testChannel,
  updateChannel,
  type Channel,
} from "../../api/notifications.ts";
import {
  Badge,
  Banner,
  Button,
  DestructiveDialog,
  Empty,
  List,
  ListCaption,
  Row,
  Spinner,
  Switch,
  useToast,
} from "../../components/index.ts";
import { Icon } from "../../icons/Icon.tsx";
import { SplitView } from "../../layout/SplitView.tsx";
import { ChannelForm, type FormResult } from "./ChannelForm.tsx";
import { CHANNEL_TYPE_LABELS, channelSubtitle } from "./channelDraft.ts";

/** `?id=` 的取值：渠道 id，或 `new` 表示新建。 */
const NEW = "new";

function typeIcon(channel: Pick<Channel, "type">) {
  return channel.type === "telegram" ? "paperplane" : "globe";
}

/** 失败文案：全局已处理的 configuration_saved_runtime_error 不再重复提示。 */
function failureText(err: unknown): string {
  if (!isApiError(err)) return "操作失败，请重试";
  const wait = err.retryAfterSeconds ? `（请 ${err.retryAfterSeconds} 秒后重试）` : "";
  return `${err.message}${wait}`;
}

/** 渠道列表-详情（F4-1、F4-2）。选中项写入 `?id=`，新建为 `?id=new`。 */
export function ChannelsTab() {
  const [params, setParams] = useSearchParams();
  const query = useQuery({
    queryKey: [...channelKeys.all, "list"],
    queryFn: ({ signal }) => listChannels(signal),
  });

  const select = (id: number | typeof NEW | null) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (id === null) next.delete("id");
        else next.set("id", String(id));
        return next;
      },
      { replace: true },
    );

  if (query.isPending) {
    return (
      <div className="card">
        <div className="empty">
          <Spinner />
          <div className="t-subhead muted">正在加载通知渠道…</div>
        </div>
      </div>
    );
  }
  if (query.isError && !query.data) {
    return (
      <Banner
        tone="critical"
        role="alert"
        title="通知渠道加载失败"
        actions={
          <Button size="sm" onClick={() => void query.refetch()}>
            重试
          </Button>
        }
      >
        {query.error.message}
      </Banner>
    );
  }

  const channels = query.data ?? [];
  const idParam = params.get("id");
  const creating = idParam === NEW;
  const wantedId = Number(idParam);
  const hasWanted = !creating && Number.isInteger(wantedId) && wantedId > 0;
  // 没有指定渠道时默认选中第一个，与效果图一致。
  const selected = creating
    ? undefined
    : hasWanted
      ? channels.find((c) => c.id === wantedId)
      : channels[0];

  const list = (
    <>
      <List hasIcons className="ch-list" aria-label="通知渠道">
        {channels.map((channel) => (
          <Row
            key={channel.id}
            icon={typeIcon(channel)}
            softIcon={!channel.enabled}
            title={<span className="strong">{channel.name}</span>}
            sub={channelSubtitle(channel)}
            selected={selected?.id === channel.id}
            onActivate={() => select(channel.id)}
          >
            {channel.enabled ? null : <Badge>已停用</Badge>}
          </Row>
        ))}
        {channels.length === 0 ? (
          <Row title={<span className="muted">还没有通知渠道</span>} />
        ) : null}
      </List>
      <ListCaption>
        通知只在规则的「通知」动作或关机计划中发送，不会自动广播到所有渠道。每个渠道独立排队（最多
        256 条），慢渠道不会拖慢其他渠道。队列只在内存中，服务重启不重发。
      </ListCaption>
    </>
  );

  let detail;
  if (creating) {
    detail = (
      <NewChannel
        onCreated={(channel) => select(channel?.id ?? null)}
        onCancel={() => select(null)}
      />
    );
  } else if (selected) {
    detail = <ChannelDetail key={selected.id} channel={selected} onDeleted={() => select(null)} />;
  } else if (hasWanted) {
    detail = (
      <div className="card">
        <Empty icon="question" tone="info" title="找不到这个渠道">
          渠道可能已被删除。
        </Empty>
      </div>
    );
  } else {
    detail = (
      <div className="card">
        <Empty
          icon="paperplane"
          tone="info"
          title="还没有通知渠道"
          action={
            <Button variant="primary" size="sm" className="mt-2" onClick={() => select(NEW)}>
              添加渠道
            </Button>
          }
        >
          添加 Telegram 或 Webhook 渠道后，才能在规则与关机计划中发送通知。
        </Empty>
      </div>
    );
  }

  return <SplitView stickyList listLabel="渠道列表" list={list} detail={detail} />;
}

// ───────────────────────── 新建 ─────────────────────────

function NewChannel({
  onCreated,
  onCancel,
}: {
  onCreated: (channel: Channel | null) => void;
  onCancel: () => void;
}) {
  return (
    <ChannelForm
      header={
        <div className="card-pad nt-head">
          <span className="row-icon" style={{ "--c": "var(--accent)" } as CSSProperties}>
            <Icon name="plus" />
          </span>
          <div className="nt-title">
            <h2 className="t-title3">新建渠道</h2>
            <div className="t-footnote muted">凭据只写不读，保存后仅显示“已设置”</div>
          </div>
        </div>
      }
      footerStart={
        <Button variant="plain" size="sm" onClick={onCancel}>
          取消
        </Button>
      }
      onSettled={(result) =>
        // 保存成功：选中新渠道。503 已保存但运行恢复失败时拿不到新 id，回到列表重新读取。
        onCreated(result.kind === "saved" ? result.channel : null)
      }
    />
  );
}

// ───────────────────────── 详情 ─────────────────────────

function ChannelDetail({ channel, onDeleted }: { channel: Channel; onDeleted: () => void }) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();
  const [testing, setTesting] = useState(false);
  const [toggling, setToggling] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleteConflict, setDeleteConflict] = useState(false);
  // 每次保存（含 503 已保存）后递增，使表单按重新读取的服务端数据重建草稿。
  const [formVersion, setFormVersion] = useState(0);
  const [baseline, setBaseline] = useState<{ stale: Channel; fresh: Channel } | null>(null);
  const [runtimeError, setRuntimeError] = useState(false);

  const refresh = () => queryClient.invalidateQueries({ queryKey: channelKeys.all });

  const onTest = async () => {
    setTesting(true);
    try {
      await testChannel(channel.id);
      // 202 只表示已入队，不是“已送达”；结果稍后出现在发送记录中。
      toast.show({
        tone: "info",
        title: "测试消息已加入发送队列",
        message: `「${channel.name}」的测试消息已入队。入队不代表送达，结果稍后出现在发送记录中。`,
        duration: 10_000,
        dedupeKey: `channel-test-${channel.id}`,
        actions: [
          {
            label: "查看发送记录",
            onClick: () => void navigate(`/notifications?tab=logs&channel=${channel.id}`),
          },
        ],
      });
    } catch (err) {
      // 409 channel_disabled / 429 notification_queue_full / 503 notification_unavailable
      toast.show({ tone: "critical", title: "测试消息未发送", message: failureText(err) });
    } finally {
      setTesting(false);
    }
  };

  const onToggle = async (enabled: boolean) => {
    setToggling(true);
    try {
      await updateChannel(channel.id, buildEnabledBody(enabled));
      await refresh();
    } catch (err) {
      await refresh();
      // 503 configuration_saved_runtime_error 全局已提示：开关以重新读取的服务端状态为准。
      if (!isConfigSavedRuntimeError(err)) {
        toast.show({
          tone: "critical",
          title: enabled ? "启用渠道失败" : "停用渠道失败",
          message: failureText(err),
        });
      }
    } finally {
      setToggling(false);
    }
  };

  const onConfirmDelete = async () => {
    try {
      // 用户确认后才发送 `{confirm:true}`。
      await deleteChannel(channel.id);
    } catch (err) {
      if (isConfigSavedRuntimeError(err)) {
        // 已删除，只是运行恢复失败：按已删除处理并重新读取。
      } else {
        if (isApiError(err) && err.status === 409) setDeleteConflict(true);
        throw err;
      }
    }
    await refresh();
    onDeleted();
  };

  const disableInstead = async () => {
    try {
      await updateChannel(channel.id, buildEnabledBody(false));
      await refresh();
      setDeleteOpen(false);
      setDeleteConflict(false);
      toast.show({
        tone: "ok",
        title: "渠道已停用",
        message: "历史发送记录会保留。",
        duration: 4000,
      });
    } catch (err) {
      await refresh();
      if (!isConfigSavedRuntimeError(err)) {
        toast.show({ tone: "critical", title: "停用渠道失败", message: failureText(err) });
      }
    }
  };

  const header = (
    <div className="card-pad nt-head">
      <span
        className={channel.enabled ? "row-icon" : "row-icon is-soft"}
        style={{ "--c": "var(--accent)" } as CSSProperties}
      >
        <Icon name={typeIcon(channel)} />
      </span>
      <div className="nt-title">
        <h2 className="t-title3">{channel.name}</h2>
        <div className="t-footnote muted">
          {CHANNEL_TYPE_LABELS[channel.type]}
          {channel.enabled ? "" : " · 已停用"}
        </div>
      </div>
      <div className="nt-acts">
        <Button
          size="sm"
          icon="paperplane"
          onClick={() => void onTest()}
          disabled={testing || !channel.enabled}
          title={channel.enabled ? "使用已保存的配置发送测试消息" : "请先启用渠道再发送测试消息"}
        >
          发送测试
        </Button>
        <Switch
          aria-label="启用"
          checked={channel.enabled}
          disabled={toggling}
          onChange={(next) => void onToggle(next)}
        />
      </div>
    </div>
  );

  return (
    <>
      {runtimeError ? (
        <Banner
          tone="warning"
          role="alert"
          title="配置已保存，但运行恢复失败"
          className="nt-banner"
        >
          新配置已写入，相关服务未能按新配置恢复运行。以下为重新读取的最新配置，请检查后再次保存。
        </Banner>
      ) : null}
      <ChannelForm
        key={`${channel.id}:${formVersion}`}
        channel={baseline && baseline.stale === channel ? baseline.fresh : channel}
        header={header}
        footerStart={
          <Button
            variant="danger-tinted"
            size="sm"
            icon="trash"
            onClick={() => setDeleteOpen(true)}
          >
            删除渠道
          </Button>
        }
        onSettled={(result: FormResult) => {
          setRuntimeError(result.kind === "saved-runtime-error");
          // react-query 向组件分发新数据是异步的：此刻 props 里的 channel 可能仍是旧值。
          // 直接取缓存中刚重新读取的服务端配置作为表单基线，避免重建后的草稿回到旧值并显示“未保存”。
          const fresh = queryClient
            .getQueryData<Channel[]>([...channelKeys.all, "list"])
            ?.find((item) => item.id === channel.id);
          setBaseline(fresh ? { stale: channel, fresh } : null);
          setFormVersion((v) => v + 1);
        }}
      />
      <DestructiveDialog
        open={deleteOpen}
        onClose={() => {
          setDeleteOpen(false);
          setDeleteConflict(false);
        }}
        title={`删除渠道「${channel.name}」？`}
        message={
          deleteConflict
            ? "存在发送记录引用，可改为停用。若仍被动作或关机计划引用，需先移除引用。"
            : "删除后无法恢复。已有发送记录，或被通知动作、关机计划引用的渠道不能删除，可改为停用。"
        }
        confirmLabel="删除渠道"
        onConfirm={onConfirmDelete}
        onDisableInstead={channel.enabled ? () => void disableInstead() : undefined}
      />
    </>
  );
}
