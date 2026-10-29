import { useEffect, useRef, useState, type FormEvent } from "react";
import { Navigate, useLocation } from "react-router";
import { login } from "../../api/auth.ts";
import { isApiError } from "../../api/client.ts";
import { Banner } from "../../components/Banner.tsx";
import { Ring } from "../../components/Ring.tsx";
import { useNow } from "../../live/hooks.ts";
import { clockText, durationText } from "../../model/format.ts";
import { SessionChecking, SessionError } from "../../session/BootState.tsx";
import { useSession, useSessionActions } from "../../session/hooks.ts";
import type { LoginLocationState } from "../../session/RequireAuth.tsx";
import { AuthLayout, GoButton, StepDots } from "./AuthLayout.tsx";
import { DEFAULT_LOCK_SECONDS } from "./credentials.ts";
import { safeReturnPath, shakeClass } from "./helpers.ts";

const INVALID_CREDENTIALS =
  "用户名或密码不正确。连续失败 5 次后，该账户在此网络位置将暂停登录 15 分钟。";

/** username：表单初始用户名；锁定结束回到表单时保留用户输入过的用户名。 */
type LoginView =
  | { kind: "form"; username: string }
  | { kind: "locked"; until: number; total: number; username: string };

/**
 * 登录页（F1-3，login.html login / error / locked）。
 * - 密码从不预填；失败后清空密码并抖动。
 * - 429：按 Retry-After 倒计时，结束后回到表单。锁定计时只在本页内存中，刷新后再次提交会重新得到 429。
 * - 凭据只经 POST 请求体发送，不进入 URL、浏览器存储或控制台。
 */
export function LoginPage() {
  const session = useSession();
  const location = useLocation();
  const state = (location.state ?? {}) as LoginLocationState;
  const [view, setView] = useState<LoginView>({ kind: "form", username: state.username ?? "" });

  if (session.status === "authenticated") {
    return <Navigate to={safeReturnPath(state.from)} replace />;
  }
  if (session.status === "unknown") return <SessionChecking />;
  if (session.status === "error") return <SessionError />;
  if (session.setupRequired) return <Navigate to="/setup" replace />;

  return (
    <AuthLayout title={view.kind === "locked" ? "暂时无法登录" : "登录"}>
      {view.kind === "locked" ? (
        <LockedView
          until={view.until}
          total={view.total}
          onDone={() => setView({ kind: "form", username: view.username })}
        />
      ) : (
        <LoginForm
          state={state}
          initialUsername={view.username}
          onLocked={(seconds, username) =>
            setView({
              kind: "locked",
              until: Date.now() + seconds * 1000,
              total: seconds,
              username,
            })
          }
        />
      )}
    </AuthLayout>
  );
}

function LoginNotice({ state }: { state: LoginLocationState }) {
  if (state.setupDone) {
    return (
      <Banner tone="info" icon="check-circle" role="status">
        管理员账户已创建，请使用刚才设置的密码登录。
      </Banner>
    );
  }
  if (state.setupClosed) {
    return (
      <Banner tone="info" role="status">
        初始化已经完成，请使用管理员账户登录。
      </Banner>
    );
  }
  if (state.reason === "expired") {
    return (
      <Banner tone="warning" icon="clock" role="status">
        会话已过期或已在其他地方退出，请重新登录。
      </Banner>
    );
  }
  if (state.reason === "logout") {
    return (
      <Banner tone="neutral" role="status">
        已退出登录。
      </Banner>
    );
  }
  return null;
}

function LoginForm({
  state,
  initialUsername,
  onLocked,
}: {
  state: LoginLocationState;
  initialUsername: string;
  onLocked: (seconds: number, username: string) => void;
}) {
  const { store } = useSessionActions();
  const [username, setUsername] = useState(initialUsername);
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<{ message: string; credentials: boolean } | null>(null);
  const [shaking, setShaking] = useState(false);
  const passwordRef = useRef<HTMLInputElement>(null);
  // 已知用户名（刚完成初始化或锁定结束）时直接聚焦密码。
  const focusPassword = initialUsername !== "";

  const trimmed = username.trim();
  const canSubmit = trimmed !== "" && password !== "" && !busy;

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    setBusy(true);
    setError(null);
    try {
      store.signIn(await login(trimmed, password));
      // 会话已建立：LoginPage 重新渲染并跳转到返回地址。
    } catch (err) {
      setBusy(false);
      if (isApiError(err) && err.status === 429) {
        setPassword("");
        onLocked(err.retryAfterSeconds ?? DEFAULT_LOCK_SECONDS, username);
        return;
      }
      if (isApiError(err) && err.code === "invalid_credentials") {
        setPassword("");
        setError({ message: INVALID_CREDENTIALS, credentials: true });
        setShaking(true);
        passwordRef.current?.focus();
        return;
      }
      // 网络、来源校验等其他失败：保留输入，便于直接重试。
      setError({
        message: isApiError(err) ? err.message : "登录失败，请稍后重试",
        credentials: false,
      });
    }
  }

  const invalid = error?.credentials === true;
  return (
    <section>
      {state.setupDone ? <StepDots step={2} /> : null}
      <h1>登录 UPS Warden</h1>
      <p className="auth-lead">使用管理员账户继续。</p>
      <LoginNotice state={state} />
      <form
        className={shakeClass(invalid, shaking)}
        onSubmit={(e) => void submit(e)}
        onAnimationEnd={() => setShaking(false)}
        noValidate
        aria-label="登录"
      >
        <div className="field">
          <label htmlFor="login-username">用户名</label>
          <input
            autoFocus={!focusPassword}
            id="login-username"
            name="username"
            autoComplete="username"
            autoCapitalize="none"
            spellCheck={false}
            value={username}
            aria-invalid={invalid || undefined}
            aria-describedby={error ? "login-error" : undefined}
            onChange={(e) => {
              setUsername(e.target.value);
              if (invalid) setError(null);
            }}
          />
        </div>
        <div className="field">
          <label htmlFor="login-password">密码</label>
          <input
            ref={passwordRef}
            autoFocus={focusPassword}
            id="login-password"
            name="password"
            type="password"
            autoComplete="current-password"
            value={password}
            aria-invalid={invalid || undefined}
            aria-describedby={error ? "login-error" : undefined}
            onChange={(e) => {
              setPassword(e.target.value);
              if (invalid) setError(null);
            }}
          />
          <GoButton label="登录" disabled={!canSubmit} busy={busy} />
        </div>
      </form>
      {error ? (
        <p id="login-error" className="auth-msg ink-red" role="alert">
          {error.message}
        </p>
      ) : null}
      <p className="auth-foot">
        会话在 24 小时无操作或 7 天后过期。
        <br />
        页面与 API 仅接受同源请求。
      </p>
    </section>
  );
}

/** 429 锁定：按 Retry-After 倒计时（login.html locked）。 */
function LockedView({
  until,
  total,
  onDone,
}: {
  until: number;
  total: number;
  onDone: () => void;
}) {
  const now = useNow(1000);
  const remaining = Math.max(0, Math.ceil((until - now) / 1000));

  useEffect(() => {
    if (remaining === 0) onDone();
  }, [remaining, onDone]);

  return (
    <section>
      <h1>暂时无法登录</h1>
      <p className="auth-lead">尝试次数过多。为保护账户，请在倒计时结束后再试。</p>
      {/* 只在进入锁定时播报一次；倒计时数字每秒变化，不放进 live region。 */}
      <p className="sr-only" role="status">
        登录已暂停，约 {durationText(total, "minute")}后可重试。
      </p>
      <div className="lock-ring">
        <Ring
          fraction={total > 0 ? remaining / total : 0}
          size={132}
          stroke={8}
          color="var(--orange)"
          label={`${clockText(remaining)} 后可重试`}
        >
          <div>
            <div className="t-metric">{clockText(remaining)}</div>
            <div className="t-caption faint mt-2">后可重试</div>
          </div>
        </Ring>
      </div>
      <p className="auth-foot">
        锁定按用户名与来源 IP 分别计算，倒计时结束后自动解除。
        <br />
        如果忘记密码，请由其他管理员在「设置 › 账户」中重置。
      </p>
    </section>
  );
}
