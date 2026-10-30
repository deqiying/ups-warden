import { useSearchParams } from "react-router";
import { useActions, useHosts } from "../../api/hooks.ts";
import type { Host } from "../../api/hosts.ts";
import { Badge, Banner, Button, Empty, ListCaption, Spinner } from "../../components/index.ts";
import { Icon } from "../../icons/Icon.tsx";
import { FingerprintSheet } from "./FingerprintFlow.tsx";
import { HostSheet } from "./HostSheet.tsx";
import { HOST_KIND_ICONS, HOST_KIND_LABELS } from "./model.ts";

const NEW = "new";

/** 受控主机（F5-1～F5-3）：`?host=<id|new>` 打开编辑浮层，`?fp=<id>` 打开指纹浮层。 */
export function HostsTab() {
  const [params, setParams] = useSearchParams();
  const hostsQuery = useHosts();
  const actionsQuery = useActions();

  const patch = (key: "host" | "fp", value: string | null) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete("host");
        next.delete("fp");
        if (value !== null) next.set(key, value);
        return next;
      },
      { replace: true },
    );

  if (hostsQuery.isPending) {
    return (
      <div className="card">
        <div className="empty">
          <Spinner />
          <div className="t-subhead muted">正在加载受控主机…</div>
        </div>
      </div>
    );
  }
  if (hostsQuery.isError && !hostsQuery.data) {
    return (
      <Banner
        tone="critical"
        role="alert"
        title="受控主机加载失败"
        actions={
          <Button size="sm" onClick={() => void hostsQuery.refetch()}>
            重试
          </Button>
        }
      >
        {hostsQuery.error.message}
      </Banner>
    );
  }

  const hosts = hostsQuery.data ?? [];
  const refCount = (hostId: number) =>
    (actionsQuery.data ?? []).filter((a) => a.type === "ssh" && a.config.host_id === hostId).length;

  const hostParam = params.get("host");
  const fpParam = Number(params.get("fp"));
  const editing =
    hostParam === NEW ? null : (hosts.find((h) => String(h.id) === hostParam) ?? undefined);
  const fpHost = hosts.find((h) => h.id === fpParam);

  return (
    <>
      {hosts.length === 0 ? (
        <div className="card">
          <Empty
            icon="server"
            tone="info"
            title="还没有受控主机"
            action={
              <Button
                variant="primary"
                size="sm"
                className="mt-2"
                onClick={() => patch("host", NEW)}
              >
                添加主机
              </Button>
            }
          >
            添加主机后，SSH 动作与关机计划才能关闭它。
          </Empty>
        </div>
      ) : (
        <div className="list has-icons ha-big-icons" aria-label="受控主机">
          {hosts.map((host) => (
            <HostRow
              key={host.id}
              host={host}
              refs={actionsQuery.data ? refCount(host.id) : null}
              onEdit={() => patch("host", String(host.id))}
              onFingerprint={() => patch("fp", String(host.id))}
            />
          ))}
        </div>
      )}
      <ListCaption>
        只支持
        SSH。凭据加密保存，之后只显示「已设置」。指纹未确认或不匹配时，拒绝认证和执行任何命令。在线检测只探测一个指定的
        TCP 端口或 ICMP，3 秒超时。
      </ListCaption>

      {hostParam === NEW ? (
        <HostSheet
          key="new"
          onClose={() => patch("host", null)}
          onCreated={(created) => patch("host", String(created.id))}
        />
      ) : editing ? (
        <HostSheet
          key={editing.id}
          host={editing}
          onClose={() => patch("host", null)}
          onCreated={() => {}}
        />
      ) : null}
      {fpHost ? <FingerprintSheet host={fpHost} onClose={() => patch("fp", null)} /> : null}
    </>
  );
}

function HostRow({
  host,
  refs,
  onEdit,
  onFingerprint,
}: {
  host: Host;
  refs: number | null;
  onEdit: () => void;
  onFingerprint: () => void;
}) {
  const confirmed = host.hostkey_fingerprint !== "";
  const auth = host.conn.auth_method === "password" ? "密码" : "私钥";
  return (
    <div className="row ha-row">
      <span className="row-icon" style={{ ["--c" as string]: "var(--accent)" }}>
        <Icon name={HOST_KIND_ICONS[host.kind]} />
      </span>
      <div className="row-main">
        <div className="row-title strong">{host.name}</div>
        <div className="row-sub">
          {HOST_KIND_LABELS[host.kind]} ·{" "}
          <span className="mono">
            {host.address}:{host.conn.port}
          </span>
        </div>
      </div>
      <div className="ha-col-auth t-subhead muted">
        {host.conn.username} · {auth}
        {host.conn.sudo ? " · sudo -n" : ""}
        <br />
        <span className="faint">
          {refs === null ? "" : refs > 0 ? `被 ${refs} 个 SSH 动作引用` : "未被引用"}
        </span>
      </div>
      <div className="ha-col-fp">
        {confirmed ? (
          <Badge tone="green" icon="fingerprint">
            指纹已确认
          </Badge>
        ) : (
          <Badge tone="yellow" icon="alert">
            指纹未确认
          </Badge>
        )}
      </div>
      <div className="ha-col-online t-caption faint">
        {host.online_check.type === "tcp" ? `TCP ${host.online_check.port}` : "ICMP"}
      </div>
      <div className="ha-col-end">
        {confirmed ? null : (
          <Button variant="tinted" size="sm" onClick={onFingerprint}>
            获取指纹
          </Button>
        )}
        <Button size="sm" onClick={onEdit} aria-label={`编辑主机「${host.name}」`}>
          编辑
        </Button>
      </div>
    </div>
  );
}
