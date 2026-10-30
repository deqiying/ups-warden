import type { ReactNode } from "react";
import type { Settings } from "../../api/types.ts";
import { useSettings } from "../../api/hooks.ts";
import { Banner, Button, Spinner } from "../../components/index.ts";

/** 分区标题（效果图 pane-head）。 */
export function PaneHead({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="pane-head">
      <h2 className="section-title">{title}</h2>
      {children}
    </div>
  );
}

/** 表单项下方的校验提示。 */
export function FieldError({ children }: { children?: string }) {
  return children ? (
    <div className="hint is-error" role="alert">
      {children}
    </div>
  ) : null;
}

/** 需要系统设置的分区：加载中显示占位，失败显示可重试的错误，不把失败显示为空白。 */
export function SettingsLoader({ children }: { children: (settings: Settings) => ReactNode }) {
  const query = useSettings();
  if (!query.data) {
    if (query.isError) {
      return (
        <Banner
          tone="critical"
          role="alert"
          title="设置加载失败"
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
    return (
      <div className="card">
        <div className="empty">
          <Spinner />
          <div className="t-subhead muted">正在加载设置…</div>
        </div>
      </div>
    );
  }
  return <>{children(query.data)}</>;
}

export interface SaveBarProps {
  /** 有未保存修改时显示的摘要。 */
  summary: ReactNode;
  dirty: boolean;
  /** 校验通过才允许保存。 */
  valid: boolean;
  saving: boolean;
  onRevert: () => void;
  onSave: () => void;
  /** 左侧的附加操作（如“恢复内置默认值”）。 */
  extra?: ReactNode;
}

/** 显式保存栏（还原 / 保存），用于数据保留与设备默认值。 */
export function SaveBar({ summary, dirty, valid, saving, onRevert, onSave, extra }: SaveBarProps) {
  return (
    <div className="save-bar">
      {extra}
      {dirty ? (
        <span className="note" role="status">
          <span className="dot-mod" aria-hidden="true" />
          {summary}
        </span>
      ) : null}
      <span className="spacer" />
      <Button disabled={!dirty || saving} onClick={onRevert}>
        还原
      </Button>
      <Button
        variant="primary"
        disabled={!dirty || !valid || saving}
        aria-busy={saving}
        onClick={onSave}
      >
        {saving ? "正在保存…" : "保存"}
      </Button>
    </div>
  );
}
