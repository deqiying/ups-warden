import { useState } from "react";
import {
  buildRetentionPatch,
  RETENTION_LIMITS,
  retentionOf,
  shortenedRetention,
  validateRetention,
  type RetentionDraft,
  type RetentionField,
} from "../../api/settings.ts";
import type { Settings } from "../../api/types.ts";
import {
  Banner,
  Button,
  Dialog,
  FormRow,
  List,
  ListCaption,
  Stepper,
  useToast,
} from "../../components/index.ts";
import { useSaveSettings } from "./hooks.ts";
import { retentionPosition, settingsFailureText } from "./model.ts";
import { FieldError, PaneHead, SaveBar } from "./shared.tsx";

interface RetentionRow {
  field: RetentionField;
  name: string;
  description: string;
  color: string;
  min: number;
  max: number;
}

const ROWS: readonly RetentionRow[] = [
  {
    field: "detail_retention_days",
    name: "明细样本",
    description: "每次采集的原始值，1～90 天。超出此范围的历史曲线只显示分钟聚合。",
    color: "var(--accent)",
    min: RETENTION_LIMITS.detailMin,
    max: RETENTION_LIMITS.detailMax,
  },
  {
    field: "aggregate_retention_days",
    name: "分钟聚合",
    description: "每分钟的最小、最大与平均值，不少于明细保留期，最长 3650 天。",
    color: "var(--indigo)",
    min: 1,
    max: RETENTION_LIMITS.aggregateMax,
  },
  {
    field: "log_retention_days",
    name: "日志与记录",
    description:
      "审计、规则与通知日志，已结束的运行、事件和停电周期，1～3650 天。仍被引用的记录不会删除。",
    color: "var(--gray)",
    min: RETENTION_LIMITS.logMin,
    max: RETENTION_LIMITS.logMax,
  },
];

const NAME: Readonly<Record<RetentionField, string>> = {
  detail_retention_days: "明细样本",
  aggregate_retention_days: "分钟聚合",
  log_retention_days: "日志与记录",
};

const TICKS: readonly { days: number; label: string }[] = [
  { days: 1, label: "1 天" },
  { days: 7, label: "7 天" },
  { days: 30, label: "30 天" },
  { days: 90, label: "90 天" },
  { days: 365, label: "1 年" },
  { days: 3650, label: "10 年" },
];

/** 对数刻度的保留期时间线（1 天 ～ 10 年）；已修改的行用虚线标出修改前的位置。 */
function Timeline({ base, draft }: { base: RetentionDraft; draft: RetentionDraft }) {
  return (
    <div className="card tl" role="img" aria-label="各类数据的保留时长">
      {ROWS.map((row) => {
        const value = draft[row.field];
        const changed = value !== base[row.field];
        const pos = `${retentionPosition(value)}%`;
        return (
          <div className="tl-row" key={row.field} aria-hidden="true">
            <span className="tl-name">{row.name}</span>
            <div className="tl-track">
              {changed ? (
                <span
                  className="tl-ghost"
                  style={{ left: `${retentionPosition(base[row.field])}%` }}
                  title={`修改前：${base[row.field]} 天`}
                />
              ) : null}
              <span className="tl-bar" style={{ ["--c" as string]: row.color, width: pos }} />
              <span
                className="tl-val"
                style={{ left: pos, color: changed ? "var(--accent)" : undefined }}
              >
                {value} 天
              </span>
            </div>
          </div>
        );
      })}
      <div className="tl-axis" aria-hidden="true">
        <span />
        <div className="tl-ticks">
          {TICKS.map((tick) => (
            <span key={tick.days} style={{ left: `${retentionPosition(tick.days)}%` }}>
              {tick.label}
            </span>
          ))}
        </div>
      </div>
    </div>
  );
}

/**
 * 数据保留（F8-2）：显式保存栏。前端校验与后端一致；缩短任一保留期前先确认（Q-P3，
 * 后端没有确认字段，只在界面确认）——取消则不发请求。
 */
export function RetentionSection({ settings }: { settings: Settings }) {
  const save = useSaveSettings();
  const toast = useToast();
  const base = retentionOf(settings);
  // 未编辑时跟随服务端值；编辑后保存或还原即回到服务端值。
  const [edits, setEdits] = useState<RetentionDraft | null>(null);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const draft = edits ?? base;

  const errors = validateRetention(draft);
  const patch = buildRetentionPatch(base, draft);
  const changed = Object.keys(patch) as RetentionField[];
  const shortened = shortenedRetention(base, draft);
  const valid = Object.keys(errors).length === 0;

  const set = (field: RetentionField, value: number) => setEdits({ ...draft, [field]: value });

  const submit = () => {
    setFailure(null);
    save.mutate(patch, {
      onSuccess: () => {
        setEdits(null);
        toast.show({ tone: "ok", title: "保留期已保存", duration: 3000 });
      },
      onError: (err) => setFailure(settingsFailureText(err)),
    });
  };

  const requestSave = () => {
    if (!valid || changed.length === 0) return;
    if (shortened.length > 0) setConfirmOpen(true);
    else submit();
  };

  return (
    <section aria-label="数据保留">
      <PaneHead title="数据保留" />
      {failure ? (
        <Banner tone="critical" role="alert" title="保存失败" className="set-mb">
          {failure}
        </Banner>
      ) : null}
      <Timeline base={base} draft={draft} />

      <List className="mt-4">
        {ROWS.map((row) => {
          const isChanged = draft[row.field] !== base[row.field];
          return (
            <FormRow key={row.field} label={row.name} description={row.description}>
              <div className={isChanged ? "set-changed" : undefined}>
                <Stepper
                  aria-label={`${row.name}保留天数`}
                  value={draft[row.field]}
                  min={row.min}
                  max={row.max}
                  unit="天"
                  onChange={(value) => set(row.field, value)}
                />
              </div>
              <FieldError>{errors[row.field]}</FieldError>
            </FormRow>
          );
        })}
      </List>
      <ListCaption>
        保存后立即生效，无需重启。缩短保留期后，超期数据会在下一轮每小时清理时删除且无法恢复；延长不会找回已经删除的数据。
      </ListCaption>

      <SaveBar
        dirty={changed.length > 0}
        valid={valid}
        saving={save.isPending}
        summary={
          <>
            {changed.map((f) => `${NAME[f]} ${base[f]} 天 → ${draft[f]} 天`).join("；")}
            ，尚未保存
          </>
        }
        onRevert={() => {
          setEdits(null);
          setFailure(null);
        }}
        onSave={requestSave}
      />

      <Dialog
        open={confirmOpen}
        onClose={() => setConfirmOpen(false)}
        icon="alert"
        tone="danger"
        title="缩短数据保留期？"
        message="保存后，超出新保留期的数据会在下一轮每小时清理时被删除，且无法恢复。"
        actions={
          <>
            <Button onClick={() => setConfirmOpen(false)}>取消</Button>
            <Button
              variant="danger"
              onClick={() => {
                setConfirmOpen(false);
                submit();
              }}
            >
              缩短并保存
            </Button>
          </>
        }
      >
        <div className="dialog-consequences">
          <ul>
            {shortened.map((f) => (
              <li key={f}>
                {NAME[f]}：{base[f]} 天 → {draft[f]} 天
              </li>
            ))}
          </ul>
        </div>
      </Dialog>
    </section>
  );
}
