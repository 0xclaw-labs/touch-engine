import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

const deskRel = "components/work/storefront/storefront-page-desk.tsx";
const desk = read(deskRel);
const shell = read("app/work/stores/[id]/page/page.tsx");
const lib = read("lib/storefront-page.ts");
const storesList = read("app/work/stores/page.tsx");
const channelsPage = read("app/work/stores/[id]/channels/page.tsx");
const panel = read("components/work/campaign-config-panel.tsx");
const campaignLib = read("lib/campaign-config.ts");

// HUI-1676 M3:门店首页设置台源码级验收(node 环境,无 DOM)。
// 票面:行业模板/分组排序/常用按钮/套餐区/手机预览(真实数据面+预览标识)/
// 逐动作预检(修复入口)/变更摘要确认/发布与回滚/旧参与保护不动参与表。

describe("M3 页面骨架", () => {
  it("路由薄壳只挂组件,页面源零交互标记基础", () => {
    expect(shell).toContain("StorefrontPageDesk");
    expect(shell).not.toMatch(/<button/);
    expect(shell).not.toMatch(/<input/);
  });

  it("入口:门店列表行有「门店首页」,渠道动作页有回链", () => {
    expect(storesList).toContain("/work/stores/${store.id}/page");
    expect(storesList).toContain("门店首页");
    expect(channelsPage).toContain("/work/stores/${storeId}/page");
  });

  it("草稿持久化在服务端:挂载即 GET 恢复,保存/放弃是两个动作", () => {
    expect(desk).toMatch(/session\.api\(\s*"GET",\s*`stores\/\$\{storeId\}\/storefront-draft`\s*\)/);
    expect(desk).toContain('data-action="save-draft"');
    expect(desk).toContain('data-action="reload-draft"');
    expect(desk).toContain("保存草稿不影响线上页面。");
  });

  it("行业模板三预设,应用只归组本店真实动作(不虚构入口)", () => {
    expect(lib).toContain('"basic"');
    expect(lib).toContain('"dining"');
    expect(lib).toContain('"retail"');
    expect(lib).toContain("if (!item.enabled) continue;");
    expect(desk).toContain("没有对应动作的段落先省略");
  });
});

describe("M3 预检/发布/回滚", () => {
  it("逐动作预检带修复入口(指向渠道动作页),一个动作失败不拖垮其他", () => {
    expect(desk).toContain('data-field="preflight-actions"');
    expect(desk).toContain("/work/stores/${storeId}/channels");
    expect(lib).toContain('v.state === "ready" ? "可以发布"');
    expect(lib).toContain("预检逐动作隔离");
  });

  it("发布三闸:预检可发布 + 确认变更摘要 + 指纹回带(服务端单点)", () => {
    expect(desk).toContain('data-field="confirm-changes"');
    expect(desk).toContain('data-action="publish"');
    expect(lib).toContain("pre.summary_fingerprint !== \"\" && pre.summary_fingerprint === fingerprint");
    expect(lib).toMatch(/confirm_changes: true/);
    expect(lib).toContain("勾选框只是 UX");
  });

  it("回滚在已发布版本上操作,且注明撤权不复活", () => {
    expect(desk).toContain('data-action="rollback"');
    expect(desk).toContain("已撤销的平台授权不会被恢复");
    expect(lib).toMatch(/buildRollbackBody/);
  });

  it("手机预览用真实预览数据面(会话约束端点),带明显预览标识", () => {
    expect(desk).toMatch(/session\.api\(\s*"GET",\s*`stores\/\$\{storeId\}\/storefront-preview/);
    expect(desk).toContain('data-state="preview-badge"');
    expect(desk).toContain("预览(草稿,未发布)");
    expect(desk).toContain("预览(线上版本)");
    expect(desk).toContain("不产生参与、发布或费用");
  });

  it("活动可选语义:本页只引用活动卡片,不编辑活动、不动门店基础服务", () => {
    // 本台不发活动 CRUD 请求(活动编辑归活动域,门店首页只挂 campaign_refs)。
    expect(desk).not.toMatch(/campaigns\/\$\{[^}]+\}\/publish-config/);
    expect(desk).not.toMatch(/POST.*`campaigns/);
    // 旧参与保护:全页无参与/奖励写路径(参与/券表归 3546/1671)。
    expect(desk).not.toMatch(/participations/);
    expect(desk).not.toMatch(/reward-claim|benefit/);
  });

  it("4593 模板 applied 回调缝:应用分发模板后发布成功回调 PATCH applied", () => {
    expect(desk).toMatch(/session\.api\(\s*"PATCH",\s*`template-assignments\/\$\{assignmentId\}`/);
    expect(desk).toContain('status: "applied"');
    expect(desk).toContain("applied_revision: String(revision)");
    expect(desk).toContain("该分发可能已被取消或已处理");
  });
});

describe("点评禁奖(活动配置侧)", () => {
  it("渠道选择区把 review 场景挡掉并给产品原因;服务端 400 有对应文案", () => {
    expect(panel).toContain("reviewProhibited(rec.scenario)");
    expect(panel).toContain("点评不做奖励");
    expect(campaignLib).toContain("review_reward_prohibited");
    expect(campaignLib).toContain("点评渠道不能作为奖励任务");
    expect(campaignLib).toContain('export function reviewProhibited');
  });
});
