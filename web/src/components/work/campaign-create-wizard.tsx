"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { failureText, useSession } from "@/components/work/merchant-session";
import { LoadErrorState } from "@/components/work/load-error-state";
import { ContentPackPanel } from "@/components/work/content-pack-panel";
import {
  EMPTY_DRAFT,
  draftErrors,
  draftStorageKey,
  parseDraft,
  serializeDraft,
  toRFC3339Local,
  type CampaignDraft,
} from "@/lib/campaign-create";
import { campaignShareText, campaignShareUrl, pickShareLink, shareAvailability, type ShareLink } from "@/lib/campaign-share";
import { surfaceLabel } from "@/lib/product-finish";

interface StoreRec {
  id: string;
  name: string;
  status?: string;
}

interface CampaignRec {
  id: string;
  title: string;
  status: string;
  store_id?: string;
  starts_at?: string;
  ends_at?: string;
}

type Step = 1 | 2 | 3;

function campaignStorageKey(tenantId: string): string {
  return `${draftStorageKey(tenantId)}:campaign`;
}

function stepTitle(step: Step): string {
  if (step === 1) return "门店与活动";
  if (step === 2) return "内容与素材";
  return "链接与发布";
}

export function CampaignCreateWizard() {
  const session = useSession();
  const router = useRouter();
  const [step, setStep] = useState<Step>(1);
  const [draft, setDraft] = useState<CampaignDraft>(EMPTY_DRAFT);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [restored, setRestored] = useState(false);
  const [stores, setStores] = useState<StoreRec[]>([]);
  const [storesPhase, setStoresPhase] = useState<"loading" | "error" | "empty" | "ready">("loading");
  const [campaignId, setCampaignId] = useState("");
  const [campaign, setCampaign] = useState<CampaignRec | null>(null);
  const [links, setLinks] = useState<ShareLink[]>([]);
  const [linksPhase, setLinksPhase] = useState<"loading" | "error" | "ready">("loading");
  const [origin, setOrigin] = useState("");

  const loadStores = useCallback(async () => {
    setStoresPhase("loading");
    const res = await session.api("GET", "stores");
    if (!res.ok) {
      setError(failureText(res.status, res.data));
      setStoresPhase("error");
      return;
    }
    const items = Array.isArray(res.data.items) ? (res.data.items as StoreRec[]) : [];
    const active = items.filter((item) => item.status !== "disabled");
    setStores(active);
    setStoresPhase(active.length === 0 ? "empty" : "ready");
  }, [session]);

  useEffect(() => {
    setOrigin(window.location.origin);
    const saved = parseDraft(window.localStorage.getItem(draftStorageKey(session.tenantId)));
    if (saved) {
      setDraft(saved);
      setRestored(true);
    }
    const savedCampaign = window.localStorage.getItem(campaignStorageKey(session.tenantId)) ?? "";
    if (savedCampaign) {
      setCampaignId(savedCampaign);
      setStep(2);
    }
    void loadStores().catch(() => {
      setError("没有连上服务，门店没有读到。");
      setStoresPhase("error");
    });
  }, [loadStores, session.tenantId]);

  useEffect(() => {
    if (!session.tenantId) return;
    window.localStorage.setItem(draftStorageKey(session.tenantId), serializeDraft(draft));
  }, [draft, session.tenantId]);

  const loadLinks = useCallback(async () => {
    if (!campaignId) return;
    setLinksPhase("loading");
    const [campaignRes, linkRes] = await Promise.all([
      session.api("GET", `campaigns/${campaignId}`),
      session.api("GET", `campaigns/${campaignId}/links`),
    ]);
    if (campaignRes.ok) setCampaign(campaignRes.data as unknown as CampaignRec);
    if (!linkRes.ok) {
      setError(failureText(linkRes.status, linkRes.data));
      setLinksPhase("error");
      return;
    }
    setLinks(Array.isArray(linkRes.data.items) ? (linkRes.data.items as ShareLink[]) : []);
    setLinksPhase("ready");
  }, [campaignId, session]);

  useEffect(() => {
    if (step === 3) void loadLinks().catch(() => setLinksPhase("error"));
  }, [step, loadLinks]);

  function clearDraft() {
    window.localStorage.removeItem(draftStorageKey(session.tenantId));
    window.localStorage.removeItem(campaignStorageKey(session.tenantId));
    setDraft(EMPTY_DRAFT);
    setCampaignId("");
    setCampaign(null);
    setRestored(false);
    setStep(1);
    setNotice("");
  }

  async function saveStepOne(event: React.FormEvent) {
    event.preventDefault();
    setError("");
    setNotice("");
    const nextErrors = draftErrors(draft);
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;
    setBusy(true);
    const body = {
      title: draft.title.trim(),
      public_content: draft.public_content.trim(),
      store_id: draft.store_id,
      starts_at: toRFC3339Local(draft.starts_at_local) || undefined,
      ends_at: toRFC3339Local(draft.ends_at_local) || undefined,
    };
    try {
      const res = campaignId
        ? await session.api("PATCH", `campaigns/${campaignId}`, body)
        : await session.api("POST", "campaigns", body);
      if (!res.ok) {
        setError(failureText(res.status, res.data));
        return;
      }
      const id = campaignId || String(res.data.id ?? "");
      if (!id) {
        setError("活动草稿没有拿到编号，没有继续。请重试。");
        return;
      }
      setCampaignId(id);
      window.localStorage.setItem(campaignStorageKey(session.tenantId), id);
      setCampaign(res.data as unknown as CampaignRec);
      setNotice(campaignId ? "活动资料已更新。" : "草稿已保存。下一步选内容；这一步还没有发布。");
      setStep(2);
    } finally {
      setBusy(false);
    }
  }

  async function publish() {
    if (!campaignId || busy) return;
    setBusy(true);
    setError("");
    try {
      let enabled = pickShareLink(links);
      if (!enabled) {
        const linkRes = await session.api("POST", `campaigns/${campaignId}/links`, {});
        if (!linkRes.ok) {
          setError(failureText(linkRes.status, linkRes.data));
          return;
        }
        const created = linkRes.data as unknown as ShareLink;
        setLinks((current) => [created, ...current]);
        enabled = created;
      }
      const statusRes = await session.api("POST", `campaigns/${campaignId}/status`, { status: "active" });
      if (!statusRes.ok) {
        setError(failureText(statusRes.status, statusRes.data));
        return;
      }
      window.localStorage.removeItem(draftStorageKey(session.tenantId));
      window.localStorage.removeItem(campaignStorageKey(session.tenantId));
      router.push(`/work/campaigns/${campaignId}?created=1`);
    } finally {
      setBusy(false);
    }
  }

  const enabledLink = useMemo(() => pickShareLink(links), [links]);
  const shareUrl = enabledLink && origin ? campaignShareUrl(origin, enabledLink.code) : "";
  const availability = shareAvailability({
    status: campaign?.status ?? "draft",
    ends_at: campaign?.ends_at ?? null,
    link: enabledLink,
  });

  return (
    <main data-pattern="creation" data-step={step}>
      <p className="tk-kicker"><a href="/work/campaigns">全部活动</a></p>
      <h1 className="tk-title">新建活动</h1>
      <p className="tk-lead">先保存草稿，再补内容和链接。每一步只做一件事；草稿不会因为换页或刷新丢掉。</p>

      <ol className="tk-steps" aria-label="创建步骤">
        {[1, 2, 3].map((item) => (
          <li key={item} className={item === step ? "tk-step-current" : item < step ? "tk-step-done" : ""} aria-current={item === step ? "step" : undefined}>
            {item}. {stepTitle(item as Step)}
          </li>
        ))}
      </ol>

      {restored && step === 1 ? (
        <p className="tk-note" data-testid="draft-restored">
          已恢复上次没提交完的草稿。
          <button type="button" className="tk-quiet" onClick={clearDraft}>清空草稿</button>
        </p>
      ) : null}
      {notice ? <p className="tk-ok" data-testid="create-notice">{notice}</p> : null}
      {error ? <p className="tk-danger" data-state="error">{error}</p> : null}

      {step === 1 ? (
        <form className="tk-form tk-section" onSubmit={saveStepOne} data-pattern-region="primary_action">
          <h2 className="tk-section-title">① 门店与活动</h2>
          {storesPhase === "loading" ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
          {storesPhase === "error" ? (
            <LoadErrorState message={error || "门店没有读到，不能随便写一个门店。"} onRetry={() => void loadStores()} />
          ) : null}
          {storesPhase === "empty" ? (
            <div className="tk-state" data-state="empty">
              <p>还没有可用门店。顾客打开活动时需要知道是哪家店。</p>
              <a className="tk-button" href="/work/stores">先登记门店</a>
            </div>
          ) : null}
          {storesPhase === "ready" ? (
            <label className="tk-label">
              门店
              <select className="tk-select" value={draft.store_id} onChange={(event) => setDraft({ ...draft, store_id: event.target.value })} required>
                <option value="">选择活动所在门店</option>
                {stores.map((store) => <option key={store.id} value={store.id}>{store.name}</option>)}
              </select>
              {errors.store_id ? <span className="tk-danger">{errors.store_id}</span> : null}
            </label>
          ) : null}
          <label className="tk-label">
            活动标题
            <input className="tk-input" value={draft.title} onChange={(event) => setDraft({ ...draft, title: event.target.value })} placeholder="例如：周末到店五折" required />
            {errors.title ? <span className="tk-danger">{errors.title}</span> : null}
          </label>
          <label className="tk-label">
            顾客能看到的内容
            <textarea className="tk-input tk-input-tall" value={draft.public_content} onChange={(event) => setDraft({ ...draft, public_content: event.target.value })} placeholder="用一句话说清顾客到店能得到什么" />
            {errors.public_content ? <span className="tk-danger">{errors.public_content}</span> : null}
          </label>
          <label className="tk-label">
            开始时间（本地时间）
            <input className="tk-input" type="datetime-local" value={draft.starts_at_local} onChange={(event) => setDraft({ ...draft, starts_at_local: event.target.value })} />
            {errors.starts_at ? <span className="tk-danger">{errors.starts_at}</span> : null}
          </label>
          <label className="tk-label">
            结束时间（本地时间）
            <input className="tk-input" type="datetime-local" value={draft.ends_at_local} onChange={(event) => setDraft({ ...draft, ends_at_local: event.target.value })} />
            {errors.ends_at ? <span className="tk-danger">{errors.ends_at}</span> : null}
          </label>
          <button className="tk-primary" type="submit" disabled={busy || storesPhase !== "ready"} data-primary-action="true">
            {busy ? "正在保存…" : "保存草稿并继续"}
          </button>
          <p className="tk-note">这一步只保存草稿，不会发布，也不会生成链接。</p>
        </form>
      ) : null}

      {step === 2 ? (
        <section className="tk-section" data-pattern-region="primary_action">
          <h2 className="tk-section-title">② 内容与素材</h2>
          <p className="tk-note">内容包来自门店授权的素材。先预览，再点「采用这一版」；采用后顾客看到的就是这一版。含 AI 合成的内容会明确标出。</p>
          {campaignId ? <ContentPackPanel campaignId={campaignId} /> : null}
          <p className="tk-note">
            需要上传成片或从素材库挑选，去<a href={`/work/materials?campaign=${encodeURIComponent(campaignId)}`}>素材页</a>。素材库没开通时，这里会说明原因，不会伪造素材。
          </p>
          <div className="tk-row">
            <button type="button" className="tk-quiet" onClick={() => setStep(1)}>返回上一步</button>
            <button type="button" className="tk-primary" data-primary-action="true" onClick={() => { setError(""); setStep(3); }}>
              继续：链接与发布
            </button>
          </div>
          <p className="tk-note">没有采用内容也可以先建链接，但顾客页会显示“还没有准备好在线内容”。</p>
        </section>
      ) : null}

      {step === 3 ? (
        <section className="tk-section" data-pattern-region="primary_action">
          <h2 className="tk-section-title">③ 链接与发布</h2>
          <p className="tk-note">发布后顾客可以打开链接参与。活动仍是草稿时，打开链接会显示“活动未发布”。</p>
          {linksPhase === "loading" ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
          {linksPhase === "error" ? <LoadErrorState message={error || "短码没有读到。这不是还没有短码。"} onRetry={() => void loadLinks()} /> : null}
          {linksPhase === "ready" && enabledLink ? (
            <div className="tk-card" data-testid="create-share">
              <p className="tk-note">活动链接</p>
              <p><code>{shareUrl}</code></p>
              <p className="tk-note">{availability.note || "链接已启用，可以分享给顾客。"}</p>
              <button
                type="button"
                className="tk-quiet"
                onClick={() => void navigator.clipboard?.writeText(campaignShareText({
                  title: draft.title,
                  store_name: stores.find((item) => item.id === draft.store_id)?.name,
                  starts_at: toRFC3339Local(draft.starts_at_local) || null,
                  ends_at: toRFC3339Local(draft.ends_at_local) || null,
                  url: shareUrl,
                })).then(() => setNotice("分享文案已复制。")).catch(() => setNotice("浏览器没有允许复制，请手动选中链接。"))}
              >
                复制分享文案
              </button>
            </div>
          ) : null}
          {linksPhase === "ready" && !enabledLink ? (
            <p className="tk-note" data-state="empty">还没有启用的短码。点下面的主行动会先生成短码，再发布活动。</p>
          ) : null}
          <div className="tk-row">
            <button type="button" className="tk-quiet" onClick={() => setStep(2)}>返回上一步</button>
            <button type="button" className="tk-primary" data-primary-action="true" onClick={() => void publish()} disabled={busy}>
              {busy ? "正在发布…" : "生成链接并发布"}
            </button>
          </div>
          <button type="button" className="tk-quiet" onClick={() => router.push(`/work/campaigns/${campaignId}`)}>
            先保存草稿，稍后发布
          </button>
        </section>
      ) : null}
    </main>
  );
}
