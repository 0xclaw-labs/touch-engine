import { expect, test } from "@playwright/test";
import { login, seedCodes } from "../support/helpers";

test("authenticated app switch goes through the BFF and returns a validated launch_url", async ({ page }) => {
  await login(page);
  const seed = await seedCodes();
  const result = await page.evaluate(async ({ tenantId }) => {
    const res = await fetch("/api/eco-nav/switch", {
      method: "POST",
      headers: { "content-type": "application/json", "x-tenant-id": tenantId },
      body: JSON.stringify({ app_id: "goboost", launch_target_id: "ti-goboost-web" }),
    });
    return { status: res.status, body: await res.json() };
  }, { tenantId: seed.tenantId });
  expect(result.status).toBe(200);
  expect(result.body.launch_url).toBe("https://goboost.modelxing.com/projects?code=e2e-one-time");

  const forged = await page.evaluate(async ({ tenantId }) => {
    const res = await fetch("/api/eco-nav/switch", {
      method: "POST",
      headers: { "content-type": "application/json", "x-tenant-id": tenantId },
      body: JSON.stringify({ app_id: "goboost", launch_target_id: "ti-goboost-web", campaign_id: "cmp_x" }),
    });
    return { status: res.status, body: await res.json() };
  }, { tenantId: seed.tenantId });
  expect(forged.status).toBe(400);
  expect(JSON.stringify(forged.body)).not.toContain("launch_url");
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
