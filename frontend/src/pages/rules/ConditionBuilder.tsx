import { Fragment, useMemo } from "react";
import {
  defaultValueFor,
  emptyConditionDraft,
  emptyGroupDraft,
  opsForKind,
  RULE_LIMITS,
  type ConditionDraft,
  type ConditionOp,
  type GroupDraft,
} from "../../api/rules.ts";
import { Banner, Button, Input, Select, Stepper } from "../../components/index.ts";
import { lookupField, splitFieldKey } from "../../model/fields.ts";
import { enumLabel } from "../../model/labels.ts";
import { conditionFieldOptions, conditionHint, OP_OPTIONS } from "./model.ts";

/**
 * 条件触发构建器（F7-3）：两层 OR of AND，与契约 `any_of[].all_of[]` 一一对应，每层最多 16 项。
 * 运算符与值的输入控件随字段类型变化：布尔、文本、枚举只允许 eq / ne；数值和时长用数字输入，
 * 提交时转为 number（不提交字符串数字）。
 */
export function ConditionBuilder({
  groups,
  onChange,
  errorFor,
}: {
  groups: GroupDraft[];
  onChange: (next: GroupDraft[]) => void;
  /** 条件错误：键为 `cond-<组>-<序号>`，未提交过时返回 undefined。 */
  errorFor: (key: string) => string | undefined;
}) {
  const fieldOptions = useMemo(() => conditionFieldOptions(), []);

  const setGroup = (key: number, conditions: ConditionDraft[]) =>
    onChange(groups.map((g) => (g.key === key ? { ...g, conditions } : g)));

  return (
    <div className="ru-groups">
      <p className="t-footnote muted ru-groups-note">
        满足<b className="ink-accent">任一</b>条件组即触发；组内条件需
        <b className="ink-accent">全部</b>成立。从不成立变为成立时只触发一次。
      </p>
      {groups.map((group, gi) => (
        <Fragment key={group.key}>
          {gi > 0 ? <div className="divider-text ru-or">或</div> : null}
          <section className="ru-group" aria-label={`条件组 ${gi + 1}`}>
            <div className="ru-group-head">
              <b>条件组 {gi + 1}</b>· 全部满足
              <span className="spacer" />
              <span className="faint">
                {group.conditions.length} / {RULE_LIMITS.conditionsMax}
              </span>
              {groups.length > 1 ? (
                <Button
                  size="sm"
                  variant="plain"
                  iconOnly
                  icon="trash"
                  aria-label={`删除条件组 ${gi + 1}`}
                  onClick={() => onChange(groups.filter((g) => g.key !== group.key))}
                />
              ) : null}
            </div>
            {group.conditions.map((cond, ci) => (
              <ConditionRow
                key={cond.key}
                cond={cond}
                label={`条件组 ${gi + 1} 第 ${ci + 1} 项`}
                fieldOptions={fieldOptions}
                error={errorFor(`cond-${gi}-${ci}`)}
                canRemove={group.conditions.length > 1}
                onChange={(next) =>
                  setGroup(
                    group.key,
                    group.conditions.map((c) => (c.key === cond.key ? next : c)),
                  )
                }
                onRemove={() =>
                  setGroup(
                    group.key,
                    group.conditions.filter((c) => c.key !== cond.key),
                  )
                }
              />
            ))}
            {group.conditions.length < RULE_LIMITS.conditionsMax ? (
              <Button
                size="sm"
                variant="plain"
                icon="plus"
                className="ru-add"
                onClick={() =>
                  setGroup(group.key, [
                    ...group.conditions,
                    emptyConditionDraft("battery_capacity_pct"),
                  ])
                }
              >
                添加条件
              </Button>
            ) : null}
          </section>
        </Fragment>
      ))}
      {groups.length < RULE_LIMITS.groupsMax ? (
        <Button
          size="sm"
          variant="plain"
          icon="plus"
          className="ru-add"
          onClick={() => onChange([...groups, emptyGroupDraft()])}
        >
          添加条件组
        </Button>
      ) : null}
      <Banner tone="neutral" className="mt-4">
        字段质量不是 good（缺测、失联过期、设备不支持）时，条件既不会触发，也不会被当作恢复。
        按相字段不指定相号时取所有有效相的最大值。
      </Banner>
    </div>
  );
}

function ConditionRow({
  cond,
  label,
  fieldOptions,
  error,
  canRemove,
  onChange,
  onRemove,
}: {
  cond: ConditionDraft;
  label: string;
  fieldOptions: ReturnType<typeof conditionFieldOptions>;
  error: string | undefined;
  canRemove: boolean;
  onChange: (next: ConditionDraft) => void;
  onRemove: () => void;
}) {
  const spec = lookupField(cond.field);
  const kind = spec?.kind ?? "number";
  const ops = opsForKind(kind);
  const opOptions = OP_OPTIONS.filter((o) => ops.includes(o.value as ConditionOp));
  const base = splitFieldKey(cond.field)?.base ?? cond.field;
  const hint = conditionHint(cond);

  const changeField = (field: string) => {
    const nextSpec = lookupField(field);
    const sameKind = nextSpec?.kind === spec?.kind && nextSpec?.kind !== "enum";
    const nextOps = nextSpec ? opsForKind(nextSpec.kind) : [];
    onChange({
      ...cond,
      field,
      op: nextOps.includes(cond.op) ? cond.op : "eq",
      // 同类型（数值、时长、布尔、文本）保留已填的值；枚举取值集合不同，换字段时重置。
      value: sameKind ? cond.value : defaultValueFor(field),
    });
  };

  let valueInput;
  if (kind === "enum") {
    valueInput = (
      <Select
        aria-label={`${label}取值`}
        options={(spec?.enum ?? []).map((v) => ({ value: v, label: enumLabel(base, v) }))}
        value={cond.value}
        onChange={(value) => onChange({ ...cond, value })}
      />
    );
  } else if (kind === "bool") {
    valueInput = (
      <Select
        aria-label={`${label}取值`}
        options={[
          { value: "true", label: "是" },
          { value: "false", label: "否" },
        ]}
        value={cond.value}
        onChange={(value) => onChange({ ...cond, value })}
      />
    );
  } else if (kind === "text") {
    valueInput = (
      <Input
        aria-label={`${label}取值`}
        value={cond.value}
        invalid={Boolean(error)}
        autoComplete="off"
        onChange={(e) => onChange({ ...cond, value: e.target.value })}
      />
    );
  } else {
    valueInput = (
      <span className="ru-num">
        <Input
          aria-label={`${label}取值`}
          type="number"
          inputMode="decimal"
          step="any"
          value={cond.value}
          invalid={Boolean(error)}
          onChange={(e) => onChange({ ...cond, value: e.target.value })}
        />
        <span className="ru-unit">{kind === "duration" ? "秒" : (spec?.unit ?? "")}</span>
      </span>
    );
  }

  return (
    <div className="ru-cond-wrap">
      <div className="ru-cond">
        <Select
          className="ru-field"
          aria-label={`${label}字段`}
          options={
            spec || !cond.field
              ? fieldOptions
              : [{ value: cond.field, label: `未知字段 · ${cond.field}` }, ...fieldOptions]
          }
          value={cond.field}
          onChange={changeField}
        />
        <Select
          aria-label={`${label}运算符`}
          options={opOptions}
          value={cond.op}
          onChange={(op) => onChange({ ...cond, op: op as ConditionOp })}
        />
        {valueInput}
        <span className="ru-for">
          持续
          <Stepper
            aria-label={`${label}持续时间（秒）`}
            value={cond.forS}
            min={0}
            max={RULE_LIMITS.secondsMax}
            unit="秒"
            onChange={(forS) => onChange({ ...cond, forS })}
          />
        </span>
        {canRemove ? (
          <Button
            size="sm"
            variant="plain"
            iconOnly
            icon="minus"
            className="ru-rm"
            aria-label={`删除${label}`}
            onClick={onRemove}
          />
        ) : (
          <span className="ru-rm" />
        )}
      </div>
      {hint || error ? (
        <div className="ru-cond-foot">
          {hint ? <span className="faint">{hint}</span> : null}
          {error ? (
            <span className="ru-err" role="alert">
              {error}
            </span>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
