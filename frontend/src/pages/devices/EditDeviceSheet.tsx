import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState } from "react";
import { Link } from "react-router";
import { isConfigSavedRuntimeError } from "../../api/client.ts";
import {
  createSource,
  deleteSource,
  testSource,
  updateDevice,
  updateSource,
} from "../../api/devices.ts";
import { queryKeys, useDeviceSources, useProfiles } from "../../api/hooks.ts";
import type { Device, ProbeResult, Source, SourceProtocol } from "../../api/types.ts";
import { Banner } from "../../components/Banner.tsx";
import { Button } from "../../components/Button.tsx";
import { DestructiveDialog } from "../../components/DestructiveDialog.tsx";
import { Empty } from "../../components/Empty.tsx";
import { Input, Textarea } from "../../components/Input.tsx";
import { Segmented } from "../../components/Segmented.tsx";
import { Sheet } from "../../components/Sheet.tsx";
import { Spinner } from "../../components/Spinner.tsx";
import { TokenInput } from "../../components/TokenInput.tsx";
import { useToast } from "../../components/toast-context.ts";
import { primarySource } from "../../model/device.ts";
import { GridField, SourceEnabledRow, SourceFields, ThresholdFields } from "./forms.tsx";
import {
  PROTOCOLS,
  buildDevicePatch,
  buildSourceCreate,
  buildSourcePatch,
  deviceDraftFrom,
  emptySourceDraft,
  hasErrors,
  protocolMeta,
  secretIsSet,
  sourceDraftFrom,
  switchProtocol,
  validateConfig,
  validateDeviceDraft,
  validateSourceDraft,
  validateTag,
  type DeviceDraft,
  type SourceDraft,
} from "./model.ts";

type Tab = "basic" | "thresholds" | "source";

export interface EditDeviceSheetProps {
  device: Device;
  onClose: () => void;
  /** 初始视图，如从“未配置数据源”入口打开时直接到数据源。 */
  initialTab?: Tab;
}

/**
 * 编辑设备（F3-3）。设备 PATCH 只提交修改的字段、config 整体替换；数据源 PATCH 只提交修改的字段，
 * 凭据只写，清除走 `clear_secrets`。503 configuration_saved_runtime_error 表示配置已保存
 * （全局已提示并刷新），按已保存处理，不回滚表单。
 */
export function EditDeviceSheet(props: EditDeviceSheetProps) {
  const sources = useDeviceSources(props.device.id);
  const profiles = useProfiles();
  if (sources.isPending || profiles.isPending) {
    return (
      <Sheet open onClose={props.onClose} title={`编辑「${props.device.name}」`}>
        <Empty title="正在加载数据源…">
          <Spinner />
        </Empty>
      </Sheet>
    );
  }
  if (sources.isError || profiles.isError) {
    const error = sources.error ?? profiles.error;
    return (
      <Sheet open onClose={props.onClose} title={`编辑「${props.device.name}」`}>
        <Banner
          tone="critical"
          role="alert"
          title="加载失败"
          actions={
            <Button
              size="sm"
              onClick={() => {
                void sources.refetch();
                void profiles.refetch();
              }}
            >
              重试
            </Button>
          }
        >
          {error?.message}
        </Banner>
      </Sheet>
    );
  }
  return (
    <EditForm
      {...props}
      source={primarySource(sources.data)}
      profiles={profiles.data}
      key={props.device.id}
    />
  );
}

function EditForm({
  device: initialDevice,
  source: initialSource,
  profiles,
  onClose,
  initialTab,
}: EditDeviceSheetProps & {
  source: Source | undefined;
  profiles: NonNullable<ReturnType<typeof useProfiles>["data"]>;
}) {
  const ids = useId();
  const queryClient = useQueryClient();
  const toast = useToast();
  const [tab, setTab] = useState<Tab>(initialTab ?? "basic");
  // 已保存的基线：部分保存成功后更新，重试时不重复提交已保存的部分。
  const [baseDevice, setBaseDevice] = useState(initialDevice);
  const [baseSource, setBaseSource] = useState(initialSource);
  const [draft, setDraft] = useState<DeviceDraft>(() => deviceDraftFrom(initialDevice));
  const [sourceDraft, setSourceDraft] = useState<SourceDraft>(() =>
    initialSource ? sourceDraftFrom(initialSource) : emptySourceDraft("snmp", profiles),
  );
  const [addingSource, setAddingSource] = useState(false);
  const [showErrors, setShowErrors] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<{ title: string; message: string } | null>(null);
  const [confirmDeleteSource, setConfirmDeleteSource] = useState(false);

  const devicePatch = buildDevicePatch(baseDevice, draft);
  const sourcePatch = baseSource ? buildSourcePatch(baseSource, sourceDraft) : null;
  const sourceDirty = baseSource ? Object.keys(sourcePatch ?? {}).length > 0 : addingSource;
  const dirty = Object.keys(devicePatch).length > 0 || sourceDirty;

  const deviceErrors = validateDeviceDraft(draft);
  const configErrors = validateConfig(draft.config);
  const sourceErrors = sourceDirty ? validateSourceDraft(sourceDraft) : {};
  const visible = <T extends object>(errors: T): T => (showErrors ? errors : ({} as T));

  const test = useMutation<ProbeResult, Error, number>({ mutationFn: (id) => testSource(id) });

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: queryKeys.devices });
    void queryClient.invalidateQueries({ queryKey: queryKeys.sources });
  };

  const save = async () => {
    if (hasErrors(deviceErrors) || hasErrors(configErrors) || hasErrors(sourceErrors)) {
      setShowErrors(true);
      setTab(hasErrors(deviceErrors) ? "basic" : hasErrors(configErrors) ? "thresholds" : "source");
      return;
    }
    setSaving(true);
    setSaveError(null);
    let deviceSaved = false;
    if (Object.keys(devicePatch).length > 0) {
      try {
        setBaseDevice(await updateDevice(baseDevice.id, devicePatch));
        deviceSaved = true;
      } catch (error) {
        invalidate();
        if (!isConfigSavedRuntimeError(error)) {
          setSaving(false);
          setSaveError({ title: "设备保存失败", message: errorText(error) });
          return;
        }
        // 已保存：以草稿为新基线，避免重试时重复提交。
        setBaseDevice({ ...baseDevice, ...devicePatch } as Device);
        deviceSaved = true;
      }
    }
    if (sourceDirty) {
      try {
        const saved = baseSource
          ? await updateSource(baseSource.id, sourcePatch ?? {})
          : await createSource(baseDevice.id, buildSourceCreate(sourceDraft));
        setBaseSource(saved);
        setSourceDraft(sourceDraftFrom(saved));
        setAddingSource(false);
      } catch (error) {
        invalidate();
        if (!isConfigSavedRuntimeError(error)) {
          setSaving(false);
          setSaveError({
            title: deviceSaved ? "设备信息已保存，但数据源保存失败" : "数据源保存失败",
            message: errorText(error),
          });
          setTab("source");
          return;
        }
      }
    }
    invalidate();
    setSaving(false);
    toast.show({ tone: "ok", title: "已保存" });
    onClose();
  };

  const changeProtocol = (protocol: SourceProtocol) =>
    setSourceDraft(switchProtocol(sourceDraft, protocol, profiles));
  const oldSecretSet = baseSource !== undefined && secretIsSet(baseSource, baseSource.protocol);
  const protocolChanged = baseSource !== undefined && baseSource.protocol !== sourceDraft.protocol;

  return (
    <>
      <Sheet
        open
        onClose={onClose}
        title={`编辑「${baseDevice.name}」`}
        width="min(760px, calc(100vw - 32px))"
        dismissible={!saving}
        trailing={
          <Button
            variant="primary"
            size="sm"
            disabled={!dirty || saving}
            aria-busy={saving}
            onClick={() => void save()}
          >
            {saving ? "正在保存…" : "保存"}
          </Button>
        }
      >
        <div className="tab-bar" style={{ marginBottom: 16 }}>
          <Segmented<Tab>
            aria-label="编辑视图"
            value={tab}
            onChange={setTab}
            options={[
              { value: "basic", label: "基本信息" },
              { value: "thresholds", label: "阈值与去抖" },
              { value: "source", label: "数据源" },
            ]}
          />
        </div>
        {saveError ? (
          <Banner tone="critical" role="alert" title={saveError.title} style={{ marginBottom: 16 }}>
            {saveError.message}
          </Banner>
        ) : null}

        {tab === "basic" ? (
          <div className="form-grid">
            <GridField label="名称" htmlFor={`${ids}-name`} error={visible(deviceErrors).name}>
              <Input
                id={`${ids}-name`}
                value={draft.name}
                invalid={Boolean(visible(deviceErrors).name)}
                onChange={(e) => setDraft({ ...draft, name: e.target.value })}
              />
            </GridField>
            <GridField label="位置" htmlFor={`${ids}-loc`} error={visible(deviceErrors).location}>
              <Input
                id={`${ids}-loc`}
                value={draft.location}
                placeholder="可选"
                onChange={(e) => setDraft({ ...draft, location: e.target.value })}
              />
            </GridField>
            <GridField label="标签" error={visible(deviceErrors).tags}>
              <TokenInput
                aria-label="标签"
                addLabel="添加标签"
                values={draft.tags}
                max={32}
                validate={validateTag}
                onChange={(tags) => setDraft({ ...draft, tags })}
              />
            </GridField>
            <GridField label="备注" htmlFor={`${ids}-note`} error={visible(deviceErrors).note}>
              <Textarea
                id={`${ids}-note`}
                rows={3}
                value={draft.note}
                onChange={(e) => setDraft({ ...draft, note: e.target.value })}
              />
            </GridField>
          </div>
        ) : null}

        {tab === "thresholds" ? (
          <>
            <Banner tone="neutral" icon="info" style={{ marginBottom: 16 }}>
              阈值与去抖作为该设备的完整配置整体保存；修改「设置 ›
              设备默认值」不会影响已保存的设备。
            </Banner>
            <ThresholdFields
              config={draft.config}
              onChange={(config) => setDraft({ ...draft, config })}
              errors={visible(configErrors)}
            />
          </>
        ) : null}

        {tab === "source" ? (
          baseSource || addingSource ? (
            <>
              <div className="list-header">协议</div>
              <Segmented<SourceProtocol>
                aria-label="协议"
                value={sourceDraft.protocol}
                onChange={changeProtocol}
                options={PROTOCOLS.map((p) => ({ value: p.value, label: p.label }))}
              />
              {protocolChanged ? (
                <Banner tone="warning" className="mt-3">
                  切换协议会清除原协议的参数
                  {oldSecretSet ? `与已保存的${protocolMeta(baseSource.protocol).secretLabel}` : ""}
                  ，保存后按新协议采集。
                </Banner>
              ) : null}
              <div className="mt-4">
                <SourceFields
                  draft={sourceDraft}
                  onChange={setSourceDraft}
                  profiles={profiles}
                  errors={visible(sourceErrors)}
                  saved={baseSource}
                />
              </div>
              <div className="mt-4">
                <SourceEnabledRow
                  enabled={sourceDraft.enabled}
                  onChange={(enabled) => setSourceDraft({ ...sourceDraft, enabled })}
                />
              </div>
              {baseSource ? (
                <>
                  <div className="hstack wrap mt-4">
                    <Button
                      variant="tinted"
                      icon="refresh"
                      disabled={sourceDirty || test.isPending}
                      onClick={() => test.mutate(baseSource.id)}
                    >
                      {test.isPending ? "正在测试…" : "只读连接测试"}
                    </Button>
                    <Button
                      variant="danger-tinted"
                      icon="trash"
                      onClick={() => setConfirmDeleteSource(true)}
                    >
                      删除数据源
                    </Button>
                    <Link className="btn btn-plain" to={`/devices/${baseDevice.id}?tab=sources`}>
                      查看诊断
                    </Link>
                  </div>
                  {sourceDirty ? (
                    <p className="list-caption">连接测试使用已保存的配置，请先保存修改。</p>
                  ) : null}
                  {test.isError ? (
                    <Banner tone="critical" role="alert" title="连接测试未成功" className="mt-3">
                      {test.error.message}
                    </Banner>
                  ) : null}
                  {test.isSuccess ? (
                    <Banner tone="info" role="status" icon="check-circle" className="mt-3">
                      连接测试成功 · 延迟 {test.data.latency_ms} ms
                      {test.data.device_info?.model ? ` · ${test.data.device_info.model}` : ""}
                    </Banner>
                  ) : null}
                </>
              ) : (
                <div className="hstack mt-4">
                  <Button variant="plain" onClick={() => setAddingSource(false)}>
                    取消添加
                  </Button>
                </div>
              )}
            </>
          ) : (
            <Empty
              icon="antenna"
              tone="info"
              title="该设备还没有数据源"
              action={
                <Button variant="tinted" icon="plus" onClick={() => setAddingSource(true)}>
                  添加数据源
                </Button>
              }
            >
              添加数据源后才会采集数据。
            </Empty>
          )
        ) : null}
      </Sheet>
      <DestructiveDialog
        open={confirmDeleteSource && baseSource !== undefined}
        onClose={() => setConfirmDeleteSource(false)}
        title="删除数据源？"
        message="删除后设备停止采集；设备、历史与事件保留。"
        onConfirm={async () => {
          if (!baseSource) return;
          await deleteSource(baseSource.id);
          invalidate();
          setBaseSource(undefined);
          setSourceDraft(emptySourceDraft("snmp", profiles));
        }}
      />
    </>
  );
}

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : "操作失败，请重试";
}
