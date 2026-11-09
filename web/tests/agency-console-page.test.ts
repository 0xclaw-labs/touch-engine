import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { pageLedger } from "../src/lib/page-map";

// HUI-4593 A1 /agency 代理工作台:源码级断言(node 环境无 DOM)。
// 锁住:census 账本同步、薄壳、六区结构(授权商户/受控进入跳 M1/门店配置入口/
// 开通续费协助/硬件/结算)、scope 语义复用(不新造权限体系)、诚实空态、移动优先。

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

const pageRel = "app/agency/page.tsx";
const deskRel = "components/work/agency-console/agency-console.tsx";

describe("census 账本同步", () => {
  it("/agency 记入账本(list 模式),恰好一条", () => {
    const ledger = pageLedger().map((p) => p.route);
    expect(ledger.filter((r) => r === "/agency")).toHaveLength(1);
    const decl = pageLedger().find((p) => p.route === "/agency");
    expect(decl?.id).toBe("list");
  });

  it("页面壳是薄壳:零 raw 标记(census 干净)", () => {
    const page = read(pageRel);
    expect(page).toContain("<AgencyConsole />");
    expect(page).not.toMatch(/<button/);
    expect(page).not.toMatch(/<input/);
    expect(page).not.toMatch(/\bstyle\s*=/);
    expect(page).not.toMatch(/#[0-9A-Fa-f]{6}\b/);
  });
});

describe("A1 控制台结构", () => {
  const desk = read(deskRel);

  it("授权商户列表来自跨租户主体全集(修复轮 2/F1):granted 端点,当前租户上下文≠授权商户也显示", () => {
    expect(desk).toContain('data-section="merchants"');
    expect(desk).toContain("/api/agency/relations/granted");
    expect(desk).toContain("/api/session/memberships");
    expect(desk).toContain('data-role="merchant-row"');
    expect(desk).toContain("filter((r) => !r.revoked_at)");
    expect(desk).toContain('m.enabled && m.source === "membership"');
    expect(desk).toContain("tenant_name");
    expect(desk).toContain("payer_granted");
  });

  it("受控进入沿用既有 switchTenant 单缝并跳 M1(/),不新造权限体系", () => {
    expect(desk).toContain('data-action="enter-merchant"');
    expect(desk).toContain("session.switchTenant(tenantId)");
    expect(desk).toContain('window.location.href = "/"');
    expect(desk).toContain("只能进入已授权商户");
    // 复用横幅组件(正在为谁工作/权限/付款主体),不复制实现
    expect(desk).toContain("<AgentBanner");
    expect(desk).toContain('data-action="back-console"');
  });

  it("修复轮 3/F1:进入必须等切换落地(phase=ready 且租户命中)再跳,不与硬跳竞速", () => {
    expect(desk).toContain("setPendingEnter(tenantId)");
    expect(desk).toContain('session.phase === "ready" && session.tenantId === pendingEnter');
    expect(desk).toContain('data-field="entering"');
    // enter() 内不得紧跟硬跳(竞速会让 localStorage 记忆停留在旧租户)
    expect(desk).not.toMatch(/switchTenant\(tenantId\);\s*window\.location/);
    // 切换失败要给出产品语句并退出等待态
    expect(desk).toContain("受控进入没有完成");
  });

  it("修复轮 3/F1:acting-for 横幅带「返回代理台」入口(backHref=/agency)", () => {
    expect(desk).toContain('backHref="/agency"');
    const settings = read("components/work/settings/settings-desk.tsx");
    expect(settings).toContain('data-action="back-agency"');
    expect(settings).toContain("返回代理台");
  });

  it("门店配置入口:按商户租户读门店,再进入 /work/stores;权限以服务端为准", () => {
    expect(desk).toContain('data-section="store-config"');
    expect(desk).toContain('data-action="open-stores"');
    expect(desk).toContain('data-action="go-store-config"');
    expect(desk).toContain('"/api/stores"');
    expect(desk).toContain('"/work/stores"');
    expect(desk).toContain("配置权限以商家授予为准");
  });

  it("授权缺失指明需商家授予的具体动作(不冒充万能管理员)", () => {
    expect(desk).toContain("scopeHints");
    expect(desk).toContain('data-field="scope-hints"');
    expect(desk).toContain("需商家授予");
    expect(desk).toContain("未获配置授权");
  });

  it("开通/续费协助:只判资格(renewal),付款主体明示,收费归平台权威不伪造开通", () => {
    expect(desk).toContain('data-section="renewal"');
    expect(desk).toContain('data-role="renewal-row"');
    expect(desk).toContain('includes("renewal")');
    expect(desk).toContain("已获续费协助授权");
    expect(desk).toContain('data-field="renewal-authority"');
    expect(desk).toContain("真实服务端支付状态才开通");
    expect(desk).toContain("这里不做任何代收代付");
  });

  it("硬件交付/结算:诚实空态+归属说明,零伪造状态", () => {
    expect(desk).toContain('data-section="hardware"');
    expect(desk).toContain('data-field="hardware-empty"');
    expect(desk).toContain("本部署尚未接入");
    expect(desk).toContain('data-section="settlement"');
    expect(desk).toContain('data-field="settlement-empty"');
    expect(desk).toContain("不会看到商家钱包余额");
  });

  it("scope 文案与 M13 同源(ACTION_LABELS 复用),不另造术语", () => {
    const settings = read("components/work/settings/settings-desk.tsx");
    expect(settings).toContain("export const ACTION_LABELS");
    expect(desk).toContain('from "@/components/work/settings/settings-desk"');
    expect(desk).not.toMatch(/const ACTION_LABELS/);
  });

  it("登录复用 SharedLogin 单缝;会话用既有原始钩子(窄补丁导出)", () => {
    const session = read("components/work/merchant-session.tsx");
    expect(session).toContain("export { useMerchantSession }");
    expect(desk).toContain("useMerchantSession");
    expect(desk).toContain("<SharedLogin");
  });

  it("移动优先:无固定像素宽,无行内样式", () => {
    expect(desk).not.toMatch(/width:\s*\d{3,}px/);
    expect(desk).not.toMatch(/min-width:\s*\d{3,}px/);
    expect(desk).not.toMatch(/\bstyle\s*=/);
  });

  it("不出现平台内部字段/机器码直出", () => {
    expect(desk).not.toContain("tenant_id=");
    expect(desk).not.toContain("grant_revision");
    expect(desk).not.toContain("ops:");
    expect(desk).toContain("failureText");
  });
});
