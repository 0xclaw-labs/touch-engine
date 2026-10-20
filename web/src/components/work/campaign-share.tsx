"use client";

import { useEffect, useMemo, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { campaignShareText, campaignShareUrl, pickShareLink, shareAvailability, type ShareLink } from "@/lib/campaign-share";
import { formatCampaignWindow } from "@/lib/public-time";

const QR_SIZES = [128, 256, 512] as const;

export interface ShareCampaignView {
  id: string;
  title: string;
  status: string;
  store_name?: string;
  starts_at?: string;
  ends_at?: string;
}

export function CampaignShare({
  campaign,
  links,
  onChanged,
  onGenerate,
  busy,
}: {
  campaign: ShareCampaignView;
  links: ShareLink[];
  onChanged: () => Promise<void> | void;
  onGenerate: () => Promise<void> | void;
  busy?: boolean;
}) {
  const session = useSession();
  const [origin, setOrigin] = useState("");
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [qrSize, setQrSize] = useState<number>(256);
  const [linkBusy, setLinkBusy] = useState("");

  useEffect(() => {
    setOrigin(window.location.origin);
  }, []);

  const enabled = useMemo(() => pickShareLink(links), [links]);
  const url = enabled && origin ? campaignShareUrl(origin, enabled.code) : "";
  const availability = shareAvailability({ status: campaign.status, ends_at: campaign.ends_at ?? null, link: enabled });
  const primary = campaign.status === "active";
  const text = enabled && url
    ? campaignShareText({ title: campaign.title, store_name: campaign.store_name, starts_at: campaign.starts_at, ends_at: campaign.ends_at, url })
    : "";

  async function copy() {
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      setNotice("分享文案已复制。");
      setError("");
    } catch {
      setError("浏览器没有允许复制。请手动选中链接。");
    }
  }

  async function toggle(link: ShareLink, nextEnabled: boolean) {
    if (linkBusy) return;
    setLinkBusy(link.code);
    setError("");
    try {
      const res = await session.api("POST", `campaigns/${campaign.id}/links/${(link as ShareLink & { id?: string }).id ?? ""}/enabled`, { enabled: nextEnabled });
      if (!res.ok) {
        setError(failureText(res.status, res.data));
        return;
      }
      setNotice(nextEnabled ? "短码已启用。" : "短码已停用。停用后顾客打开会看到“链接已停用”。");
      await onChanged();
    } finally {
      setLinkBusy("");
    }
  }

  async function downloadQr() {
    if (!enabled) return;
    const linkId = (enabled as ShareLink & { id?: string }).id ?? "";
    const res = await fetch(`/api/campaigns/${campaign.id}/links/${linkId}/qrcode?size=${qrSize}`, {
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
    anchor.download = `qr-${enabled.code}-${qrSize}.png`;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(href);
  }

  return (
    <section id="share" className="tk-section" data-testid="campaign-share" data-pattern-region="status">
      <h2 className="tk-section-title">分享给顾客</h2>
      <p className="tk-note">
        活动窗口：{formatCampaignWindow(campaign.starts_at, campaign.ends_at)}
      </p>
      {!enabled ? (
        <div data-state="empty">
          <p className="tk-note">还没有启用的短码。生成后才能分享；停用的短码不会出现在这里。</p>
          <button type="button" className="tk-quiet" disabled={busy} onClick={() => void onGenerate()}>生成短码</button>
        </div>
      ) : (
        <>
          <p className="tk-note">顾客打开的公开链接</p>
          <p><code data-testid="share-url">{url}</code></p>
          <p className={availability.note ? "tk-unknown" : "tk-note"} data-testid="share-note">
            {availability.note || "链接已启用，可以分享给顾客。"}
          </p>
          <div className="tk-row">
            <button type="button" className={primary ? "tk-primary" : "tk-quiet"} data-primary-action={primary ? "true" : undefined} onClick={() => void copy()}>复制分享文案</button>
            <button type="button" className="tk-quiet" onClick={() => void copy()}>复制链接</button>
          </div>
          <p className="tk-note" data-testid="share-text">{text}</p>
          <div className="tk-row tk-gap">
            <label className="tk-label">
              二维码尺寸
              <select className="tk-select" value={qrSize} onChange={(event) => setQrSize(Number(event.target.value))}>
                {QR_SIZES.map((size) => <option key={size} value={size}>{size}px</option>)}
              </select>
            </label>
            <button type="button" className="tk-quiet" onClick={() => void downloadQr()}>下载二维码</button>
            <button type="button" className="tk-quiet" disabled={Boolean(linkBusy)} onClick={() => void toggle(enabled, false)}>停用短码</button>
          </div>
        </>
      )}
      {links.some((link) => !link.enabled) ? (
        <details className="tk-gap">
          <summary className="tk-note">已停用的短码</summary>
          <ul className="tk-list">
            {links.filter((link) => !link.enabled).map((link) => (
              <li key={link.code}>
                <code>{link.code}</code>
                <button type="button" className="tk-quiet" disabled={Boolean(linkBusy)} onClick={() => void toggle(link, true)}>启用</button>
              </li>
            ))}
          </ul>
        </details>
      ) : null}
      {notice ? <p className="tk-ok">{notice}</p> : null}
      {error ? <p className="tk-danger" data-state="error">{error}</p> : null}
    </section>
  );
}
