import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { pageLedger } from "../src/lib/page-map";

// HUI-4593 M13 /work/settings 成员与权限页:源码级断言(node 环境无 DOM)。
// 锁住:census 账本同步、薄壳、四区结构、代理横幅(正在为谁工作/权限/付款主体)、
// 特权操作必填原因、403 权限不足产品语句、FEATURE off 诚实空态、移动优先。

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

const pageRel = "app/work/settings/page.tsx";
const deskRel = "components/work/settings/settings-desk.tsx";

describe("census 账本同步", () => {
  it("/work/settings 记入账本(settings 模式),恰好一条", () => {
    const ledger = pageLedger().map((p) => p.route);
    expect(ledger.filter((r) => r === "/work/settings")).toHaveLength(1);
    const decl = pageLedger().find((p) => p.route === "/work/settings");
    expect(decl?.id).toBe("settings");
  });

  it("页面壳是薄壳:零 raw 标记(census 干净)", () => {
    const page = read(pageRel);
    expect(page).toContain("<SettingsDesk />");
    expect(page).not.toMatch(/<button/);
    expect(page).not.toMatch(/<input/);
    expect(page).not.toMatch(/\bstyle\s*=/);
    expect(page).not.toMatch(/#[0-9A-Fa-f]{6}\b/);
  });

  it("工作台导航含设置入口", () => {
    const frame = read("components/work/work-frame.tsx");
    expect(frame).toContain('/work/settings');
  });
});

describe("四区结构(移动优先)", () => {
  const desk = read(deskRel);

  it("成员与权限区:列表/授权表单/停用带原因", () => {
    expect(desk).toContain('data-section="members"');
    expect(desk).toContain('data-action="grant-member"');
    expect(desk).toContain('data-action="disable-member"');
    expect(desk).toContain("admin/members");
  });

  it("代理协作区:建立/scope 授权(动作+门店+期限+代付开关)/撤销", () => {
    expect(desk).toContain('data-section="agency"');
    expect(desk).toContain('data-action="establish-agency"');
    expect(desk).toContain('data-action="save-scope"');
    expect(desk).toContain('data-action="revoke-agency"');
    expect(desk).toContain("agency/relations");
    expect(desk).toContain("/scope");
    expect(desk).toContain("grant_revision");
  });

  it("模板下发区:选店/覆盖字段白名单/幂等 request_key/撤回;门店经理只读", () => {
    expect(desk).toContain('data-section="templates"');
    expect(desk).toContain('data-action="distribute-template"');
    expect(desk).toContain('data-action="cancel-template"');
    expect(desk).toContain("request_key");
    expect(desk).toContain("template-assignments");
    expect(desk).toContain("模板下发由总部操作");
  });

  it("白标授权区:只读展示,管理判定=org_owner 且 storefront:configure", () => {
    expect(desk).toContain('data-section="brand"');
    expect(desk).toContain('data-field="brand-manage"');
    expect(desk).toContain("storefront:configure");
    expect(desk).toContain("域名与证书的审核归平台");
  });

  it("代理横幅:正在为谁工作/获准动作/付款主体(无代付授权默认口径)", () => {
    expect(desk).toContain('data-section="agent-banner"');
    expect(desk).toContain('data-field="acting-for"');
    expect(desk).toContain('data-field="agent-perms"');
    expect(desk).toContain('data-field="payer-status"');
    expect(desk).toContain("无代付授权");
    expect(desk).toContain("正在为");
  });

  it("特权操作必填原因(授权/停用/scope/撤销/下发都先挡空原因)", () => {
    const reasonChecks = desk.match(/必须填写原因/g) ?? [];
    expect(reasonChecks.length).toBeGreaterThanOrEqual(4);
    // 原因留痕口径出现在操作语境(代理撤销文案)
    expect(desk).toContain("留痕可查");
  });

  it("权限不足/off 状态用产品语句,不崩不装成功", () => {
    expect(desk).toContain("failureText");
    expect(desk).toContain('data-state="empty"');
    expect(desk).toContain("还没有对本组织开启");
    expect(desk).toContain('data-action="retry"');
  });

  it("移动优先:无固定像素宽,不写桌面专用布局", () => {
    expect(desk).not.toMatch(/width:\s*\d{3,}px/);
    expect(desk).not.toMatch(/min-width:\s*\d{3,}px/);
    expect(desk).not.toMatch(/\bstyle\s*=/);
  });

  it("不出现手填租户编号/平台内部字段", () => {
    expect(desk).not.toContain("tenant_id=");
    expect(desk).not.toContain("租户编号");
    expect(desk).not.toContain("ops:");
  });
});
