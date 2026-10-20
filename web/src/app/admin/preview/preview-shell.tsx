"use client";

import { useState } from "react";
import { EcoTopNav } from "@/components/eco-nav/EcoTopNav";
import { useLaunchTargets } from "@/components/eco-nav/use-launch-resolver";
import { TaskHandoffActions } from "@/components/admin/TaskHandoffActions";
import { PROVISIONAL_DOCUMENT, provisionalEcoNav } from "@/lib/eco-nav/fixture";
import { parseEcoNavDocument, toViewModel, type EcoNavModel } from "@/lib/eco-nav/model";
import { applyMerchantSwitch } from "@/lib/eco-nav/touch-shell";

function brandModel(displayName: string, appId: string, appName: string, targetId: string): EcoNavModel {
  const doc = structuredClone(PROVISIONAL_DOCUMENT);
  doc.brand.display_name = displayName;
  doc.brand.brand_id = displayName === "品牌甲" ? "brand-a" : "brand-b";
  doc.visible_apps = [
    doc.visible_apps[0],
    {
      app_id: appId,
      display_name: appName,
      icon_ref: null,
      state: "launchable",
      launch_mode: "sso_launch",
      launch_target_id: targetId,
      unavailable_reason: null,
    },
  ];
  const parsed = parseEcoNavDocument(doc);
  if (!parsed.ok) return provisionalEcoNav();
  return toViewModel(parsed.document, { provenance: "provisional_fixture", statusSummary: "预览上下文 · 未接公共身份" });
}

export function PreviewShell() {
  const [model, setModel] = useState(() => brandModel("品牌甲", "product-image", "产品图", "ti-product-image-web"));
  const resolveLaunchTarget = useLaunchTargets();
  const [surface, setSurface] = useState({ campaigns: [{ id: "cmp_preview", title: "活动甲" }], selectedCampaignId: "cmp_preview" });
  const [notice, setNotice] = useState("");

  return (
    <main className="tk-admin-shell">
      <EcoTopNav
        model={model}
        nickname="代理小林"
        sessionRole="agent"
        honorScopeMerchant
        resolveLaunchTarget={resolveLaunchTarget}
        onTenantChange={() => {
          setSurface((current) => applyMerchantSwitch(current));
          setNotice("");
        }}
      />
      <p data-testid="preview-note">这是商家后台预览。公共活动页、NFC、二维码和留资页没有这条导航。</p>
      <div className="tk-admin-form-row tk-admin-mt-12">
        <button type="button" onClick={() => setModel(brandModel("品牌甲", "product-image", "产品图", "ti-product-image-web"))}>
          品牌甲
        </button>
        <button type="button" onClick={() => setModel(brandModel("品牌乙", "leads", "获客", "ti-leads-web"))}>
          品牌乙
        </button>
      </div>
      <section data-testid="admin-campaigns">
        <h2>活动</h2>
        {surface.campaigns.length === 0 ? <p>当前商家还没有活动。</p> : null}
        {surface.campaigns.map((campaign) => (
          <div key={campaign.id}>
            <span>{campaign.title}</span>
            <TaskHandoffActions campaignId={campaign.id} onPlanned={setNotice} />
          </div>
        ))}
        {notice ? <p data-testid="task-notice">{notice}</p> : null}
      </section>
    </main>
  );
}
