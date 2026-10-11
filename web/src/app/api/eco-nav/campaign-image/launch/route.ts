import { NextResponse } from "next/server";
import { launchCampaignImage, receiveCandidate } from "@/lib/eco-nav/campaign-image";

// 活动到产品图的入口。这里只收下候选引用，不调用产品图，也不生成。
export async function POST(request: Request) {
  const body = (await request.json().catch(() => null)) as Record<string, unknown> | null;
  if (!body) {
    return NextResponse.json({ message: "缺少交接", generated: false, charges_customer: false }, { status: 400 });
  }
  try {
    const launch = launchCampaignImage({
      traceId: String(body.trace_id ?? ""),
      campaignId: String(body.campaign_id ?? ""),
      campaignVersion: String(body.campaign_version ?? ""),
      assetRef: String(body.asset_ref ?? ""),
      sha256: String(body.sha256 ?? ""),
      grantRef: String(body.grant_ref ?? ""),
      returnTargetId: String(body.return_target_id ?? ""),
    });
    return NextResponse.json(receiveCandidate(body, launch));
  } catch (error) {
    const message = error instanceof Error ? error.message : "没有接收候选";
    const status = message === "upstream_claimed_generation" ? 409 : 400;
    return NextResponse.json({ message, generated: false, charges_customer: false }, { status });
  }
}
