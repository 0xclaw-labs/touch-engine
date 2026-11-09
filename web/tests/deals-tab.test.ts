import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.resolve(process.cwd(), "src");

function read(rel: string): string {
  return readFileSync(path.join(root, rel), "utf8");
}

const page = read("app/work/rewards/page.tsx");
const lib = read("lib/deals.ts");

// HUI-4590: /work/rewards 外部团购页签落地——上下架/编辑/校验时间管理 +
// 外部券授权核销工具 + 分渠道归因（点击≠成交）诚实文案。

describe("/work/rewards 外部团购页签(HUI-4590 落地)", () => {
  it("真实套餐管理端口接入:按门店维护(无活动也可用)", () => {
    expect(page).toContain("deal-offers`");
    expect(page).toContain('data-action="create-offer"');
    expect(page).toContain('data-field="offer-create"');
    expect(page).toContain('data-field="external-store"');
    expect(page).toContain('data-field="external-offers"');
  });

  it("上下架/退役/编辑/校验时间/授权记录都在页签内", () => {
    expect(page).toContain('data-action="offer-pause"');
    expect(page).toContain('data-action="offer-resume"');
    expect(page).toContain('data-action="offer-retire"');
    expect(page).toContain('data-action="offer-retitle"');
    expect(page).toContain('data-action="offer-reprice"');
    expect(page).toContain('data-action="offer-verify"');
    expect(page).toContain('data-action="offer-auth"');
    expect(page).toContain("mark_price_verified");
    expect(page).toContain("72 小时");
  });

  it("三平台下拉与错挂渠道警告(美团↔美团、点评↔点评、抖音↔抖音)", () => {
    expect(page).toContain('value="meituan"');
    expect(page).toContain('value="dianping"');
    expect(page).toContain('value="douyin"');
    expect(page).toContain("错挂渠道会被拒绝");
  });

  it("外部券核销工具:走授权适配器,未接通诚实拒绝;超时回查不重发核销", () => {
    expect(page).toContain('data-action="coupon-check"');
    expect(page).toContain('data-action="coupon-redeem"');
    expect(page).toContain("external-vouchers/${kind}");
    expect(page).toContain('run("inspect")');
    expect(page).toContain('run("redeem")');
    expect(page).toContain("不本地假核销");
    // 券码不回显进任何日志面文案。
    expect(page).toContain("券码不进日志");
    // 待确认文案：同一请求回查，不重发核销。
    expect(lib).toContain("待确认");
    expect(lib).toContain("不会重复核销");
  });

  it("诚实文案:点击不是成交;平台成交本地不可知;1671 占位纪律升级保留", () => {
    expect(page).toContain("点击不是成交");
    expect(page).toContain("HUI-4590");
    expect(page).toContain("不代发也不本地核销");
    expect(page).toContain("没有事实不写成功");
    // 1671 外部发放登记保留,待确认仍是合法展示态。
    expect(page).toContain("reward-external-issuances");
    expect(page).toContain(externalStateRef);
  });

  it("分渠道保留:套餐行按平台徽标展示,点击数单独标注", () => {
    expect(page).toContain("platformText(offer.platform)");
    expect(page).toContain('data-field="offer-clicks"');
    expect(lib).toContain('case "meituan":');
    expect(lib).toContain('case "dianping":');
    expect(lib).toContain('case "douyin":');
  });

  it("lib 文案纪律:未核对价格=到平台查看;未知不是已核销;已核销回读不重复", () => {
    expect(lib).toContain("到平台查看");
    expect(lib).toContain("未知不是已核销");
    expect(lib).toContain("already_redeemed");
    expect(lib).toContain("failed");
    expect(lib).toContain("券当前不可用");
  });
});

const externalStateRef = "待确认";
