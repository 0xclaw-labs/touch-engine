import { expect, test } from "@playwright/test";
import { login, seedCodes } from "../support/helpers";

function localInput(offsetHours: number): string {
  const time = new Date(Date.now() + offsetHours * 3600_000);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${time.getFullYear()}-${pad(time.getMonth() + 1)}-${pad(time.getDate())}T${pad(time.getHours())}:${pad(time.getMinutes())}`;
}

test("merchant creates, publishes and shares a campaign through one normal path", async ({ page }) => {
  await login(page);
  const seed = await seedCodes();
  const title = `E2E 创建 ${Date.now()}`;

  await page.goto("/work/campaigns/new");
  await expect(page.getByRole("heading", { name: "新建活动" })).toBeVisible();
  await page.getByLabel("门店").selectOption(seed.storeId);
  await page.getByLabel("活动标题").fill(title);
  await page.getByLabel("顾客能看到的内容").fill("这是创建流真实创建的内容。");
  await page.getByLabel("开始时间（本地时间）").fill(localInput(-1));
  await page.getByLabel("结束时间（本地时间）").fill(localInput(24));

  // Draft survives a reload before the server draft is saved.
  await page.reload();
  await expect(page.getByTestId("draft-restored")).toBeVisible();
  await expect(page.getByLabel("活动标题")).toHaveValue(title);

  await page.getByRole("button", { name: "保存草稿并继续" }).click();
  await expect(page.getByRole("heading", { name: "② 内容与素材" })).toBeVisible({ timeout: 15_000 });
  await expect(page.locator("#content-packs")).toBeVisible();
  await page.getByRole("button", { name: "继续：链接与发布" }).click();
  await expect(page.getByRole("heading", { name: "③ 链接与发布" })).toBeVisible();

  await page.getByRole("button", { name: "生成链接并发布" }).click();
  await expect(page).toHaveURL(/\/work\/campaigns\/cmp_[^?]+\?created=1/, { timeout: 15_000 });
  await expect(page.getByTestId("publish-notice")).toContainText("活动已发布");
  await expect(page.getByTestId("campaign-status")).toContainText("进行中");
  const shareUrl = await page.getByTestId("share-url").innerText();
  expect(shareUrl).toMatch(/\/c\/[A-Z0-9]{12}$/);

  const publicPath = new URL(shareUrl).pathname;
  await page.goto(publicPath);
  const main = page.locator('[data-testid="public-activity"]');
  await expect(main).toHaveAttribute("data-state", "available");
  await expect(main).toContainText(title);
  await expect(page.locator('[data-testid="eco-top-nav"]')).toHaveCount(0);
});
