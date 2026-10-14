import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

const panelRel = "components/work/campaign-config-panel.tsx";
const panel = read(panelRel);
const detailPage = read("app/work/campaigns/[id]/page.tsx");
const rewardsPage = read("app/work/rewards/page.tsx");
const lib = read("lib/campaign-config.ts");

// HUI-1676 增量:配置面板源码级验收(node 环境,无 DOM)。
// 票面:五步一次配置;服务端草稿恢复;内容选已采用包;渠道真实可用 + 缺项
// 原因;三核验模式;错误落具体字段;预览是测试模式;无技术 ID 输入。

describe("发布与启用面板:五步一次配置", () => {
  it("五个步骤都在,顺序为活动与门店→内容→发布方式→优惠→预览与启用", () => {
    const steps = [
      ...panel.matchAll(/data-step="([^"]+)"/g),
    ].map((m) => m[1]);
    expect(steps).toEqual(["1", "2", "3", "4", "verify", "5"]);
    expect(panel).toContain("① 活动与门店");
    expect(panel).toContain("② 准备内容");
    expect(panel).toContain("③ 发布方式");
    expect(panel).toContain("④ 给什么优惠");
    expect(panel).toContain("⑤ 顾客预览与启用");
  });

  it("挂载即 GET 恢复草稿(服务端持久,刷新重登不丢)", () => {
    expect(panel).toMatch(/session\.api\(\s*"GET",\s*`campaigns\/\$\{campaignId\}\/publish-config`\s*\)/);
    expect(panel).toContain("草稿已保存。刷新或重新登录都会回到这里。");
  });

  it("草稿保存与启用是两个动作,启用前先保存", () => {
    expect(panel).toContain('data-action="save-draft"');
    expect(panel).toContain('data-action="enable"');
    const enableBody = panel.slice(panel.indexOf("async function enable"));
    expect(enableBody).toContain("await saveDraft()");
  });

  it("三核验模式单选,顾客文案在配置时写,不在完成后加严", () => {
    expect(lib).toContain("staff_observed_submission");
    expect(lib).toContain("staff_verified_post");
    expect(lib).toContain("official_publish_confirmed");
    expect(panel).toContain('name="verification_mode"');
    expect(panel).toContain('name="customer_copy"');
  });

  it("预览是明确测试模式,不消耗真实奖励", () => {
    expect(panel).toContain("【测试预览】");
    expect(panel).toContain("不会消耗任何真实奖励");
  });

  it("内容只选已采用版本;渠道不可用不给勾选(禁只存平台字符串)", () => {
    expect(panel).toContain("v.status === \"adopted\"");
    expect(panel).toContain("disabled={!selectable}");
    // 缺项原因文案在 lib 的 admissionNote(渠道展示收口)。
    expect(panel).toContain("admissionNote(rec)");
    expect(lib).toContain("还缺:");
    expect(panel).toContain("渠道清单没有读到");
  });

  it("启用拒绝落到具体字段(机器原因码不直出)", () => {
    expect(panel).toContain('data-state="refused"');
    expect(panel).toContain("refusalCopy(res.status, res.data)");
    expect(panel).not.toMatch(/content_not_ready|channel_not_admitted|benefit_version_mismatch/);
  });

  it("没有技术 ID 输入:没有 order_id/user_id 输入框,数字输入用 inputMode", () => {
    expect(panel).not.toMatch(/name="(order_id|user_id|campaign_id|tenant_id)"/);
    expect(panel).not.toContain('type="number"');
    expect(panel.match(/inputMode="numeric"/g)?.length).toBeGreaterThan(2);
  });

  it("不重复填门店资料:门店展示来自活动资料", () => {
    expect(panel).toContain("不用重填");
  });

  it("紧急暂停语义:停新参与,老参与照常核对履约", () => {
    expect(panel).toContain('data-action="pause"');
    expect(panel).toContain("不再放新参与;已经参与的顾客照常核对与履约");
  });

  it("enabled 下保存被收回草稿的语义如实展示", () => {
    expect(panel).toContain("enabled 下保存会被服务端收回草稿");
  });
});

describe("活动详情页窄接线", () => {
  it("只加一个组件挂载,不改动既有节", () => {
    expect(detailPage).toContain('from "@/components/work/campaign-config-panel"');
    expect(detailPage).toMatch(/<CampaignConfigPanel\s+campaignId=\{id\}/);
    // ContentPackPanel 等既有挂载保持原样(3553 先例不被推翻)。
    expect(detailPage).toContain("<ContentPackPanel campaignId={id} />");
  });

  it("面板带活动与门店资料(只读展示用)", () => {
    expect(detailPage).toMatch(/campaignTitle=\{campaign\.title\}/);
    expect(detailPage).toMatch(/campaignStoreId=\{campaign\.store_id\}/);
    expect(panel).toContain("不用重填");
  });
});

describe("/work/rewards 窄嵌入(1671 页面主体零改动)", () => {
  it("只加一行窄链接指向活动详情的发布与启用节,不嵌整套编辑器", () => {
    expect(rewardsPage).toContain('publish-config');
    expect(rewardsPage).toContain("发布与启用");
    // 不 import 配置面板组件,主体结构不被这一票改写。
    expect(rewardsPage).not.toContain("campaign-config-panel");
  });
});

describe("奖励种类不越界", () => {
  it("只有四种合法奖励,没有现金/抽奖/POS", () => {
    expect(panel).toContain('"store_coupon"');
    expect(panel).toContain('"imported_code"');
    expect(panel).toContain('"onsite_gift"');
    expect(panel).toContain('"onsite_discount"');
    // 选项列表里只有四种;cash/抽奖/POS 不是可选项(注释除外,查 option value)。
    expect(panel).not.toContain('value="cash"');
    expect(panel).not.toMatch(/抽奖|POS(?!T)/);
  });

  it("去重判定没有 cookie/IP 选项", () => {
    expect(panel.toLowerCase()).not.toMatch(/cookie/);
    expect(lib).toContain("按平台授权账号");
  });
});
