import { Empty } from "../components/Empty.tsx";
import { Page } from "../layout/Page.tsx";

/** 尚未实现的业务页面：保留路由与外壳，标明所属里程碑。 */
export function PlaceholderPage({ title, milestone }: { title: string; milestone: string }) {
  return (
    <Page title={title}>
      <div className="card card-pad">
        <Empty icon="hourglass" tone="info" title={`${title}尚未实现`}>
          该页面将在 {milestone} 交付。
        </Empty>
      </div>
    </Page>
  );
}
