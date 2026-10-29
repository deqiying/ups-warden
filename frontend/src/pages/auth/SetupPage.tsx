import { useRef, useState, type FormEvent } from "react";
import { flushSync } from "react-dom";
import { Navigate } from "react-router";
import { setup } from "../../api/auth.ts";
import { isApiError } from "../../api/client.ts";
import { Banner } from "../../components/Banner.tsx";
import { Icon, type IconName } from "../../icons/Icon.tsx";
import { SessionChecking, SessionError } from "../../session/BootState.tsx";
import { useSession, useSessionActions } from "../../session/hooks.ts";
import type { LoginLocationState } from "../../session/RequireAuth.tsx";
import { AuthLayout, GoButton, StepDots } from "./AuthLayout.tsx";
import { shakeClass } from "./helpers.ts";
import {
  checkPassword,
  isValidUsername,
  normalizeUsername,
  PASSWORD_MAX_BYTES,
  PASSWORD_MIN_CHARS,
} from "./credentials.ts";

type ReqState = "ok" | "bad" | "pending";

const REQ_ICON: Record<ReqState, IconName> = {
  ok: "check-circle",
  bad: "x-circle",
  pending: "check-circle",
};

function Requirement({ state, children }: { state: ReqState; children: string }) {
  return (
    <li className={state === "pending" ? undefined : state}>
      <Icon name={REQ_ICON[state]} />
      {children}
      {/* 状态不只靠颜色与图标：读屏补充“已满足 / 未满足” */}
      <span className="sr-only">{state === "ok" ? "（已满足）" : "（未满足）"}</span>
    </li>
  );
}

/**
 * 首次初始化页（F1-2，login.html setup）：用 setup token 创建第一个管理员。
 * - setup token 只在后端启动时向 stderr 输出一次、只存内存；前端不保存、不回显到 URL。
 * - 成功后转到登录页（预填用户名，密码从不预填）；返回 setup_closed 时同样转到登录页。
 */
export function SetupPage() {
  const session = useSession();
  const { store } = useSessionActions();
  // 初始化结束后带到登录页的提示（创建成功或 setup_closed）。
  const [outcome, setOutcome] = useState<LoginLocationState | undefined>(undefined);

  if (session.status === "authenticated") return <Navigate to="/" replace />;
  if (session.status === "unknown") return <SessionChecking />;
  if (session.status === "error") return <SessionError />;
  if (!session.setupRequired) return <Navigate to="/login" replace state={outcome} />;
  return (
    <AuthLayout title="首次初始化">
      <SetupForm
        onComplete={(state) => {
          // setupCompleted() 经 useSyncExternalStore 同步重渲染；先 flushSync 提交 outcome，
          // 保证那次渲染的跳转已带上提示，而不是先跳一次无 state 的登录页。
          flushSync(() => setOutcome(state));
          store.setupCompleted();
        }}
      />
    </AuthLayout>
  );
}

function SetupForm({ onComplete }: { onComplete: (state: LoginLocationState) => void }) {
  const [token, setToken] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<{ message: string; token: boolean } | null>(null);
  const [shaking, setShaking] = useState(false);
  const tokenRef = useRef<HTMLInputElement>(null);

  const checks = checkPassword(password, confirm);
  const usernameOk = isValidUsername(username);
  const canSubmit =
    token.trim() !== "" &&
    usernameOk &&
    checks.minLength &&
    checks.maxBytes &&
    checks.matches &&
    !busy;

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    setBusy(true);
    setError(null);
    try {
      const user = await setup({ setup_token: token.trim(), username, password });
      onComplete({ username: user.username, setupDone: true });
    } catch (err) {
      setBusy(false);
      if (isApiError(err) && err.code === "setup_closed") {
        onComplete({ setupClosed: true });
        return;
      }
      if (isApiError(err) && err.code === "invalid_setup_token") {
        setError({
          message: "初始化令牌无效。请核对终端输出的令牌；服务重启后令牌会重新生成。",
          token: true,
        });
        setShaking(true);
        tokenRef.current?.select();
        return;
      }
      setError({
        message: isApiError(err) ? err.message : "初始化失败，请稍后重试",
        token: false,
      });
    }
  }

  const matchState: ReqState = checks.matches ? "ok" : confirm !== "" ? "bad" : "pending";
  const usernameState: ReqState = username === "" ? "pending" : usernameOk ? "ok" : "bad";

  return (
    <section>
      <StepDots step={1} />
      <h1>欢迎使用 UPS Warden</h1>
      <p className="auth-lead">创建第一个管理员账户。之后即可添加 UPS、配置通知与关机计划。</p>
      <form
        className={shakeClass(error?.token === true, shaking)}
        onSubmit={(e) => void submit(e)}
        onAnimationEnd={() => setShaking(false)}
        noValidate
        aria-label="创建管理员账户"
      >
        <div className="field">
          <label htmlFor="setup-token">初始化令牌</label>
          <input
            ref={tokenRef}
            id="setup-token"
            name="setup_token"
            className="mono"
            autoFocus
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            value={token}
            aria-invalid={error?.token || undefined}
            aria-describedby={error ? "setup-error setup-token-hint" : "setup-token-hint"}
            onChange={(e) => {
              setToken(e.target.value);
              if (error?.token) setError(null);
            }}
          />
        </div>
        <div className="field">
          <label htmlFor="setup-username">用户名</label>
          <input
            id="setup-username"
            name="username"
            autoComplete="username"
            autoCapitalize="none"
            spellCheck={false}
            maxLength={64}
            value={username}
            aria-invalid={usernameState === "bad" || undefined}
            aria-describedby="setup-username-rule"
            // 与后端一致统一转小写，用户看到的就是将要保存的用户名。
            onChange={(e) => setUsername(normalizeUsername(e.target.value))}
          />
        </div>
        <div className="field">
          <label htmlFor="setup-password">密码</label>
          <input
            id="setup-password"
            name="password"
            type="password"
            autoComplete="new-password"
            value={password}
            aria-describedby="setup-password-rules"
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>
        <div className="field">
          <label htmlFor="setup-confirm">确认密码</label>
          <input
            id="setup-confirm"
            name="confirm_password"
            type="password"
            autoComplete="new-password"
            value={confirm}
            aria-invalid={matchState === "bad" || undefined}
            aria-describedby="setup-password-rules"
            onChange={(e) => setConfirm(e.target.value)}
          />
          <GoButton label="创建账户" disabled={!canSubmit} busy={busy} />
        </div>
      </form>
      {error ? (
        <p id="setup-error" className="auth-msg ink-red" role="alert">
          {error.message}
        </p>
      ) : null}
      <ul className="auth-req" id="setup-password-rules">
        <Requirement state={checks.minLength ? "ok" : password === "" ? "pending" : "bad"}>
          {`密码至少 ${PASSWORD_MIN_CHARS} 个字符`}
        </Requirement>
        {checks.maxBytes ? null : (
          <Requirement state="bad">{`密码不能超过 ${PASSWORD_MAX_BYTES} 字节`}</Requirement>
        )}
        <Requirement state={matchState}>两次输入一致</Requirement>
        <li id="setup-username-rule" className={usernameState === "bad" ? "bad" : undefined}>
          <Icon name={usernameState === "bad" ? "x-circle" : "info"} />
          用户名会统一转为小写，仅限字母、数字、点、下划线与连字符，最多 64 个字符
        </li>
      </ul>
      <Banner tone="neutral" icon="terminal" title="令牌在哪里？">
        <span id="setup-token-hint">
          服务首次启动时会在终端（stderr）打印一次「首次初始化 setup
          token」。令牌只保存在内存中，重启后会重新生成；请勿公开终端日志。
        </span>
      </Banner>
    </section>
  );
}
