"use client";

import { useCallback, useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { LoadErrorState } from "@/components/work/load-error-state";
import { isPastEnd, surfaceLabel } from "@/lib/product-finish";

interface Campaign {
  id: string;
  title: string;
  public_content?: string;
  status: string;
  starts_at?: string;
  ends_at?: string;
}

interface StoreRec {
  id: string;
  name: string;
  status?: string;
}

export default function CampaignsPage() {
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "error" | "empty" | "ready">("loading");
  const [campaigns, setCampaigns] = useState<Campaign[]>([]);
  const [stores, setStores] = useState<StoreRec[]>([]);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState("");
  const [draft, setDraft] = useState({ title: "", public_content: "", starts_at: "", ends_at: "", store_id: "" });

  const load = useCallback(async () => {
    setPhase("loading");
    const [campaignRes, storeRes] = await Promise.all([session.api("GET", "campaigns"), session.api("GET", "stores")]);
    if (!campaignRes.ok) {
      setError(failureText(campaignRes.status, campaignRes.data));
      setPhase("error");
      return;
    }
    setCampaigns(Array.isArray(campaignRes.data.items) ? (campaignRes.data.items as Campaign[]) : []);
    setStores(Array.isArray(storeRes.data.items) ? (storeRes.data.items as StoreRec[]) : []);
    setPhase("ready");
  }, [session]);

  useEffect(() => {
    setFilter(new URLSearchParams(window.location.search).get("state") ?? "");
    void load().catch(() => setPhase("error"));
  }, [load]);

  async function transition(id: string, status: string) {
    const res = await session.api("POST", `campaigns/${id}/status`, { status });
    if (!res.ok) setError(failureText(res.status, res.data));
    await load();
  }

  async function createCampaign(event: React.FormEvent) {
    event.preventDefault();
    const res = await session.api("POST", "campaigns", {
      title: draft.title,
      public_content: draft.public_content,
      starts_at: draft.starts_at || undefined,
      ends_at: draft.ends_at || undefined,
      store_id: draft.store_id || undefined,
    });
    if (!res.ok) {
      setError(failureText(res.status, res.data));
      return;
    }
    setDraft({ title: "", public_content: "", starts_at: "", ends_at: "", store_id: "" });
    setError("");
    await load();
  }

  const visible = campaigns.filter((item) => matches(item, filter));
  const showEmpty = phase === "ready" && (campaigns.length === 0 || visible.length === 0);

  return (
    <main>
      <h1 className="tk-title">活动</h1>
      <p className="tk-lead">今天要推进的活动在这里。暂停、结束和过了结束时间都单独标出，不写成还在进行。</p>
      <div className="tk-row">
        {["", "draft", "active", "paused", "ended", "expired"].map((item) => (
          <a key={item || "all"} href={item ? `/work/campaigns?state=${item}` : "/work/campaigns"}>{item ? labelFor(item) : "全部"}</a>
        ))}
      </div>
      {phase === "loading" ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
      {phase === "error" ? <LoadErrorState message={error || surfaceLabel("error")} onRetry={() => void load()} /> : null}
      {showEmpty ? <p className="tk-state" data-state="empty">{filter ? `没有${labelFor(filter)}的活动。` : "还没有活动。下面可以建一个草稿。"}</p> : null}
      {filter === "paused" && visible.length === 0 && phase === "ready" ? <p data-state="paused">{surfaceLabel("paused")}的活动还没有。</p> : null}
      {filter === "ended" && visible.length === 0 && phase === "ready" ? <p data-state="ended">{surfaceLabel("ended")}的活动还没有。</p> : null}
      {filter === "expired" && visible.length === 0 && phase === "ready" ? <p data-state="expired">{surfaceLabel("expired")}的活动还没有。</p> : null}
      {error && phase !== "error" ? <p className="tk-danger">{error}</p> : null}
      <ul className="tk-list">
        {visible.map((item) => {
          const expired = isPastEnd(item.ends_at);
          return (
            <li key={item.id}>
              <a href={`/work/campaigns/${item.id}`}>{item.title}</a>
              <span className="tk-note"> {statusLabel(item.status)}</span>
              {item.status === "paused" ? <span data-state="paused" className="tk-unknown"> {surfaceLabel("paused")}</span> : null}
              {item.status === "ended" ? <span data-state="ended" className="tk-note"> {surfaceLabel("ended")}</span> : null}
              {expired ? <span data-state="expired" className="tk-unknown"> {surfaceLabel("expired")}</span> : null}
              <div className="tk-row tk-gap">
                {item.status === "draft" ? <button className="tk-button" type="button" onClick={() => void transition(item.id, "active")}>启用</button> : null}
                {item.status === "active" ? <button className="tk-quiet" type="button" onClick={() => void transition(item.id, "paused")}>暂停</button> : null}
                {item.status === "paused" ? <button className="tk-quiet" type="button" onClick={() => void transition(item.id, "active")}>恢复</button> : null}
                {item.status === "active" || item.status === "paused" ? <button className="tk-quiet" type="button" onClick={() => void transition(item.id, "ended")}>结束</button> : null}
              </div>
            </li>
          );
        })}
      </ul>
      <form className="tk-form tk-section" onSubmit={createCampaign}>
        <h2 className="tk-section-title">新建草稿</h2>
        <input className="tk-input" placeholder="活动标题" value={draft.title} onChange={(event) => setDraft({ ...draft, title: event.target.value })} required />
        <input className="tk-input" placeholder="顾客能看到的内容" value={draft.public_content} onChange={(event) => setDraft({ ...draft, public_content: event.target.value })} />
        <input className="tk-input" placeholder="开始时间 RFC3339，可空" value={draft.starts_at} onChange={(event) => setDraft({ ...draft, starts_at: event.target.value })} />
        <input className="tk-input" placeholder="结束时间 RFC3339，可空" value={draft.ends_at} onChange={(event) => setDraft({ ...draft, ends_at: event.target.value })} />
        <select className="tk-select" aria-label="关联门店" value={draft.store_id} onChange={(event) => setDraft({ ...draft, store_id: event.target.value })}>
          <option value="">不关联门店</option>
          {stores.filter((store) => store.status !== "disabled").map((store) => (
            <option key={store.id} value={store.id}>{store.name}</option>
          ))}
        </select>
        <button className="tk-button" type="submit">创建活动</button>
      </form>
    </main>
  );
}

function matches(item: Campaign, filter: string): boolean {
  if (!filter) return true;
  if (filter === "expired") return isPastEnd(item.ends_at);
  return item.status === filter;
}

function labelFor(filter: string): string {
  if (filter === "draft") return "草稿";
  if (filter === "active") return "进行中";
  if (filter === "paused") return "暂停";
  if (filter === "ended") return "结束";
  if (filter === "expired") return "过期";
  return filter;
}

function statusLabel(status: string): string {
  if (status === "draft") return "草稿";
  if (status === "active") return "进行中";
  if (status === "paused") return "暂停";
  if (status === "ended") return "结束";
  return status || "状态未知";
}
