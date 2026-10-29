import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { AppProviders } from "../../App.tsx";
import { createAppServices } from "../../app/services.ts";
import type { ProfileDetail, ProfileSummary } from "../../api/types.ts";
import { FakeEventSource, sessionResponse, settings } from "../../test/fixtures.ts";
import { ProfilesPage } from "./ProfilesPage.tsx";
import { lineExcerpt, profileErrorText } from "./model.ts";
import { ApiError } from "../../api/client.ts";

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const BUILTIN: ProfileSummary = {
  key: "rfc1628-generic",
  name: "RFC 1628",
  protocol: "snmp",
  version: 1,
  builtin: true,
};
const CUSTOM: ProfileSummary = {
  key: "lab-snmp",
  name: "实验室 SNMP",
  protocol: "snmp",
  version: 2,
  builtin: false,
};

function detail(p: ProfileSummary): ProfileDetail {
  return { ...p, content: `schema: 1\nkey: ${p.key}\n` };
}

interface Call {
  method: string;
  path: string;
  body: unknown;
}

let calls: Call[];
let routes: Record<string, (method: string, body: unknown) => Response>;

beforeEach(() => {
  FakeEventSource.reset();
  calls = [];
  routes = {
    "/api/v1/auth/session": () => json(200, sessionResponse()),
    "/api/v1/settings": () => json(200, settings()),
    "/api/v1/profiles": () => json(200, { items: [BUILTIN, CUSTOM] }),
    [`/api/v1/profiles/${BUILTIN.key}`]: () => json(200, detail(BUILTIN)),
    [`/api/v1/profiles/${CUSTOM.key}`]: () => json(200, detail(CUSTOM)),
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      const url = new URL(raw, "http://localhost");
      const method = (init?.method ?? "GET").toUpperCase();
      const body = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
      calls.push({ method, path: url.pathname, body });
      const handler = routes[url.pathname];
      return handler
        ? handler(method, body)
        : json(404, { code: "not_found", message: url.pathname });
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function writes(method: string, path: string): Call[] {
  return calls.filter((c) => c.method === method && c.path === path);
}

function CurrentLocation() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname + location.search}</output>;
}

function renderPage(path = "/profiles") {
  const services = createAppServices({ createSource: (url) => new FakeEventSource(url) });
  render(
    <AppProviders services={services}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/profiles" element={<ProfilesPage />} />
        </Routes>
        <CurrentLocation />
      </MemoryRouter>
    </AppProviders>,
  );
}

describe("Profile 模型", () => {
  it("422 profile_invalid 拼接行号与受限路径，409 区分内置与冲突", () => {
    const invalid = new ApiError({
      status: 422,
      code: "profile_invalid",
      serverMessage: "x",
      details: { line: 3, path: "fields[1]" },
    });
    expect(profileErrorText(invalid)).toBe("解析或语义校验失败（第 3 行 · fields 第 2 项）");
    expect(
      profileErrorText(new ApiError({ status: 409, code: "builtin_readonly", serverMessage: "x" })),
    ).toContain("内置");
    expect(lineExcerpt("a\nbb\nccc", 2)).toBe("bb");
    expect(lineExcerpt("x".repeat(200), 1, 10)).toBe(`${"x".repeat(10)}…`);
  });
});

describe("Profile 页（F3-4）", () => {
  it("列表分组显示，内置只读不提供删除，导出走下载链接", async () => {
    renderPage();
    const card = await screen.findByRole("region", { name: `Profile ${BUILTIN.key}` });
    expect(within(card).getByText("内置只读")).toBeTruthy();
    expect(within(card).queryByRole("button", { name: "删除" })).toBeNull();
    const exportLink = within(card).getByRole("link", { name: "导出" });
    expect(exportLink.getAttribute("href")).toBe(`/api/v1/profiles/${BUILTIN.key}/export`);
    expect(exportLink.hasAttribute("download")).toBe(true);
  });

  it("导入先校验，422 时显示出错行号与本地摘录，不导入", async () => {
    routes["/api/v1/profiles/validate"] = () =>
      json(422, {
        code: "profile_invalid",
        message: "invalid",
        details: { line: 2, path: "key" },
      });
    const user = userEvent.setup();
    renderPage();
    await screen.findByRole("region", { name: `Profile ${BUILTIN.key}` });
    await user.click(screen.getByRole("button", { name: "导入" }));
    const sheet = await screen.findByRole("dialog");
    // userEvent.type 会把花括号/方括号当特殊键，这里只用普通文本。
    await user.type(within(sheet).getByLabelText("Profile 声明"), "schema: 1\nkey: BAD KEY");
    await user.click(within(sheet).getByRole("button", { name: "校验" }));

    const alert = await within(sheet).findByRole("alert");
    expect(alert.textContent).toContain("第 2 行有误");
    expect(alert.textContent).toContain("key: BAD KEY");
    expect(within(sheet).getByRole("button", { name: "定位到第 2 行" })).toBeTruthy();
    expect(writes("POST", "/api/v1/profiles/validate")[0].body).toEqual({
      content: "schema: 1\nkey: BAD KEY",
    });
    expect(writes("POST", "/api/v1/profiles")).toHaveLength(0);
  });

  it("导入成功后选中新 Profile", async () => {
    const created = { ...CUSTOM, key: "new-snmp", version: 1 };
    routes["/api/v1/profiles"] = (method) =>
      method === "POST"
        ? json(201, detail(created))
        : json(200, { items: [BUILTIN, CUSTOM, created] });
    routes["/api/v1/profiles/new-snmp"] = () => json(200, detail(created));
    const user = userEvent.setup();
    renderPage();
    await screen.findByRole("region", { name: `Profile ${BUILTIN.key}` });
    await user.click(screen.getByRole("button", { name: "导入" }));
    const sheet = await screen.findByRole("dialog");
    await user.type(within(sheet).getByLabelText("Profile 声明"), "key: new-snmp");
    await user.click(within(sheet).getByRole("button", { name: "导入" }));

    await waitFor(() =>
      expect(screen.getByTestId("location").textContent).toBe("/profiles?key=new-snmp"),
    );
    expect(await screen.findByRole("region", { name: "Profile new-snmp" })).toBeTruthy();
  });

  it("删除需确认，发送 confirm，被引用（409）时提示而不关闭", async () => {
    routes[`/api/v1/profiles/${CUSTOM.key}`] = (method) =>
      method === "DELETE"
        ? json(409, { code: "resource_conflict", message: "in use" })
        : json(200, detail(CUSTOM));
    const user = userEvent.setup();
    renderPage(`/profiles?key=${CUSTOM.key}`);
    const card = await screen.findByRole("region", { name: `Profile ${CUSTOM.key}` });
    await user.click(within(card).getByRole("button", { name: "删除" }));
    expect(writes("DELETE", `/api/v1/profiles/${CUSTOM.key}`)).toHaveLength(0);

    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "删除" }));
    expect((await within(dialog).findByRole("alert")).textContent).toContain(
      "仍有数据源使用这个 Profile",
    );
    expect(within(dialog).queryByRole("button", { name: "改为停用" })).toBeNull();
    expect(writes("DELETE", `/api/v1/profiles/${CUSTOM.key}`)[0].body).toEqual({ confirm: true });
  });

  it("复制 key 冲突时在对话框内提示", async () => {
    routes[`/api/v1/profiles/${BUILTIN.key}/copy`] = () =>
      json(409, { code: "resource_conflict", message: "exists" });
    const user = userEvent.setup();
    renderPage();
    const card = await screen.findByRole("region", { name: `Profile ${BUILTIN.key}` });
    await user.click(within(card).getByRole("button", { name: "复制" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "复制" }));

    expect((await within(dialog).findByRole("alert")).textContent).toContain("该 key 已存在");
    expect(writes("POST", `/api/v1/profiles/${BUILTIN.key}/copy`)[0].body).toEqual({
      key: `${BUILTIN.key}-custom`,
      name: "RFC 1628（副本）",
    });
  });
});
