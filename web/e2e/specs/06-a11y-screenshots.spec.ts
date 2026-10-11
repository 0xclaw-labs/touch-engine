import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { login, screenshot, seedCodes } from "../support/helpers";

type RouteDef = { slug: string; path: string; auth: boolean };

// 9 个代表/列表页 × 390/430/1024/1440 截图 + axe；9 页 1920 + /admin 3 张。
// HUI-2628 r3：增 1440（17→25 张）。
// HUI-2628 fix2（gate-r2 矩阵缺口）：增 1024 整腿、1920 补齐全页、
// /work/campaigns（路由表 List）独立截图不再由 detail 代表、/admin（路由表 Home）三张——
// admin 登录视图只保留邮箱和密码。组织从成员关系解析。
// axe 阻断面保持 8 代表页（+work-campaigns 记录性扫描不阻断）；/admin 工具页不入阻断面
// （登录不再手填组织编号。/admin 工具页仍不入 axe 阻断面）。

const ROUTES: RouteDef[] = [
  { slug: "home", path: "/", auth: true },
  { slug: "work-stores", path: "/work/stores", auth: true },
  { slug: "work-campaigns", path: "/work/campaigns", auth: true },
  { slug: "work-campaign-detail", path: "", auth: true }, // 运行时用 seed active 活动 id 拼 /work/campaigns/{id}
  { slug: "work-materials", path: "/work/materials", auth: true },
  { slug: "work-rewards", path: "/work/rewards", auth: true },
  { slug: "work-analytics", path: "/work/analytics", auth: true },
  { slug: "public-campaign", path: "", auth: false }, // /c/{activeCode}
  { slug: "public-contact", path: "", auth: false }, // /c/{activeCode}/contact
];

// axe 阻断面：8 代表页（r3 口径）；work-campaigns 属记录性扫描。
const AXE_BLOCKING_SLUGS = new Set(["home", "work-stores", "work-campaign-detail", "work-materials", "work-rewards", "work-analytics", "public-campaign", "public-contact"]);

async function resolvePath(def: RouteDef, campaignId: string, activeCode: string): Promise<string> {
  if (def.slug === "work-campaign-detail") return `/work/campaigns/${campaignId}`;
  if (def.slug === "public-campaign") return `/c/${activeCode}`;
  if (def.slug === "public-contact") return `/c/${activeCode}/contact`;
  return def.path;
}

test("a11y + screenshots across representative pages", async ({ page }) => {
  // 浏览器不可用必须红（评审 A1）：不再 test.skip——skip 会被 playwright 判 exit 0 造成假绿。
  await login(page);
  const codes = await seedCodes();

  const blocking: Array<{ slug: string; width: number; violations: Array<{ id: string; impact: string | null }> }> = [];
  const recorded: Array<{ slug: string; width: number; violations: Array<{ id: string; impact: string | null }> }> = [];
  const widths = [390, 430, 1024, 1440];
  for (const def of ROUTES) {
    for (const width of widths) {
      await page.setViewportSize({ width, height: 900 });
      const target = await resolvePath(def, codes.activeId ?? "", codes.activeCode);
      await page.goto(target);
      await page.waitForLoadState("networkidle").catch(() => undefined);
      const axe = await new AxeBuilder({ page }).analyze();
      const serious = axe.violations
        .filter((v) => v.impact === "critical" || v.impact === "serious")
        .map((v) => ({ id: v.id, impact: v.impact ?? null }));
      for (const v of serious) {
        (AXE_BLOCKING_SLUGS.has(def.slug) ? blocking : recorded).push({ slug: def.slug, width, violations: [v] });
      }
      await screenshot(page, `${def.slug}-${width}`);
    }
  }

  // 1920 全页腿（fix2 补齐：r3 仅 home 一张）
  for (const def of ROUTES) {
    await page.setViewportSize({ width: 1920, height: 1080 });
    const target = await resolvePath(def, codes.activeId ?? "", codes.activeCode);
    await page.goto(target);
    await page.waitForLoadState("networkidle").catch(() => undefined);
    await screenshot(page, `${def.slug}-1920`);
  }

  // 验收门：代表页 critical/serious 违规阻断（work-campaigns 记录性不阻断）
  expect(blocking).toEqual([]);
  mkdirSync(path.resolve(__dirname, "..", "evidence"), { recursive: true });
  writeFileSync(
    path.resolve(__dirname, "..", "evidence", "axe-summary.json"),
    JSON.stringify({ blocking, recorded_non_blocking: recorded, note: "阻断面=8 代表页；work-campaigns 记录性扫描；moderate 及以下见 playwright json 报告" }),
    "utf8",
  );
});

test("admin 路由表 Home 页截图（登录视图如实呈现 + 登录后壳）", async ({ page }) => {
  // 登录视图（未带会话）。组织不在这张表单里手填。
  await page.context().clearCookies();
  await page.goto("/admin");
  await page.evaluate(() => window.localStorage.removeItem("touch_admin_tenant"));
  await page.goto("/admin");
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.waitForLoadState("networkidle").catch(() => undefined);
  await screenshot(page, "admin-1440");
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/admin");
  await page.waitForLoadState("networkidle").catch(() => undefined);
  await screenshot(page, "admin-390");

  // 登录后壳（1440）
  await page.setViewportSize({ width: 1440, height: 900 });
  await login(page);
  await page.goto("/admin");
  await page.locator("main.tk-admin-shell").waitFor({ timeout: 15_000 });
  await page.waitForLoadState("networkidle").catch(() => undefined);
  await screenshot(page, "admin-shell-1440");
});
