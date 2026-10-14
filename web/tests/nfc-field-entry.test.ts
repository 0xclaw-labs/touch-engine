import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { ENTRY_WHITELIST, STATE_ACTION, STATE_TEXT } from "../src/lib/public-state";

// HUI-1663 工程准备:检查表引用的「稳定 URL → /t/{code} → /c/{code} → 正确活动」
// 链路在 web 侧的接线事实,以及错租户/错误域名/暂停服务家系的顾客文案。
// node 环境无 DOM,页面接线用源码级断言(与 page-census 同口径);
// 纯函数语义(五态表/白名单/redirect 分支)已由 public-state.test.ts 与
// touchpoints.test.ts 钉住,这里只补检查表引用且两处都没有的钉子,不重复造。

const cPageSrc = readFileSync(join(__dirname, "../src/app/c/[code]/page.tsx"), "utf8");
const campaignSrc = readFileSync(join(__dirname, "../src/app/c/[code]/public-campaign.tsx"), "utf8");
const tPageSrc = readFileSync(join(__dirname, "../src/app/t/[code]/page.tsx"), "utf8");
const touchEntrySrc = readFileSync(join(__dirname, "../src/app/t/[code]/touch-entry.tsx"), "utf8");

describe("公共入口接线(源码级)", () => {
  it("/c/[code] 页渲染 activity lane 的 PublicCampaign", () => {
    expect(cPageSrc).toContain('PublicCampaign lane="activity"');
  });

  it("公共面按 URL 中的 code 原样请求解析接口(无改写/无第二套码制)", () => {
    expect(campaignSrc).toContain("fetch(`/api/public/links/${encodeURIComponent(code)}`)");
  });

  it("/t/[code] 页受 FEATURE_TOUCHPOINT_ENTRY 三道闸之一(默认 off 整页 404)", () => {
    expect(tPageSrc).toContain("featureEnabled(process.env.FEATURE_TOUCHPOINT_ENTRY)");
    expect(tPageSrc).toContain("notFound()");
  });

  it("/t 入口页按 URL 中的 code 原样请求触点解析接口,跳转只经白名单函数", () => {
    expect(touchEntrySrc).toContain("fetch(`/api/public/touchpoints/${encodeURIComponent(code)}`)");
    expect(touchEntrySrc).toContain("touchEntryRedirect(data)");
    expect(touchEntrySrc).toContain("window.location.replace(target)");
  });

  it("entry 白名单含 nfc 与 qr(NFC 与扫码共用同一页面与解析)", () => {
    expect(ENTRY_WHITELIST).toContain("nfc");
    expect(ENTRY_WHITELIST).toContain("qr");
  });
});

describe("检查表引用的非可用态文案(明确提示,不误标成功)", () => {
  // public-state.test.ts 已钉五态主表与 brand_suspended;这里补检查表引用的
  // 暂停服务/错误域名/退出家系文案(错租户=404 同形,无独立错误态,见检查表)。
  const fieldCases: Array<[keyof typeof STATE_TEXT, string, string]> = [
    ["tenant_suspended", "商家暂停接待", "已提交的信息仍然保留"],
    ["domain_mismatch", "不属于当前域名", "不要从其他品牌域名打开"],
    ["domain_error", "域名未通过验证", "请稍后再试"],
    ["not_found", "活动不存在", "请核对二维码是否正确"],
    ["link_disabled", "链接已停用", "请联系商家获取最新的活动入口"],
  ];
  for (const [state, textPart, actionPart] of fieldCases) {
    it(`${state} 文案与动作非空且含判据词`, () => {
      expect(STATE_TEXT[state]).toContain(textPart);
      expect(STATE_ACTION[state]).toContain(actionPart);
    });
  }
});
