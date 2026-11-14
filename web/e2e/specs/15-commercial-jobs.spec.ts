import { test, expect, type Page } from "@playwright/test";

// All business writes use the normal product forms. Identity is the explicit
// engineering fixture in identity-stub.mjs; no real identity/payment proof.
async function signIn(page: Page, email: string) {
  await page.goto("/");
  await page.getByLabel("用户名").fill(email);
  await page.getByLabel("密码", { exact: true }).fill("e2e-pass-123");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("heading", { name: "今天要做的事", exact: true })).toBeVisible();
}

async function settings(page: Page) {
  await page.getByText("管理与更多", { exact: true }).click();
  await page.getByRole("link", { name: "团队、权限与代理协作", exact: true }).click();
  await expect(page.getByRole("heading", { name: "成员与权限", exact: true })).toBeVisible();
}

async function choosePerson(form: ReturnType<Page["locator"]>, email: string) {
  await form.getByLabel("对方的完整登录邮箱").fill(email);
  await form.getByRole("button", { name: "核对人员", exact: true }).click();
  await form.getByRole("button", { name: `选择 ${email}`, exact: true }).click();
}

test("正常入口选人、任务授权、撤回；代理受控进入与返回；账单接续", async ({ page, browser }) => {
  await signIn(page,"owner@e2e.test");
  await settings(page);
  for (const width of [390,430,1440]) {
    await page.setViewportSize({ width, height: 900 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  }
  const staff = page.locator('[data-form="grant-staff"]');
  await choosePerson(staff,"member-commercial@e2e.test");
  await staff.getByRole("checkbox",{name:"首页配置",exact:true}).check();
  await staff.getByLabel("店员门店范围").selectOption({label:"仅本店：旗舰旗舰店"});
  await staff.getByRole("button",{name:"新增店员",exact:true}).click();
  let review=page.getByRole("dialog");
  await expect(review).toContainText("member-commercial@e2e.test");
  await expect(review).toContainText("旗舰旗舰店");
  await expect(review).toContainText("本次不授予付款权限");
  await review.getByRole("button",{name:"确认办理",exact:true}).click();
  const staffRow=page.locator('[data-role="staff-row"]').filter({hasText:"member-commercial@e2e.test"});
  await expect(staffRow).toContainText("现行任务：首页配置");
  await staffRow.getByRole("checkbox",{name:"首页配置",exact:true}).uncheck();
  await staffRow.getByRole("button",{name:"保存授权",exact:true}).click();
  await page.getByRole("dialog").getByRole("button",{name:"确认办理",exact:true}).click();
  await expect(staffRow).toContainText("现行:未获配置权限");

  const agency=page.locator('[data-form="establish-agency"]');
  await choosePerson(agency,"agent-commercial@e2e.test");
  await agency.getByRole("button",{name:"建立代管",exact:true}).click();
  await page.getByRole("dialog").getByRole("button",{name:"确认办理",exact:true}).click();
  const agentRow=page.locator('[data-role="agency-row"]').filter({hasText:"agent-commercial@e2e.test"});
  await agentRow.getByRole("checkbox",{name:"首页配置",exact:true}).check();
  await agentRow.getByRole("checkbox",{name:"续费协助",exact:true}).check();
  await agentRow.getByRole("button",{name:"保存授权",exact:true}).click();
  await page.getByRole("dialog").getByRole("button",{name:"确认办理",exact:true}).click();
  await expect(agentRow).toContainText("尚无独立付款授权");

  const context=await browser.newContext();
  const agentPage=await context.newPage();
  await signIn(agentPage,"agent-commercial@e2e.test");
  await agentPage.getByRole("link",{name:"返回代理商家列表",exact:true}).click();
  await expect(agentPage.locator('[data-role="merchant-row"]')).toContainText("磁石科技");
  await agentPage.getByRole("button",{name:"进入工作台",exact:true}).click();
  await expect(agentPage.getByRole("complementary",{name:"当前代办商家"})).toContainText("磁石科技");
  await agentPage.getByRole("link",{name:"返回代理商家列表",exact:true}).click();
  await expect(agentPage.getByRole("heading",{name:"代理工作台",exact:true})).toBeVisible();
  await expect(agentPage.locator('[data-section="inside-banner"]')).toHaveCount(0);
  expect(await agentPage.evaluate(() => window.localStorage.getItem("touch_admin_tenant"))).toBeNull();
  await context.close();

  await page.goto("/work/billing");
  await expect(page.getByTestId("billing-summary")).toContainText("当前服务与待办");
  await expect(page.getByTestId("pane-plan")).toContainText("价格目录暂无生效行");
  await expect(page.getByTestId("expand-stores")).toHaveCount(0);
});
