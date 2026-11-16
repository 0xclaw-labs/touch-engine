// @vitest-environment jsdom

import * as React from "react";
import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterAll, afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// The real membership reader captures fetch when its module loads. Keep one
// transport boundary for it and the real session/API hooks throughout the test.
const { fetchMock, router } = vi.hoisted(() => {
  const fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
  return { fetchMock, router: { replace: vi.fn() } };
});
vi.mock("next/navigation", () => ({
  useRouter: () => router,
  usePathname: () => "/ops/tenants",
}));

import { OpsGate } from "../src/components/ops/ops-shell";
import { TenantsDesk } from "../src/components/ops/tenants-desk";
import { PlansDesk } from "../src/components/ops/plans-desk";
import { OrdersDesk } from "../src/components/ops/orders-desk";
import { ChannelsDesk } from "../src/components/ops/channels-desk";
import { TemplatesDesk } from "../src/components/ops/templates-desk";
import { OperationsDesk } from "../src/components/ops/operations-desk";
import { SSO_ACCOUNT_EPOCH_KEY } from "../src/lib/sso-workspace-reset";
import { TENANT_STORAGE_KEY, type MembershipScope } from "../src/lib/workspace-tenant";

const tenantA: MembershipScope = {
  tenant_id: "tenant-a", display_name: "一店组织", role: "org_owner", enabled: true, source: "membership",
};
const tenantB: MembershipScope = {
  tenant_id: "tenant-b", display_name: "二店组织", role: "org_owner", enabled: true, source: "membership",
};
const opsGrant = {
  ops_permissions: ["tenants", "plans", "orders", "channels", "templates", "operations"]
    .map((name) => ({ command: `ops:${name}.read` })),
};

function json(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), { status, headers: { "content-type": "application/json" } });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

type RequestSeen = { path: string; method: string; tenant: string | null };
let requests: RequestSeen[];
let memberships: () => Response | Promise<Response>;
let access: () => Response | Promise<Response>;
let principal: string;
let root: Root;
let container: HTMLDivElement;

const businessData: Record<string, unknown> = {
  "/api/ops/tenants": { tenants: [] },
  "/api/ops/support-access": { support_access: [] },
  "/api/ops/plans": { plans: [] },
  "/api/ops/orders": { orders: [], refunds: [], ai_usage: [], discrepancy_queue: [] },
  "/api/ops/channels": { records: [] },
  "/api/ops/templates": { templates: [], content_versions: [], assignments: [] },
  "/api/ops/operations": {
    domain_health: { status: "degraded", targets: [] },
    hardware_fulfillment: { state: "unavailable", source: "billing", orders: [] },
    touchpoint_inventory: {}, tickets: [], drills: [], recent_operations: [],
  },
  "/api/ops/audit": { audit: [] },
};

beforeEach(() => {
  vi.stubGlobal("React", React);
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  window.localStorage.clear();
  document.cookie = "touch_sso_reset=; Path=/; Max-Age=0";
  router.replace.mockClear();
  requests = [];
  principal = "principal-a";
  memberships = () => json({ items: [tenantA] });
  access = () => json(opsGrant);
  fetchMock.mockReset().mockImplementation(async (input: string, init?: RequestInit) => {
    const path = new URL(input, window.location.href).pathname;
    const tenant = new Headers(init?.headers).get("x-tenant-id");
    requests.push({ path, method: init?.method ?? "GET", tenant });
    if (path === "/api/session/memberships") return memberships();
    if (path === "/api/auth/refresh") return json({}, 401);
    if (path === "/api/whoami") {
      return json({
        principal_ref: principal, tenant_id: tenant, enabled: true, role: "org_owner",
        // Account changes must not rely on this deliberately identical masked email.
        email: "o***@example.test", tenant_name: tenant === "tenant-b" ? "二店组织" : "一店组织",
      });
    }
    if (path === "/api/access/effective") return access();
    if (Object.hasOwn(businessData, path)) return json(businessData[path]);
    throw new Error(`Unexpected request: ${path}`);
  });
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
});
afterAll(() => vi.unstubAllGlobals());

async function render(child = createElement("section", { "data-sensitive": "ops" }, "运营业务")) {
  await act(async () => { root.render(createElement(OpsGate, null, child)); });
}

async function clickButton(label: string) {
  const button = Array.from(container.querySelectorAll("button"))
    .find((item) => item.textContent?.trim() === label);
  expect(button, `按钮应可见：${label}`).toBeDefined();
  await act(async () => { button!.click(); });
}

async function changeAccount() {
  principal = "principal-b";
  await act(async () => {
    window.localStorage.setItem(SSO_ACCOUNT_EPOCH_KEY, "account-b");
    window.dispatchEvent(new Event("focus"));
  });
}

function sensitive() { return container.querySelector("[data-sensitive=ops]"); }
function requestsTo(path: string) { return requests.filter((item) => item.path === path); }

describe("运营入口复用真实会话边界", () => {
  it("两个有效组织无记忆时在当前页选择组织，并以选中组织核对授权", async () => {
    memberships = () => json({ items: [tenantA, tenantB] });
    await render();
    expect(container.textContent).toContain("选择组织");
    expect(requestsTo("/api/access/effective")).toHaveLength(0);
    await clickButton("二店组织");
    expect(sensitive()).not.toBeNull();
    expect(requestsTo("/api/access/effective").map((item) => item.tenant)).toEqual(["tenant-b"]);
    expect(window.localStorage.getItem(TENANT_STORAGE_KEY)).toBe("tenant-b");
    expect(router.replace).not.toHaveBeenCalled();
  });

  it("匿名态显示原有登录表单，不把用户导向不存在的工作台", async () => {
    memberships = () => json({}, 401);
    await render();
    expect(container.querySelector('input[type="password"]')).not.toBeNull();
    expect(requestsTo("/api/auth/refresh")).toHaveLength(1);
    expect(requestsTo("/api/access/effective")).toHaveLength(0);
    expect(container.textContent).not.toContain("/work");
  });

  it("无组织时保留原有空态，不误报未登录", async () => {
    memberships = () => json({ items: [] });
    await render();
    expect(container.textContent).toContain("当前账号没有可进入的组织");
    expect(container.querySelector('input[type="password"]')).toBeNull();
    expect(requestsTo("/api/access/effective")).toHaveLength(0);
  });

  it("会话服务失败后可从原页重试并进入运营台", async () => {
    memberships = () => json({}, 503);
    await render();
    expect(container.textContent).toContain("没有连上服务");
    memberships = () => json({ items: [tenantA] });
    await clickButton("重试");
    expect(sensitive()).not.toBeNull();
    expect(requestsTo("/api/session/memberships")).toHaveLength(2);
  });
});

describe("运营授权与当前会话绑定", () => {
  it("同一组织换号后旧成功授权迟到，不能覆盖新账号的无权结果", async () => {
    const oldGrant = deferred<Response>();
    access = () => oldGrant.promise;
    await render();
    expect(sensitive()).toBeNull();
    access = () => json({ ops_permissions: [] });
    await changeAccount();
    expect(container.querySelector('[data-state="restricted"]')).not.toBeNull();
    await act(async () => { oldGrant.resolve(json(opsGrant)); });
    expect(sensitive()).toBeNull();
    expect(container.querySelector('[data-state="restricted"]')).not.toBeNull();
    expect(requestsTo("/api/access/effective").map((item) => item.tenant)).toEqual(["tenant-a", "tenant-a"]);
  });

  it("换号发现新组织时先卸载旧业务，新授权待定和无权均不沿用旧授权", async () => {
    await render();
    expect(sensitive()).not.toBeNull();
    const nextScopes = deferred<Response>();
    const nextGrant = deferred<Response>();
    memberships = () => nextScopes.promise;
    access = () => nextGrant.promise;
    await changeAccount();
    expect(sensitive()).toBeNull();
    await act(async () => { nextScopes.resolve(json({ items: [tenantB] })); });
    expect(requestsTo("/api/access/effective").at(-1)?.tenant).toBe("tenant-b");
    expect(sensitive()).toBeNull();
    await act(async () => { nextGrant.resolve(json({ ops_permissions: [] })); });
    expect(sensitive()).toBeNull();
    expect(container.querySelector('[data-state="restricted"]')).not.toBeNull();
  });

  it.each([200, 403, 404])("商家负责人在授权响应 %s 无 ops grant 时不能挂载业务", async (status) => {
    access = () => json({ ops_permissions: [] }, status);
    await render(createElement(TenantsDesk));
    expect(container.querySelector('[data-state="restricted"]')).not.toBeNull();
    expect(container.querySelector('[data-view="ops-tenants"]')).toBeNull();
    expect(requests.some((item) => item.path.startsWith("/api/ops/"))).toBe(false);
  });

  it.each(["503", "网络断开"])("授权服务%s提供重试，恢复时复用当前会话", async (failure) => {
    access = () => {
      if (failure === "网络断开") throw new TypeError("fetch failed");
      return json({}, 503);
    };
    await render();
    expect(container.querySelector('[data-state="error"]')).not.toBeNull();
    expect(sensitive()).toBeNull();
    access = () => json(opsGrant);
    await clickButton("重试授权");
    expect(sensitive()).not.toBeNull();
    expect(requestsTo("/api/session/memberships")).toHaveLength(1);
    expect(requestsTo("/api/whoami")).toHaveLength(1);
    expect(requestsTo("/api/access/effective")).toHaveLength(2);
  });

  it("授权会话已过期时可重新核对身份，并回到原有登录表单", async () => {
    access = () => json({}, 401);
    await render();
    expect(sensitive()).toBeNull();
    memberships = () => json({}, 401);
    await clickButton("重新核对会话");
    expect(container.querySelector('input[type="password"]')).not.toBeNull();
    expect(requestsTo("/api/session/memberships")).toHaveLength(2);
    expect(requestsTo("/api/auth/refresh")).toHaveLength(1);
    expect(requestsTo("/api/access/effective")).toHaveLength(1);
  });
});

describe("六个运营视图只消费门闸确认的会话", () => {
  it.each([
    ["tenants", TenantsDesk], ["plans", PlansDesk], ["orders", OrdersDesk],
    ["channels", ChannelsDesk], ["templates", TemplatesDesk], ["operations", OperationsDesk],
  ] as const)("%s 视图不另读身份，也不会以空组织读取业务", async (view, Desk) => {
    await render(createElement(Desk));
    expect(container.querySelector(`[data-view="ops-${view}"]`)).not.toBeNull();
    const businessRequests = requests.filter((item) => item.path.startsWith("/api/ops/"));
    expect(businessRequests.length).toBeGreaterThan(0);
    expect(businessRequests.every((item) => item.tenant === "tenant-a")).toBe(true);
    expect(requestsTo("/api/session/memberships")).toHaveLength(1);
    expect(requestsTo("/api/whoami")).toHaveLength(1);
    expect(requestsTo("/api/access/effective")).toHaveLength(1);
  });
});

it.each(["无权账号", "有权账号", "另一商户"])("旧支持访问读取在切到%s后返回，不能继续发出关闭请求", async (next) => {
  const delayed = deferred<Response>();
  const base = fetchMock.getMockImplementation()!;
  let detailCalls = 0;
  let sentClose = 0;
  fetchMock.mockImplementation(async (input: string, init?: RequestInit) => {
    const path = new URL(input, window.location.href).pathname;
    if (path === "/api/ops/tenants") return json({ tenants: [
      { id: "target", name: "目标商户", lifecycle: "active", store_count: 0, member_count: 0 },
      { id: "other", name: "另一商户", lifecycle: "active", store_count: 0, member_count: 0 },
    ] });
    if (path === "/api/ops/tenants/target" && ++detailCalls === 2) return delayed.promise;
    if (path.startsWith("/api/ops/tenants/")) return json({ tenant: { id: path.endsWith("other") ? "other" : "target", name: "商户", lifecycle: "active" }, subscription: { available: false }, business_data: { state: "locked", message: "锁定" }, ops_revision: 1 });
    if (path === "/api/ops/support-access/grant-a/close") { sentClose++; return json({}); }
    return base(input, init);
  });
  await render(createElement(TenantsDesk));
  await clickButton("目标商户");
  await clickButton("结束支持访问");
  if (next === "另一商户") {
    await clickButton("另一商户");
    expect(container.querySelector('[data-detail-tenant="other"]')).not.toBeNull();
  } else {
    access = () => json(next === "有权账号" ? opsGrant : { ops_permissions: [] });
    await changeAccount();
    if (next === "无权账号") expect(container.querySelector('[data-state="restricted"]')).not.toBeNull();
    else expect(container.querySelector('[data-view="ops-tenants"]')).not.toBeNull();
  }
  await act(async () => { delayed.resolve(json({ business_data: { available: true, state: "open", support_access_ref: "grant-a" } })); });
  expect(sentClose).toBe(0);
});

it.each([true, false])("结束支持访问只操作服务端确认的当前主体有效授权（有效=%s）", async (active) => {
  const base = fetchMock.getMockImplementation()!;
  const closed: string[] = [];
  fetchMock.mockImplementation(async (input: string, init?: RequestInit) => {
    const path = new URL(input, window.location.href).pathname;
    if (path === "/api/ops/tenants") return json({ tenants: [{ id: "target", name: "目标商户", lifecycle: "active", store_count: 0, member_count: 0 }] });
    if (path === "/api/ops/tenants/target") return json({
      tenant: { id: "target", name: "目标商户", lifecycle: "active" }, subscription: { available: false },
      business_data: active ? { state: "open", available: true, support_access_ref: "mine-current" } : { state: "locked", available: false }, ops_revision: 1,
    });
    if (path === "/api/ops/support-access") return json({ support_access: [
      { id: "other-operator", tenant_id: "target", principal_ref: "another-person", closed_at: "", expires_at: "2099-01-01T00:00:00Z" },
      { id: "mine-expired", tenant_id: "target", principal_ref: "principal-a", closed_at: "", expires_at: "2020-01-01T00:00:00Z" },
    ] });
    if (path.endsWith("/close")) {
      expect(JSON.parse(String(init?.body))).toMatchObject({ own_access_only: true });
      closed.push(path); return json({});
    }
    return base(input, init);
  });
  await render(createElement(TenantsDesk));
  await clickButton("目标商户");
  await clickButton("结束支持访问");
  expect(closed).toEqual(active ? ["/api/ops/support-access/mine-current/close"] : []);
  if (!active) expect(container.textContent).toContain("没有可关闭的活跃支持访问");
});

it("核对当前支持访问失败时不写入，恢复后可在原按钮重试", async () => {
  const base = fetchMock.getMockImplementation()!;
  let details = 0;
  let closed = 0;
  fetchMock.mockImplementation(async (input: string, init?: RequestInit) => {
    const path = new URL(input, window.location.href).pathname;
    if (path === "/api/ops/tenants") return json({ tenants: [{ id: "target", name: "目标商户", lifecycle: "active", store_count: 0, member_count: 0 }] });
    if (path === "/api/ops/tenants/target") {
      if (++details === 2) return json({}, 503);
      return json({ tenant: { id: "target", name: "目标商户", lifecycle: "active" }, subscription: { available: false }, business_data: { available: true, state: "open", support_access_ref: "mine" }, ops_revision: 1 });
    }
    if (path.endsWith("/close")) { closed++; return json({}); }
    return base(input, init);
  });
  await render(createElement(TenantsDesk));
  await clickButton("目标商户");
  await clickButton("结束支持访问");
  expect(closed).toBe(0);
  expect(container.textContent).toContain("没有读到当前支持访问，请重试");
  expect(container.querySelector<HTMLButtonElement>('[data-action="close-support-access"]')?.disabled).toBe(false);
  await clickButton("结束支持访问");
  expect(closed).toBe(1);
  expect(container.textContent).not.toContain("没有读到当前支持访问，请重试");
  expect(container.querySelector('[data-field="support-result"]')?.textContent).toContain("支持访问已结束");
});
