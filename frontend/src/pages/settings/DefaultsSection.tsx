import { useState } from "react";
import { changedFields } from "../../api/patch.ts";
import { BUILTIN_DEVICE_DEFAULTS, buildDefaultsPatch } from "../../api/settings.ts";
import type { DeviceConfig, Settings } from "../../api/types.ts";
import { Banner, Button, useToast } from "../../components/index.ts";
import { ThresholdFields } from "../devices/forms.tsx";
import { hasErrors, validateConfig } from "../devices/model.ts";
import { useSaveSettings } from "./hooks.ts";
import { settingsFailureText } from "./model.ts";
import { PaneHead, SaveBar } from "./shared.tsx";

/**
 * 设备默认值（F8-2）：与设备 `config` 相同的对象，复用设备编辑里的阈值表单与校验。
 * `device_defaults` 整体替换，因此保存总是提交完整对象；它只影响之后新建的设备，
 * 已有设备各自保存了完整配置，不会随之改变（settings-api.md「生效范围」）。
 */
export function DefaultsSection({ settings }: { settings: Settings }) {
  const save = useSaveSettings();
  const toast = useToast();
  const base = settings.device_defaults;
  const [edits, setEdits] = useState<DeviceConfig | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const draft = edits ?? base;

  const errors = validateConfig(draft);
  const changedKeys = Object.keys(changedFields(base, draft));
  const dirty = changedKeys.length > 0;
  const differsFromBuiltin =
    Object.keys(changedFields(BUILTIN_DEVICE_DEFAULTS as DeviceConfig, draft)).length > 0;

  const submit = () => {
    if (!dirty || hasErrors(errors)) return;
    setFailure(null);
    save.mutate(buildDefaultsPatch(draft), {
      onSuccess: () => {
        setEdits(null);
        toast.show({ tone: "ok", title: "设备默认值已保存", duration: 3000 });
      },
      onError: (err) => setFailure(settingsFailureText(err)),
    });
  };

  return (
    <section aria-label="设备默认值">
      <PaneHead title="设备默认值" />
      <Banner tone="neutral" className="set-mb">
        这些值只用于之后新建的设备，或之后修改设备配置时省略的字段。现有设备（包括已停用的）各自保存了完整配置，不受影响；如需调整，请到设备的「阈值与去抖」中修改。
      </Banner>
      {failure ? (
        <Banner tone="critical" role="alert" title="保存失败" className="set-mb">
          {failure}
        </Banner>
      ) : null}

      <ThresholdFields config={draft} errors={errors} onChange={setEdits} />
      <p className="list-caption">无效或过期的读数不会触发告警，也不会用来解除告警。</p>

      <SaveBar
        dirty={dirty}
        valid={!hasErrors(errors)}
        saving={save.isPending}
        summary={`${changedKeys.length} 项默认值尚未保存`}
        onRevert={() => {
          setEdits(null);
          setFailure(null);
        }}
        onSave={submit}
        extra={
          <Button
            variant="plain"
            size="sm"
            disabled={save.isPending || !differsFromBuiltin}
            icon="undo"
            onClick={() => setEdits({ ...BUILTIN_DEVICE_DEFAULTS })}
          >
            恢复内置默认值
          </Button>
        }
      />
    </section>
  );
}
