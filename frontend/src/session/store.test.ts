import { describe, expect, it, vi } from "vite-plus/test";
import { sessionResponse } from "../test/fixtures.ts";
import { SessionStore } from "./store.ts";

describe("SessionStore", () => {
  it("初始状态为 unknown，无凭据", () => {
    const snap = new SessionStore().getSnapshot();
    expect(snap).toEqual({
      status: "unknown",
      user: null,
      csrfToken: null,
      expiresAt: null,
      signedOutReason: null,
      setupRequired: false,
      error: null,
    });
  });

  it("signIn 保存 user / csrfToken / expiresAt，setupRequired=false", () => {
    const store = new SessionStore();
    // 先处于需要初始化的未登录状态，确认 signIn 会清除该标志。
    store.signOut(null, true);
    const session = sessionResponse();
    store.signIn(session);

    const snap = store.getSnapshot();
    expect(snap.status).toBe("authenticated");
    expect(snap.user).toEqual(session.user);
    expect(snap.csrfToken).toBe(session.csrf_token);
    expect(snap.expiresAt).toBe(session.expires_at);
    expect(snap.signedOutReason).toBeNull();
    expect(snap.setupRequired).toBe(false);
    expect(snap.error).toBeNull();
    expect(store.csrfToken).toBe("csrf-1");
  });

  it("signOut 清空凭据并记录 reason 与 setupRequired", () => {
    const store = new SessionStore();
    store.signIn(sessionResponse());
    store.signOut("expired", true);

    const snap = store.getSnapshot();
    expect(snap.status).toBe("anonymous");
    expect(snap.user).toBeNull();
    expect(snap.csrfToken).toBeNull();
    expect(snap.expiresAt).toBeNull();
    expect(snap.signedOutReason).toBe("expired");
    expect(snap.setupRequired).toBe(true);
    expect(store.csrfToken).toBeNull();
  });

  it("signOut 的 setupRequired 默认为 false", () => {
    const store = new SessionStore();
    store.signIn(sessionResponse());
    store.signOut("logout");

    const snap = store.getSnapshot();
    expect(snap.signedOutReason).toBe("logout");
    expect(snap.setupRequired).toBe(false);
  });

  it("setupCompleted 在 setupRequired 为 true 时置 false 并通知订阅者", () => {
    const store = new SessionStore();
    store.signOut(null, true);
    const listener = vi.fn();
    store.subscribe(listener);

    store.setupCompleted();

    expect(store.getSnapshot().setupRequired).toBe(false);
    expect(store.getSnapshot().status).toBe("anonymous");
    expect(listener).toHaveBeenCalledTimes(1);
  });

  it("setupCompleted 在 setupRequired 为 false 时不变更快照、不通知", () => {
    const store = new SessionStore();
    store.signOut(null, false);
    const before = store.getSnapshot();
    const listener = vi.fn();
    store.subscribe(listener);

    store.setupCompleted();

    expect(store.getSnapshot()).toBe(before);
    expect(listener).not.toHaveBeenCalled();
  });

  it("setError 清空凭据并记录错误文案", () => {
    const store = new SessionStore();
    store.signIn(sessionResponse());
    store.setError("无法连接到服务");

    const snap = store.getSnapshot();
    expect(snap.status).toBe("error");
    expect(snap.error).toBe("无法连接到服务");
    expect(snap.user).toBeNull();
    expect(snap.csrfToken).toBeNull();
    expect(snap.expiresAt).toBeNull();
    expect(snap.signedOutReason).toBeNull();
    expect(snap.setupRequired).toBe(false);
  });

  it("subscribe 在每次变更时通知，取消订阅后不再通知", () => {
    const store = new SessionStore();
    const listener = vi.fn();
    const unsubscribe = store.subscribe(listener);

    store.signIn(sessionResponse());
    store.signOut("logout");
    expect(listener).toHaveBeenCalledTimes(2);

    unsubscribe();
    store.setError("x");
    expect(listener).toHaveBeenCalledTimes(2);
  });

  it("多个订阅者互不影响：取消其一后另一个仍被通知", () => {
    const store = new SessionStore();
    const a = vi.fn();
    const b = vi.fn();
    const unsubscribeA = store.subscribe(a);
    store.subscribe(b);

    unsubscribeA();
    store.signIn(sessionResponse());

    expect(a).not.toHaveBeenCalled();
    expect(b).toHaveBeenCalledTimes(1);
  });
});
