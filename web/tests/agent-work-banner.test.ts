import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { pageLedger } from "../src/lib/page-map";

// HUI-4593 走查收尾(r8):受控进入落地页(/ M1)挂「返回代理台」横幅。
// 锁住:根页最小挂载(不动 TodayDesk 内部)、代理角色门控、backHref=/agency、
// AgentBanner 复用不另造横幅、原始钩子会话(根页无 MerchantGate 上下文)、census 干净。

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

describe("M1 落地页横幅挂载(修复轮收尾)", () => {
  it("根页最小挂载 <AgentWorkBanner />,TodayDesk 内部零改动", () => {
    const page = read("app/page.tsx");
    expect(page).toContain("<AgentWorkBanner />");
    const desk = read("components/work/today-desk.tsx");
    expect(desk).not.toContain("AgentBanner");
    expect(desk).not.toContain("AgentWorkBanner");
  });

  it("横幅组件:代理角色门控 + backHref=/agency + AgentBanner 复用", () => {
    const banner = read("components/work/agent-work-banner.tsx");
    expect(banner).toContain('role !== "agent"');
    expect(banner).toContain('backHref="/agency"');
    expect(banner).toContain("<AgentBanner");
    expect(banner).toContain("access/effective");
    // 非代理渲染 null(商家视图零打扰)
    expect(banner).toContain("if (!acting) return null;");
    // 不另起会话钩子:与 TodayDesk 的 MerchantGate 竞争模块级租户操作序号会让
    // 横幅的 confirm 被判 stale 永不渲染(修复轮收尾实测教训);直读记忆租户取数
    expect(banner).not.toContain("useMerchantSession");
    expect(banner).toContain("TENANT_STORAGE_KEY");
    expect(banner).toContain("/api/whoami");
    // 租户名缺省回退成员列表
    expect(banner).toContain("/api/session/memberships");
  });

  it("census 不受影响:根页仍是 workspace 模式、零标记账本", () => {
    const ledger = pageLedger().find((p) => p.route === "/");
    expect(ledger?.id).toBe("workspace");
    const page = read("app/page.tsx");
    expect(page).not.toMatch(/<button/);
    expect(page).not.toMatch(/<input/);
    expect(page).not.toMatch(/\bstyle\s*=/);
    expect(page).not.toMatch(/#[0-9A-Fa-f]{6}\b/);
  });
});
