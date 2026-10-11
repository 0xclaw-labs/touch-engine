import { mkdirSync } from "node:fs";
import path from "node:path";
import { expect, type Page } from "@playwright/test";

const EVIDENCE = path.resolve(__dirname, "..", "evidence");

/** 从 evidence/seed.json 读 env.mjs 写入的 seed 短码/租户 ID/owner 凭据（凭据唯一真实来源）。 */
export async function seedCodes(): Promise<Record<string, string>> {
  const data = await import("node:fs/promises").then((f) => f.readFile(path.join(EVIDENCE, "seed.json"), "utf8"));
  return JSON.parse(data) as Record<string, string>;
}

/**
 * 走 /admin 登录表单建立商家会话。
 * 表单只有邮箱和密码。单一成员关系由服务端自动选中，不手填租户编号。
 * 凭据只来自 seed.json（env.mjs seed 写入），本文件不硬编码第二份。
 */
export async function login(page: Page): Promise<void> {
  const seed = await seedCodes();
  if (!seed.email || !seed.password) throw new Error("[helpers] seed.json 缺 owner 凭据（env.mjs seed 未写入？）");
  await page.goto("/admin");
  await page.getByPlaceholder("平台账号邮箱").fill(seed.email);
  await page.getByPlaceholder("密码", { exact: true }).fill(seed.password);
  await page.getByRole("button", { name: "登录" }).click();
  // 登录视图是 main.tk-admin-narrow；单一成员关系选中后渲染 main.tk-admin-shell
  await expect(page.locator("main.tk-admin-shell")).toBeVisible({ timeout: 15_000 });
}

/** 全页截图统一落 evidence/，文件名 = name.png。
 * 截图前强制回滚到页顶：axe .analyze() 会滚动页面，带 sticky 头的页面若带着
 * 滚动偏移截 fullPage，证据图的起点不可复现。回顶是截图确定性的防御措施。 */
export async function screenshot(page: Page, name: string): Promise<string> {
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.waitForTimeout(100);
  mkdirSync(EVIDENCE, { recursive: true });
  const file = path.join(EVIDENCE, `${name}.png`);
  await page.screenshot({ path: file, fullPage: true });
  return file;
}
