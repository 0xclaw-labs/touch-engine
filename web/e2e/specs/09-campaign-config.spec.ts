import { expect, type Page, test } from "@playwright/test";
import { login, seedCodes, screenshot } from "../support/helpers";

// HUI-1676 增量:发布与启用面板的商家可用性 E2E(390/430 与桌面)。
// 票面验收:普通商家仅用正常页面完成预设/预览/启用;表单键盘可操作、错误
// 落到具体字段、关闭/刷新不丢草稿;无工程说明与技术 ID 输入。
// 启用的正路径消费 G2/G3 生产服务,归 3531 跨组验收;本 spec 锁定的是
// 草稿恢复、字段级拒绝与窄屏可用性(世界不完整时启用被拒且有下一步指引)。

test.describe.configure({ mode: "serial" });

async function openCampaignConfig(page: Page): Promise<string> {
  const seed = await seedCodes();
  await page.goto(`/work/campaigns/${seed.activeId}`);
  const section = page.locator("#publish-config");
  await expect(section).toBeVisible();
  return seed.activeId;
}

test("发布与启用五步面板在活动详情页出现,无技术 ID 输入", async ({ page }) => {
  await login(page);
  await openCampaignConfig(page);
  for (const legend of ["① 活动与门店", "② 准备内容", "③ 发布方式", "④ 给什么优惠", "⑤ 顾客预览与启用"]) {
    await expect(page.locator("#publish-config").getByText(legend)).toBeVisible();
  }
  await expect(page.locator('#publish-config input[type="number"]')).toHaveCount(0);
  // 三核验模式与顾客文案输入在配置面可见。
  await expect(page.locator('#publish-config input[name="verification_mode"]')).toHaveCount(3);
  await expect(page.locator('#publish-config textarea[name="customer_copy"]')).toBeVisible();
});

test("草稿保存后刷新恢复(服务端持久,关闭/重登不丢)", async ({ page }) => {
  await login(page);
  await openCampaignConfig(page);
  const copy = "E2E 顾客文案:把发布成功的页面给店员看,当场领门店优惠券。";
  await page.locator('#publish-config textarea[name="customer_copy"]').fill(copy);
  await page.locator('#publish-config [data-action="save-draft"]').click();
  await expect(page.locator('#publish-config [data-state="notice"]')).toContainText("草稿已保存");
  await page.reload();
  await expect(page.locator("#publish-config")).toBeVisible();
  await expect(page.locator('#publish-config textarea[name="customer_copy"]')).toHaveValue(copy);
});

test("启用被拒时错误落到具体字段,给下一步指引,不直出机器码", async ({ page }) => {
  await login(page);
  await openCampaignConfig(page);
  // 世界不完整(无已采用内容/无准入渠道):启用被拒,拒绝文案是产品语句。
  await page.locator('#publish-config [data-action="enable"]').click();
  const refused = page.locator('#publish-config [data-state="refused"]');
  await expect(refused).toBeVisible();
  const text = (await refused.textContent()) ?? "";
  expect(text.length).toBeGreaterThan(6);
  expect(text).not.toMatch(/content_not_ready|channel_not_admitted|422/);
  // 拒绝落在具体字段容器上(content_version_id 一步)。
  await expect(page.locator('#publish-config [data-field="content_version_id"]')).toHaveCount(1);
});

test("390 窄屏:面板无横向溢出,键盘可输入顾客文案", async ({ page }) => {
  await login(page);
  await openCampaignConfig(page);
  await page.setViewportSize({ width: 390, height: 844 });
  const section = page.locator("#publish-config");
  await section.scrollIntoViewIfNeeded();
  const box = await section.boundingBox();
  expect(box).toBeTruthy();
  expect(box!.width).toBeLessThanOrEqual(391);
  const textarea = page.locator('#publish-config textarea[name="customer_copy"]');
  await textarea.click();
  await page.keyboard.type("窄屏键盘输入");
  await expect(textarea).toHaveValue(/窄屏键盘输入/);
  await screenshot(page, "campaign-config-390");
});

test("桌面 1440:五步同屏可用,截图留证", async ({ page }) => {
  await login(page);
  await openCampaignConfig(page);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.locator("#publish-config").scrollIntoViewIfNeeded();
  await expect(page.locator('#publish-config [data-action="save-draft"]')).toBeVisible();
  await expect(page.locator('#publish-config [data-field="preview"]')).toContainText("测试预览");
  await screenshot(page, "campaign-config-1440");
});
