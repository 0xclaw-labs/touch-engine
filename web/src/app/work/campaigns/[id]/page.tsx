"use client";

import { useCallback, useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { ExtraJumpPanel } from "@/components/admin/ExtraJumpPanel";
import { TaskHandoffActions } from "@/components/admin/TaskHandoffActions";
import { failureText, useSession } from "@/components/work/merchant-session";
import { CampaignConfigPanel } from "@/components/work/campaign-config-panel";
import { ContentPackPanel } from "@/components/work/content-pack-panel";
import { MotionHandoffBrief } from "@/components/work/motion-handoff-brief";
import { ProductionPanel } from "@/components/work/production-panel";
import { StoreMotionNote } from "@/components/work/store-motion-note";
import { isPastEnd, surfaceLabel } from "@/lib/product-finish";
import { takenList } from "@/lib/campaign-detail";

interface Campaign {
  id: string;
  title: string;
  public_content?: string;
  status: string;
  starts_at?: string;
  ends_at?: string;
  store_id?: string;
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

const QR_SIZES = [128, 256, 512] as const;

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
  const [qrSize, setQrSize] = useState<number>(256);

  const load = useCallback(async () => {
    if (!id) return;
    setPhase("loading");
    const campaignRes = await session.api("GET", `campaigns/${id}`);
    if (!campaignRes.ok) {
      setError(failureText(campaignRes.status, campaignRes.data));
      setPhase("error");
      return;
    }
    setCampaign(campaignRes.data as unknown as Campaign);
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
    void load().catch(() => setPhase("error"));
  }, [load]);

  async function transition(status: string) {
    const res = await session.api("POST", `campaigns/${id}/status`, { status });
    if (!res.ok) setError(failureText(res.status, res.data));
    await load();
  }

  async function createLink() {
    const res = await session.api("POST", `campaigns/${id}/links`, {});
    if (!res.ok) setError(failureText(res.status, res.data));
    await load();
  }

  async function downloadQr(link: LinkRec) {
    const res = await fetch(`/api/campaigns/${id}/links/${link.id}/qrcode?size=${qrSize}`, {
      headers: { "x-tenant-id": session.tenantId },
    });
    if (!res.ok) {
      const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
      setError(failureText(res.status, data));
      return;
    }
    const blob = await res.blob();
    const href = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = href;
    anchor.download = `qr-${link.code}-${qrSize}.png`;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(href);
  }

  const expired = isPastEnd(campaign?.ends_at);

  return (
    <main>
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
          <p className="tk-note">
            状态 {campaign.status}
            {campaign.status === "paused" ? <span data-state="paused"> {surfaceLabel("paused")}</span> : null}
            {campaign.status === "ended" ? <span data-state="ended"> {surfaceLabel("ended")}</span> : null}
            {expired ? <span data-state="expired" className="tk-unknown"> {surfaceLabel("expired")}，服务端状态没有被自动改掉</span> : null}
          </p>
          <p className="tk-note">{campaign.starts_at || "开始未填"} 到 {campaign.ends_at || "结束未填"}</p>
          <div className="tk-row">
            {campaign.status === "draft" ? <button className="tk-button" type="button" onClick={() => void transition("active")}>启用</button> : null}
            {campaign.status === "active" ? <button className="tk-quiet" type="button" onClick={() => void transition("paused")}>暂停</button> : null}
            {campaign.status === "paused" ? <button className="tk-quiet" type="button" onClick={() => void transition("active")}>恢复</button> : null}
            {campaign.status === "active" || campaign.status === "paused" ? <button className="tk-quiet" type="button" onClick={() => void transition("ended")}>结束</button> : null}
          </div>
          {error ? <p className="tk-danger">{error}</p> : null}
          <section id="store-motion" className="tk-section">
            <h2 className="tk-section-title">门店参数</h2>
            <p className="tk-note">还没生成成片</p>
            <StoreMotionNote campaignId={id} />
          </section>
          <MotionHandoffBrief campaignId={id} />
          <ProductionPanel campaignId={id} campaignEndsAt={campaign.ends_at ?? null} />
          <ContentPackPanel campaignId={id} />
          <CampaignConfigPanel
            campaignId={id}
            campaignTitle={campaign.title}
            campaignStoreId={campaign.store_id}
          />
          <section id="touch" className="tk-section">
            <h2 className="tk-section-title">碰一碰和二维码</h2>
            <p className="tk-note">{leads}。两个数不合成一个转化。</p>
            <button className="tk-button" type="button" onClick={() => void createLink()}>生成链接</button>
            <label className="tk-label tk-gap">
              二维码尺寸
              <select className="tk-select" value={qrSize} onChange={(event) => setQrSize(Number(event.target.value))}>
                {QR_SIZES.map((size) => <option key={size} value={size}>{size}px</option>)}
              </select>
            </label>
            {linkPhase === "error" ? (
              <p className="tk-state tk-danger" data-list="links" data-state="error">
                短码没有读到。这不是还没有短码。
                <button className="tk-quiet" type="button" data-action="retry" onClick={() => void load()}>重试</button>
              </p>
            ) : null}
            {linkPhase === "empty" ? <p className="tk-state" data-list="links" data-state="empty">还没有短码。生成之后才能下载二维码。</p> : null}
            <ul className="tk-list">
              {links.map((link) => (
                <li key={link.id}>
                  <code>{link.code}</code>
                  {!link.enabled ? <span className="tk-unknown"> 已停用</span> : null}
                  <button className="tk-quiet" type="button" onClick={() => void downloadQr(link)}>下载二维码</button>
                </li>
              ))}
            </ul>
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
          <section className="tk-section">
            <h2 className="tk-section-title">下一步</h2>
            <TaskHandoffActions campaignId={id} onPlanned={setNotice} />
            {notice ? <p>{notice}</p> : null}
            <p className="tk-gap"><a href="/work/materials">去看素材</a> <a href="/work/rewards">去看奖励规则</a></p>
          </section>
          <section id="jumps">
            <ExtraJumpPanel campaigns={[{ id: campaign.id, title: campaign.title }]} role={session.role} tenantId={session.tenantId} />
          </section>
        </>
      ) : null}
    </main>
  );
}
