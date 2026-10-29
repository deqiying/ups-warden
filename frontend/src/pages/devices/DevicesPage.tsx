import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState, type CSSProperties } from "react";
import { Link, useLocation, useNavigate, useSearchParams } from "react-router";
import { isConfigSavedRuntimeError } from "../../api/client.ts";
import { deleteDevice, setDeviceEnabled } from "../../api/devices.ts";
import { queryKeys, useDevices, useSourcesByDevice, useTimeZone } from "../../api/hooks.ts";
import type { Device, DeviceState, Source } from "../../api/types.ts";
import { Badge } from "../../components/Badge.tsx";
import { Banner } from "../../components/Banner.tsx";
import { Button } from "../../components/Button.tsx";
import { DestructiveDialog } from "../../components/DestructiveDialog.tsx";
import { Empty } from "../../components/Empty.tsx";
import { Pill } from "../../components/Pill.tsx";
import { Segmented } from "../../components/Segmented.tsx";
import { Spinner } from "../../components/Spinner.tsx";
import { Switch } from "../../components/Switch.tsx";
import { useToast } from "../../components/toast-context.ts";
import { Icon } from "../../icons/Icon.tsx";
import { Page } from "../../layout/Page.tsx";
import { useLive } from "../../live/hooks.ts";
import {
  powerStatus,
  primarySource,
  protocolLabel,
  relativeTime,
  sourceErrorText,
} from "../../model/device.ts";
import { formatDateTime } from "../../model/time.ts";
import { useLiveClock } from "../monitor/hooks.ts";
import { PowerPill } from "../monitor/shared.tsx";
import { AddDeviceWizard } from "./AddDeviceWizard.tsx";
import { EditDeviceSheet } from "./EditDeviceSheet.tsx";
import { filterDevices, type DeviceFilter } from "./model.ts";
import "./devices.css";

const ROW_ICON_COLOR: Readonly<Record<string, string>> = {
  battery: "var(--orange)",
  warn: "var(--yellow)",
  lost: "var(--gray)",
  normal: "var(--green)",
};

/**
 * 设备列表（F3-1，devices.html list / delete）。
 * - `/devices/new` 打开添加向导，`?edit=<id>` 打开编辑浮层，便于从设备详情直接跳转。
 * - 供电状态来自实时存储；数据源摘要逐台请求（Q-C1）。
 */
export function DevicesPage() {
  const location = useLocation();
  const navigate = useNavigate();
  const [search, setSearch] = useSearchParams();
  const queryClient = useQueryClient();
  const toast = useToast();
  const devices = useDevices();
  const list = devices.data ?? [];
  const sources = useSourcesByDevice(list.map((d) => d.id));
  const states = useLive((s) => s.data.devices);
  const synced = useLive((s) => s.data.syncedAt !== null);
  const { now } = useLiveClock();
  const timeZone = useTimeZone();
  const [filter, setFilter] = useState<DeviceFilter>("all");
  const [deleting, setDeleting] = useState<Device | null>(null);

  const wizardOpen = location.pathname.replace(/\/+$/, "") === "/devices/new";
  const editId = Number(search.get("edit"));
  const editTab = search.get("section") === "source" ? "source" : undefined;
  const editing = list.find((d) => d.id === editId);
  const closeEdit = () => {
    const p = new URLSearchParams(search);
    p.delete("edit");
    p.delete("section");
    setSearch(p, { replace: true });
  };
  const openEdit = (device: Device, section?: "source") => {
    const p = new URLSearchParams(search);
    p.set("edit", String(device.id));
    if (section) p.set("section", section);
    else p.delete("section");
    setSearch(p);
  };

  const toggle = useMutation<Device, Error, { id: number; enabled: boolean }>({
    mutationFn: ({ id, enabled }) => setDeviceEnabled(id, enabled),
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.devices });
      void queryClient.invalidateQueries({ queryKey: queryKeys.sources });
    },
    onError: (error, vars) => {
      // 503 已由全局提示“配置已保存”，这里只提示其他失败。
      if (!isConfigSavedRuntimeError(error)) {
        toast.show({
          tone: "warning",
          title: vars.enabled ? "启用失败" : "停用失败",
          message: error.message,
          dedupeKey: `device-toggle-${vars.id}`,
        });
      }
    },
  });

  const filtered = filterDevices(list, filter).sort((a, b) => a.id - b.id);
  const counts = {
    all: list.length,
    enabled: list.filter((d) => d.enabled).length,
    disabled: list.filter((d) => !d.enabled).length,
  };
  const filterControl = (
    <Segmented<DeviceFilter>
      aria-label="按启用状态筛选"
      value={filter}
      onChange={setFilter}
      options={[
        { value: "all", label: "全部", count: counts.all },
        { value: "enabled", label: "已启用", count: counts.enabled },
        { value: "disabled", label: "已停用", count: counts.disabled },
      ]}
    />
  );

  return (
    <Page
      title="设备"
      subtitle="每台设备绑定一个只读数据源。停用设备会停止采集，但保留历史。"
      headAside={list.length > 0 ? filterControl : null}
      toolbar={
        <Button
          variant="primary"
          size="sm"
          icon="plus"
          onClick={() => void navigate("/devices/new")}
        >
          添加设备
        </Button>
      }
    >
      {devices.isPending ? (
        <div className="card">
          <Empty title="正在加载设备…">
            <Spinner />
          </Empty>
        </div>
      ) : devices.isError ? (
        <Banner
          tone="critical"
          role="alert"
          title="设备加载失败"
          actions={
            <Button size="sm" onClick={() => void devices.refetch()}>
              重试
            </Button>
          }
        >
          {devices.error.message}
        </Banner>
      ) : list.length === 0 ? (
        <div className="card">
          <Empty
            icon="ups"
            tone="info"
            title="还没有设备"
            action={
              <Button variant="primary" icon="plus" onClick={() => void navigate("/devices/new")}>
                添加设备
              </Button>
            }
          >
            添加 UPS 网卡后即可开始只读采集。
          </Empty>
        </div>
      ) : (
        <>
          <div className="devices-filter-narrow">{filterControl}</div>
          {sources.isError ? (
            <Banner
              tone="warning"
              role="alert"
              title="数据源摘要加载失败"
              style={{ marginBottom: 16 }}
              actions={
                <Button size="sm" onClick={() => void sources.refetch()}>
                  重试
                </Button>
              }
            >
              {sources.error.message}
            </Banner>
          ) : null}
          {filtered.length === 0 ? (
            <div className="card">
              <Empty icon="filter" tone="info" title="没有符合筛选条件的设备" />
            </div>
          ) : (
            <div className="list has-icons" style={{ "--row-inset": "64px" } as CSSProperties}>
              {filtered.map((device) => {
                const pending = toggle.isPending && toggle.variables?.id === device.id;
                return (
                  <DeviceRow
                    key={device.id}
                    device={device}
                    state={states.find((s) => s.device_id === device.id) ?? null}
                    synced={synced}
                    source={primarySource(sources.data?.[device.id])}
                    sourcesLoaded={sources.isSuccess}
                    now={now}
                    timeZone={timeZone}
                    enabled={pending ? toggle.variables.enabled : device.enabled}
                    toggling={pending}
                    onToggle={(enabled) => toggle.mutate({ id: device.id, enabled })}
                    onEdit={(section) => openEdit(device, section)}
                    onDelete={() => setDeleting(device)}
                  />
                );
              })}
            </div>
          )}
          <p className="list-caption">
            驱动只发送只读请求：SNMP GET / GETNEXT / GETBULK，Modbus FC03 / FC04，AYI9 仅 QAD /
            RTD。同一 IP 的采集与连接测试串行执行。
          </p>
        </>
      )}

      {wizardOpen ? (
        <AddDeviceWizard
          onClose={() => void navigate("/devices")}
          onCreated={(device) => void navigate(`/devices/${device.id}`)}
        />
      ) : null}
      {editing && !wizardOpen ? (
        <EditDeviceSheet device={editing} onClose={closeEdit} initialTab={editTab} />
      ) : null}
      <DestructiveDialog
        open={deleting !== null}
        onClose={() => setDeleting(null)}
        title={`删除「${deleting?.name ?? ""}」？`}
        message="将同时删除它的数据源、状态、事件与历史，且无法撤销。若规则或计划运行记录仍引用它，删除会被拒绝——此时可以改为停用。"
        consequences={["数据源与历史数据", "设备状态、事件与停电周期"]}
        onConfirm={async () => {
          if (!deleting) return;
          await deleteDevice(deleting.id);
          void queryClient.invalidateQueries({ queryKey: queryKeys.devices });
          void queryClient.invalidateQueries({ queryKey: queryKeys.sources });
          toast.show({ tone: "ok", title: `已删除「${deleting.name}」` });
        }}
        onDisableInstead={
          deleting?.enabled
            ? () => {
                toggle.mutate({ id: deleting.id, enabled: false });
                setDeleting(null);
              }
            : undefined
        }
      />
    </Page>
  );
}

function StatusPill({
  device,
  state,
  synced,
}: {
  device: Device;
  state: DeviceState | null;
  synced: boolean;
}) {
  if (!device.enabled) return <Pill size="sm">已停用</Pill>;
  if (state) return <PowerPill state={state} size="sm" />;
  return <Pill size="sm">{synced ? "暂无数据" : "同步中…"}</Pill>;
}

function SourceSummary({
  device,
  source,
  loaded,
  onAdd,
}: {
  device: Device;
  source: Source | undefined;
  loaded: boolean;
  onAdd: () => void;
}) {
  if (!loaded) return <span className="faint">—</span>;
  if (!source) {
    return (
      <>
        <span className="ink-orange">未配置数据源</span>
        <br />
        <button type="button" className="link-button t-footnote" onClick={onAdd}>
          添加数据源
        </button>
      </>
    );
  }
  const status = source.status;
  const unit = source.protocol === "modbus" && typeof source.params?.unit_id === "number";
  return (
    <>
      {protocolLabel(source)} · <span className="mono">{source.host}</span>
      <br />
      {device.enabled && status && status.failures > 0 ? (
        <span className="ink-orange">
          连续 {status.failures} 次
          {status.error_code ? ` ${sourceErrorText(status.error_code)}` : "失败"}
        </span>
      ) : (
        <span className="faint">
          {source.profile_key} v{source.profile_version}
          {unit ? ` · Unit ${String(source.params?.unit_id)}` : ""}
          {source.enabled ? "" : " · 数据源已停用"}
        </span>
      )}
    </>
  );
}

function DeviceRow({
  device,
  state,
  synced,
  source,
  sourcesLoaded,
  now,
  timeZone,
  enabled,
  toggling,
  onToggle,
  onEdit,
  onDelete,
}: {
  device: Device;
  state: DeviceState | null;
  synced: boolean;
  source: Source | undefined;
  sourcesLoaded: boolean;
  now: number;
  timeZone: string | undefined;
  enabled: boolean;
  toggling: boolean;
  onToggle: (enabled: boolean) => void;
  onEdit: (section?: "source") => void;
  onDelete: () => void;
}) {
  const status = state && device.enabled ? powerStatus(state) : null;
  const iconColor = status ? ROW_ICON_COLOR[status.card] : undefined;
  const lastOk = device.enabled ? (source?.status?.last_ok_at ?? null) : null;
  const seen = lastOk ? (relativeTime(lastOk, now) ?? formatDateTime(lastOk, timeZone)) : null;
  const failing = (source?.status?.failures ?? 0) > 0;
  const sub = device.enabled
    ? device.location
    : ["已停用 · 历史保留到保留期结束", device.location].filter(Boolean).join(" · ");

  return (
    <div className={device.enabled ? "row dev-row" : "row dev-row is-disabled"}>
      <span
        className={iconColor ? "row-icon" : "row-icon is-soft"}
        style={iconColor ? ({ "--c": iconColor } as CSSProperties) : undefined}
      >
        <Icon name="ups" />
      </span>
      <div className="row-main">
        <div className="row-title strong">
          <Link to={`/devices/${device.id}`}>{device.name}</Link>
        </div>
        {sub || device.tags.length > 0 ? (
          <div className="row-sub">
            {sub}
            {device.tags.map((tag) => (
              <span key={tag}>
                {" "}
                <Badge tone="outline">{tag}</Badge>
              </span>
            ))}
          </div>
        ) : null}
      </div>
      <div className="col-proto t-subhead muted">
        <SourceSummary
          device={device}
          source={source}
          loaded={sourcesLoaded}
          onAdd={() => onEdit("source")}
        />
      </div>
      <div className="col-status">
        <StatusPill device={device} state={state} synced={synced} />
      </div>
      <div className={failing ? "col-seen t-footnote ink-orange" : "col-seen t-footnote faint"}>
        {source?.status && device.enabled
          ? [failing ? null : `${source.status.latency_ms} ms`, seen ?? "从未成功"]
              .filter(Boolean)
              .join(" · ")
          : ""}
      </div>
      <Switch
        size="sm"
        checked={enabled}
        disabled={toggling}
        aria-label={`启用「${device.name}」`}
        onChange={onToggle}
      />
      <RowMenu
        name={device.name}
        deviceId={device.id}
        onEdit={() => onEdit()}
        onDelete={onDelete}
      />
    </div>
  );
}

/** 行内「更多」菜单：编辑、查看详情、删除。Esc 或点击外部关闭并回焦。 */
function RowMenu({
  name,
  deviceId,
  onEdit,
  onDelete,
}: {
  name: string;
  deviceId: number;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const firstRef = useRef<HTMLButtonElement>(null);
  const menuId = useId();
  const navigate = useNavigate();

  useEffect(() => {
    if (!open) return;
    firstRef.current?.focus();
    const onPointer = (e: PointerEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      setOpen(false);
      triggerRef.current?.focus();
    };
    document.addEventListener("pointerdown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const pick = (action: () => void) => {
    setOpen(false);
    action();
  };

  return (
    <div className="row-menu" ref={rootRef}>
      <Button
        ref={triggerRef}
        size="sm"
        iconOnly
        icon="ellipsis"
        aria-label={`「${name}」的更多操作`}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        onClick={() => setOpen((v) => !v)}
      />
      {open ? (
        <div className="popover" role="menu" id={menuId} aria-label={`「${name}」的操作`}>
          <button
            ref={firstRef}
            type="button"
            role="menuitem"
            className="menu-item"
            onClick={() => pick(onEdit)}
          >
            <Icon name="pencil" /> 编辑
          </button>
          <button
            type="button"
            role="menuitem"
            className="menu-item"
            onClick={() => pick(() => void navigate(`/devices/${deviceId}`))}
          >
            <Icon name="chart" /> 查看详情
          </button>
          <div className="menu-sep" role="separator" />
          <button
            type="button"
            role="menuitem"
            className="menu-item ink-red"
            onClick={() => pick(onDelete)}
          >
            <Icon name="trash" /> 删除…
          </button>
        </div>
      ) : null}
    </div>
  );
}
