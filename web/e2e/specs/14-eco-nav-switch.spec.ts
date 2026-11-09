import { expect, test, type Page } from "@playwright/test";
import { login, seedCodes } from "../support/helpers";

// HUI-2222 两阶段跳转:mint 只回短时单用途凭据(jump_token),launch_url 只在
// redeem 一次成功时出现;同凭据二次兑换、错形态、跨 principal/跨组织出示一律
// 拒绝。全链走真实 HTTP(identity stub shell mint → go grants → BFF 透传)。

async function mint(page: Page, tenantId: string, body: Record<string, unknown>) {
  return page.evaluate(async ({ tenantId, body }) => {
    const res = await fetch("/api/eco-nav/switch", {
      method: "POST",
      headers: { "content-type": "application/json", "x-tenant-id": tenantId },
      body: JSON.stringify(body),
    });
    return { status: res.status, body: await res.json() };
  }, { tenantId, body });
}

async function redeem(page: Page, tenantId: string, jumpToken: string) {
  return page.evaluate(async ({ tenantId, jumpToken }) => {
    const res = await fetch("/api/eco-nav/switch/redeem", {
      method: "POST",
      headers: { "content-type": "application/json", "x-tenant-id": tenantId },
      body: JSON.stringify({ jump_token: jumpToken }),
    });
    return { status: res.status, body: await res.json() };
  }, { tenantId, jumpToken });
}

/** 在根页登录(可指定凭据);多组织 principal 自动选中记忆租户。 */
async function loginAtRoot(page: Page, email: string, password: string): Promise<void> {
  await page.goto("/");
  await page.getByLabel("用户名").fill(email);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录" }).click();
}

test("authenticated app switch is two-phase: mint a token, redeem the launch_url once", async ({ page }) => {
  await login(page);
  const seed = await seedCodes();

  const minted = await mint(page, seed.tenantId, { app_id: "goboost", launch_target_id: "ti-goboost-web" });
  expect(minted.status).toBe(200);
  expect(typeof minted.body.jump_token).toBe("string");
  expect(String(minted.body.jump_token)).toMatch(/^jtg_/);
  expect(JSON.stringify(minted.body)).not.toContain("launch_url");
  expect(JSON.stringify(minted.body)).not.toContain("goboost.modelxing.com");

  const first = await redeem(page, seed.tenantId, String(minted.body.jump_token));
  expect(first.status).toBe(200);
  expect(first.body.launch_url).toBe("https://goboost.modelxing.com/projects?code=e2e-one-time");

  // 单用途:同凭据第二次兑换拒绝,且不再交还 URL。
  const second = await redeem(page, seed.tenantId, String(minted.body.jump_token));
  expect(second.status).toBe(404);
  expect(JSON.stringify(second.body)).not.toContain("launch_url");
});

test("malformed jump tokens are uniformly refused without leaking a URL", async ({ page }) => {
  await login(page);
  const seed = await seedCodes();
  const malformed = await redeem(page, seed.tenantId, "jtg_garbage.aa");
  expect(malformed.status).toBe(404);
  expect(JSON.stringify(malformed.body)).not.toContain("launch_url");
});

test("a grant bound to one principal cannot be redeemed by another member of the same org", async ({ page }) => {
  const seed = await seedCodes();
  // owner 在 A mint。
  await login(page);
  const minted = await mint(page, seed.tenantId, { app_id: "goboost", launch_target_id: "ti-goboost-web" });
  expect(minted.status).toBe(200);

  // 换 multi principal(同租户 A 的 staff)出示同一凭据:同形 404。
  // clearCookies 只清会话;组织记忆(touch_admin_tenant)必须显式清掉,
  // 否则 multi 登录会被 remembered=A 自动选中,组织选择器不出现。
  await page.context().clearCookies();
  await page.evaluate(() => window.localStorage.removeItem("touch_admin_tenant"));
  await loginAtRoot(page, seed.multiEmail, seed.multiPassword);
  await page.getByRole("button", { name: seed.tenantName }).click();
  await expect(page.getByRole("heading", { name: "今天", exact: true })).toBeVisible();
  const stolen = await redeem(page, seed.tenantId, String(minted.body.jump_token));
  expect(stolen.status).toBe(404);
  expect(JSON.stringify(stolen.body)).not.toContain("launch_url");
  // 「被拒绝的出示不烧凭据」由 appjump 包级并发/拒兑测试覆盖(60s TTL 下
  // 跨两次登录再兑换在 e2e 里不可确定性完成,不在浏览器层重复)。
});

test("redeeming with a tenant the session has no membership in is refused at the gate", async ({ page }) => {
  await login(page);
  const seed = await seedCodes();
  const minted = await mint(page, seed.tenantId, { app_id: "goboost", launch_target_id: "ti-goboost-web" });
  expect(minted.status).toBe(200);
  // owner 会话带 B 的租户头:owner 在 B 无成员关系,requireSession 先拒绝。
  const foreign = await redeem(page, seed.tenantBId, String(minted.body.jump_token));
  expect(foreign.status).toBe(403);
  expect(JSON.stringify(foreign.body)).not.toContain("launch_url");
});

test("forged switch payloads are still refused before any credential exists", async ({ page }) => {
  await login(page);
  const seed = await seedCodes();
  const forged = await mint(page, seed.tenantId, {
    app_id: "goboost",
    launch_target_id: "ti-goboost-web",
    campaign_id: "cmp_x",
  });
  expect(forged.status).toBe(400);
  expect(JSON.stringify(forged.body)).not.toContain("jump_token");
});

test("without the public-ai nav authority the merchant page shows no fixture brand or nav", async ({ page }) => {
  await login(page);
  await page.goto("/work/campaigns");
  await expect(page.getByRole("heading", { name: "活动" })).toBeVisible();
  await expect(page.locator('[data-testid="eco-top-nav"]')).toHaveCount(0);
  const text = await page.locator("body").innerText();
  expect(text).not.toContain("数海观澜");
  expect(text).not.toContain("客户甲");
  expect(text).not.toContain("预览上下文");
});
