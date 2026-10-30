import "../devices/devices.css";
import "./settings.css";
import { useState } from "react";
import { Navigate, useNavigate, useParams } from "react-router";
import { Button, List, Row } from "../../components/index.ts";
import { Page } from "../../layout/Page.tsx";
import { SplitView } from "../../layout/SplitView.tsx";
import { AccountSection } from "./AccountSection.tsx";
import { AuditSection } from "./AuditSection.tsx";
import { DefaultsSection } from "./DefaultsSection.tsx";
import { GeneralSection } from "./GeneralSection.tsx";
import { useUsers } from "./hooks.ts";
import { parseSection, SECTIONS, type SectionKey } from "./model.ts";
import { RetentionSection } from "./RetentionSection.tsx";
import { SecuritySection } from "./SecuritySection.tsx";
import { SettingsLoader } from "./shared.tsx";
function SettingsNav({ current }: { current: SectionKey }) {
  const navigate = useNavigate();
  const users = useUsers();
  const renderGroup = (group: 1 | 2) => (
    <List hasIcons className={group === 2 ? "mt-4" : undefined}>
      {SECTIONS.filter((s) => s.group === group).map((s) => (
        <Row
          key={s.key}
          icon={s.icon}
          iconColor={s.color}
          title={s.label}
          selected={s.key === current}
          // 管理员数量来自已加载的列表，加载前不显示。
          value={s.key === "account" ? users.data?.length : undefined}
          onActivate={() => void navigate(`/settings/${s.key}`)}
        />
      ))}
    </List>
  );
  return (
    <nav className="set-nav" aria-label="设置分类">
      {renderGroup(1)}
      {renderGroup(2)}
    </nav>
  );
}

/**
 * 设置、账户与审计（F8）。路由 `/settings/:section`，section 为
 * general | retention | defaults | security | account | audit；`/settings` 与未知分区重定向到 general。
 */
export function SettingsPage() {
  const { section } = useParams();
  const key = parseSection(section);
  const [addOpen, setAddOpen] = useState(false);

  if (!key) return <Navigate replace to="/settings/general" />;

  let content;
  switch (key) {
    case "general":
      content = <SettingsLoader>{(s) => <GeneralSection settings={s} />}</SettingsLoader>;
      break;
    case "retention":
      content = <SettingsLoader>{(s) => <RetentionSection settings={s} />}</SettingsLoader>;
      break;
    case "defaults":
      content = <SettingsLoader>{(s) => <DefaultsSection settings={s} />}</SettingsLoader>;
      break;
    case "security":
      content = <SettingsLoader>{(s) => <SecuritySection settings={s} />}</SettingsLoader>;
      break;
    case "account":
      content = <AccountSection addOpen={addOpen} onAddClose={() => setAddOpen(false)} />;
      break;
    case "audit":
      content = <AuditSection />;
      break;
  }

  return (
    <Page
      title="设置"
      subtitle="对所有管理员生效；每次修改都会记入审计日志。"
      toolbar={
        key === "account" ? (
          <Button variant="primary" size="sm" icon="plus" onClick={() => setAddOpen(true)}>
            添加管理员
          </Button>
        ) : null
      }
    >
      <div className="settings-page">
        {/* key：切换分区时丢弃上一个分区的未保存草稿与错误提示。 */}
        <SplitView
          narrow
          stickyList
          list={<SettingsNav current={key} />}
          detail={
            <div className="pane" key={key}>
              {content}
            </div>
          }
        />
      </div>
    </Page>
  );
}
