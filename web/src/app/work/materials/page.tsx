"use client";

import { useCallback, useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { LoadErrorState } from "@/components/work/load-error-state";
import { StoreContentDrawer } from "@/components/work/store-content-drawer";
import { readWorkReturnContext } from "@/lib/work/workbench-shell";

interface StoreOption { id: string; name: string }
interface Campaign { id: string; store_id?: string; title: string; public_content?: string }

export default function MaterialsPage() {
  const session = useSession();
  const [stores, setStores] = useState<StoreOption[]>([]);
  const [storeID, setStoreID] = useState("");
  const [campaign, setCampaign] = useState<Campaign | null>(null);
  const [context, setContext] = useState({ campaignId: "", draftId: "", purpose: "门店顾客页内容", explicitReturn: "" });
  const [phase, setPhase] = useState<"loading" | "error" | "ready">("loading");
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setPhase("loading");
    try {
      const search = window.location.search;
      const params = new URLSearchParams(search);
      const origin = readWorkReturnContext(search, "");
      const campaignId = origin.campaignId;
      setContext({
        campaignId,
        draftId: origin.draftId,
        purpose: params.get("purpose") || "门店顾客页内容",
        explicitReturn: origin.hasExplicitReturn ? origin.returnHref : "",
      });
      const result = await session.api("GET", "stores");
      if (!result.ok) { setError(failureText(result.status, result.data)); setPhase("error"); return; }
      const items = Array.isArray(result.data.items) ? result.data.items as StoreOption[] : [];
      let targetStore = origin.storeId;
      if (campaignId) {
        const campaignResult = await session.api("GET", `campaigns/${encodeURIComponent(campaignId)}`);
        if (!campaignResult.ok) { setError(failureText(campaignResult.status, campaignResult.data)); setPhase("error"); return; }
        const current = campaignResult.data as unknown as Campaign;
        setCampaign(current); targetStore = current.store_id || targetStore;
      } else setCampaign(null);
      if (targetStore && !items.some((item) => item.id === targetStore)) { setError("当前用途的门店无权访问，请返回原用途重新打开内容编辑器。"); setPhase("error"); return; }
      setStores(items); setStoreID((current) => targetStore || (items.some((item) => item.id === current) ? current : items[0]?.id || ""));
      setError(""); setPhase("ready");
    } catch { setError("内容用途尚未读到，请重试。"); setPhase("error"); }
  }, [session]);

  useEffect(() => { void load(); }, [load]);
  const store = stores.find((item) => item.id === storeID);
  const fallbackReturn = context.campaignId
    ? `/work/campaigns/${encodeURIComponent(context.campaignId)}`
    : storeID
      ? `/work/stores/${encodeURIComponent(storeID)}`
      : "/work/stores";
  const returnHref = context.explicitReturn || fallbackReturn;

  return <main>
    <h1 className="tk-title">准备经营内容</h1>
    <p className="tk-lead">上传文件、选择已有或帮我制作，共用一份内容。预览后明确采用，再回当前用途确认发布。</p>
    <p className="tk-note"><a data-action="return-origin" href={returnHref}>返回{context.explicitReturn ? "原位置" : campaign ? "当前活动" : "门店工作区"}</a></p>
    {phase === "loading" ? <p className="tk-state">正在读取当前用途…</p> : null}
    {phase === "error" ? <LoadErrorState message={error} onRetry={() => void load()} /> : null}
    {phase === "ready" ? <>
      {!stores.length ? <p className="tk-note">还没有门店。请先在<a href="/work/stores">门店管理</a>建立门店。</p> : <>
        <label className="tk-label">当前门店<select className="tk-select" aria-label="选择门店" value={storeID} disabled={Boolean(context.campaignId)} onChange={(event) => setStoreID(event.target.value)}>{stores.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
        {storeID ? <StoreContentDrawer key={`${storeID}:${context.campaignId}:${context.draftId}`} storeId={storeID} storeName={store?.name} campaignId={context.campaignId || undefined} purpose={context.purpose} initialTitle={campaign?.title || ""} initialBody={campaign?.public_content || ""} returnHref={returnHref} /> : null}
      </>}
    </> : null}
  </main>;
}
