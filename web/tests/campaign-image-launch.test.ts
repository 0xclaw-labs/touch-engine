import { describe, expect, it } from "vitest";
import { launchCampaignImage, receiveCandidate } from "@/lib/eco-nav/campaign-image";

const input = {
  traceId: "lead:sub_1",
  campaignId: "cmp_1",
  campaignVersion: "7",
  assetRef: "ast_vid",
  sha256: "ab".repeat(32),
  grantRef: "grant_1",
  returnTargetId: "return:cmp_1",
};

describe("活动到产品图的候选入口", () => {
  it("只带引用和同一条 trace，不标成已生成", () => {
    const launch = launchCampaignImage(input);
    expect(launch).toMatchObject({
      trace_id: "lead:sub_1",
      campaign_id: "cmp_1",
      campaign_version: "7",
      asset_ref: "ast_vid",
      sha256: "ab".repeat(32),
      grant_ref: "grant_1",
      return_target_id: "return:cmp_1",
      candidate: true,
      generated: false,
      charges_customer: false,
    });
    expect(JSON.stringify(launch)).not.toMatch(/bytes|base64/);
  });

  it("上游声称已生成或已发布时拒绝接收", () => {
    const launch = launchCampaignImage(input);
    expect(() => receiveCandidate({ generated: true }, launch)).toThrow("upstream_claimed_generation");
    expect(() => receiveCandidate({ status: "generated" }, launch)).toThrow("upstream_claimed_generation");
    expect(() => receiveCandidate({ status: "published" }, launch)).toThrow("upstream_claimed_generation");
    expect(receiveCandidate({ status: "draft" }, launch).generated).toBe(false);
  });
});
