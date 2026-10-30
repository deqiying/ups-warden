import { useState } from "react";
import { useRuleTemplates } from "../../api/hooks.ts";
import { Banner, Button, List, Row, Sheet, Spinner } from "../../components/index.ts";
import { Icon } from "../../icons/Icon.tsx";
import { templateDescription } from "./model.ts";

/**
 * 从模板创建（F7-5）：`GET /rules/templates` 选择模板后进入预填的新建表单，由用户选择动作后再 POST。
 * 模板默认停用且 dry-run；关机模板要求显式选择 plan 动作，不会自动挑选计划。
 */
export function TemplateSheet({
  open,
  onClose,
  onPick,
}: {
  open: boolean;
  onClose: () => void;
  onPick: (key: string) => void;
}) {
  const query = useRuleTemplates(open);
  const [selected, setSelected] = useState<string | null>(null);

  let body;
  if (query.isPending) {
    body = (
      <div className="empty">
        <Spinner />
        <div className="t-subhead muted">正在加载模板…</div>
      </div>
    );
  } else if (query.isError) {
    body = (
      <Banner
        tone="critical"
        role="alert"
        title="模板加载失败"
        actions={
          <Button size="sm" onClick={() => void query.refetch()}>
            重试
          </Button>
        }
      >
        {query.error.message}
      </Banner>
    );
  } else {
    body = (
      <List hasIcons className="ru-tpl" aria-label="规则模板">
        {query.data.map((t) => {
          const on = selected === t.key;
          return (
            <Row
              key={t.key}
              selected={on}
              onActivate={() => setSelected(t.key)}
              title={<span className="strong">{t.rule.name}</span>}
              sub={
                <>
                  {templateDescription(t)}
                  {t.suggested_action_type === "plan" ? (
                    <Banner tone="warning" icon="power" className="mt-3 ru-tpl-warn">
                      需要你选择一个「启动关机计划」动作。模板不会自动挑选计划。
                    </Banner>
                  ) : (
                    <div className="faint">建议动作：通知</div>
                  )}
                </>
              }
            >
              <span className={on ? "check is-on ru-tpl-check" : "check ru-tpl-check"} aria-hidden>
                {on ? <Icon name="check" /> : null}
              </span>
            </Row>
          );
        })}
      </List>
    );
  }

  return (
    <Sheet
      open={open}
      onClose={onClose}
      title="从模板创建"
      width="min(560px, calc(100vw - 32px))"
      leading={
        <Button variant="plain" onClick={onClose}>
          取消
        </Button>
      }
      trailing={
        <Button
          variant="primary"
          size="sm"
          disabled={selected === null}
          onClick={() => {
            if (selected !== null) onPick(selected);
          }}
        >
          下一步
        </Button>
      }
    >
      {body}
      <Banner tone="indigo" className="mt-4">
        从模板创建的规则默认<b>停用</b>且处于 <b>dry-run</b>。下一步选择动作并确认范围后保存，
        之后再手动启用。
      </Banner>
    </Sheet>
  );
}
