import { Button } from "../components/Button.tsx";
import { Empty } from "../components/Empty.tsx";
import { Spinner } from "../components/Spinner.tsx";
import { useSession, useSessionActions } from "./hooks.ts";

/** 启动探测中的整屏占位。 */
export function SessionChecking() {
  return (
    <div className="boot-state">
      <Spinner label="正在检查登录状态" />
    </div>
  );
}

/**
 * 探测失败（网络或服务端错误）：可重试；不当作未登录，避免把断网误判为会话过期或未初始化。
 */
export function SessionError() {
  const session = useSession();
  const { probe } = useSessionActions();
  return (
    <div className="boot-state">
      <Empty
        icon="wifi-off"
        tone="warning"
        title="无法连接到 UPS Warden"
        action={
          <Button variant="tinted" onClick={() => void probe()}>
            重试
          </Button>
        }
      >
        {session.error}
      </Empty>
    </div>
  );
}
