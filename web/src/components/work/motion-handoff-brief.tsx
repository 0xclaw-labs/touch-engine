"use client";

import { useCallback, useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { surfaceLabel } from "@/lib/product-finish";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";

interface HandoffBrief {
  version: string;
  origin_context_ref: string;
  campaign: { id: string; title: string; status: string };
  store: { id: string; name: string; address: string; status: string };
  brand: {
    brand_id: string;
    context_available: boolean;
    context_unavailable_reason?: string;
  };
  offer: { offer_copy: string; price: string };
  cta: string;
  channels: string[];
  aspect_ratios: string[];
  landing: {
    short_code?: string;
    extra_jump_kinds: string[];
    authorized_return?: { href: string };
  };
  assets: { asset_id: string; version: string }[];
  params_version: number;
  digest: string;
  disposition: {
    motion_consumable: boolean;
    reason_code: string;
    required: string[];
  };
}

function briefFrom(data: unknown): HandoffBrief | null {
  if (!data || typeof data !== "object") return null;
  const b = data as Record<string, unknown>;
  if (typeof b.digest !== "string" || !b.digest.startsWith("sha256:"))
    return null;
  if (!b.disposition || typeof b.disposition !== "object") return null;
  const d = b.disposition as Record<string, unknown>;
  if (d.motion_consumable !== false || d.reason_code !== "upstream_unavailable")
    return null;
  if (typeof b.params_version !== "number") return null;
  return data as HandoffBrief;
}

export function MotionHandoffBrief({ campaignId }: { campaignId: string }) {
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "ready" | "error">("loading");
  const [brief, setBrief] = useState<HandoffBrief | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setPhase("loading");
    const res = await session.api("GET", `campaigns/${campaignId}/motion-handoff`);
    if (!res.ok) {
      setError(failureText(res.status, res.data));
      setBrief(null);
      setPhase("error");
      return;
    }
    const view = briefFrom(res.data);
    if (!view) {
      setError("没有完成（Motion Handoff Brief 的返回不能显示）");
      setBrief(null);
      setPhase("error");
      return;
    }
    setBrief(view);
    setError("");
    setPhase("ready");
  }, [campaignId, session]);

  useEffect(() => {
    void load().catch(() => setPhase("error"));
  }, [load]);

  return (
    <div id="motion-handoff" data-state={phase}>
      <h3 className="tk-section-title">Motion Handoff Brief</h3>
      {phase === "loading" ? (
        <div className="tk-gap" data-state="loading" role="status">
          <Skeleton className="h-6 w-1/3" />
          <Skeleton className="h-24 w-full" />
        </div>
      ) : null}
      {phase === "error" ? (
        <Alert variant="destructive" role="alert" data-state="error" className="tk-danger">
          <AlertDescription>{error || surfaceLabel("error")}</AlertDescription>
        </Alert>
      ) : null}
      {phase === "ready" && brief ? (
        <Card>
          <CardContent className="tk-gap pt-5">
            <p className="tk-note tk-unknown" data-field="disposition">
              上游 Motion 消费不可用（{brief.disposition.reason_code}
              ）。这里只是交给上游的输入，不是生成结果。
            </p>
            <p className="tk-note" data-field="campaign">
              活动 {brief.campaign.title}（{brief.campaign.status}）
            </p>
            <p className="tk-note" data-field="store">
              门店 {brief.store.name} {brief.store.address}
            </p>
            <p className="tk-note" data-field="offer">
              优惠 {brief.offer.offer_copy || "（未填）"}，价格{" "}
              {brief.offer.price || "（未填）"}
            </p>
            <p className="tk-note" data-field="cta">
              CTA {brief.cta || "（未填）"}
            </p>
            <p className="tk-note" data-field="channels">
              投放渠道 {brief.channels.join("、") || "（未填）"}
            </p>
            <p className="tk-note" data-field="aspect-ratios">
              画幅比例 {brief.aspect_ratios.join("、") || "（未填）"}
            </p>
            <p className="tk-note" data-field="landing">
              短码 {brief.landing.short_code || "（未铸码）"}；附加跳转{" "}
              {brief.landing.extra_jump_kinds.join("、") || "（无）"}
              {brief.landing.authorized_return
                ? `；授权返回 ${brief.landing.authorized_return.href}`
                : ""}
            </p>
            <p className="tk-note" data-field="assets">
              素材引用{" "}
              {brief.assets.length === 0
                ? "（无）"
                : brief.assets
                    .map((a) => `${a.asset_id}@${a.version}`)
                    .join("、")}
            </p>
            <p className="tk-note" data-field="params-version">
              参数版本 v{brief.params_version}
            </p>
            <p className="tk-note" data-field="digest">
              digest 尾8位 {brief.digest.slice(-8)}
            </p>
            <p className="tk-note" data-field="brand">
              品牌上下文{" "}
              {brief.brand.context_available
                ? "可读"
                : `不可用（${brief.brand.context_unavailable_reason || "unknown_reason"}）`}
            </p>
          </CardContent>
        </Card>
      ) : null}
    </div>
  );
}
