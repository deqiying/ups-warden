import type { ReactNode } from "react";
import type { Field } from "../api/types.ts";
import { fieldLabel } from "../model/fields.ts";
import { formatField, NA_TEXT } from "../model/format.ts";
import { Badge } from "./Badge.tsx";
import { ReasonTip } from "./ReasonTip.tsx";

export interface FieldValueProps {
  /** 统一字段键，可为按相实例键（`input_voltage[1]`）。 */
  fieldKey: string;
  /** undefined 表示快照中缺少该键，按「—」加原因显示。 */
  field: Field | undefined;
  /** 实时连接断开或尚未重新同步：保留读数并置灰。 */
  stale?: boolean;
  /** 设备 runtime_source；剩余时间为估算值时加“约”和「估算」标记。 */
  runtimeSource?: string;
  /** 「—」原因的显示方式：tip（默认，可聚焦提示）或 inline（值下方小字，用于字段网格）。 */
  reasonMode?: "tip" | "inline";
  className?: string;
}

const cx = (...parts: (string | false | undefined)[]) =>
  parts.filter(Boolean).join(" ") || undefined;

/**
 * 统一字段的显示：所有页面都通过它（或 model/format.ts）读取字段，不自行判断 quality。
 * 非 good 字段一律显示「—」与原因，绝不显示 0。
 */
export function FieldValue({
  fieldKey,
  field,
  stale,
  runtimeSource,
  reasonMode = "tip",
  className,
}: FieldValueProps) {
  const display = formatField(fieldKey, field, { stale, runtimeSource });
  if (display.kind === "na") {
    if (reasonMode === "inline") {
      return (
        <span className={cx("na", className)}>
          {NA_TEXT}
          <span className="reason">{display.reason}</span>
        </span>
      );
    }
    return (
      <ReasonTip
        reason={display.reason}
        label={`${fieldLabel(fieldKey)}无数据`}
        className={cx("na", className)}
      />
    );
  }
  return (
    <span className={cx(display.stale && "is-stale", className)}>
      {display.text}
      {display.unit ? <small>{display.unit}</small> : null}
      {display.estimated ? (
        <>
          {" "}
          <Badge tone="orange">估算</Badge>
        </>
      ) : null}
    </span>
  );
}

export interface MetricProps extends Omit<FieldValueProps, "className" | "reasonMode"> {
  /** 默认取字段目录中的中文名。 */
  label?: ReactNode;
  className?: string;
  /** 数值下方的附加内容（如 Meter、读数时间）。 */
  children?: ReactNode;
}

/** 指标块：`.metric` 标签 + 大号数值，含质量与原因提示。 */
export function Metric({ label, className, children, ...value }: MetricProps) {
  return (
    <div className={cx("metric", className)}>
      <span className="metric-label">{label ?? fieldLabel(value.fieldKey)}</span>
      <FieldValue {...value} className="metric-value" />
      {children}
    </div>
  );
}
