import "./hosts-actions.css";
import { useSearchParams } from "react-router";
import { Button, Segmented, type SegmentedOption } from "../../components/index.ts";
import { Page } from "../../layout/Page.tsx";
import { ActionsTab } from "./ActionsTab.tsx";
import { HostsTab } from "./HostsTab.tsx";
import { RunsTab } from "./RunsTab.tsx";

type Tab = "hosts" | "actions" | "runs";

const TAB_OPTIONS: SegmentedOption<Tab>[] = [
  { value: "hosts", label: "受控主机" },
  { value: "actions", label: "动作" },
  { value: "runs", label: "执行记录" },
];

function parseTab(value: string | null): Tab {
  return value === "actions" || value === "runs" ? value : "hosts";
}

/** 各视图自己的 query 参数；切换视图时清掉。 */
const TAB_PARAMS = ["host", "fp", "edit", "run", "action", "filter", "open"];

/**
 * 主机与动作（F5）：受控主机 / 动作 / 执行记录三个页内视图，写入 `?tab=`。
 * 开发与联调只使用本机测试 SSH 服务端与假 HTTP 端点；手动执行默认 dry-run。
 */
export function HostsActionsPage() {
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

  const toolbar =
    tab === "hosts" ? (
      <Button
        variant="primary"
        size="sm"
        icon="plus"
        onClick={() => setParams({ tab: "hosts", host: "new" })}
      >
        添加主机
      </Button>
    ) : tab === "actions" ? (
      <Button
        variant="primary"
        size="sm"
        icon="plus"
        onClick={() => setParams({ tab: "actions", edit: "new" })}
      >
        新建动作
      </Button>
    ) : null;

  return (
    <Page
      title="主机与动作"
      subtitle="动作是规则与关机计划可调用的最小单元：通知、HTTP、本地脚本、SSH 命令或启动计划。"
      headAside={
        <Segmented
          size="lg"
          aria-label="主机与动作视图"
          options={TAB_OPTIONS}
          value={tab}
          onChange={setTab}
        />
      }
      toolbar={toolbar}
    >
      <div className="hosts-actions-page">
        {/* Page 在窄屏会隐藏 headAside，因此内容区放一份仅窄屏显示的切换控件。 */}
        <div className="ha-tabs-narrow">
          <Segmented
            size="lg"
            block
            aria-label="主机与动作视图（窄屏）"
            options={TAB_OPTIONS}
            value={tab}
            onChange={setTab}
          />
        </div>
        {tab === "hosts" ? <HostsTab /> : tab === "actions" ? <ActionsTab /> : <RunsTab />}
      </div>
    </Page>
  );
}
