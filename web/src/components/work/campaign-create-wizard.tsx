"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { failureText, useSession } from "@/components/work/merchant-session";
import { LoadErrorState } from "@/components/work/load-error-state";
import { ContentEditor } from "@/components/work/store-content-drawer";
import { CampaignConfigPanel } from "@/components/work/campaign-config-panel";
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
import { DateTimeField } from "@/components/ui/date-time-field";

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
  const [adoptedVersion,setAdoptedVersion]=useState("");
  const [origin, setOrigin] = useState("");
  const hydrated = useRef(false);

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
    if(active.length===1)setDraft(current=>current.store_id?current:{...current,store_id:active[0].id});
    setStoresPhase(active.length === 0 ? "empty" : "ready");
  }, [session]);

  useEffect(() => {
    setOrigin(window.location.origin);
    const saved = parseDraft(window.localStorage.getItem(draftStorageKey(session.tenantId)));
    const queryStore=new URLSearchParams(window.location.search).get("store");
    if(queryStore)setDraft(current=>({...current,store_id:queryStore}));
    if (saved) {
      setDraft({...saved,store_id:queryStore||saved.store_id});
      setRestored(true);
    }
    const savedCampaign = window.localStorage.getItem(campaignStorageKey(session.tenantId)) ?? "";
    if (savedCampaign) {
      setCampaignId(savedCampaign);
      setStep(saved?.goal === "promotion" ? 2 : 3);
    }
    void loadStores().catch(() => {
      setError("没有连上服务，门店没有读到。");
      setStoresPhase("error");
    });
    hydrated.current = true;
  }, [loadStores, session.tenantId]);

  useEffect(() => {
    if (!hydrated.current || !session.tenantId) return;
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
      setNotice(campaignId ? "活动资料已更新。" : "草稿已保存。继续配置本次目标需要的内容或链接；这一步还没有发布。");
      setStep(draft.goal === "promotion" ? 2 : 3);
    } finally {
      setBusy(false);
    }
  }

  async function copyShare() {
    if (!shareUrl) return;
    const text = campaignShareText({
      title: draft.title,
      store_name: stores.find((item) => item.id === draft.store_id)?.name,
      starts_at: toRFC3339Local(draft.starts_at_local) || null,
      ends_at: toRFC3339Local(draft.ends_at_local) || null,
      url: shareUrl,
    });
    try {
      if (!navigator.clipboard) throw new Error("clipboard_unavailable");
      await navigator.clipboard.writeText(text);
      setNotice("分享文案已复制。");
    } catch {
      setError("浏览器没有允许复制。请手动选中链接。");
    }
  }

  async function publish() {
    if (!campaignId || busy) return;
    setBusy(true);
    setError("");
    try {
      if(draft.goal === "promotion") {
        const check=await session.api("GET",`campaigns/${campaignId}/publish-config`);
        if(!check.ok || check.data.status!=="enabled"){setError("先保存顾客承诺、准备关联库存并通过启用检查，再发布活动。");return;}
      }
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
      <p className="tk-lead">先决定顾客要办什么。普通门店服务直接进入门店工作区；只有本次优惠、咨询或推广才需要活动。草稿支持刷新恢复。</p>

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
          <fieldset className="tk-fieldset"><legend>本次希望顾客做什么</legend>
            <p>{draft.store_id?<a href={`/work/stores/${draft.store_id}?tab=entrances`}>只提供普通门店服务，去门店工作区</a>:<a href="/work/stores">只提供普通门店服务，先选择门店</a>}</p>
            <label className="tk-label"><input type="radio" name="goal" checked={draft.goal!=="promotion"} onChange={()=>setDraft({...draft,goal:"consultation"})}/>主动咨询：说明本次服务与期限，不配置推广奖励</label>
            <label className="tk-label"><input type="radio" name="goal" checked={draft.goal==="promotion"} onChange={()=>setDraft({...draft,goal:"promotion"})}/>内容推广与优惠：准备内容、核验条件和可承担的库存</label>
          </fieldset>
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
            开始时间（北京时间 UTC+8）
            <DateTimeField
              name="starts_at_local"
              aria-label="活动开始时间"
              value={draft.starts_at_local}
              invalid={Boolean(errors.starts_at)}
              errorId="wizard-starts-at-error"
              onChange={(value) => setDraft({ ...draft, starts_at_local: value })}
            />
            {errors.starts_at ? <span className="tk-danger" id="wizard-starts-at-error" role="alert" aria-live="polite">{errors.starts_at}</span> : null}
          </label>
          <label className="tk-label">
            结束时间（北京时间 UTC+8）
            <DateTimeField
              name="ends_at_local"
              aria-label="活动结束时间"
              value={draft.ends_at_local}
              invalid={Boolean(errors.ends_at)}
              errorId="wizard-ends-at-error"
              onChange={(value) => setDraft({ ...draft, ends_at_local: value })}
            />
            {errors.ends_at ? <span className="tk-danger" id="wizard-ends-at-error" role="alert" aria-live="polite">{errors.ends_at}</span> : null}
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
          <p className="tk-note">上传、选材或制作都在这里完成。采用只更新候选内容，尚未发布活动。</p>
          {campaignId && draft.store_id ? <ContentEditor storeId={draft.store_id} storeName={stores.find(s=>s.id===draft.store_id)?.name} campaignId={campaignId} purpose="活动推广内容" initialTitle={draft.title} initialBody={draft.public_content} returnHref="/work/campaigns/new" onAdopted={result=>{setAdoptedVersion(result.versionId);setNotice("内容已采用为候选，下一步配置顾客承诺。");}}/>:null}
          <div className="tk-row">
            <button type="button" className="tk-quiet" onClick={() => setStep(1)}>返回上一步</button>
            <button type="button" className="tk-primary" data-primary-action="true" onClick={() => { setError(""); setStep(3); }}>
              继续：链接与发布
            </button>
          </div>
          <p className="tk-note">内容推广启用前，服务端将检查采用版本、渠道准入与真实库存。</p>
        </section>
      ) : null}

      {step === 3 ? (
        <section className="tk-section" data-pattern-region="primary_action">
          <h2 className="tk-section-title">③ 链接与发布</h2>
          {draft.goal === "promotion" ? <CampaignConfigPanel campaignId={campaignId} campaignTitle={draft.title} campaignBody={draft.public_content} campaignStoreId={draft.store_id} adoptedVersionId={adoptedVersion}/> : <p className="tk-note">咨询活动只展示本次服务说明，不承诺推广奖励。</p>}
          <p className="tk-note">发布后顾客可以打开链接参与。活动仍是草稿时，打开链接会显示“活动未发布”。</p>
          {linksPhase === "loading" ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
          {linksPhase === "error" ? <LoadErrorState message={error || "短码没有读到。这不是还没有短码。"} onRetry={() => void loadLinks()} /> : null}
          {linksPhase === "ready" && enabledLink ? (
            <div className="tk-card" data-testid="create-share">
              <p className="tk-note">活动链接</p>
              <p><code>{shareUrl}</code></p>
              <p className="tk-note">{availability.note || "链接已启用，可以分享给顾客。"}</p>
              <button type="button" className="tk-quiet" onClick={() => void copyShare()}>
                复制分享文案
              </button>
            </div>
          ) : null}
          {linksPhase === "ready" && !enabledLink ? (
            <p className="tk-note" data-state="empty">还没有启用的短码。点下面的主行动会先生成短码，再发布活动。</p>
          ) : null}
          <div className="tk-row">
            <button type="button" className="tk-quiet" onClick={() => setStep(draft.goal === "promotion" ? 2 : 1)}>返回上一步</button>
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
