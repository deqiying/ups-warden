import "./rules.css";
import { useState } from "react";
import { Navigate, useNavigate, useParams, useSearchParams } from "react-router";
import { useDevices, useRules, useRuleTemplates } from "../../api/hooks.ts";
import type { Rule } from "../../api/rules.ts";
import {
  Badge,
  Banner,
  Button,
  Empty,
  List,
  ListCaption,
  Row,
  Spinner,
} from "../../components/index.ts";
import { Page } from "../../layout/Page.tsx";
import { RuleEditor } from "./RuleEditor.tsx";
import { ruleIcon, scopeText, triggerTypeText } from "./model.ts";
import { TemplateSheet } from "./TemplateSheet.tsx";

/**
 * 规则（F7）：左侧规则列表，右侧编辑器与评估日志。路由：`/rules`（有规则时打开第一个）、
 * `/rules/new`（`?template=<key>` 从模板预填）、`/rules/:id`。
 * 新建规则默认 dry-run；开发与联调只用 dry-run 与模拟器，不对真实主机执行动作。
 */
export function RulesPage() {
  const { id: idParam } = useParams();
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const rulesQuery = useRules();
  const rules = rulesQuery.data;
  const [templatesOpen, setTemplatesOpen] = useState(false);
  const isNew = idParam === "new";
  const ruleId = idParam && !isNew ? Number(idParam) : null;
  const templateKey = isNew ? params.get("template") : null;

  if (idParam === undefined && rules && rules.length > 0) {
    return <Navigate replace to={`/rules/${rules[0].id}`} />;
  }

  let detail;
  if (isNew) {
    detail = templateKey ? (
      <TemplateEditor key={`tpl-${templateKey}`} templateKey={templateKey} />
    ) : (
      <RuleEditor key="new" rule={null} />
    );
  } else if (rulesQuery.isPending) {
    detail = <Loading text="正在加载规则…" />;
  } else if (rulesQuery.isError && !rules) {
    detail = (
      <Banner
        tone="critical"
        role="alert"
        title="规则加载失败"
        actions={
          <Button size="sm" onClick={() => void rulesQuery.refetch()}>
            重试
          </Button>
        }
      >
        {rulesQuery.error.message}
      </Banner>
    );
  } else if (ruleId === null) {
    detail = (
      <div className="card">
        <Empty
          icon="bolt"
          tone="info"
          title="还没有规则"
          action={
            <div className="hstack gap-1">
              <Button icon="stack" onClick={() => setTemplatesOpen(true)}>
                从模板创建
              </Button>
              <Button variant="primary" icon="plus" onClick={() => void navigate("/rules/new")}>
                新建规则
              </Button>
            </div>
          }
        >
          规则在设备状态更新时评估，命中后派发动作。先在「主机与动作」中准备好通知或关机计划动作。
        </Empty>
      </div>
    );
  } else {
    const rule = rules?.find((r) => r.id === ruleId);
    detail = rule ? (
      <RuleEditor key={rule.id} rule={rule} />
    ) : (
      <div className="card">
        <Empty icon="info" tone="warning" title="规则不存在">
          规则 #{idParam} 可能已被删除。
        </Empty>
      </div>
    );
  }

  return (
    <Page
      title="规则"
      subtitle="设备状态每次更新都会评估规则，命中后派发动作。新建规则默认 dry-run：只记录，不执行。"
      toolbar={
        <>
          <Button size="sm" icon="stack" onClick={() => setTemplatesOpen(true)}>
            从模板创建
          </Button>
          <Button
            variant="primary"
            size="sm"
            icon="plus"
            onClick={() => void navigate("/rules/new")}
          >
            新建规则
          </Button>
        </>
      }
    >
      <div className="split ru-split">
        <div className="sticky-col ru-col">
          <RuleList rules={rules ?? []} selectedId={ruleId} isNew={isNew} />
          {rules && rules.length > 0 ? (
            <ListCaption>
              规则按「规则 × 设备」分别计算。修改范围或触发方式会开始新的计算实例，旧日志保留。
            </ListCaption>
          ) : null}
        </div>
        <div className="ru-detail">{detail}</div>
      </div>
      <TemplateSheet
        open={templatesOpen}
        onClose={() => setTemplatesOpen(false)}
        onPick={(key) => {
          setTemplatesOpen(false);
          void navigate(`/rules/new?template=${encodeURIComponent(key)}`);
        }}
      />
    </Page>
  );
}

function Loading({ text }: { text: string }) {
  return (
    <div className="card">
      <div className="empty">
        <Spinner />
        <div className="t-subhead muted">{text}</div>
      </div>
    </div>
  );
}

/** `/rules/new?template=<key>`：读取模板后预填编辑器；未知模板给出提示。 */
function TemplateEditor({ templateKey }: { templateKey: string }) {
  const query = useRuleTemplates();
  if (query.isPending) return <Loading text="正在加载模板…" />;
  if (query.isError) {
    return (
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
  }
  const template = query.data.find((t) => t.key === templateKey);
  if (!template) {
    return (
      <div className="card">
        <Empty icon="info" tone="warning" title="模板不存在">
          找不到模板「{templateKey}」。
        </Empty>
      </div>
    );
  }
  return <RuleEditor rule={null} template={template} />;
}

function RuleList({
  rules,
  selectedId,
  isNew,
}: {
  rules: readonly Rule[];
  selectedId: number | null;
  isNew: boolean;
}) {
  const navigate = useNavigate();
  const devices = useDevices().data;
  if (rules.length === 0 && !isNew) return null;
  return (
    <List hasIcons className="ru-list" aria-label="规则">
      {isNew ? <Row icon="plus" iconColor="var(--accent)" title="新建规则" selected /> : null}
      {rules.map((rule) => (
        <Row
          key={rule.id}
          icon={rule.enabled ? ruleIcon(rule) : "pause"}
          iconColor={rule.enabled ? (rule.dry_run ? "var(--indigo)" : "var(--orange)") : undefined}
          softIcon={!rule.enabled}
          className={rule.enabled ? undefined : "is-off"}
          title={<span className="strong">{rule.name}</span>}
          sub={
            <>
              {triggerTypeText(rule)} · {scopeText(rule, devices)}
              <span className="ru-meta">
                {!rule.enabled ? <Badge>已停用</Badge> : null}
                {rule.dry_run ? (
                  <Badge tone="indigo">Dry-run</Badge>
                ) : (
                  <Badge tone="orange">真实执行</Badge>
                )}
              </span>
            </>
          }
          selected={rule.id === selectedId}
          onActivate={() => void navigate(`/rules/${rule.id}`)}
        />
      ))}
    </List>
  );
}
