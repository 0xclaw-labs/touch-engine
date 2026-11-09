import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

import {
  benefitRowFace,
  expiryReasonFace,
  maskBenefitCode,
  parseVisitorBenefits,
  type VisitorBenefitRow,
} from "../src/lib/visitor-benefits";

// HUI-1896 C5 我的权益与凭证(/s/{storeCode}/benefits):
// - 只读 1671 访客本人权益面(GET /api/visitor/reward-benefits),活动结束/
//   订阅到期不拦读(T16 由服务端保证,页面不给「活动结束看不到券」错误);
// - 展示码重签:旧码不续,重签短时新码,只显示一次;staff_verify 码不进本页兑换;
// - external_state=pending_unknown 不把本系统已发放拖成「奖励未知」;
// - 无访客会话 401 给诚实引导,不自动造身份;REWARD_OPS off 404 诚实待开通。

const root = path.dirname(fileURLToPath(import.meta.url));
const src = (p: string) => readFileSync(path.join(root, "..", "src", p), "utf8");

function codeOf(source: string): string {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split("\n")
    .map((line) => line.replace(/(^|\s)\/\/.*$/, ""))
    .join("\n");
}

const view = codeOf(src("app/s/[storeCode]/benefits/benefits-view.tsx"));
const rewardStatus = codeOf(src("components/customer/RewardStatus.tsx"));

function row(partial: Partial<VisitorBenefitRow>): VisitorBenefitRow {
  return {
    benefit: { id: "bn_1", status: "issued", code: "CUP-8817", store_id: "st_1", face_value_minor: 2000, currency: "CNY" },
    expiry_reasons: [],
    redeem_allowed: true,
    is_external: false,
    ...partial,
  };
}

describe("parseVisitorBenefits:白名单解析", () => {
  it("解析 items;非对象/缺 items → null", () => {
    const rows = parseVisitorBenefits({ items: [row({})], platform_user_created: false });
    expect(rows).toHaveLength(1);
    expect(rows?.[0].benefit.id).toBe("bn_1");
    expect(parseVisitorBenefits(null)).toBeNull();
    expect(parseVisitorBenefits({})).toBeNull();
    expect(parseVisitorBenefits({ items: "x" })).toBeNull();
  });

  it("缺 id/status 的条目丢弃(不渲染残缺卡)", () => {
    const rows = parseVisitorBenefits({ items: [{ benefit: { status: "issued" } }] });
    expect(rows).toHaveLength(0);
  });
});

describe("maskBenefitCode:只露尾 4 位", () => {
  it("长码掩码;短码全掩;无码给现场说明", () => {
    expect(maskBenefitCode("CUP-8817")).toBe("••••8817");
    expect(maskBenefitCode("ab")).toBe("••••");
    expect(maskBenefitCode("")).toContain("现场");
  });
});

describe("expiryReasonFace:六种到期独立解释", () => {
  it("每种原因有独立中文;未知原因按未知", () => {
    expect(expiryReasonFace("subscription_expired")).toContain("订阅");
    expect(expiryReasonFace("campaign_ended")).toContain("活动");
    expect(expiryReasonFace("channel_closed")).toContain("渠道");
    expect(expiryReasonFace("participation_closed")).toContain("参与");
    expect(expiryReasonFace("reservation_ttl_elapsed")).toContain("预留");
    expect(expiryReasonFace("benefit_expired")).toContain("有效期");
    expect(expiryReasonFace("weird")).toContain("未知");
  });
});

describe("benefitRowFace:状态与外部发放诚实", () => {
  it("issued:已发放+掩码券码+适用门店;redeem_allowed=false 给 blocker 说明但不否认权益", () => {
    const face = benefitRowFace(row({ redeem_allowed: false, redeem_blockers: ["benefit_expired"] }));
    expect(face.status).toBe("issued");
    expect(face.lines.join("")).toContain("已发放");
    expect(face.lines.join("")).toContain("••••8817");
    expect(face.lines.join("")).toContain("有效期");
  });

  it("redeemed:已兑现不再引导重复领券", () => {
    const face = benefitRowFace(row({ benefit: { id: "bn_1", status: "redeemed", code: "C2" } }));
    expect(face.status).toBe("redeemed");
    expect(face.lines.join("")).toContain("已兑现");
    expect(face.lines.join("")).not.toContain("领取");
  });

  it("external pending_unknown 不拖黑本系统已发放", () => {
    const face = benefitRowFace(row({ is_external: true, external_state: "pending_unknown" }));
    expect(face.lines.join("")).toContain("已发放");
    expect(face.lines.join("")).toContain("确认中");
    expect(face.status).toBe("issued");
  });

  it("external failed:外部发放没有完成,给请店员协助;issued 的本地事实仍在", () => {
    const face = benefitRowFace(row({ is_external: true, external_state: "failed" }));
    expect(face.lines.join("")).toContain("没有完成");
    expect(face.lines.join("")).toContain("店员");
  });

  it("未知状态 → unknown 语气,不伪报", () => {
    const face = benefitRowFace(row({ benefit: { id: "bn_1", status: "weird" } }));
    expect(face.status).toBe("unknown");
    expect(face.lines.join("")).toContain("未知");
  });
});

describe("C5 视图:源码级断言", () => {
  it("读访客本人权益面;401/404/网络三态诚实", () => {
    expect(view.includes("/api/visitor/reward-benefits")).toBe(true);
    expect(view.includes("benefits-visitor-required")).toBe(true);
    expect(view.includes("benefits-unavailable")).toBe(true);
    expect(view.includes("重试")).toBe(true);
  });

  it("活动结束/订阅到期不拦读的说明在场(T16)", () => {
    expect(view.includes("活动结束")).toBe(true);
    expect(view.includes("仍")).toBe(true);
  });

  it("展示码重签 POST display-code;只显示一次;不落盘", () => {
    expect(view.includes("display-code")).toBe(true);
    expect(view.includes("只显示这一次")).toBe(true);
    expect(view.includes("localStorage")).toBe(false);
    expect(view.includes("sessionStorage")).toBe(false);
    expect(view.includes("customer_resume")).toBe(false);
  });

  it("无生态导航;返回门店入口;390/430/桌面列模式", () => {
    expect(view.includes("EcoTopNav")).toBe(false);
    expect(view.includes("返回门店")).toBe(true);
    expect(view.includes("data-columns")).toBe(true);
    expect(view.includes("data-viewport")).toBe(true);
  });

  it("RewardStatus 窄扩展:可选 benefitsHref 链(无则不渲染,零行为变化)", () => {
    expect(rewardStatus.includes("benefitsHref")).toBe(true);
    expect(rewardStatus.includes("查看我的权益与凭证")).toBe(true);
  });
});
