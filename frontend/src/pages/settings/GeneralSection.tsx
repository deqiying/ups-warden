import { useMemo, useState } from "react";
import { buildGeneralPatch, type SettingsPatch } from "../../api/settings.ts";
import type { Settings } from "../../api/types.ts";
import {
  Banner,
  FormRow,
  List,
  ListCaption,
  ListHeader,
  Segmented,
  Select,
  useToast,
  type SegmentedOption,
} from "../../components/index.ts";
import { useSaveSettings } from "./hooks.ts";
import { settingsFailureText, timeZoneOptions } from "./model.ts";
import { PaneHead } from "./shared.tsx";
import { readTheme, saveTheme, THEME_OPTIONS, type ThemePreference } from "./theme.ts";

const LANGUAGE_OPTIONS: SegmentedOption<Settings["language"]>[] = [
  { value: "zh-CN", label: "简体中文" },
  { value: "en-US", label: "English" },
];

/** 外观缩略图：浅色与深色两组图形，「跟随系统」为两者对角拼接（效果图 ap-lt / ap-dk）。 */
function ThemeThumb({ value }: { value: ThemePreference }) {
  return (
    <svg viewBox="0 0 96 62" aria-hidden="true">
      {value === "dark" ? <use href="#ap-dk" /> : <use href="#ap-lt" />}
      {value === "system" ? (
        <g clipPath="url(#ap-cut)">
          <use href="#ap-dk" />
        </g>
      ) : null}
    </svg>
  );
}

function ThemeThumbDefs() {
  return (
    <svg width="0" height="0" style={{ position: "absolute" }} aria-hidden="true">
      <defs>
        <g id="ap-lt">
          <rect width="96" height="62" fill="#dfe3ea" />
          <rect x="8" y="8" width="80" height="48" rx="5" fill="#fff" />
          <path d="M13 8h19v48H13a5 5 0 0 1-5-5V13a5 5 0 0 1 5-5z" fill="#eeeef3" />
          <rect x="12" y="14" width="16" height="4" rx="2" fill="#007aff" />
          <rect x="12" y="21" width="13" height="3" rx="1.5" fill="#c7c7cc" />
          <rect x="12" y="27" width="15" height="3" rx="1.5" fill="#c7c7cc" />
          <rect x="38" y="14" width="28" height="4" rx="2" fill="#1d1d1f" />
          <rect x="38" y="23" width="44" height="13" rx="3" fill="#f2f2f7" />
          <circle cx="44" cy="29.5" r="2.4" fill="#34c759" />
          <rect x="49" y="28" width="22" height="3" rx="1.5" fill="#c7c7cc" />
          <rect x="38" y="39" width="44" height="11" rx="3" fill="#f2f2f7" />
        </g>
        <g id="ap-dk">
          <rect width="96" height="62" fill="#3a3f4b" />
          <rect x="8" y="8" width="80" height="48" rx="5" fill="#1c1c1e" />
          <path d="M13 8h19v48H13a5 5 0 0 1-5-5V13a5 5 0 0 1 5-5z" fill="#2a2a2e" />
          <rect x="12" y="14" width="16" height="4" rx="2" fill="#0a84ff" />
          <rect x="12" y="21" width="13" height="3" rx="1.5" fill="#48484a" />
          <rect x="12" y="27" width="15" height="3" rx="1.5" fill="#48484a" />
          <rect x="38" y="14" width="28" height="4" rx="2" fill="#f5f5f7" />
          <rect x="38" y="23" width="44" height="13" rx="3" fill="#2c2c2e" />
          <circle cx="44" cy="29.5" r="2.4" fill="#30d158" />
          <rect x="49" y="28" width="22" height="3" rx="1.5" fill="#48484a" />
          <rect x="38" y="39" width="44" height="11" rx="3" fill="#2c2c2e" />
        </g>
        <clipPath id="ap-cut">
          <path d="M58 0H96V62H38z" />
        </clipPath>
      </defs>
    </svg>
  );
}

/**
 * 通用（F8-1）：时区、语言选中即保存，每次 PATCH 只提交这一个字段；
 * 「外观」是本机偏好，只写 localStorage，不请求后端。
 */
export function GeneralSection({ settings }: { settings: Settings }) {
  const save = useSaveSettings();
  const toast = useToast();
  const [failure, setFailure] = useState<string | null>(null);
  const [theme, setTheme] = useState<ThemePreference>(readTheme);
  const [themeUnsaved, setThemeUnsaved] = useState(false);

  // 保存中先显示用户选择的值；失败后回到服务端值（受控），并显示错误。
  const pending = save.isPending ? save.variables : undefined;
  const timezone = pending?.timezone ?? settings.timezone;
  const language = pending?.language ?? settings.language;
  const zoneOptions = useMemo(() => timeZoneOptions(settings.timezone), [settings.timezone]);

  const commit = (patch: SettingsPatch, title: string) => {
    setFailure(null);
    save.mutate(patch, {
      onSuccess: () => toast.show({ tone: "ok", title, duration: 2500 }),
      onError: (err) => setFailure(settingsFailureText(err)),
    });
  };

  const chooseTheme = (next: ThemePreference) => {
    setTheme(next);
    setThemeUnsaved(!saveTheme(next));
  };

  return (
    <section aria-label="通用">
      <PaneHead title="通用" />
      {failure ? (
        <Banner tone="critical" role="alert" title="保存失败" className="set-mb">
          {failure}
        </Banner>
      ) : null}
      <ListHeader>地区与语言</ListHeader>
      <List>
        <FormRow
          label="时区"
          htmlFor="settings-timezone"
          description="网页上的时间按此时区显示。通知消息中的时间使用服务器所在时区。"
        >
          <Select
            id="settings-timezone"
            className="set-tz"
            options={zoneOptions}
            value={timezone}
            disabled={save.isPending}
            onChange={(value) => commit(buildGeneralPatch("timezone", value), "时区已保存")}
          />
        </FormRow>
        <FormRow label="语言" description="网页界面语言；通知内容由各模板决定。">
          <Segmented
            aria-label="语言"
            options={LANGUAGE_OPTIONS}
            value={language}
            disabled={save.isPending}
            onChange={(value) => commit(buildGeneralPatch("language", value), "语言已保存")}
          />
        </FormRow>
      </List>
      <ListCaption>
        更改后立即保存并生效。当前界面只提供简体中文，选择 English
        仅保存偏好，供之后的多语言界面使用。
      </ListCaption>

      <div className="list-header mt-6">外观</div>
      <List>
        <ThemeThumbDefs />
        <div className="appear" role="group" aria-label="外观">
          {THEME_OPTIONS.map((option) => (
            <button
              key={option.value}
              type="button"
              className="appear-opt"
              aria-pressed={theme === option.value}
              onClick={() => chooseTheme(option.value)}
            >
              <ThemeThumb value={option.value} />
              <span>{option.label}</span>
            </button>
          ))}
        </div>
      </List>
      <ListCaption>外观只保存在这台设备的浏览器中，不影响其他管理员。</ListCaption>
      {themeUnsaved ? (
        <p className="list-caption ink-red" role="alert">
          浏览器不允许保存偏好，主题仅在本次打开期间有效。
        </p>
      ) : null}
    </section>
  );
}
