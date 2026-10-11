"use client";

import { useCallback, useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { LoadErrorState } from "@/components/work/load-error-state";
import { surfaceLabel } from "@/lib/product-finish";

interface Campaign {
  id: string;
  title: string;
}

interface AssetRef {
  id: string;
  campaign_id?: string;
  asset_id: string;
  version?: string;
}

interface LibraryItem {
  id: string;
  purpose?: string;
  media_type?: string;
  asset_ref?: string;
}

export default function MaterialsPage() {
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "error" | "empty" | "ready">("loading");
  const [error, setError] = useState("");
  const [campaigns, setCampaigns] = useState<Campaign[]>([]);
  const [refs, setRefs] = useState<AssetRef[]>([]);
  const [library, setLibrary] = useState<LibraryItem[]>([]);
  const [libraryNote, setLibraryNote] = useState("");
  const [draft, setDraft] = useState({ campaign: "", asset_id: "", version: "" });

  const load = useCallback(async () => {
    setPhase("loading");
    const campaignRes = await session.api("GET", "campaigns");
    if (!campaignRes.ok) {
      setError(failureText(campaignRes.status, campaignRes.data));
      setPhase("error");
      return;
    }
    const items = Array.isArray(campaignRes.data.items) ? (campaignRes.data.items as Campaign[]) : [];
    setCampaigns(items);
    const lists = await Promise.all(items.map(async (item) => {
      const res = await session.api("GET", `campaigns/${item.id}/assets`);
      if (!res.ok || !Array.isArray(res.data.items)) return [] as AssetRef[];
      return res.data.items as AssetRef[];
    }));
    setRefs(lists.flat());
    const lib = await session.api("GET", "assets");
    if (lib.status === 404) {
      setLibrary([]);
      setLibraryNote("素材库未开通。下面只显示已经挂到活动上的引用，不显示 0 个文件。");
    } else if (!lib.ok) {
      setLibraryNote("素材库没有读到。活动引用仍然列在下面。");
      setLibrary([]);
    } else {
      setLibrary(Array.isArray(lib.data.items) ? (lib.data.items as LibraryItem[]) : []);
      setLibraryNote("");
    }
    setPhase(items.length === 0 && lists.flat().length === 0 ? "empty" : "ready");
  }, [session]);

  useEffect(() => {
    void load().catch(() => setPhase("error"));
  }, [load]);

  async function addRef(event: React.FormEvent) {
    event.preventDefault();
    const res = await session.api("POST", `campaigns/${draft.campaign}/assets`, {
      asset_id: draft.asset_id,
      version: draft.version || undefined,
    });
    if (!res.ok) {
      setError(failureText(res.status, res.data));
      return;
    }
    setDraft({ campaign: "", asset_id: "", version: "" });
    setError("");
    await load();
  }

  return (
    <main>
      <h1 className="tk-title">素材</h1>
      <p className="tk-lead">这里引用已经存在的素材，不复制文件，也不把未知数量写成 0。</p>
      {phase === "loading" ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
      {phase === "error" ? <LoadErrorState message={error || surfaceLabel("error")} onRetry={() => void load()} /> : null}
      {phase === "empty" ? <p className="tk-state" data-state="empty">还没有可挂的活动素材。先有活动，再添加引用。</p> : null}
      {libraryNote ? <p className="tk-unknown">{libraryNote}</p> : null}
      {error && phase !== "error" ? <p className="tk-danger">{error}</p> : null}
      {library.length > 0 ? (
        <section>
          <h2 className="tk-section-title">素材库</h2>
          <ul className="tk-list">
            {library.map((item) => (
              <li key={item.id}>{item.purpose || item.media_type || item.asset_ref || item.id}</li>
            ))}
          </ul>
        </section>
      ) : null}
      <section>
        <h2 className="tk-section-title">活动上的引用</h2>
        {refs.length === 0 && phase === "ready" ? <p className="tk-note">活动上还没有素材引用。</p> : null}
        <ul className="tk-list">
          {refs.map((item) => (
            <li key={item.id}>{item.asset_id}{item.version ? ` ${item.version}` : ""}</li>
          ))}
        </ul>
      </section>
      <form className="tk-form tk-section" onSubmit={addRef}>
        <h2 className="tk-section-title">添加引用</h2>
        <select className="tk-select" aria-label="选择活动" value={draft.campaign} onChange={(event) => setDraft({ ...draft, campaign: event.target.value })} required>
          <option value="">选择活动</option>
          {campaigns.map((item) => <option key={item.id} value={item.id}>{item.title}</option>)}
        </select>
        <input className="tk-input" placeholder="平台 asset_id" value={draft.asset_id} onChange={(event) => setDraft({ ...draft, asset_id: event.target.value })} required />
        <input className="tk-input" placeholder="版本，可空" value={draft.version} onChange={(event) => setDraft({ ...draft, version: event.target.value })} />
        <button className="tk-button" type="submit">添加引用</button>
      </form>
    </main>
  );
}
