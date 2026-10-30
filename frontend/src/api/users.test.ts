import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { isApiError } from "./client.ts";
import { changePassword, createUser, deleteUser, listUsers, renameUser } from "./users.ts";

interface Call {
  method: string;
  url: string;
  body: unknown;
}

function stubFetch(respond: (call: Call) => Response): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      const call: Call = {
        method: init.method ?? "GET",
        url: typeof input === "string" ? input : input instanceof URL ? input.href : input.url,
        body: typeof init.body === "string" ? JSON.parse(init.body) : undefined,
      };
      calls.push(call);
      return respond(call);
    }),
  );
  return calls;
}

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("管理员请求", () => {
  it("删除请求体为 {confirm:true}", async () => {
    const calls = stubFetch(() => json(200, { ok: true }));
    await deleteUser(3);
    expect(calls[0]).toMatchObject({
      method: "DELETE",
      url: "/api/v1/users/3",
      body: { confirm: true },
    });
  });

  it("删除自己 → 422，最后一位管理员 → 409，均抛 ApiError 并保留状态码", async () => {
    stubFetch(() => json(422, { code: "validation_failed", message: "x" }));
    await expect(deleteUser(1)).rejects.toSatisfy((e) => isApiError(e) && e.status === 422);
    vi.unstubAllGlobals();
    stubFetch(() => json(409, { code: "user_conflict", message: "x" }));
    await expect(deleteUser(1)).rejects.toSatisfy((e) => isApiError(e) && e.status === 409);
  });

  it("创建、改名、改密的方法、路径与请求体", async () => {
    const calls = stubFetch(() => json(200, { ok: true }));
    await createUser("ops", "correct-horse");
    await renameUser(2, "oncall");
    await changePassword(2, "new-password-1");
    expect(calls.map((c) => [c.method, c.url, c.body])).toStrictEqual([
      ["POST", "/api/v1/users", { username: "ops", password: "correct-horse" }],
      ["PATCH", "/api/v1/users/2", { username: "oncall" }],
      ["PUT", "/api/v1/users/2/password", { password: "new-password-1" }],
    ]);
  });

  it("listUsers 逐页读取到 next_before=0", async () => {
    const user = (id: number) => ({
      id,
      username: `u${id}`,
      role: "admin",
      created_at: "2026-10-27T06:20:58Z",
      last_login_at: null,
    });
    const calls = stubFetch((call) =>
      call.url.includes("before=2")
        ? json(200, { items: [user(1)], next_before: 0 })
        : json(200, { items: [user(3), user(2)], next_before: 2 }),
    );
    const users = await listUsers();
    expect(users.map((u) => u.id)).toStrictEqual([3, 2, 1]);
    expect(calls).toHaveLength(2);
  });
});
