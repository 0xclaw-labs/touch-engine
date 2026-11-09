import { existsSync } from "node:fs";
import { defineConfig } from "@playwright/test";

// 浏览器获取策略（spec §8.5）：优先 npx playwright install chromium；
// 不可用则系统 Chrome/Edge 可执行文件；再不可用由 a11y spec 自行标 BLOCKED。
function resolveBrowser(): { launchOptions: { executablePath: string } } | { channel: string } {
  const explicit = process.env.HARNESS_BROWSER_EXECUTABLE;
  if (explicit) return { launchOptions: { executablePath: explicit } };
  const candidates = [
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
    "/Applications/Chromium.app/Contents/MacOS/Chromium",
  ];
  const found = candidates.find((p) => existsSync(p));
  if (found) return { launchOptions: { executablePath: found } };
  return { channel: process.env.HARNESS_BROWSER_CHANNEL || "chromium" };
}

// HUI-2222: honor the same E2E_*_PORT variables env.mjs already reads, so a
// harness on per-ticket ports (E2E_GO_PORT/E2E_WEB_PORT/E2E_IDENTITY_PORT)
// points the runner at the matching instance. Defaults keep the original
// 18460-18462 behavior byte-for-byte.
export const PORTS = {
  go: Number(process.env.E2E_GO_PORT) || 18460,
  identity: Number(process.env.E2E_IDENTITY_PORT) || 18461,
  web: Number(process.env.E2E_WEB_PORT) || 18462,
};

export default defineConfig({
  testDir: "./specs",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 90_000,
  expect: { timeout: 15_000 },
  outputDir: "./_art/test-results",
  reporter: [["list"], ["json", { outputFile: "./evidence/playwright-report.json" }]],
  use: {
    ...resolveBrowser(),
    headless: true,
    baseURL: `http://127.0.0.1:${PORTS.web}`,
    viewport: { width: 1440, height: 900 },
    locale: "zh-CN",
    screenshot: "off",
    trace: "retain-on-failure",
  },
});
