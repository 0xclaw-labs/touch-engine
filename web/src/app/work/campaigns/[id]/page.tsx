"use client";

import { useCallback, useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { failureText, useSession } from "@/components/work/merchant-session";
import { CampaignLegacyHistory } from "@/components/work/campaign-legacy-history";
import { CampaignConfigPanel } from "@/components/work/campaign-config-panel";
import { CampaignShare } from "@/components/work/campaign-share";
import { isPastEnd, surfaceLabel } from "@/lib/product-finish";
import { formatCampaignWindow } from "@/lib/public-time";
import { takenList } from "@/lib/campaign-detail";
import { refusalCopy } from "@/lib/campaign-config";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

interface Campaign {
  id: string;
  title: string;
  public_content?: string;
  status: string;
  starts_at?: string;
  ends_at?: string;
  store_id?: string;
  availability?: {state:string;label:string;can_start:boolean;reason?:string};
}

interface LinkRec {
  id: string;
  code: string;
  enabled: boolean;
}

interface TagRec {
  id: string;
  label: string;
  code: string;
  status: "active" | "disabled";
}

export default function CampaignDetailPage() {
  const params = useParams<{ id: string }>();
  const id = params?.id ?? "";
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "error" | "ready">("loading");
  const [campaign, setCampaign] = useState<Campaign | null>(null);
  const [links, setLinks] = useState<LinkRec[]>([]);
  const [tags, setTags] = useState<TagRec[]>([]);
  const [linkPhase, setLinkPhase] = useState<"empty" | "error" | "ready">("empty");
  const [tagPhase, setTagPhase] = useState<"empty" | "error" | "ready">("empty");
  const [leads, setLeads] = useState<string>("留资数量未知");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [storeName, setStoreName] = useState("");
  const [created, setCreated] = useState(false);
  const [legacy,setLegacy]=useState(false);
  const [promotion,setPromotion]=useState(false);
  const [pendingTransition,setPendingTransition]=useState("");
  const [detailTab, setDetailTab] = useState<"config" | "share">("config");

  const load = useCallback(async () => {
    if (!id) return;
    setPhase("loading");
    const campaignRes = await session.api("GET", `campaigns/${id}`);
    if (!campaignRes.ok) {
      setError(failureText(campaignRes.status, campaignRes.data));
      setPhase("error");
      return;
    }
    const loaded = campaignRes.data as unknown as Campaign;
    setCampaign(loaded);
    const configRes=await session.api("GET",`campaigns/${id}/publish-config`);
    if(configRes.ok)setPromotion(true);
    if (loaded.store_id) {
      const storeRes = await session.api("GET", `stores/${loaded.store_id}`).catch(() => null);
      setStoreName(storeRes && storeRes.ok && typeof storeRes.data.name === "string" ? storeRes.data.name : "");
    }
    const [linkRes, tagRes, statRes] = await Promise.all([
      session.api("GET", `campaigns/${id}/links`),
      session.api("GET", `nfc/tags?campaign_id=${encodeURIComponent(id)}`),
      session.api("GET", `campaigns/${id}/lead-stats`),
    ]);
    const linkLoad = takenList<LinkRec>({ ok: linkRes.ok, items: linkRes.data.items as unknown[] | null | undefined });
    setLinks(linkLoad.items);
    setLinkPhase(linkLoad.state);
    const tagLoad = takenList<TagRec>({ ok: tagRes.ok, items: tagRes.data.items as unknown[] | null | undefined });
    setTags(tagLoad.items);
    setTagPhase(tagLoad.state);
    if (statRes.status === 404) setLeads("留资统计未开通，不显示 0");
    else if (!statRes.ok) setLeads("留资统计没有读到，不显示 0");
    else setLeads(`授权提交 ${String(statRes.data.submissions ?? "未知")}，匿名浏览 ${String(statRes.data.anonymous_views ?? "未知")}`);
    setPhase("ready");
  }, [id, session]);

  useEffect(() => {
    setCreated(new URLSearchParams(window.location.search).get("created") === "1");
    setLegacy(["#store-motion","#motion-handoff","#production"].includes(window.location.hash) || new URLSearchParams(window.location.search).get("history")==="1");
    void load().catch(() => setPhase("error"));
  }, [load]);

  async function transition(status: string, confirmed=false) {
    if((status==="paused" || status==="ended") && !confirmed){setPendingTransition(status);return;}
    setPendingTransition("");
    const res = await session.api("POST", `campaigns/${id}/status`, { status });
    if (!res.ok) setError(res.status === 422 ? refusalCopy(res.status, res.data).text : failureText(res.status, res.data));
    await load();
  }

  async function generateLink() {
    const res = await session.api("POST", `campaigns/${id}/links`, {});
    if (!res.ok) {
      setError(failureText(res.status, res.data));
      return;
    }
    setError("");
    await load();
  }

  async function publish() {
    if (!campaign || busy) return;
    setBusy(true);
    setError("");
    try {
      if (!links.some((link) => link.enabled)) {
        const linkRes = await session.api("POST", `campaigns/${id}/links`, {});
        if (!linkRes.ok) {
          setError(failureText(linkRes.status, linkRes.data));
          return;
        }
      }
      const res = await session.api("POST", `campaigns/${id}/status`, { status: "active" });
      if (!res.ok) {
        setError(res.status === 422 ? refusalCopy(res.status, res.data).text : failureText(res.status, res.data));
        return;
      }
      setCreated(true);
      await load();
    } finally {
      setBusy(false);
    }
  }

  const expired = isPastEnd(campaign?.ends_at);

  return (
    <main data-pattern="detail">
      {phase === "loading" ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
      {phase === "error" ? (
        <p className="tk-state tk-danger" data-state="error">
          {error || surfaceLabel("error")}
          <button className="tk-quiet" type="button" data-action="retry" onClick={() => void load()}>重试</button>
        </p>
      ) : null}
      {phase === "ready" && campaign ? (
        <>
          <p className="tk-kicker"><a href="/work/campaigns">全部活动</a></p>
          <h1 className="tk-title">{campaign.title}</h1>
          <p className="tk-lead">{campaign.public_content || "顾客能看到的内容还是空的。"}</p>
          {created ? <p className="tk-note" data-state="published" data-testid="publish-notice">活动已发布。把下面的链接或分享文案发给顾客。</p> : null}
          <p className="tk-note" data-state={campaign.status} data-testid="campaign-status">
            状态：{campaign.availability?.label || statusLabel(campaign.status)}
            {campaign.status === "paused" ? <span> · {surfaceLabel("paused")}</span> : null}
            {campaign.status === "ended" ? <span> · {surfaceLabel("ended")}</span> : null}
            {campaign.availability?.reason ? <span> · {campaign.availability.reason}</span> : null}
          </p>
          <p className="tk-note">活动时间：{formatCampaignWindow(campaign.starts_at, campaign.ends_at,{timeZone:"Asia/Shanghai"})}（北京时间 UTC+8）</p>
          <div className="tk-row">
            {campaign.status === "draft" ? <button className="tk-primary" type="button" disabled={busy} data-primary-action="true" onClick={() => void publish()}>{busy ? "正在发布…" : "发布活动"}</button> : null}
            {campaign.status === "paused" && !expired ? <button className="tk-primary" type="button" data-primary-action="true" onClick={() => void transition("active")}>恢复活动</button> : null}
            {campaign.status === "active" && !expired ? <a className="tk-quiet" href="#share">去分享给顾客</a> : null}
            {campaign.status === "active" ? <button className="tk-quiet" type="button" onClick={() => void transition("paused")}>暂停</button> : null}
            {campaign.status === "active" || campaign.status === "paused" && !expired ? <button className="tk-quiet" type="button" onClick={() => void transition("ended")}>结束</button> : null}
            {campaign.status === "ended" || expired ? <p className="tk-note">活动已结束，不能继续发布。需要再办一场请新建活动。</p> : null}
          </div>
          {pendingTransition?<section className="tk-card" aria-label="确认活动影响"><p>将停止新参与与新发放。在途预留按冻结条件核对，已发有效券继续按原条件使用；历史记录保留。</p><button className="tk-button" type="button" onClick={()=>void transition(pendingTransition,true)}>确认{pendingTransition==="ended"?"结束":"暂停"}活动</button><button className="tk-quiet" type="button" onClick={()=>setPendingTransition("")}>取消</button></section>:null}
          {error ? <p className="tk-danger" data-state="error">{error}{promotion ? <> <a href="#publish-config">查看活动承诺与启用检查</a></> : null}</p> : null}
          {campaign.store_id?<p><a href={`/work/stores/${campaign.store_id}?tab=page`}>进入{storeName || "当前门店"}工作区</a></p>:null}
          <Tabs value={detailTab} onValueChange={(v) => setDetailTab(v === "share" ? "share" : "config")} className="tk-gap">
            <TabsList>
              <TabsTrigger value="config">配置</TabsTrigger>
              <TabsTrigger value="share">分享与触达</TabsTrigger>
            </TabsList>
            <TabsContent value="config" className="tk-gap">
          {session.role!=="org_owner"?<p className="tk-note">活动承诺由负责人维护。当前角色可到<a href={`/work/service?campaign=${encodeURIComponent(id)}`}>店员台办理顾客事项</a>。</p>:promotion?<CampaignConfigPanel campaignId={id} campaignTitle={campaign.title} campaignBody={campaign.public_content} campaignStoreId={campaign.store_id} campaignStoreName={storeName} onAuthorityChanged={()=>void load()}/>:<section className="tk-section"><h2 className="tk-section-title">活动目标</h2><p>本活动展示门店本次服务与咨询信息。</p><button className="tk-quiet" type="button" onClick={()=>setPromotion(true)}>增加内容推广与优惠承诺</button></section>}
          <p><a href={`/work/rewards?campaign=${encodeURIComponent(id)}`}>管理优惠库存、待处理与旧券履约</a></p>
          <p><button className="tk-quiet" type="button" onClick={()=>setLegacy(v=>!v)}>{legacy?"收起历史制作记录":"核对历史制作交接记录"}</button></p>
          {legacy?<CampaignLegacyHistory campaignId={id} endsAt={campaign.ends_at}/>:null}
            </TabsContent>
            <TabsContent value="share" className="tk-gap">
          {linkPhase === "error" ? (
            <p className="tk-state tk-danger" data-list="links" data-state="error">
              短码没有读到。这不是还没有短码。
              <button className="tk-quiet" type="button" data-action="retry" onClick={() => void load()}>重试</button>
            </p>
          ) : null}
          {linkPhase === "empty" ? <p className="tk-note" data-list="links" data-state="empty">还没有短码。生成后会在下面给出分享链接。</p> : null}
          <CampaignShare
            campaign={{ id, title: campaign.title, status: campaign.status, store_name: storeName, starts_at: campaign.starts_at, ends_at: campaign.ends_at }}
            links={links}
            onChanged={() => load()}
            onGenerate={() => generateLink()}
            busy={busy}
          />
          <section id="touch" className="tk-section">
            <h2 className="tk-section-title">碰一碰和留资</h2>
            <p className="tk-note">{leads}。两个数不合成一个转化。</p>
            <h3 className="tk-section-title">标签</h3>
            {tagPhase === "error" ? (
              <p className="tk-state tk-danger" data-list="tags" data-state="error">
                标签没有读到。这不是还没有标签。
                <button className="tk-quiet" type="button" data-action="retry" onClick={() => void load()}>重试</button>
              </p>
            ) : null}
            {tagPhase === "empty" ? <p className="tk-note" data-list="tags" data-state="empty">这个活动还没有标签。批量写入仍走标签操作。</p> : null}
            <ul className="tk-list">
              {tags.map((tag) => (
                <li key={tag.id}>{tag.label} {tag.code} {tag.status === "disabled" ? "停用" : "启用"}</li>
              ))}
            </ul>
            <p><a href="/admin">去批量生成标签</a></p>
          </section>
            </TabsContent>
          </Tabs>

        </>
      ) : null}
    </main>
  );
}

function statusLabel(status: string): string {
  if (status === "draft") return "草稿（未发布）";
  if (status === "active") return "进行中";
  if (status === "paused") return "暂停";
  if (status === "ended") return "已结束";
  return status || "状态未知";
}
