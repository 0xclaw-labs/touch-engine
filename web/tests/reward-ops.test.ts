import { describe, expect, it } from "vitest";
import {
  benefitStatusText,
  canConfirmRewards,
  canManageInventory,
  canReverseRewards,
  confirmRequestBody,
  evidenceText,
  faceAndCostText,
  inventoryLine,
  maskCode,
  summaryLines,
} from "../src/lib/reward-ops";

describe("奖励状态文案不把未知画成成功", () => {
  it("五个状态各自有诚实文案", () => {
    expect(benefitStatusText("reserved")).toContain("预留");
    expect(benefitStatusText("issued")).toContain("已发放");
    expect(benefitStatusText("redeemed")).toContain("已核销");
    expect(benefitStatusText("reversed")).toContain("已更正撤销");
    expect(benefitStatusText("expired")).toContain("预留已过期");
    // 未知状态保持未知,不写成已核销。
    expect(benefitStatusText("")).toContain("未知");
    expect(benefitStatusText("shipped")).toContain("未知");
  });

  it("证据三态 + 未认定", () => {
    expect(evidenceText("staff_observed_submission")).toContain("店员见到提交");
    expect(evidenceText("staff_verified_post")).toContain("店员核对作品");
    expect(evidenceText("official_publish_confirmed")).toContain("官方确认");
    expect(evidenceText("")).toContain("未认定");
    expect(evidenceText("self_report")).toContain("未知");
  });
});

describe("敏感券码默认掩码", () => {
  it("只露尾四位,赠品无码如实说明", () => {
    expect(maskCode("SCabcdef1234")).toBe("••••1234");
    expect(maskCode("")).toContain("现场赠品");
    expect(maskCode("ab")).toBe("••••");
    // 掩码结果不泄露完整码。
    expect(maskCode("SCabcdef1234")).not.toContain("SCabcdef1234");
  });
});

describe("面额与成本分开", () => {
  it("面额不是成本", () => {
    const line = faceAndCostText(500, 300, "FEN");
    expect(line).toContain("面额");
    expect(line).toContain("成本");
    expect(line).not.toContain("扣款");
    expect(faceAndCostText(0, 200, "FEN")).toContain("面额 0");
  });
});

describe("角色权限:预算与更正归 org_owner,店员只确认核销", () => {
  it("org_owner 管批次/撤销;staff、店经理可确认;agent 都不行", () => {
    expect(canManageInventory("org_owner")).toBe(true);
    expect(canManageInventory("staff")).toBe(false);
    expect(canManageInventory("store_manager")).toBe(false);
    expect(canReverseRewards("org_owner")).toBe(true);
    expect(canReverseRewards("staff")).toBe(false);
    expect(canConfirmRewards("staff")).toBe(true);
    expect(canConfirmRewards("org_owner")).toBe(true);
    expect(canConfirmRewards("store_manager")).toBe(true);
    expect(canConfirmRewards("agent")).toBe(false);
  });
});

describe("汇总行", () => {
  it("五状态 + 三证据 + 待核验分开", () => {
    const lines = summaryLines({
      campaign_id: "c1",
      as_of: "2026-10-14T00:00:00Z",
      counts: { reserved: 1, issued: 2, redeemed: 3, reversed: 0, expired: 1 },
      evidence_granted: {
        staff_observed_submission: 2,
        staff_verified_post: 1,
        official_publish_confirmed: 0,
      },
      pending_review: 4,
      committed_face_minor: 100,
      committed_cost_minor: 50,
      pending_face_minor: 10,
      pending_cost_minor: 5,
    });
    const joined = lines.join(" ");
    expect(joined).toContain("预留 1");
    expect(joined).toContain("已发放 2");
    expect(joined).toContain("已核销 3");
    expect(joined).toContain("待核验（顾客自报，未处理） 4");
    expect(joined).toContain("店员见到提交");
  });
});

describe("确认请求体", () => {
  it("携带参与引用、slot、版本、证据、事件键与现场兑现", () => {
    const body = confirmRequestBody({
      campaignId: "c1",
      participationId: "part-9",
      benefitSlot: "first_publish",
      ruleVersion: 3,
      evidence: "staff_verified_post",
      storeId: "sto-1",
      fulfillNow: true,
      note: "现场兑现",
    });
    expect(body["participation_id"]).toBe("part-9");
    expect(body["benefit_slot"]).toBe("first_publish");
    expect(body["rule_version"]).toBe(3);
    expect(body["evidence"]).toBe("staff_verified_post");
    expect(body["fulfill_now"]).toBe(true);
    expect(typeof body["event_id"]).toBe("string");
    expect((body["event_id"] as string).length).toBeGreaterThan(8);
    expect(body["redeem_event_id"]).not.toBe(body["event_id"]);
  });
});

describe("批次行", () => {
  it("余量、状态、适用门店可见", () => {
    const line = inventoryLine({
      id: "inv-1",
      name: "开业券",
      kind: "store_coupon",
      benefit_slot: "first_publish",
      rule_version: 3,
      total_qty: 10,
      available_qty: 7,
      status: "active",
      store_id: "sto-1",
      face_value_minor: 500,
      cost_minor: 300,
      currency: "FEN",
      allowed_evidence: ["staff_observed_submission"],
    });
    expect(line).toContain("开业券");
    expect(line).toContain("7/10");
    expect(line).toContain("首发发布");
  });
});
