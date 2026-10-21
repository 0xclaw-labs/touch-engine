import { expect, test, type Page } from "@playwright/test";
import { login, screenshot, seedCodes } from "../support/helpers";

// HUI-2222 受控登录链正向验证（本地 harness：identity stub + go + next，全真实 HTTP）。
// 覆盖：匿名落地 / 已登录落到今日台 / 多组织选择器 / 无权限文案 / 会话过期重登 /
// 切组织清残留 / 手工切应用不自动建活动 / 确认与核销入口（FEATURE_SERVICE_DESK=on + staff）。
// 不让任何场景手填 tenant_id（memberships 端点忽略手填编号，此处只点组织名称）。

async function loginAtRoot(page: Page, email: string, password: string): Promise<void> {
  await page.goto("/");
  await page.getByLabel("用户名").fill(email);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录" }).click();
}

async function campaignCount(page: Page): Promise<number> {
  // 商家面 API 要求 x-tenant-id（会话租户，取自受控会话记忆，不手填）。
  return page.evaluate(async () => {
    const tenant = window.localStorage.getItem("touch_admin_tenant") ?? "";
    const res = await fetch("/api/campaigns", { headers: { "x-tenant-id": tenant } });
    const data = (await res.json()) as { items?: unknown[] };
    return Array.isArray(data.items) ? data.items.length : -1;
  });
}

test("anonymous visitor at / sees the login page, not the today desk", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "今天要做的事" })).toBeVisible();
  await expect(page.getByLabel("用户名")).toBeVisible();
  await expect(page.getByRole("heading", { name: "今天", exact: true })).toHaveCount(0);
  await screenshot(page, "hui-2222-anonymous-root");
});

test("logged-in merchant lands on the real today desk; /admin bookmark still works", async ({ page }) => {
  await login(page); // /admin 登录表单（旧书签路径）
  await expect(page.locator("main.tk-admin-shell")).toBeVisible();
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "今天", exact: true })).toBeVisible();
  // 种子活动真实可见（active 场）
  const list = page.locator("ol.tk-list li");
  await expect(list.first()).toBeVisible({ timeout: 15_000 });
  await expect(list.getByText(/今天进行中：E2E 进行中活动/)).toBeVisible();
  await screenshot(page, "hui-2222-owner-today-desk");
});

test("multi-org member chooses by org name and staff sees the service desk entry", async ({ page }) => {
  const seed = await seedCodes();
  await loginAtRoot(page, seed.multiEmail, seed.multiPassword);
  // 组织选择器：只列组织名称，不出现任何编号输入
  await expect(page.getByRole("heading", { name: "选择组织" })).toBeVisible();
  await expect(page.getByText("用组织名称进入。不需要填写编号。")).toBeVisible();
  await expect(page.locator("input")).toHaveCount(0);
  await expect(page.getByRole("button", { name: seed.tenantName })).toBeVisible();
  await expect(page.getByRole("button", { name: seed.tenantBName })).toBeVisible();
  await screenshot(page, "hui-2222-org-chooser");

  // 选 A（staff 角色）：今日台 + 确认与核销入口（开关 on + 核销角色）
  await page.getByRole("button", { name: seed.tenantName }).click();
  await expect(page.getByRole("heading", { name: "今天", exact: true })).toBeVisible();
  await expect(page.locator("p.tk-kicker")).toHaveText(seed.tenantName);
  const deskEntry = page.locator('ul.tk-nav a[href="/work/service"]');
  await expect(deskEntry).toHaveText("确认与核销");
  await screenshot(page, "hui-2222-staff-service-desk-entry");
});

test("switching org clears the previous org's campaigns and responses", async ({ page }) => {
  const seed = await seedCodes();
  await loginAtRoot(page, seed.multiEmail, seed.multiPassword);
  await page.getByRole("button", { name: seed.tenantName }).click();
  await expect(page.getByRole("heading", { name: "今天", exact: true })).toBeVisible();
  const list = page.locator("ol.tk-list li");
  await expect(list.first()).toBeVisible({ timeout: 15_000 });

  // 记忆切到 B 并重载：解析走 remembered 命中路径，不手填编号
  await page.evaluate((tenantB) => window.localStorage.setItem("touch_admin_tenant", tenantB), seed.tenantBId);
  await page.reload();
  await expect(page.locator("p.tk-kicker")).toHaveText(seed.tenantBName, { timeout: 15_000 });
  // 上一户的活动标题与数据不残留（B 无任何种子数据）
  await expect(page.getByText("E2E 进行中活动")).toHaveCount(0);
  expect(await campaignCount(page)).toBe(0);
  await screenshot(page, "hui-2222-switched-to-org-b");
});

test("principal without memberships gets a clear no-org message, never a tenant id field", async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("用户名").fill("nobody@e2e.test");
  await page.getByLabel("密码", { exact: true }).fill("e2e-pass-nobody");
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByText("当前账号没有可进入的组织。")).toBeVisible({ timeout: 15_000 });
  await expect(page.locator('input[placeholder*="租户"], input[name="tenant"]')).toHaveCount(0);
  await screenshot(page, "hui-2222-no-membership");
});

test("expired/lost session returns to login; signing in again recovers", async ({ page }) => {
  await login(page);
  await page.context().clearCookies(); // 会话失效等价扰动（stub 无 TTL 注入口）
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "今天要做的事" })).toBeVisible();
  const seed = await seedCodes();
  await page.getByLabel("用户名").fill(seed.email);
  await page.getByLabel("密码", { exact: true }).fill(seed.password);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "今天", exact: true })).toBeVisible();
});

test("manual app switch in eco nav does not create campaigns or tasks", async ({ page }) => {  await login(page);
  await page.goto("/work/stores");
  await expect(page.locator("p.tk-kicker")).toHaveText("磁石科技", { timeout: 15_000 });
  const before = await campaignCount(page);
  expect(before).toBeGreaterThan(0);

  // 切应用是导航离开本应用（登记 URL），不是在本应用建活动/任务。
  // 拦截外域导航（harness 不打真实生态站），点击前在捕获阶段 preventDefault
  // （导航被取消、点击处理器仍触发），断言：
  //   1) 应用链接 href 是登记 https 地址且不带 campaign_id/handoff_id；
  //   2) data-creates-handoff="false"；3) 活动计数不变、仍在本页。
  const external: string[] = [];
  await page.route((u) => !u.href.startsWith("http://127.0.0.1:18462"), (route) => {
    external.push(route.request().url());
    return route.abort();
  });
  const appLink = page.locator('a[data-testid^="eco-app-"]').first();
  if ((await appLink.count()) > 0) {
    expect(await appLink.getAttribute("data-creates-handoff")).toBe("false");
    const href = await appLink.getAttribute("href");
    expect(href).toMatch(/^https:\/\//);
    expect(new URL(href ?? "").search).not.toMatch(/campaign_id=|handoff_id=/);
    await appLink.evaluate((el) => el.addEventListener("click", (e) => e.preventDefault(), { capture: true, once: true }));
    await appLink.click();
    await page.waitForTimeout(500);
    expect(page.url()).toContain("/work/stores");
    expect(external).toEqual([]);
    expect(await campaignCount(page)).toBe(before);
  }
  await page.unrouteAll({ behavior: "ignoreErrors" });
});

test("confirm-and-redeem entry stays findable at 390 and 1440 widths", async ({ page }) => {
  const seed = await seedCodes();
  await page.setViewportSize({ width: 390, height: 844 });
  await loginAtRoot(page, seed.multiEmail, seed.multiPassword);
  await page.getByRole("button", { name: seed.tenantName }).click();
  await expect(page.getByRole("heading", { name: "今天", exact: true })).toBeVisible();
  const entry = page.locator('ul.tk-nav a[href="/work/service"]');
  await expect(entry).toBeVisible();
  await screenshot(page, "hui-2222-entry-390");
  await page.setViewportSize({ width: 1440, height: 900 });
  await expect(entry).toBeVisible();
});
