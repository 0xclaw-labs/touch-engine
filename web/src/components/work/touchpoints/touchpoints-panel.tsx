"use client";

// HUI-1665 触点管理台(/work/touchpoints):一卡一稳定入口的日常操作面。
//
// 纪律:
//   - 独占新页,日常操作全在本页完成,不跳旧管理后台;
//   - 稳定链接 = 服务端给的 /t/ 短链原样复制,前端不自行拼接;
//   - 下载二维码/写卡 CSV、查看目标检查:零推进;部署五态只经显式按钮
//     单步前进(nextStage + isStage 守卫),没到真机验证绝不当作完成;
//   - 换绑先预览:展示 当前→新 与「已开始参与数」(只读统计,3546),
//     确认后才提交,并带 expected_campaign_id 乐观并发;409 = 有并发变更,
//     重查列表,绝不覆盖别人的改动;
//   - 别名的停用/恢复只动别名层,与共享短码互不影响;
//   - 无别名标签走旧停用端点前,必须按同短码共享数先说清传播影响
//     (sharedStopFace + confirm),不默认传播。

import { useCallback, useEffect, useMemo, useState } from "react";

import { LoadErrorState } from "@/components/work/load-error-state";
import { failureText, useSession } from "@/components/work/merchant-session";
import {
  entryStateFace,
  isStage,
  nextStage,
  rebindImpactFace,
  sharedStopFace,
  stageFace,
} from "@/lib/touchpoints";

interface TouchpointItem {
  id: string;
  code: string;
  label?: string;
  position?: string;
  status: string;
  deployment_stage?: string;
  tag_id?: string;
  tag_label?: string;
  store_name?: string;
  campaign_id: string;
  campaign_title?: string;
  campaign_status?: string;
  url?: string;
  entry_code?: string;
  entry_state?: string;
  created_at?: string;
}

interface CampaignItem {
  id: string;
  title?: string;
  store_id?: string;
}

interface TagItem {
  id: string;
  label: string;
  status: string;
  link_id: string;
}

interface RebindPreview {
  current: { id: string; title?: string; status?: string };
  next: { id: string; title?: string; status?: string };
  started_participations: number;
}

function queryOf(params: Record<string, string>): string {
  const qs = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value) qs.set(key, value);
  }
  const text = qs.toString();
  return text ? `?${text}` : "";
}

export function TouchpointsPanel() {
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "error" | "ready">("loading");
  const [loadError, setLoadError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [notice, setNotice] = useState("");

  const [items, setItems] = useState<TouchpointItem[]>([]);
  const [campaigns, setCampaigns] = useState<CampaignItem[]>([]);
  const [tags, setTags] = useState<TagItem[]>([]);

  const [campaignFilter, setCampaignFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("");

  const [createTagId, setCreateTagId] = useState("");
  const [createLabel, setCreateLabel] = useState("");
  const [createPosition, setCreatePosition] = useState("");

  const [rebindTarget, setRebindTarget] = useState<TouchpointItem | null>(null);
  const [rebindCampaignId, setRebindCampaignId] = useState("");
  const [preview, setPreview] = useState<RebindPreview | null>(null);
  const [legacyPending, setLegacyPending] = useState<{ tag: TagItem; shared: number } | null>(null);

  const load = useCallback(async () => {
    const [tpRes, campRes, tagRes] = await Promise.all([
      session.api("GET", "touchpoints" + queryOf({ campaign_id: campaignFilter, status: statusFilter })),
      session.api("GET", "campaigns"),
      session.api("GET", "nfc/tags"),
    ]);
    if (!tpRes.ok) {
      setLoadError(failureText(tpRes.status, tpRes.data));
      setPhase("error");
      return;
    }
    setItems(Array.isArray(tpRes.data.items) ? (tpRes.data.items as TouchpointItem[]) : []);
    if (campRes.ok && Array.isArray(campRes.data.items)) setCampaigns(campRes.data.items as CampaignItem[]);
    if (tagRes.ok && Array.isArray(tagRes.data.items)) setTags(tagRes.data.items as TagItem[]);
    setPhase("ready");
  }, [session, campaignFilter, statusFilter]);

  useEffect(() => {
    let alive = true;
    setPhase("loading");
    void load().then(() => {
      if (!alive) return;
    }).catch(() => {
      if (alive) {
        setLoadError("没有连上服务。请稍后重试。");
        setPhase("error");
      }
    });
    return () => {
      alive = false;
    };
  }, [load, attempt]);

  const aliasTagIds = useMemo(() => new Set(items.map((item) => item.tag_id).filter(Boolean)), [items]);
  const unprovisionedTags = useMemo(() => tags.filter((tag) => !aliasTagIds.has(tag.id)), [tags, aliasTagIds]);

  const run = useCallback(async (action: () => Promise<void>) => {
    setNotice("");
    try {
      await action();
    } catch {
      setNotice("操作没有完成。网络未知,已保守处理,请刷新核对后再试。");
    }
  }, []);

  const create = useCallback(() => run(async () => {
    if (!createTagId) {
      setNotice("先选一张还没有稳定入口的卡。");
      return;
    }
    const res = await session.api("POST", "touchpoints", {
      tag_id: createTagId,
      ...(createLabel ? { label: createLabel } : {}),
      ...(createPosition ? { position: createPosition } : {}),
    });
    if (!res.ok) {
      setNotice(res.status === 409 ? "这张卡已经有稳定入口了。" : failureText(res.status, res.data));
      return;
    }
    setCreateTagId("");
    setCreateLabel("");
    setCreatePosition("");
    setNotice("已创建稳定入口。把稳定链接或二维码交给门店即可。");
    await load();
  }), [run, session, createTagId, createLabel, createPosition, load]);

  const toggleStatus = useCallback((tp: TouchpointItem) => run(async () => {
    const status = tp.status === "active" ? "disabled" : "active";
    const res = await session.api("POST", `touchpoints/${tp.id}/status`, { status });
    if (!res.ok) {
      setNotice(failureText(res.status, res.data));
      return;
    }
    setNotice(status === "disabled" ? "稳定入口已停用,只影响这一张卡。" : "稳定入口已恢复。");
    await load();
  }), [run, session, load]);

  const advanceStage = useCallback((tp: TouchpointItem) => run(async () => {
    if (!isStage(tp.deployment_stage)) {
      setNotice(stageFace(tp.deployment_stage));
      return;
    }
    const nxt = nextStage(tp.deployment_stage);
    if (!nxt) {
      setNotice("已经是最后一态:真机打开验证完成。");
      return;
    }
    const res = await session.api("POST", `touchpoints/${tp.id}/stage`, { stage: nxt });
    if (!res.ok) {
      setNotice(res.status === 409 ? "状态被别人先改了一步,已重查,请看最新状态。" : failureText(res.status, res.data));
      await load();
      return;
    }
    await load();
  }), [run, session, load]);

  const previewRebind = useCallback((tp: TouchpointItem) => run(async () => {
    if (!rebindCampaignId) {
      setNotice("先选要换到的目标活动。");
      return;
    }
    const res = await session.api("POST", `touchpoints/${tp.id}/rebind-preview`, { campaign_id: rebindCampaignId });
    if (!res.ok) {
      setNotice(failureText(res.status, res.data));
      return;
    }
    setPreview({
      current: (res.data.current ?? {}) as RebindPreview["current"],
      next: (res.data.next ?? {}) as RebindPreview["next"],
      started_participations: typeof res.data.started_participations === "number" ? res.data.started_participations : 0,
    });
  }), [run, session, rebindCampaignId]);

  const confirmRebind = useCallback((tp: TouchpointItem) => run(async () => {
    if (!preview) return;
    const res = await session.api("POST", `touchpoints/${tp.id}/rebind`, {
      campaign_id: preview.next.id,
      expected_campaign_id: tp.campaign_id,
    });
    if (res.status === 409) {
      setNotice("目标活动刚被别人改过,没有覆盖。已重查,请重新预览再确认。");
      setPreview(null);
      await load();
      return;
    }
    if (!res.ok) {
      setNotice(failureText(res.status, res.data));
      return;
    }
    setRebindTarget(null);
    setPreview(null);
    setRebindCampaignId("");
    setNotice("换绑完成。稳定链接不变,顾客下次打开直达新活动。");
    await load();
  }), [run, session, preview, load]);

  const copyUrl = useCallback((tp: TouchpointItem) => run(async () => {
    if (!tp.url) {
      setNotice("这个入口还没有可复制的稳定链接(服务端未配置对外地址)。");
      return;
    }
    await navigator.clipboard.writeText(tp.url);
    setNotice("稳定链接已复制。同一链接,换绑活动后仍然有效。");
  }), [run]);

  const downloadQr = useCallback((tp: TouchpointItem) => run(async () => {
    const res = await fetch(`/api/touchpoints/${tp.id}/qrcode?size=256`, {
      headers: { "x-tenant-id": session.tenantId },
    });
    if (!res.ok) {
      const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
      setNotice(res.status === 503 ? "服务端还没配置对外地址,出不了二维码。" : failureText(res.status, data));
      return;
    }
    const blob = await res.blob();
    const href = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = href;
    anchor.download = `qr-touchpoint-${tp.code}.png`;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(href);
  }), [run, session.tenantId]);

  const downloadCsv = useCallback(() => run(async () => {
    const res = await fetch("/api/touchpoints/export.csv" + queryOf({ campaign_id: campaignFilter, status: statusFilter }), {
      headers: { "x-tenant-id": session.tenantId },
    });
    if (!res.ok) {
      const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
      setNotice(res.status === 503 ? "服务端还没配置对外地址,出不了写卡资料。" : failureText(res.status, data));
      return;
    }
    const blob = await res.blob();
    const href = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = href;
    anchor.download = "touchpoints-stable-links.csv";
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(href);
  }), [run, session.tenantId, campaignFilter, statusFilter]);

  // 旧链接停用是两步:先算共享影响并展示(sharedStopFace),用户显式确认才发请求。
  // 不用浏览器原生弹窗:文案要留在页面里可回看,也守 uifinish 文件级闸。
  const legacyStop = useCallback((tag: TagItem) => {
    const shared = tags.filter((other) => other.link_id === tag.link_id && other.status === "active").length;
    setLegacyPending({ tag, shared });
  }, [tags]);

  const confirmLegacyStop = useCallback(() => run(async () => {
    const pending = legacyPending;
    if (!pending) return;
    const res = await session.api("POST", `nfc/tags/${pending.tag.id}/status`, { status: "disabled" });
    if (!res.ok) {
      setNotice(failureText(res.status, res.data));
      setLegacyPending(null);
      return;
    }
    setLegacyPending(null);
    setNotice("旧链接标签已停用(共享影响已按页面提示确认)。");
    await load();
  }), [run, session, legacyPending, load]);

  const openRebind = useCallback((tp: TouchpointItem) => {
    setRebindTarget(tp);
    setPreview(null);
    setRebindCampaignId("");
    setNotice("");
  }, []);

  if (phase === "loading") {
    return <p className="tk-state" data-state="loading">正在读取…</p>;
  }
  if (phase === "error") {
    return <LoadErrorState message={loadError} onRetry={() => setAttempt((a) => a + 1)} />;
  }

  return (
    <section>
      <h1 className="tk-title">稳定入口(触点)</h1>
      <p className="tk-lead">
        一张卡一个稳定链接:换绑活动不改链接;停用只停这一张卡;
        写卡与回读推进只经下面显式操作,下载与检查不改变状态。
      </p>
      {notice ? <p className="tk-state" data-state="notice">{notice}</p> : null}

      <div className="tk-toolbar">
        <label className="tk-label">
          活动
          <select className="tk-select" data-field="campaign-filter" value={campaignFilter} onChange={(event) => setCampaignFilter(event.target.value)}>
            <option value="">全部活动</option>
            {campaigns.map((camp) => (
              <option key={camp.id} value={camp.id}>{camp.title || camp.id}</option>
            ))}
          </select>
        </label>
        <label className="tk-label">
          状态
          <select className="tk-select" data-field="status-filter" value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}>
            <option value="">全部</option>
            <option value="active">启用</option>
            <option value="disabled">停用</option>
          </select>
        </label>
        <button className="tk-quiet" type="button" data-action="download-csv" onClick={() => void downloadCsv()}>
          下载写卡资料(CSV)
        </button>
      </div>

      <div className="tk-card">
        <h2 className="tk-subtitle">给卡开稳定入口</h2>
        {unprovisionedTags.length === 0 ? (
          <p className="tk-note">每张卡最多一个稳定入口;没有待开的卡了。</p>
        ) : (
          <>
            <label className="tk-label">
              选卡
              <select className="tk-select" data-field="create-tag" value={createTagId} onChange={(event) => setCreateTagId(event.target.value)}>
                <option value="">选一张还没有稳定入口的卡</option>
                {unprovisionedTags.map((tag) => (
                  <option key={tag.id} value={tag.id}>{tag.label || tag.id}</option>
                ))}
              </select>
            </label>
            <label className="tk-label">
              名称(可选)
              <input className="tk-input" data-field="create-label" value={createLabel} onChange={(event) => setCreateLabel(event.target.value)} placeholder="如:前台立牌" />
            </label>
            <label className="tk-label">
              摆位(可选)
              <input className="tk-input" data-field="create-position" value={createPosition} onChange={(event) => setCreatePosition(event.target.value)} placeholder="如:收银台" />
            </label>
            <button className="tk-quiet" type="button" data-action="create" onClick={() => void create()}>创建稳定入口</button>
            {createTagId ? (
              (() => {
                const tag = unprovisionedTags.find((item) => item.id === createTagId);
                return tag ? (
                  <button className="tk-quiet" type="button" data-action="legacy-stop" onClick={() => void legacyStop(tag)}>
                    停用这张卡的旧共享链接
                  </button>
                ) : null;
              })()
            ) : null}
            <p className="tk-note">
              旧链接停用会影响共用同一短码的所有卡,点下去之前会先跟你确认影响范围。
            </p>
            {legacyPending ? (
              <div className="tk-card" data-state="legacy-stop">
                <p className="tk-note">{sharedStopFace(legacyPending.shared)}</p>
                <button className="tk-quiet" type="button" data-action="confirm-legacy-stop" onClick={() => void confirmLegacyStop()}>
                  确认停用旧共享链接
                </button>
                <button className="tk-quiet" type="button" data-action="cancel-legacy-stop" onClick={() => setLegacyPending(null)}>取消</button>
              </div>
            ) : null}
          </>
        )}
      </div>

      {items.length === 0 ? (
        <p className="tk-state" data-state="empty">还没有稳定入口。先在上面给卡开一个。</p>
      ) : (
        items.map((tp) => {
          const nxt = isStage(tp.deployment_stage) ? nextStage(tp.deployment_stage) : null;
          return (
            <article key={tp.id} className="tk-card" data-touchpoint={tp.code}>
              <h2 className="tk-subtitle">{tp.label || tp.tag_label || "未命名入口"}</h2>
              <p className="tk-note">
                {tp.store_name ? `${tp.store_name} · ` : ""}{tp.campaign_title || tp.campaign_id}
                {tp.position ? ` · ${tp.position}` : ""}
              </p>
              <p className="tk-note">
                状态:{tp.status === "active" ? "启用" : "停用"} ·
                部署:{stageFace(tp.deployment_stage)}
              </p>
              <p className="tk-note">检查:{entryStateFace(tp.entry_state)}</p>
              {tp.url ? <p className="tk-note"><code>{tp.url}</code></p> : null}
              <div className="tk-toolbar">
                <button className="tk-quiet" type="button" data-action="copy-url" onClick={() => void copyUrl(tp)}>复制稳定链接</button>
                <button className="tk-quiet" type="button" data-action="download-qr" onClick={() => void downloadQr(tp)}>下载二维码</button>
                <button className="tk-quiet" type="button" data-action="toggle-status" onClick={() => void toggleStatus(tp)}>
                  {tp.status === "active" ? "停用稳定入口" : "恢复稳定入口"}
                </button>
                {nxt ? (
                  <button className="tk-quiet" type="button" data-action="advance-stage" onClick={() => void advanceStage(tp)}>
                    推进到:{stageFace(nxt)}
                  </button>
                ) : null}
                <button className="tk-quiet" type="button" data-action="open-rebind" onClick={() => openRebind(tp)}>换绑活动</button>
              </div>
              {rebindTarget?.id === tp.id ? (
                <div className="tk-card" data-state="rebind">
                  <label className="tk-label">
                    目标活动
                    <select className="tk-select" data-field="rebind-campaign" value={rebindCampaignId} onChange={(event) => setRebindCampaignId(event.target.value)}>
                      <option value="">选目标活动</option>
                      {campaigns.filter((camp) => camp.id !== tp.campaign_id).map((camp) => (
                        <option key={camp.id} value={camp.id}>{camp.title || camp.id}</option>
                      ))}
                    </select>
                  </label>
                  <button className="tk-quiet" type="button" data-action="preview-rebind" onClick={() => void previewRebind(tp)}>预览影响</button>
                  {preview ? (
                    <>
                      <p className="tk-note">
                        {preview.current.title || preview.current.id} → {preview.next.title || preview.next.id}
                      </p>
                      <p className="tk-note">{rebindImpactFace(preview.started_participations)}</p>
                      <button className="tk-quiet" type="button" data-action="confirm-rebind" onClick={() => void confirmRebind(tp)}>确认换绑</button>
                      <button className="tk-quiet" type="button" data-action="cancel-rebind" onClick={() => setRebindTarget(null)}>取消</button>
                    </>
                  ) : null}
                </div>
              ) : null}
            </article>
          );
        })
      )}
    </section>
  );
}
