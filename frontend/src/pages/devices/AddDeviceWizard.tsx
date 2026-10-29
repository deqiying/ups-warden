import { useMutation, useQueryClient, type UseMutationResult } from "@tanstack/react-query";
import { useId, useState, type CSSProperties } from "react";
import { isApiError, isConfigSavedRuntimeError } from "../../api/client.ts";
import { createDevice, createSource, deleteDevice, testSourceDraft } from "../../api/devices.ts";
import { queryKeys, useProfiles, useSettings } from "../../api/hooks.ts";
import type { Device, DeviceConfig, ProbeResult, SourceInput } from "../../api/types.ts";
import { Banner } from "../../components/Banner.tsx";
import { Button } from "../../components/Button.tsx";
import { DestructiveDialog } from "../../components/DestructiveDialog.tsx";
import { Empty } from "../../components/Empty.tsx";
import { Input } from "../../components/Input.tsx";
import { Select } from "../../components/Select.tsx";
import { Sheet } from "../../components/Sheet.tsx";
import { Spinner } from "../../components/Spinner.tsx";
import { TokenInput } from "../../components/TokenInput.tsx";
import { useToast } from "../../components/toast-context.ts";
import { Icon } from "../../icons/Icon.tsx";
import { protocolLabel } from "../../model/device.ts";
import {
  GridField,
  ProbeResultView,
  ReadOnlyBanner,
  SourceFields,
  ThresholdFields,
} from "./forms.tsx";
import {
  PROTOCOLS,
  buildDeviceCreate,
  buildSourceCreate,
  buildSourceTest,
  defaultProfile,
  emptySourceDraft,
  hasErrors,
  profilesFor,
  switchProtocol,
  validateConfig,
  validateDeviceDraft,
  validateSourceDraft,
  validateTag,
  type DeviceDraft,
  type SourceDraft,
} from "./model.ts";

const STEPS = ["设备与协议", "连接", "测试与 Profile", "阈值与去抖"] as const;

type SaveFailure = { stage: "device" | "source"; error: unknown };

/** 第 1 步的设备基本信息；阈值（config）单独保存在第 4 步。 */
type BasicDraft = Omit<DeviceDraft, "config">;

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : "操作失败，请重试";
}

export interface AddDeviceWizardProps {
  onClose: () => void;
  /** 设备与数据源均已保存。 */
  onCreated: (device: Device) => void;
}

/**
 * 添加设备向导（F3-2，devices.html proto / test / pwd / thresholds）。
 * 保存依次调用 `POST /devices`、`POST /devices/{id}/sources`；第二步失败时保留已创建的设备，
 * 允许重试保存数据源或由用户选择删除，不静默删除（Q-C2）。
 */
export function AddDeviceWizard({ onClose, onCreated }: AddDeviceWizardProps) {
  const ids = useId();
  const queryClient = useQueryClient();
  const toast = useToast();
  const profiles = useProfiles();
  const settings = useSettings();

  const [step, setStep] = useState(0);
  const [device, setDevice] = useState<BasicDraft>({ name: "", note: "", location: "", tags: [] });
  // 进入第 4 步时从 device_defaults 预填；之前为 null。
  const [config, setConfig] = useState<DeviceConfig | null>(null);
  const [source, setSource] = useState<SourceDraft>(() => emptySourceDraft("snmp"));
  const [showErrors, setShowErrors] = useState(false);
  const [testSkipped, setTestSkipped] = useState(false);
  const [created, setCreated] = useState<Device | null>(null);
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState<SaveFailure | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const test = useMutation<ProbeResult, Error, SourceInput>({
    mutationFn: (input) => testSourceDraft(input),
  });

  const deviceErrors = validateDeviceDraft({ ...device, config: config ?? ({} as DeviceConfig) });
  const sourceErrors = validateSourceDraft(source);
  const configErrors = config ? validateConfig(config) : {};

  /** 协议列表加载后为草稿补上默认 Profile。 */
  const withProfile = (draft: SourceDraft): SourceDraft => {
    if (draft.profileKey) return draft;
    const p = defaultProfile(profiles.data, draft.protocol);
    return p ? { ...draft, profileKey: p.key, profileVersion: p.version } : draft;
  };

  const runTest = (draft: SourceDraft) => {
    setTestSkipped(false);
    test.mutate(buildSourceTest(draft));
  };

  const testedProfile = test.variables?.profile_key ?? source.profileKey;
  const testOk = test.isSuccess && testedProfile === source.profileKey;

  const goNext = () => {
    if (step === 0) {
      if (hasErrors(deviceErrors)) {
        setShowErrors(true);
        return;
      }
      setShowErrors(false);
      setSource(withProfile(source));
      setStep(1);
      return;
    }
    if (step === 1) {
      const next = withProfile(source);
      if (hasErrors(validateSourceDraft(next))) {
        setSource(next);
        setShowErrors(true);
        return;
      }
      setShowErrors(false);
      setSource(next);
      setStep(2);
      runTest(next);
      return;
    }
    if (step === 2) {
      if (!config && settings.data) setConfig({ ...settings.data.device_defaults });
      setStep(3);
    }
  };

  const save = async () => {
    if (!created && (!config || hasErrors(configErrors))) {
      setShowErrors(true);
      return;
    }
    setSaving(true);
    setFailure(null);
    let saved = created;
    if (!saved) {
      try {
        saved = await createDevice(buildDeviceCreate({ ...device, config: config! }));
        setCreated(saved);
      } catch (error) {
        // 503：设备已写入但运行恢复失败，响应中没有设备 id，无法继续保存数据源。
        void queryClient.invalidateQueries({ queryKey: queryKeys.devices });
        setFailure({ stage: "device", error });
        setSaving(false);
        return;
      }
    }
    try {
      await createSource(saved.id, buildSourceCreate(source));
    } catch (error) {
      // 503：数据源已保存，只是采集恢复失败（全局已提示），按已保存处理。
      if (!isConfigSavedRuntimeError(error)) {
        void queryClient.invalidateQueries({ queryKey: queryKeys.devices });
        setFailure({ stage: "source", error });
        setSaving(false);
        return;
      }
    }
    void queryClient.invalidateQueries({ queryKey: queryKeys.devices });
    void queryClient.invalidateQueries({ queryKey: queryKeys.sources });
    setSaving(false);
    toast.show({ tone: "ok", title: `已添加「${saved.name}」`, message: "采集将按配置开始。" });
    onCreated(saved);
  };

  const close = () => {
    if (created) {
      toast.show({
        tone: "info",
        title: `设备「${created.name}」已保留`,
        message: "尚未配置数据源，可在设备列表中编辑后添加。",
      });
      void queryClient.invalidateQueries({ queryKey: queryKeys.devices });
    }
    onClose();
  };

  // 设备已创建后基本信息与阈值不再随向导保存，只能回到连接步骤修正数据源。
  const minStep = created ? 1 : 0;
  const primary = (() => {
    if (step === 0) return { label: "下一步", disabled: false, onClick: goNext };
    if (step === 1) return { label: "测试连接", disabled: false, onClick: goNext };
    if (step === 2) {
      if (testOk) return { label: `使用 ${source.profileKey}`, disabled: false, onClick: goNext };
      return {
        label: "下一步",
        disabled: !testSkipped || (!settings.data && !config),
        onClick: goNext,
      };
    }
    return {
      label: saving ? "正在保存…" : failure?.stage === "source" ? "重试保存数据源" : "添加设备",
      disabled: saving || (!config && !created),
      onClick: () => void save(),
    };
  })();

  return (
    <>
      <Sheet
        open
        onClose={close}
        title="添加设备"
        width="min(760px, calc(100vw - 32px))"
        dismissible={!saving}
        bare
        trailing={
          <>
            {step > minStep ? (
              <Button variant="plain" disabled={saving} onClick={() => setStep(step - 1)}>
                上一步
              </Button>
            ) : null}
            <Button
              variant="primary"
              size="sm"
              disabled={primary.disabled}
              aria-busy={saving}
              onClick={primary.onClick}
            >
              {primary.label}
            </Button>
          </>
        }
      >
        <ol className="steps" aria-label="添加步骤">
          {STEPS.map((label, index) => (
            <li
              key={label}
              className={index < step ? "step done" : index === step ? "step on" : "step"}
              aria-current={index === step ? "step" : undefined}
            >
              {label}
            </li>
          ))}
        </ol>
        <div className="sheet-body">
          {step === 0 ? (
            <StepDevice
              ids={ids}
              device={device}
              onDevice={setDevice}
              protocol={source.protocol}
              onProtocol={(p) => setSource(switchProtocol(source, p, profiles.data))}
              errors={showErrors ? deviceErrors : {}}
            />
          ) : null}
          {step === 1 ? (
            <>
              {created ? (
                <Banner tone="neutral" style={{ marginBottom: 16 }}>
                  设备「{created.name}」已创建，这里只修改数据源连接。
                </Banner>
              ) : null}
              <div className="list-header">
                {protocolLabel({ protocol: source.protocol, params: null })} 连接
              </div>
              <SourceFields
                draft={source}
                onChange={setSource}
                profiles={profiles.data}
                errors={showErrors ? sourceErrors : {}}
                showProfile={false}
              />
              <p className="list-caption">
                下一步会用默认 Profile「{source.profileKey || "—"}」做一次只读连接测试，并给出推荐的
                Profile。同一 IP 的采集与连接测试串行执行。
              </p>
            </>
          ) : null}
          {step === 2 ? (
            <StepTest
              source={source}
              onSource={setSource}
              profiles={profilesFor(profiles.data, source.protocol)}
              test={test}
              testOk={testOk}
              skipped={testSkipped}
              onRetest={runTest}
              onSkip={() => setTestSkipped(true)}
              onBack={() => setStep(1)}
            />
          ) : null}
          {step === 3 ? (
            <>
              {failure ? (
                <SaveFailureBanner
                  failure={failure}
                  created={created}
                  onDelete={() => setConfirmDelete(true)}
                  onKeep={close}
                />
              ) : null}
              {created ? (
                <Empty icon="check-circle" tone="ok" title={`设备「${created.name}」已创建`}>
                  阈值与去抖已随设备保存；修正连接后点击「重试保存数据源」。
                </Empty>
              ) : !config ? (
                settings.isError ? (
                  <Banner
                    tone="critical"
                    role="alert"
                    title="设备默认值加载失败"
                    actions={
                      <Button size="sm" onClick={() => void settings.refetch()}>
                        重试
                      </Button>
                    }
                  >
                    {settings.error.message}
                  </Banner>
                ) : (
                  <Empty title="正在读取设备默认值…">
                    <Spinner />
                  </Empty>
                )
              ) : (
                <>
                  <Banner tone="neutral" icon="info">
                    默认值来自「设置 ›
                    设备默认值」。保存后作为该设备的完整配置，之后修改系统默认值不会影响此设备。
                  </Banner>
                  <div className="mt-4">
                    <ThresholdFields
                      config={config}
                      onChange={setConfig}
                      errors={showErrors ? configErrors : {}}
                    />
                  </div>
                </>
              )}
            </>
          ) : null}
        </div>
      </Sheet>
      <DestructiveDialog
        open={confirmDelete && created !== null}
        onClose={() => setConfirmDelete(false)}
        title={`删除「${created?.name ?? ""}」？`}
        message="设备尚未配置数据源，删除后不可撤销。"
        onConfirm={async () => {
          if (!created) return;
          await deleteDevice(created.id);
          void queryClient.invalidateQueries({ queryKey: queryKeys.devices });
          onClose();
        }}
      />
    </>
  );
}

function StepDevice({
  ids,
  device,
  onDevice,
  protocol,
  onProtocol,
  errors,
}: {
  ids: string;
  device: BasicDraft;
  onDevice: (next: BasicDraft) => void;
  protocol: SourceDraft["protocol"];
  onProtocol: (next: SourceDraft["protocol"]) => void;
  errors: ReturnType<typeof validateDeviceDraft>;
}) {
  return (
    <>
      <div className="form-grid">
        <GridField label="名称" htmlFor={`${ids}-name`} error={errors.name}>
          <Input
            id={`${ids}-name`}
            value={device.name}
            invalid={Boolean(errors.name)}
            placeholder="如：机房 UPS-A"
            onChange={(e) => onDevice({ ...device, name: e.target.value })}
          />
        </GridField>
        <GridField label="位置" htmlFor={`${ids}-loc`} error={errors.location}>
          <Input
            id={`${ids}-loc`}
            value={device.location}
            placeholder="可选"
            onChange={(e) => onDevice({ ...device, location: e.target.value })}
          />
        </GridField>
        <GridField label="标签" error={errors.tags} hint="用于规则按标签选择设备">
          <TokenInput
            aria-label="标签"
            addLabel="添加标签"
            values={device.tags}
            max={32}
            validate={validateTag}
            onChange={(tags) => onDevice({ ...device, tags })}
          />
        </GridField>
      </div>
      <div className="list-header mt-6" id={`${ids}-proto`}>
        协议
      </div>
      <div className="proto-grid" role="radiogroup" aria-labelledby={`${ids}-proto`}>
        {PROTOCOLS.map((p) => {
          const on = p.value === protocol;
          return (
            <button
              key={p.value}
              type="button"
              role="radio"
              aria-checked={on}
              className={on ? "proto is-on" : "proto"}
              onClick={() => onProtocol(p.value)}
            >
              <span className={on ? "check is-on" : "check"} aria-hidden="true">
                {on ? <Icon name="check" /> : null}
              </span>
              <span className="row-icon" style={{ "--c": p.color } as CSSProperties}>
                <Icon name={p.icon} />
              </span>
              <h4>{p.label}</h4>
              <p>{p.description}</p>
            </button>
          );
        })}
      </div>
      <div className="mt-4">
        <ReadOnlyBanner />
      </div>
    </>
  );
}

function StepTest({
  source,
  onSource,
  profiles,
  test,
  testOk,
  skipped,
  onRetest,
  onSkip,
  onBack,
}: {
  source: SourceDraft;
  onSource: (next: SourceDraft) => void;
  profiles: ReturnType<typeof profilesFor>;
  test: UseMutationResult<ProbeResult, Error, SourceInput>;
  testOk: boolean;
  skipped: boolean;
  onRetest: (draft: SourceDraft) => void;
  onSkip: () => void;
  onBack: () => void;
}) {
  const ids = useId();
  const address = `${source.host.trim()}:${source.port}`;
  const label = protocolLabel({
    protocol: source.protocol,
    params: { version: source.snmpVersion },
  });
  const chooseProfile = (key: string) => {
    const p = profiles.find((c) => c.key === key);
    if (!p || key === source.profileKey) return;
    const next = { ...source, profileKey: p.key, profileVersion: p.version };
    onSource(next);
    // 读数预览依赖 Profile 映射，换 Profile 后重新测试。
    onRetest(next);
  };

  if (test.isPending) {
    return (
      <div className="test-result is-pending" role="status">
        <Spinner label="正在测试" />
        <div style={{ flex: 1 }}>
          <div className="t-headline">正在只读连接…</div>
          <div className="t-subhead muted">
            {label} · <span className="mono">{address}</span> · Profile {source.profileKey}
          </div>
        </div>
      </div>
    );
  }

  if (test.isError) {
    const passwordWrong = isApiError(test.error) && test.error.code === "source_password_wrong";
    return (
      <>
        <div className="test-result is-fail" role="alert">
          <span className="sev">
            <Icon name={passwordWrong ? "key" : "x"} />
          </span>
          <div style={{ flex: 1 }}>
            <div className="t-headline">{test.error.message}</div>
            <div className="t-subhead muted">
              {label} · <span className="mono">{address}</span>
              {passwordWrong
                ? " 拒绝了请求。请核对网卡「集成设置」中的访问密码。"
                : " · 请检查地址、端口与凭据，或稍后重试。"}
            </div>
          </div>
        </div>
        {passwordWrong ? (
          <div className="form-grid mt-6">
            <GridField
              label="密码"
              htmlFor={`${ids}-pwd`}
              hint="密码加密保存，之后只显示「已设置」。留空表示设备未设置访问密码。"
            >
              <Input
                id={`${ids}-pwd`}
                type="password"
                className="field-mid"
                autoComplete="new-password"
                invalid
                value={source.secret}
                onChange={(e) => onSource({ ...source, secret: e.target.value })}
              />
            </GridField>
          </div>
        ) : null}
        <div className="hstack wrap mt-6" style={{ justifyContent: "flex-end" }}>
          <Button variant="plain" onClick={onBack}>
            返回修改连接
          </Button>
          {!passwordWrong ? (
            <Button variant="gray" disabled={skipped} onClick={onSkip}>
              {skipped ? "已跳过测试" : "跳过测试"}
            </Button>
          ) : null}
          <Button variant="tinted" icon="refresh" onClick={() => onRetest(source)}>
            重新测试
          </Button>
        </div>
        {skipped ? (
          <p className="list-caption">
            已跳过连接测试：保存后将按配置开始采集，可在设备详情「数据源与诊断」中再次测试。
          </p>
        ) : null}
      </>
    );
  }

  if (!test.isSuccess) return null;
  const result = test.data;
  const recommendations = result.recommendations ?? [];
  return (
    <>
      <div className="test-result" role="status">
        <span className="sev">
          <Icon name="check" />
        </span>
        <div style={{ flex: 1 }}>
          <div className="t-headline">已连接到 {result.device_info?.model || "设备"}</div>
          <div className="t-subhead muted">
            {label} · <span className="mono">{address}</span> · 延迟 {result.latency_ms} ms
          </div>
        </div>
        <Button size="sm" icon="refresh" onClick={() => onRetest(source)}>
          重新测试
        </Button>
      </div>
      {!testOk ? (
        <Banner tone="warning" className="mt-4">
          当前选择的 Profile 与测试时不同，请重新测试。
        </Banner>
      ) : null}
      <ProbeResultView
        result={result}
        testedProfile={test.variables?.profile_key ?? source.profileKey}
        recommendations={
          <>
            <div className="list-header">推荐的 Profile</div>
            <div className="list" role="radiogroup" aria-label="推荐的 Profile">
              {recommendations.map((r) => {
                const on = r.key === source.profileKey;
                return (
                  <button
                    key={r.key}
                    type="button"
                    role="radio"
                    aria-checked={on}
                    className={on ? "row is-link is-selected" : "row is-link"}
                    style={{ width: "100%", border: 0, textAlign: "left", font: "inherit" }}
                    onClick={() => chooseProfile(r.key)}
                  >
                    <span className={on ? "check is-on" : "check"} aria-hidden="true">
                      {on ? <Icon name="check" /> : null}
                    </span>
                    <span className="row-main">
                      <span className={on ? "row-title strong" : "row-title"}>{r.key}</span>
                      <span className="row-sub">{r.reason}</span>
                    </span>
                    <span className="score">{r.score}</span>
                  </button>
                );
              })}
              {recommendations.length === 0 ? (
                <div className="row">
                  <span className="row-main muted">没有推荐，请手动选择</span>
                </div>
              ) : null}
            </div>
            <div className="hstack gap-3 mt-3">
              <label className="t-footnote muted" htmlFor={`${ids}-other`}>
                其他 Profile
              </label>
              <Select
                id={`${ids}-other`}
                value={source.profileKey}
                onChange={chooseProfile}
                options={profiles.map((p) => ({
                  value: p.key,
                  label: `${p.key} · v${p.version}`,
                }))}
              />
            </div>
          </>
        }
      />
    </>
  );
}

function SaveFailureBanner({
  failure,
  created,
  onDelete,
  onKeep,
}: {
  failure: SaveFailure;
  created: Device | null;
  onDelete: () => void;
  onKeep: () => void;
}) {
  if (failure.stage === "device") {
    return (
      <Banner tone="critical" role="alert" title="设备保存失败" style={{ marginBottom: 16 }}>
        {errorText(failure.error)}
        {isConfigSavedRuntimeError(failure.error)
          ? " 设备可能已写入，请关闭向导后在设备列表中检查，避免重复添加。"
          : ""}
      </Banner>
    );
  }
  return (
    <Banner
      tone="critical"
      role="alert"
      style={{ marginBottom: 16 }}
      title={`设备「${created?.name ?? ""}」已创建，但数据源保存失败`}
      actions={
        <>
          <Button size="sm" variant="danger-tinted" onClick={onDelete}>
            删除该设备
          </Button>
          <Button size="sm" onClick={onKeep}>
            稍后配置
          </Button>
        </>
      }
    >
      {errorText(failure.error)}
      。可以返回上一步修正连接后重试，也可以保留设备稍后在编辑中添加数据源。
    </Banner>
  );
}
