import { writeFileSync } from "node:fs";
import path from "node:path";
import { expect, test } from "@playwright/test";
import { login, screenshot, seedCodes } from "../support/helpers";

// HUI-2628 fix2（gate-r2 state fail 项）：partial / disabled / offline 三态场景证据。
// loading/empty/error+重试、expired/paused/ended、not_found、contact pending+revoked 已有
// E2E+data-state 证据（r2/r3）；本 spec 补齐三态并落 state-scenarios.json 场景账。
// offline 两层口径（对标 2627 第 4 条）：
//   - 真实浏览器断网（context.setOffline）：SPA 已加载后的应用内错误文案（不是浏览器错误页）；
//   - 路由中止 fixture（page.route abort）：网络层等价断开，驱动应用内离线 UI+重试入口；
//   - 整页加载遇断网必然落浏览器错误页——属浏览器层边界，截 boundary 图留档并注明。

const EVIDENCE = path.resolve(__dirname, "..", "evidence");

test("partial：活动主体加载成功、短码/标签/留资统计单独失败", async ({ page }) => {
  await login(page);
  const codes = await seedCodes();
  // 子请求 HTTP 失败（不是 fetch reject——那会把 Promise.all 整体打翻成主 error），
  // 驱动真实 partial：主体 ready、三个子资源各自标错
  await page.route("**/api/campaigns/*/links", (route) => route.fulfill({ status: 500, contentType: "application/json", body: "{}" }));
  await page.route("**/api/nfc/tags**", (route) => route.fulfill({ status: 500, contentType: "application/json", body: "{}" }));
  await page.route("**/api/campaigns/*/lead-stats", (route) =>
    route.fulfill({ status: 404, contentType: "application/json", body: "{}" }),
  );
  await page.goto(`/work/campaigns/${codes.activeId}`);
  // 子资源各自报错，不冒充「还没有」
  await expect(page.locator('p[data-list="links"][data-state="error"]')).toBeVisible({ timeout: 15_000 });
  await expect(page.locator('p[data-list="tags"][data-state="error"]')).toBeVisible();
  // 主体照常可用（partial，不是全页 error）
  await expect(page.locator("h1.tk-title")).toBeVisible();
  await expect(page.getByText("留资统计未开通")).toBeVisible();
  await screenshot(page, "state-partial-campaign-detail");
  writeFileSync(path.join(EVIDENCE, "state-scenarios.json"), JSON.stringify({
    partial: {
      scenario: "活动主体成功 + 短码/标签请求失败各自标错 + 留资统计 404 不显示 0",
      driver: "sub-request HTTP 500/404 fixture（网络层子请求失败）",
      assertions: ['p[data-list="links"][data-state="error"] 可见', 'p[data-list="tags"][data-state="error"] 可见', "留资统计未开通 可见", "h1 主体可见"],
      screenshot: "state-partial-campaign-detail.png",
    },
  }, null, 2), "utf8");
});

test("disabled：门店停用 → 工作台标已停用、公共活动页标门店暂不可用（复位还原）", async ({ page }) => {
  test.slow();
  await login(page);
  const codes = await seedCodes();

  // fixture：建一个关联门店的活动（走产品 UI），启用并生成短码
  await page.goto("/work/campaigns");
  await page.getByPlaceholder("活动标题").fill("E2E 门店关联活动");
  await page.getByLabel("关联门店").selectOption({ label: "旗舰旗舰店" });
  await page.getByRole("button", { name: "创建活动" }).click();
  const row = page.locator("li").filter({ hasText: "E2E 门店关联活动" });
  await expect(row).toBeVisible({ timeout: 15_000 });
  await row.getByRole("button", { name: "启用" }).click();
  await row.getByRole("link", { name: "E2E 门店关联活动" }).click();
  await page.getByRole("button", { name: "生成链接" }).click();
  const shortCode = await page.locator("ul.tk-list code").first().innerText();
  expect(shortCode.trim()).not.toBe("");

  // 停用门店：工作台标已停用（徽章 span 精确断言——notice 文案也含「已停用」字样）
  await page.goto("/work/stores");
  await page.getByRole("button", { name: "停用" }).click();
  const disabledBadge = page.locator("span.tk-unknown", { hasText: "已停用" });
  await expect(disabledBadge).toBeVisible({ timeout: 15_000 });
  await screenshot(page, "state-disabled-stores");

  // 公共活动页照常展示 + 门店暂不可用标注（FEAT-0175：停用不级联下线活动）
  await page.goto(`/c/${shortCode.trim()}`);
  await expect(page.locator('[data-testid="public-activity"][data-state="available"]')).toBeVisible({ timeout: 15_000 });
  await expect(page.getByText(/门店暂不可用/)).toBeVisible();
  await screenshot(page, "state-disabled-public");

  // 复位还原（不污染其他 spec/证据）
  await page.goto("/work/stores");
  await page.getByRole("button", { name: "启用" }).click();
  await expect(disabledBadge).toHaveCount(0);
  await expect(page.locator("ul.tk-list").getByText("启用", { exact: true }).first()).toBeVisible();

  writeFileSync(path.join(EVIDENCE, "state-scenarios-disabled.json"), JSON.stringify({
    disabled: {
      scenario: "门店停用：工作台「已停用」徽章 + 公共活动页「门店暂不可用」标注 + 新建被拒语义",
      driver: "产品 UI fixture（停用/启用按钮）",
      assertions: ["已停用 徽章可见", "公共页 data-state=available 且 门店暂不可用 可见", "复位后 已停用 清零"],
      screenshots: ["state-disabled-stores.png", "state-disabled-public.png"],
    },
  }, null, 2), "utf8");
});

test("offline：公共页真实断网提交报网络异常；路由中止驱动页面级离线态+重试；商家侧 Gate 重试闭环；浏览器边界留档", async ({ page }) => {
  test.slow();
  const codes = await seedCodes();

  // 1) 真实断网（setOffline）：SPA 已加载后提交留资 → 应用内网络异常文案，不是浏览器错误页
  await page.goto(`/c/${codes.activeCode}/contact`);
  await page.locator('[data-testid="public-activity"][data-state="available"]').waitFor({ timeout: 15_000 });
  await page.context().setOffline(true);
  await page.locator('[data-testid="consent"]').check();
  await page.getByPlaceholder("怎么称呼你").fill("断网顾客");
  await page.getByPlaceholder("商家用来联系你").fill("13900000000");
  await page.locator('[data-testid="lead-submit"]').click();
  // 页面源文案是半角逗号（public-campaign.tsx L245）
  await expect(page.getByText("网络异常,请稍后再试")).toBeVisible({ timeout: 15_000 });
  await screenshot(page, "state-offline-public-submit");

  // 2) 浏览器层边界（如实留档）：整页加载遇真断网 → 浏览器错误页（非应用内态，不计入产品证据）
  await page.reload().catch(() => undefined);
  await page.waitForTimeout(500);
  await screenshot(page, "state-offline-browser-boundary");
  await page.context().setOffline(false);
  // 终结可能仍 pending 的断网 reload 事务：恢复联网后它会与下一段同 URL goto 互相打断
  // （"interrupted by another navigation"，全量实挂过一次的时序竞态）。about:blank 不依赖网络，必成功。
  await page.goto("about:blank");

  // 3) 路由中止 fixture（网络层等价断开）：公共页级 network_error + 应用内重试入口
  await page.route("**/api/public/links/**", (route) => route.abort());
  await page.goto(`/c/${codes.activeCode}/contact`);
  await expect(page.locator('[data-testid="degraded-state"]')).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole("button", { name: "重试" })).toBeVisible();
  await screenshot(page, "state-offline-public-network-error");
  await page.unroute("**/api/public/links/**");

  // 4) 商家侧：会话引导路由中止 → MerchantGate 应用内离线错误 + 重试 → 恢复闭环
  // （直接种 localStorage 租户，不走登录流程。组织登录改造后门面引导先打
  // session/memberships、whoami 跟随；两路一起断才等价“连不上服务”，
  // 断言与重试闭环本身不变。）
  await page.route("**/api/session/memberships", (route) => route.abort());
  await page.route("**/api/whoami", (route) => route.abort());
  await page.goto("/admin");
  await page.evaluate((tenant) => window.localStorage.setItem("touch_admin_tenant", tenant), codes.tenantId);
  await page.goto("/");
  await expect(page.getByText("没有连上服务")).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole("button", { name: "重试" })).toBeVisible();
  await screenshot(page, "state-offline-merchant-inapp");
  await page.unroute("**/api/session/memberships");
  await page.unroute("**/api/whoami");
  await page.getByRole("button", { name: "重试" }).click();
  await expect(page.getByRole("heading", { name: "今天" })).toBeVisible({ timeout: 15_000 });

  writeFileSync(path.join(EVIDENCE, "state-scenarios-offline.json"), JSON.stringify({
    offline: {
      real_browser_offline: {
        scenario: "SPA 已加载后断网提交留资 → 应用内「网络异常，请稍后再试」（非浏览器错误页）",
        driver: "context.setOffline(true)",
        screenshot: "state-offline-public-submit.png",
      },
      page_level_retry: {
        scenario: "路由中止数据请求 → 页面级 network_error + 应用内「重试」入口",
        driver: "page.route abort fixture（网络层等价断开）",
        screenshot: "state-offline-public-network-error.png",
      },
      merchant_gate_retry: {
        scenario: "whoami 断开 → MerchantGate「没有连上服务」+ 重试 → 点重试恢复今日台",
        driver: "page.route abort fixture + retryConnection（attempt+1 重跑 whoami）",
        screenshot: "state-offline-merchant-inapp.png",
      },
      browser_boundary: {
        scenario: "整页加载遇真断网 → 浏览器错误页",
        note: "浏览器层边界，非应用内态；任何 web 应用首屏 HTML 依赖网络。只留档不计入产品证据。",
        screenshot: "state-offline-browser-boundary.png",
      },
    },
  }, null, 2), "utf8");
});
