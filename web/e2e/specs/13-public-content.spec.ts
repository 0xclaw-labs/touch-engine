import { expect, test } from "@playwright/test";
import { seedCodes } from "../support/helpers";

test("guest starts participation, sees the pinned content and AI marking, and gets a text verification code", async ({ page }) => {
  const seed = await seedCodes();
  await page.goto(`/c/${seed.activeCode}`);
  const main = page.locator('[data-testid="public-activity"]');
  await expect(main).toHaveAttribute("data-state", "available");

  const start = page.getByTestId("activity-start");
  if (await start.count()) {
    await start.click();
  }
  await expect(page.getByTestId("content-publish")).toHaveAttribute("data-version", seed.contentVersionId, { timeout: 15_000 });
  await expect(page.getByTestId("content-snapshot")).toContainText("E2E 已采用内容");
  await expect(page.getByTestId("content-ai-marking")).toContainText("AI 合成");
  await expect(page.getByTestId("content-version-mismatch")).toHaveCount(0);
  await expect(page.getByTestId("reward-status")).toHaveAttribute("data-status", /pending|in_progress|awaiting_review/);
  await expect(page.getByTestId("reward-progress")).toContainText("已开始");

  await page.getByTestId("reward-show-code").click();
  const token = page.getByTestId("reward-token");
  await expect(token).toBeVisible();
  await expect(token).toContainText("ptx_");
  // The old /p/<token> URL had no route; the page must not render it as a link.
  await expect(page.locator('a[href*="/p/ptx_"]')).toHaveCount(0);
});
