import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { pageLedger } from "../src/lib/page-map";

// HUI-4593 M13 /work/settings 成员与权限页:源码级断言(node 环境无 DOM)。
// 修复轮 2(F2):布局按裁定书四区(组织负责人/门店经理/店员动作授权/代理协作)
// 呈现;店员动作级授予/撤回操作面(授权→访问→撤回→同会话 403 旅程的写面);
// my-access 展示当前有效授权(旅程的「访问」一侧)。
// 另锁:census 账本同步、薄壳、代理横幅、原因必填、403 产品语句、off 诚实空态、移动优先。

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

describe("裁定书四区(按角色维度)", () => {
  const desk = read(deskRel);

  it("区一 组织负责人:治理面清单只读展示", () => {
    expect(desk).toContain('data-section="owner"');
    expect(desk).toContain('data-role="owner-row"');
    expect(desk).toContain('data-field="owner-scope"');
  });

  it("区二 门店经理授权:授予(带门店+原因)/停用带原因", () => {
    expect(desk).toContain('data-section="store-manager"');
    expect(desk).toContain('data-role="manager-row"');
    expect(desk).toContain('data-action="grant-manager"');
    expect(desk).toContain('data-action="disable-manager"');
    expect(desk).toContain("store_manager");
    expect(desk).toContain("门店经理必须指定负责的门店");
  });

  it("区三 店员动作授权:对现有店员授予/撤回 storefront/action 权限(同会话即时生效)", () => {
    expect(desk).toContain('data-section="staff-action"');
    expect(desk).toContain('data-role="staff-row"');
    expect(desk).toContain("granted_permissions");
    expect(desk).toContain('data-action="save-staff-perms"');
    expect(desk).toContain('data-action="disable-staff"');
    // 权限白名单与后端一一对应(仅 storefront/action 两维;billing/ops 不出现)
    expect(desk).toContain('"storefront:configure"');
    expect(desk).toContain('"action:configure"');
    expect(desk).toContain('"action:channel_target"');
    expect(desk).toContain('"action:manage_deal"');
    expect(desk).toContain('"action:manage_voucher"');
    expect(desk).not.toContain('"billing:view"');
    // 走授权端点,撤回即时生效口径
    expect(desk).toContain("/permissions");
    expect(desk).toContain("撤回后,店员的下一次请求立即失去对应能力");
  });

  it("区四 代理协作授权:建立/scope 授权(动作+门店+期限+代付开关)/撤销", () => {
    expect(desk).toContain('data-section="agency"');
    expect(desk).toContain('data-action="establish-agency"');
    expect(desk).toContain('data-action="save-scope"');
    expect(desk).toContain('data-action="revoke-agency"');
    expect(desk).toContain("agency/relations");
    expect(desk).toContain("/scope");
    expect(desk).toContain("grant_revision");
  });

  it("my-access:当前身份与有效授权展示(旅程的「访问」一侧)", () => {
    expect(desk).toContain('data-section="my-access"');
    expect(desk).toContain('data-field="my-role"');
    expect(desk).toContain('data-field="my-perms"');
    expect(desk).toContain('data-field="my-scope"');
  });
});

describe("其余区域与纪律", () => {
  const desk = read(deskRel);

  it("模板下发区:选店/覆盖字段白名单/幂等 request_key/原因透传审计/撤回;门店经理只读", () => {
    expect(desk).toContain('data-section="templates"');
    expect(desk).toContain('data-action="distribute-template"');
    expect(desk).toContain('data-action="cancel-template"');
    expect(desk).toContain("request_key: draft.key");
    // F3:业务原因独立于幂等键,原样透传进审计
    expect(desk).toContain("reason: draft.reason.trim()");
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

  it("特权操作必填原因(授权/停用/scope/撤销/下发/动作授权都先挡空原因)", () => {
    const reasonChecks = desk.match(/必须填写原因/g) ?? [];
    expect(reasonChecks.length).toBeGreaterThanOrEqual(4);
    expect(desk).toContain("留痕可查");
  });

  it("权限不足/off 状态用产品语句,不崩不装成功", () => {
    expect(desk).toContain("failureText");
    expect(desk).toContain('data-state="empty"');
    expect(desk).toContain("还没有对本组织开启");
    expect(desk).toContain('data-action="retry"');
  });

  it("修复轮 3/F2①:页面级 access 门控 —— 店员无配置权限呈现受限视图(403 语义)", () => {
    expect(desk).toContain('data-state="restricted"');
    expect(desk).toContain('data-field="restricted-403"');
    expect(desk).toContain("当前账号没有成员与权限的管理能力（403）");
    // 门控条件:staff 且有效权限为空(access/effective 每请求实时读取)
    expect(desk).toContain('session.role === "staff"');
    expect(desk).toContain("myPerms.length === 0");
    // 受限视图仍保留 my-access(透明展示自己当前授权)
    expect(desk).toContain("<MyAccessSection");
  });

  it("修复轮 5/F②:组织切换=身份级能力,受限视图保留切换入口不受租户内权限门控", () => {
    expect(desk).toContain('data-section="org-switch"');
    expect(desk).toContain('data-field="org-options"');
    expect(desk).toContain('data-action="switch-org"');
    // 身份级判据:>1 个 enabled membership;当前租户排除
    expect(desk).toContain("o.enabled && o.source === \"membership\" && o.tenant_id !== currentTenantId");
    // 切换走既有 switchTenant 单缝(目标租户按那边 membership+role 授权)
    expect(desk).toContain("switchTenant(o.tenant_id)");
    // 受限视图分支里也渲染(403 语义不挡身份功能)
    expect(desk).toContain("<OrgSwitchSection options={session.options} currentTenantId={session.tenantId} switchTenant={session.switchTenant} />");
    // 受限分支内出现两次 OrgSwitchSection(受限视图与正常视图各一,组件同源)
    expect((desk.match(/<OrgSwitchSection /g) ?? []).length).toBe(2);
  });

  it("修复轮 3/F2②:被授 storefront 后有「前往门店首页配置」直达入口(既有门店配置面)", () => {
    expect(desk).toContain('data-field="page-config-entry"');
    expect(desk).toContain('data-action="goto-page-config"');
    expect(desk).toContain('href="/work/stores"');
    // 入口按 storefront:configure 门控
    expect(desk).toContain('perms.includes("storefront:configure")');
  });

  it("修复轮 3/F1:代理横幅带「返回代理台」入口(不动 2628 共享壳)", () => {
    expect(desk).toContain('backHref="/agency"');
    expect(desk).toContain('data-action="back-agency"');
    expect(desk).toContain("返回代理台");
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
