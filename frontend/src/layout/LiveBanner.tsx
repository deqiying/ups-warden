import { useTimeZone } from "../api/hooks.ts";
import { Banner } from "../components/Banner.tsx";
import { Button } from "../components/Button.tsx";
import { useConnection, useNow, useReconnect } from "../live/hooks.ts";
import { formatTime } from "../model/time.ts";

/**
 * 实时连接断开横幅（FR-UI-01）。断线、重连中以及重连后尚未收到 sync 时显示：
 * 页面数据停在断开时刻，不把断流期间当作连续观测。
 */
export function LiveBanner() {
  const connection = useConnection();
  const reconnect = useReconnect();
  const timeZone = useTimeZone();
  const now = useNow(1000);

  const { phase, disconnectedAt, attempt, retryAt } = connection;
  if (phase === "idle" || disconnectedAt === null) return null;

  let progress: string;
  if (phase === "offline" && retryAt !== null) {
    const seconds = Math.max(0, Math.ceil((retryAt - now) / 1000));
    progress = `正在第 ${attempt} 次重连，${seconds} 秒后重试。`;
  } else if (phase === "offline") {
    progress = "正在检查登录状态…";
  } else {
    progress = "正在重新连接并同步…";
  }

  return (
    <Banner
      tone="warning"
      icon="wifi-off"
      role="status"
      title="实时连接已断开"
      className="live-banner"
      actions={
        phase === "offline" ? (
          <Button size="sm" onClick={reconnect}>
            立即重连
          </Button>
        ) : null
      }
    >
      {progress}下方是 {formatTime(disconnectedAt, timeZone)}{" "}
      之前的数据；重连后将重新全量同步，断开期间的变化不视为连续观测。
    </Banner>
  );
}
