import { Link } from "react-router";
import { Empty } from "../components/Empty.tsx";
import { Page } from "../layout/Page.tsx";

/** 未知路由。 */
export function NotFoundPage() {
  return (
    <Page title="页面不存在" liveBanner={false}>
      <div className="card card-pad">
        <Empty
          icon="question"
          tone="info"
          title="找不到这个页面"
          action={
            <Link className="btn btn-tinted" to="/">
              返回总览
            </Link>
          }
        >
          链接可能已失效，或地址输入有误。
        </Empty>
      </div>
    </Page>
  );
}
