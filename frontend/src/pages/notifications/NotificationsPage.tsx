import "./notifications.css";
import { useSearchParams } from "react-router";
import { Button, Segmented, type SegmentedOption } from "../../components/index.ts";
import { Page } from "../../layout/Page.tsx";
import { ChannelsTab } from "./ChannelsTab.tsx";
import { LogsTab } from "./LogsTab.tsx";
import { TemplatesTab } from "./TemplatesTab.tsx";

type Tab = "channels" | "templates" | "logs";

const TAB_OPTIONS: SegmentedOption<Tab>[] = [
  { value: "channels", label: "渠道" },
  { value: "templates", label: "模板" },
  { value: "logs", label: "发送记录" },
];

function parseTab(value: string | null): Tab {
  return value === "templates" || value === "logs" ? value : "channels";
}

/** 各视图自己的 query 参数；切换视图时清掉，避免地址越积越长。 */
const TAB_PARAMS = ["id", "event", "format", "channel", "result", "range"];

/**
 * 通知（F4）：渠道 / 模板 / 发送记录三个页内视图，写入 `?tab=`。
 * 通知只在规则的「通知」动作或关机计划中发送，不会自动广播到所有渠道。
 */
export function NotificationsPage() {
  const [params, setParams] = useSearchParams();
  const tab = parseTab(params.get("tab"));

  const setTab = (next: Tab) =>
    setParams(
      (prev) => {
        const p = new URLSearchParams(prev);
        for (const key of TAB_PARAMS) p.delete(key);
        p.set("tab", next);
        return p;
      },
      { replace: true },
    );
  const addChannel = () => setParams({ tab: "channels", id: "new" });

  return (
    <Page
      title="通知"
      subtitle="通知只在规则的「通知」动作或关机计划中发送，不会自动广播到所有渠道。"
      headAside={
        <Segmented
          size="lg"
          aria-label="通知视图"
          options={TAB_OPTIONS}
          value={tab}
          onChange={setTab}
        />
      }
      toolbar={
        tab === "channels" ? (
          <Button variant="primary" size="sm" icon="plus" onClick={addChannel}>
            添加渠道
          </Button>
        ) : null
      }
    >
      <div className="notifications-page">
        {/* Page 在窄屏会隐藏 headAside，因此内容区放一份仅窄屏显示的切换控件。 */}
        <div className="nt-tabs-narrow">
          <Segmented
            size="lg"
            block
            aria-label="通知视图（窄屏）"
            options={TAB_OPTIONS}
            value={tab}
            onChange={setTab}
          />
        </div>
        {tab === "channels" ? (
          <ChannelsTab />
        ) : tab === "templates" ? (
          <TemplatesTab />
        ) : (
          <LogsTab />
        )}
      </div>
    </Page>
  );
}
