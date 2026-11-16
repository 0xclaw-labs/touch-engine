"use client";

import { useCallback, useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { surfaceLabel } from "@/lib/product-finish";
import { StoreProductionPanel } from "@/components/work/store-production-panel";
import { ensureContentPack } from "@/lib/work/content-editor-flow";
import styles from "./content-editor.module.css";

// HUI-3553 门店内容包抽屉(/work/materials):真实上传 → 组织候选 → 明确采用
// → 换版;预览/下载走 B05 受控媒体(media-access 短 TTL URL)。
// 纪律:
//   - 商家全程不填 asset_id/版本号等技术字段:媒体用选择器挑,选择器内部
//     携带 media_id;
//   - 上传完成 = 服务器实测字节(MIME/大小/哈希/时长)返回;仅登记引用不算
//     上传完成 —— 上传结果的元数据直接展示给商家核对;
//   - 采用必须显式(按钮),换版 = 新候选 → 再采用 → 指针切换;旧版本保留
//     可追溯,已发布引用不受影响;
//   - 预览/下载只取已采用版本;浏览/换稿/下载零生成零收费;
//   - 下载按真实结果表述:「已开始下载」+ 以浏览器实际保存结果为准。

interface StoreMedia {
  id: string;
  kind: string;
  mime_type: string;
  byte_size: number;
  sha256: string;
  duration_ms?: number;
  width?: number;
  height?: number;
  filename?: string;
  replayed?: boolean;
}

interface MediaSnapshot {
  media_id: string;
  mime_type?: string;
  byte_size?: number;
  duration_ms?: number;
  width?: number;
  height?: number;
}

interface StorePackVersion {
  pack_id: string;
  id: string;
  version_no: number;
  content_kind: string;
  status: string;
  source?: string;
  title: string;
  body: string;
  topics?: string[];
  store_name: string;
  price_note: string;
  valid_note: string;
  video?: MediaSnapshot | null;
  cover?: MediaSnapshot | null;
  image_text?: MediaSnapshot[];
  platform_versions?: { platform: string; note?: string }[];
  marking?: { ad_required?: boolean; ai_generated?: boolean };
  blocked_reason?: string;
}

interface StorePackAdoption {
  platform: string;
  pack_id: string;
  version_id: string;
  adopted_by?: string;
  updated_at?: string;
}

interface StoreContentPack {
  id: string;
  title: string;
  campaign_id?: string;
  legacy?: boolean;
  versions: StorePackVersion[];
  adoptions: StorePackAdoption[];
}

interface MediaAccessItem {
  media_id: string;
  url: string;
  expires_at: string;
  mime_type: string;
  byte_size: number;
  sha256: string;
  filename: string;
}

function statusCopy(status: string): string {
  switch (status) {
    case "draft":
      return "编辑中";
    case "candidate":
      return "待采用";
    case "adopted":
      return "已采用";
    case "retired":
      return "已被新版本取代";
    case "blocked":
      return "已停止分发";
    default:
      return status;
  }
}

function kindCopy(kind: string): string {
  return kind === "video" ? "视频" : "图片";
}

// HUI-4595: 回流候选来源如实展示(真实制作生产方的成片,不是商家上传)。
function sourceCopy(source?: string): string {
  switch (source) {
    case "product_photo_return":
      return "制作回流";
    case "merchant_upload":
      return "商家上传";
    default:
      return source || "未注明来源";
  }
}

function bytesCopy(n: number): string {
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + " MB";
  if (n >= 1 << 10) return (n / (1 << 10)).toFixed(0) + " KB";
  return n + " B";
}

function durationCopy(ms?: number): string {
  if (!ms) return "";
  const s = Math.round(ms / 100) / 10;
  return ` · 时长 ${s} 秒`;
}

const PLATFORMS = [
  { id: "douyin", label: "抖音" },
  { id: "kuaishou", label: "快手" },
  { id: "wechat", label: "朋友圈／视频号" },
  { id: "shipinhao", label: "视频号" },
  { id: "xiaohongshu", label: "小红书" },
  { id: "bilibili", label: "B站" },
  { id: "weibo", label: "微博" },
  { id: "qq", label: "QQ好友／群分享" },
  { id: "qzone", label: "QQ空间" },
  { id: "dianping", label: "大众点评" },
];

const EMPTY_DRAFT = {
  title: "",
  body: "",
  topics: "",
  store_name: "",
  price_note: "",
  valid_note: "",
};

export interface ContentAdoption {
  storeId: string; campaignId?: string; packId: string; versionId: string; platform: string;
  state: "adopted_pending_publish";
}
export interface ContentEditorProps {
  storeId: string; storeName?: string; campaignId?: string; purpose?: string; initialTitle?: string; initialBody?: string;
  returnHref?: string; onAdopted?: (result: ContentAdoption) => void;
}
export function StoreContentDrawer({ storeId, storeName = "", campaignId, purpose = "门店顾客页内容", initialTitle = "", initialBody = "", returnHref, onAdopted }: ContentEditorProps) {
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [packs, setPacks] = useState<StoreContentPack[]>([]);
  const [media, setMedia] = useState<StoreMedia[]>([]);
  // 真实上传:文件选择后立即上传,展示服务器实测元数据。
  const [uploading, setUploading] = useState(false);
  const [uploadedNote, setUploadedNote] = useState("");
  // 组织候选(换版也走这里:选同一个包 = 新开一版)。
  const [source, setSource] = useState<"upload" | "library" | "produce">("upload");
  const [reuseVersion, setReuseVersion] = useState("");
  const [reuseSnapshot, setReuseSnapshot] = useState("");
  const [aiGenerated, setAiGenerated] = useState(false);
  const [adoptionNote, setAdoptionNote] = useState("");
  const [draft, setDraft] = useState({ ...EMPTY_DRAFT, title: initialTitle, body: initialBody, store_name: storeName });
  const [draftVideo, setDraftVideo] = useState("");
  const [draftCover, setDraftCover] = useState("");
  const [draftKind, setDraftKind] = useState<"video" | "imagetext">("video");
  const [draftImages, setDraftImages] = useState<string[]>([]);
  const [draftPlatform, setDraftPlatform] = useState("douyin");
  const [draftPack, setDraftPack] = useState("");
  const [busy, setBusy] = useState("");
  // 受控预览/下载(purpose=preview|download 的短 TTL URL)。
  const [previewURL, setPreviewURL] = useState("");
  const [previewImages, setPreviewImages] = useState<MediaAccessItem[]>([]);
  const [downloadItems, setDownloadItems] = useState<MediaAccessItem[]>([]);
  const [previewNote, setPreviewNote] = useState("");
  const draftStorageKey = `touch-content-draft:${session.email}:${session.tenantId}:${storeId}:${campaignId || "store"}`;
  const [restoredKey, setRestoredKey] = useState("");

  useEffect(() => {
    const initialDraft = { ...EMPTY_DRAFT, title: initialTitle, body: initialBody, store_name: storeName };
    setDraft(initialDraft); setSource("upload"); setDraftVideo(""); setDraftCover(""); setDraftImages([]); setDraftPack(""); setDraftKind("video"); setDraftPlatform("douyin"); setAiGenerated(false);
    setReuseVersion(""); setReuseSnapshot(""); setPreviewURL(""); setPreviewImages([]); setDownloadItems([]); setPreviewNote(""); setAdoptionNote("");
    try {
      const saved = JSON.parse(localStorage.getItem(draftStorageKey) || "null") as Record<string, unknown> | null;
      if (saved && saved.draft && typeof saved.draft === "object") {
        const input = saved.draft as Record<string, unknown>;
        const next = { ...initialDraft };
        for (const key of Object.keys(EMPTY_DRAFT) as (keyof typeof EMPTY_DRAFT)[]) if (typeof input[key] === "string") next[key] = input[key] as string;
        setDraft(next);
        if (saved.source === "upload" || saved.source === "library" || saved.source === "produce") setSource(saved.source);
        if (saved.kind === "imagetext") setDraftKind("imagetext");
        if (typeof saved.platform === "string" && PLATFORMS.some((item) => item.id === saved.platform)) setDraftPlatform(saved.platform);
        if (typeof saved.pack === "string") setDraftPack(saved.pack);
        if (typeof saved.video === "string") setDraftVideo(saved.video);
        if (typeof saved.cover === "string") setDraftCover(saved.cover);
        if (Array.isArray(saved.images)) setDraftImages(saved.images.filter((id): id is string => typeof id === "string"));
        setAiGenerated(saved.ai === true);
      }
    } catch { /* Draft persistence is optional; the authoritative records are loaded below. */ }
    setRestoredKey(draftStorageKey);
    // Restore only on a change of the authenticated editing context.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draftStorageKey]);

  useEffect(() => {
    if (restoredKey !== draftStorageKey) return;
    try { localStorage.setItem(draftStorageKey, JSON.stringify({ draft, source, kind: draftKind, images: draftImages, platform: draftPlatform, video: draftVideo, cover: draftCover, pack: draftPack, ai: aiGenerated })); }
    catch { /* Editing continues even when the browser cannot persist a draft. */ }
  }, [restoredKey, draftStorageKey, draft, source, draftKind, draftImages, draftPlatform, draftVideo, draftCover, draftPack, aiGenerated]);

  const load = useCallback(async () => {
    setPhase("loading");
    const packsRes = await session.api("GET", `stores/${storeId}/content-packs`);
    if (!packsRes.ok) {
      setError(failureText(packsRes.status, packsRes.data));
      setPhase("error");
      return;
    }
    setPacks(Array.isArray(packsRes.data.items) ? (packsRes.data.items as StoreContentPack[]) : []);
    const mediaRes = await session.api("GET", `stores/${storeId}/content-media`).catch(() => null);
    setMedia(mediaRes && mediaRes.ok && Array.isArray(mediaRes.data.items) ? (mediaRes.data.items as StoreMedia[]) : []);
    setError("");
    setPhase("ready");
  }, [session, storeId]);

  useEffect(() => {
    void load().catch(() => {
      setError("没有连上服务。内容包没有读到,不把它当成空的。");
      setPhase("error");
    });
  }, [load]);

  // 真实上传:multipart 直传,服务器按真实字节判定类型并实测大小/哈希/时长。
  async function uploadFile(file: File) {
    setUploading(true);
    setUploadedNote("");
    try {
      const form = new FormData();
      form.append("file", file);
      const res = await fetch(`/api/stores/${encodeURIComponent(storeId)}/content-media`, {
        method: "POST",
        headers: { "x-tenant-id": session.tenantId },
        body: form,
      });
      const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
      if (!res.ok) {
        setUploadedNote(failureText(res.status, data));
        return;
      }
      // 上传完成 = 服务器实测元数据已返回(MIME 来自内容判定,非文件名)。
      const seconds = typeof data.duration_ms === "number" ? Math.round((data.duration_ms as number) / 100) / 10 : 0;
      setUploadedNote(
        `上传完成(按真实内容核验):${data.mime_type} · ${bytesCopy(Number(data.byte_size))}` +
          (seconds ? ` · 时长 ${seconds} 秒` : "") +
          ` · 内容指纹 ${String(data.sha256).slice(0, 12)}…`,
      );
      if (data.kind === "video") setDraftVideo(String(data.id));
      if (data.kind === "image") {
        setDraftCover(String(data.id));
        setDraftImages((items) => items.includes(String(data.id)) ? items : [...items, String(data.id)]);
      }
      await load();
    } catch {
      setUploadedNote("没有完成(没有连上服务)。文件还没有上传,不把它当成已上传。");
    } finally {
      setUploading(false);
    }
  }

  // 组织候选:选同一个包提交 = 新开一版(改文案/换素材 = 新版本,旧版不动)。
  async function submitCandidate(event: React.FormEvent) {
    event.preventDefault();
    const reuse = packs.flatMap((pack) => pack.versions.map((version) => ({ pack, version }))).find((item) => item.version.id === reuseVersion);
    if (source === "library" && reuse && reuseSnapshot === JSON.stringify({ draft, kind: draftKind, images: draftImages, video: draftVideo, cover: draftCover, platform: draftPlatform, ai: aiGenerated })) {
      await openMedia(reuse.version, "preview");
      setAdoptionNote("已复用原候选版本。请在下方确认预览后明确采用；没有创建新版本或制作费用。");
      return;
    }
    if (draftKind === "video" && (!draftVideo || !draftCover)) {
      setError("先选成片和封面,再组织候选。");
      return;
    }
    if (draftKind === "imagetext" && draftImages.length === 0) {
      setError("请至少选择一张真实图片，再提交图文候选。");
      return;
    }
    setBusy("draft");
    setError("");
    try {
      const packId = draftPack || await ensureContentPack(session.api, storeId, packs, draft.title || purpose, campaignId);
      setDraftPack(packId);
      const res = await session.api("POST", `stores/${storeId}/content-packs/${packId}/versions`, {
        content_kind: draftKind,
        ...(draftKind === "video" ? { video_media_id: draftVideo, cover_media_id: draftCover } : { image_text_media_ids: draftImages }),
        title: draft.title,
        body: draft.body,
        topics: draft.topics.split(/[,，]/).map((t) => t.trim()).filter(Boolean),
        store_name: draft.store_name,
        price_note: draft.price_note,
        valid_note: draft.valid_note,
        platform_versions: [{ platform: draftPlatform, note: "", source_version: draft.title }],
        marking: { ad_required: true, ai_generated: aiGenerated },
        source: source === "upload" ? "merchant_upload" : "asset_lib",
        status: "candidate",
      });
      if (!res.ok) {
        setError(failureText(res.status, res.data));
        return;
      }
      setDraft({ ...EMPTY_DRAFT, store_name: storeName });
      setDraftVideo("");
      setDraftCover("");
      setDraftImages([]);
      await load();
    } catch {
      setError("没有完成(没有连上服务)。候选还没有提交。");
    } finally {
      setBusy("");
    }
  }

  async function adopt(packId: string, versionId: string, platform: string) {
    setBusy(versionId + platform);
    setError("");
    try {
      const res = await session.api("POST", `stores/${storeId}/content-packs/${packId}/versions/${versionId}/adopt`, { platform });
      if (!res.ok) {
        setError(failureText(res.status, res.data));
        return;
      }
      setAdoptionNote("已选用待发布：已保存到当前用途；上线需在原门店或活动确认发布。既有顾客会话保留原版本。");
      onAdopted?.({ storeId, campaignId, packId, versionId, platform, state: "adopted_pending_publish" });
      await load();
    } catch {
      setError("采用结果尚未确认。请刷新回查，避免把断网当作未采用。");
    } finally {
      setBusy("");
    }
  }

  async function block(packId: string, versionId: string) {
    const reason = window.prompt("停止后，新访问和正在办理的顾客都不能继续获取这一版素材；已在平台发布的内容不会被自动删除。请输入停止原因（会保留处理记录）：");
    if (!reason || !reason.trim()) return;
    setBusy(versionId + ":block");
    setError("");
    try {
      const res = await session.api("POST", `stores/${storeId}/content-packs/${packId}/versions/${versionId}/block`, { reason });
      if (!res.ok) {
        setError(failureText(res.status, res.data));
        return;
      }
      await load();
    } finally {
      setBusy("");
    }
  }

  // 受控预览/下载:短 TTL URL,绑定版本+媒体+用途;到期重试只需再点一次
  // (自动续签同一合法版本),不会触发任何制作或扣费。
  async function openMedia(version: StorePackVersion, purpose: "preview" | "download") {
    const mediaIDs = version.content_kind === "imagetext" ? (version.image_text || []).map((item) => item.media_id) : [version.video?.media_id, version.cover?.media_id].filter((x): x is string => Boolean(x));
    if (mediaIDs.length === 0) {
      setError("这一版没有可预览的媒体。");
      return;
    }
    setBusy(version.id + ":" + purpose);
    setError("");
    setPreviewNote("");
    try {
      const res = await session.api("POST", `stores/${storeId}/content-packs/${version.pack_id}/versions/${version.id}/media-access`, {
        media_ids: mediaIDs,
        purpose,
      });
      if (!res.ok) {
        setError(failureText(res.status, res.data));
        return;
      }
      const items = Array.isArray(res.data.items) ? (res.data.items as MediaAccessItem[]) : [];
      const first = items[0];
      if (!first) return;
      if (purpose === "preview") {
        setPreviewURL(version.content_kind === "video" ? first.url : "");
        setPreviewImages(version.content_kind === "imagetext" ? items : []);
        setPreviewNote(`预览链接 ${first.expires_at} 前有效。到期重新点「预览」即可,不产生任何费用。`);
      } else {
        setDownloadItems(items);
        if (items.length > 1) {
          setPreviewNote(`本版共有 ${items.length} 个文件。请逐个保存；链接到期后重新点击「下载这一版」。`);
          return;
        }
        // 按真实结果表述:这里是「开始下载」,保存成功与否以浏览器为准。
        const a = document.createElement("a");
        a.href = first.url;
        a.download = first.filename || "";
        document.body.appendChild(a);
        a.click();
        a.remove();
        setPreviewNote(`已开始下载「${first.filename}」(内容指纹 ${first.sha256.slice(0, 12)}…)。是否已保存,以浏览器实际结果为准。`);
      }
    } catch {
      setError("没有完成(没有连上服务)。");
    } finally {
      setBusy("");
    }
  }

  return (
    <section className="tk-section" data-testid="store-content-drawer" data-store-id={storeId}>
      <h2 className="tk-section-title">准备当前用途的内容</h2>
      <p className="tk-note" data-field="content-context">{storeName || "当前门店"} · {purpose}{campaignId ? " · 用于当前活动草稿" : " · 可直接用于门店"}</p>
      <p className="tk-note">选择一种来源，补充同一份文案，预览后明确采用。采用状态为「已选用待发布」，上线需回原用途确认发布。</p>
      {adoptionNote ? <p className="tk-note tk-ok" role="status">{adoptionNote} {returnHref ? <a href={returnHref}>返回原用途</a> : null}</p> : null}
      {phase === "loading" ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
      {phase === "error" ? (
        <p className="tk-state tk-danger" data-state="error">
          {error || surfaceLabel("error")}
          <button className="tk-quiet" type="button" data-action="retry" onClick={() => void load()}>重试</button>
        </p>
      ) : null}
      {error && phase === "ready" ? <p className="tk-danger" data-state="error">{error}</p> : null}
      {phase === "ready" ? (
        <>
          <fieldset className="tk-section"><legend className="tk-label">内容来源</legend>
            {([{ id: "upload", label: "上传文件" }, { id: "library", label: "选择已有" }, { id: "produce", label: "帮我制作" }] as const).map((item) => <label className="tk-label" key={item.id}><input type="radio" name={`content-source-${storeId}`} checked={source === item.id} onChange={() => setSource(item.id)} /> {item.label}</label>)}
          </fieldset>
          {source === "library" ? <label className="tk-label">已有候选或已采用内容
            <select className="tk-select" aria-label="选择已有内容版本" value={reuseVersion} onChange={(event) => {
              const id = event.target.value; setReuseVersion(id);
              const existing = packs.flatMap((pack) => pack.versions.map((version) => ({ pack, version }))).find((item) => item.version.id === id);
              if (!existing) { setReuseSnapshot(""); return; }
              const version = existing.version;
              const next = { title: version.title, body: version.body, topics: (version.topics || []).join("，"), store_name: version.store_name, price_note: version.price_note, valid_note: version.valid_note };
              const platform = version.platform_versions?.[0]?.platform || draftPlatform;
              const kind = version.content_kind === "imagetext" ? "imagetext" : "video";
              const images = (version.image_text || []).map((item) => item.media_id);
              setDraft(next); setDraftKind(kind); setDraftImages(images); setDraftVideo(version.video?.media_id || ""); setDraftCover(version.cover?.media_id || ""); setDraftPack(existing.pack.id); setDraftPlatform(platform); setAiGenerated(Boolean(version.marking?.ai_generated));
              setReuseSnapshot(JSON.stringify({ draft: next, kind, images, video: version.video?.media_id || "", cover: version.cover?.media_id || "", platform, ai: Boolean(version.marking?.ai_generated) }));
            }}><option value="">选择已有内容，或在下方媒体中编新稿</option>{packs.flatMap((pack) => pack.versions.filter((version) => ["candidate", "adopted"].includes(version.status)).map((version) => <option key={version.id} value={version.id}>{pack.title} · 第 {version.version_no} 版 · {version.title}</option>))}</select>
            <span className="tk-note">保留原候选和版本；修改文案或媒体才会提交新版本。</span>
          </label> : null}
          {/* 真实上传:上传完成以服务器实测元数据为准。 */}
          {source === "upload" ? <div className="tk-section" data-entry="real-upload">
            <h3 className="tk-label">上传视频或图片</h3>
            <input
              className="tk-input"
              type="file"
              accept="video/mp4,video/quicktime,image/jpeg,image/png,image/webp"
              aria-label="选择要上传的视频或图片文件"
              data-action="upload"
              disabled={uploading}
              onChange={(event) => {
                const file = event.target.files?.[0];
                if (file) void uploadFile(file);
                event.target.value = "";
              }}
            />
            {uploading ? <p className="tk-note" data-state="uploading">正在上传并核验文件…</p> : null}
            {uploadedNote ? <p className="tk-note tk-ok" data-field="upload-result">{uploadedNote}</p> : null}
          </div> : null}

          {/* 组织候选/换版:同一包再提交 = 新版本。 */}
          <form className="tk-form tk-section" data-entry="store-draft" onSubmit={(event) => void submitCandidate(event)}>
            <h3 className="tk-label">准备内容{draftPack ? "（保存为新版本）" : ""}</h3>
            <label className="tk-label">内容形式<select className="tk-select" aria-label="选择内容形式" value={draftKind} onChange={(event) => setDraftKind(event.target.value as "video" | "imagetext")}><option value="video">视频</option><option value="imagetext">图文</option></select></label>
            {source !== "produce" ? <>{media.length === 0 ? (
              <p className="tk-note tk-unknown">还没有已核验的媒体。先在上面上传真实文件。</p>
            ) : (
              <>
                {draftKind === "video" ? <><label className="tk-label">
                  成片
                  <select className="tk-select" aria-label="选择成片" value={draftVideo} onChange={(event) => setDraftVideo(event.target.value)} required>
                    <option value="">选择成片</option>
                    {media.filter((m) => m.kind === "video").map((m) => (
                      <option key={m.id} value={m.id}>
                        {m.filename || "视频"} · {m.mime_type} · {bytesCopy(m.byte_size)}{durationCopy(m.duration_ms)}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="tk-label">
                  封面
                  <select className="tk-select" aria-label="选择封面" value={draftCover} onChange={(event) => setDraftCover(event.target.value)} required>
                    <option value="">选择封面</option>
                    {media.filter((m) => m.kind === "image").map((m) => (
                      <option key={m.id} value={m.id}>
                        {m.filename || "图片"} · {m.mime_type} · {bytesCopy(m.byte_size)}
                      </option>
                    ))}
                  </select>
                </label>
                </> : <fieldset className="tk-section"><legend className="tk-label">图文图片（按所选顺序发布）</legend>
                  {media.filter((item) => item.kind === "image").map((item) => <label key={item.id} className="tk-label"><input type="checkbox" checked={draftImages.includes(item.id)} onChange={(event) => setDraftImages((items) => event.target.checked ? [...items, item.id] : items.filter((id) => id !== item.id))} /> {item.filename || "图片"} · {bytesCopy(item.byte_size)}</label>)}
                  {draftImages.length > 0 ? <ol>{draftImages.map((id, index) => <li key={id}>{media.find((item) => item.id === id)?.filename || "已选图片"} <button type="button" className="tk-quiet" disabled={index === 0} aria-label={`将第${index + 1}张图片上移`} onClick={() => setDraftImages((items) => { const next = [...items]; [next[index - 1], next[index]] = [next[index], next[index - 1]]; return next; })}>上移</button></li>)}</ol> : <p className="tk-note">请选择图片，或在上方上传。</p>}
                </fieldset>}
              </>
            )}
            </> : null}
            <details><summary>更新已有内容系列（可选）</summary><label className="tk-label">
              内容系列（可选）
              <select className="tk-select" aria-label="选择内容包" value={draftPack} onChange={(event) => setDraftPack(event.target.value)}>
                <option value="">系统按当前标题创建或复用</option>
                {packs.filter((p) => !p.legacy).map((p) => (
                  <option key={p.id} value={p.id}>{p.title}</option>
                ))}
              </select>
            </label></details>
            <label className="tk-label">
              平台
              <select className="tk-select" aria-label="选择平台" value={draftPlatform} onChange={(event) => setDraftPlatform(event.target.value)}>
                {PLATFORMS.map((p) => (
                  <option key={p.id} value={p.id}>{p.label}</option>
                ))}
              </select>
            </label>
            <label className="tk-label">
              标题
              <input className="tk-input" name="title" type="text" value={draft.title} onChange={(event) => setDraft({ ...draft, title: event.target.value })} required />
            </label>
            <label className="tk-label">
              正文
              <input className="tk-input" name="body" type="text" value={draft.body} onChange={(event) => setDraft({ ...draft, body: event.target.value })} required />
            </label>
            <label className="tk-label">
              话题(逗号分隔,可空)
              <input className="tk-input" name="topics" type="text" value={draft.topics} onChange={(event) => setDraft({ ...draft, topics: event.target.value })} />
            </label>
            <label className="tk-label">
              门店名
              <input className="tk-input" name="store_name" type="text" value={draft.store_name} onChange={(event) => setDraft({ ...draft, store_name: event.target.value })} required />
            </label>
            <details><summary>价格与期限说明（内容涉及时填写）</summary><label className="tk-label">
              价格说明（内容涉及价格时填写）
              <input className="tk-input" name="price_note" type="text" value={draft.price_note} onChange={(event) => setDraft({ ...draft, price_note: event.target.value })} />
            </label>
            <label className="tk-label">
              展示期限说明（可选）
              <input className="tk-input" name="valid_note" type="text" value={draft.valid_note} onChange={(event) => setDraft({ ...draft, valid_note: event.target.value })} />
            </label></details>
            <label className="tk-label"><input type="checkbox" checked={aiGenerated} onChange={(event) => setAiGenerated(event.target.checked)} /> 含 AI 合成画面或配音（会明确标给顾客）</label>
            <p className="tk-note">请确认素材版权及使用授权；优惠推广内容按平台要求标注广告。</p>
            {source !== "produce" ? <button className="tk-button" type="submit" disabled={busy === "draft" || uploading}>{source === "library" && reuseVersion ? "预览原版本；有修改则提交新候选" : "提交候选，随后预览采用"}</button> : null}
          </form>
          {source === "produce" ? <StoreProductionPanel storeId={storeId} campaignId={campaignId} packId={draftPack} title={draft.title || purpose} brief={{ ...draft, platform: draftPlatform, content_kind: draftKind, purpose, ai_generated: aiGenerated }} onChanged={() => void load()} /> : null}

          {/* 包/版本/采用指针。 */}
          <ul className="tk-list" data-field="pack-list">
            {packs.length === 0 ? <p className="tk-note">这个门店还没有内容包。上传文件并提交候选后,这里会列出来。</p> : null}
            {packs.map((pack) => {
              const adoptedByVersion = new Map(pack.adoptions.map((a) => [a.version_id, a]));
              return (
                <li key={pack.id} data-pack-id={pack.id}>
                  <p className="tk-note">
                    {pack.title}
                    {pack.legacy ? " · 历史内容" : ""}
                    {pack.campaign_id ? " · 活动内容" : " · 门店内容"}
                  </p>
                  <ul className="tk-list">
                    {pack.versions.map((version) => {
                      const adoption = adoptedByVersion.get(version.id);
                      const isBlocked = version.status === "blocked";
                      const isAdopted = Boolean(adoption);
                      return (
                        <li key={version.id} data-version-id={version.id} data-status={version.status}>
                          <p className="tk-note">
                            第 {version.version_no} 版 · {statusCopy(version.status)} · {sourceCopy(version.source)} · {version.title}
                          </p>
                          <p className="tk-note">{version.body}</p>
                          <p className="tk-note">门店 {version.store_name}{version.price_note ? ` · ${version.price_note}` : ""}{version.valid_note ? ` · ${version.valid_note}` : ""}</p>
                          {version.marking?.ai_generated ? (
                            <p className="tk-unknown" data-field="ai-marking">含 AI 合成画面或配音。采用后会明确标给顾客,不隐藏。</p>
                          ) : null}
                          {isBlocked ? (
                            <p className="tk-note tk-unknown" data-field="blocked-reason">
                              已停止分发:{version.blocked_reason || "素材或授权有问题"}。已发出去的内容保留原版本。
                            </p>
                          ) : null}
                          {adoption && !isBlocked ? (
                            <p className="tk-note tk-ok" data-field="adopted-note">
                              已选用待发布({adoption.platform}):第 {version.version_no} 版。线上是否使用，以原用途的发布版本为准。
                            </p>
                          ) : null}
                          <div className="tk-row">
                            {isAdopted && !isBlocked ? (
                              <>
                                <button
                                  className="tk-quiet"
                                  type="button"
                                  data-action="preview"
                                  disabled={busy === version.id + ":preview"}
                                  onClick={() => void openMedia(version, "preview")}
                                >
                                  {version.content_kind === "imagetext" ? "预览图文" : "预览视频"}
                                </button>
                                <button
                                  className="tk-quiet"
                                  type="button"
                                  data-action="download"
                                  disabled={busy === version.id + ":download"}
                                  onClick={() => void openMedia(version, "download")}
                                >
                                  下载这一版
                                </button>
                              </>
                            ) : !isBlocked && version.status === "candidate" ? (
                              <>
                                {/* HUI-4595: 回流候选采用前可预览真实成片(media-access
                                    短 TTL 受控 URL,纯预览零计费);采用仍须显式。 */}
                                <button
                                  className="tk-quiet"
                                  type="button"
                                  data-action="preview-candidate"
                                  disabled={busy === version.id + ":preview"}
                                  onClick={() => void openMedia(version, "preview")}
                                >
                                  {version.content_kind === "imagetext" ? "预览图文" : "预览视频"}
                                </button>
                                <button
                                  className="tk-button"
                                  type="button"
                                  data-action="adopt"
                                  disabled={busy === version.id + draftPlatform}
                                  onClick={() => void adopt(pack.id, version.id, draftPlatform)}
                                >
                                  采用这一版({PLATFORMS.find((p) => p.id === draftPlatform)?.label ?? draftPlatform})
                                </button>
                              </>
                            ) : null}
                            {!isBlocked ? (
                              <button
                                className="tk-quiet"
                                type="button"
                                data-action="block"
                                disabled={busy === version.id + ":block"}
                                onClick={() => void block(pack.id, version.id)}
                              >
                                停止分发
                              </button>
                            ) : null}
                          </div>
                        </li>
                      );
                    })}
                  </ul>
                </li>
              );
            })}
          </ul>
          {previewURL ? (
            <div className="tk-section" data-field="preview">
              <video className={styles.preview} src={previewURL} controls preload="metadata" data-testid="preview-player"></video>
              <p className="tk-note">{previewNote}</p>
            </div>
          ) : null}
          {previewImages.length > 0 ? <div className="tk-section" data-field="image-preview"><h3 className="tk-label">图文预览</h3>{previewImages.map((item, index) => <img key={item.media_id} className={styles.preview} src={item.url} alt={`第${index + 1}张：${item.filename || "图文图片"}`} />)}<p className="tk-note">{previewNote}</p></div> : null}
          {downloadItems.length > 1 ? <div className="tk-section" data-field="download-files"><p className="tk-note">{previewNote}</p><ul className="tk-list">{downloadItems.map((item, index) => <li key={item.media_id}><a className="tk-quiet" href={item.url} download={item.filename}>保存第{index + 1}个文件：{item.filename || "内容文件"}</a></li>)}</ul></div> : null}
          {!previewURL && previewImages.length === 0 && downloadItems.length <= 1 && previewNote ? <p className="tk-note tk-ok" data-field="download-result">{previewNote}</p> : null}
        </>
      ) : null}
    </section>
  );
}

export const ContentEditor = StoreContentDrawer;
