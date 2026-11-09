"use client";

import { useCallback, useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { surfaceLabel } from "@/lib/product-finish";

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
  { id: "shipinhao", label: "视频号" },
  { id: "xiaohongshu", label: "小红书" },
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

export function StoreContentDrawer({ storeId }: { storeId: string }) {
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState("");
  const [packs, setPacks] = useState<StoreContentPack[]>([]);
  const [media, setMedia] = useState<StoreMedia[]>([]);
  // 真实上传:文件选择后立即上传,展示服务器实测元数据。
  const [uploading, setUploading] = useState(false);
  const [uploadedNote, setUploadedNote] = useState("");
  // 组织候选(换版也走这里:选同一个包 = 新开一版)。
  const [draft, setDraft] = useState(EMPTY_DRAFT);
  const [draftVideo, setDraftVideo] = useState("");
  const [draftCover, setDraftCover] = useState("");
  const [draftPlatform, setDraftPlatform] = useState("douyin");
  const [draftPack, setDraftPack] = useState("");
  const [busy, setBusy] = useState("");
  // 受控预览/下载(purpose=preview|download 的短 TTL URL)。
  const [previewURL, setPreviewURL] = useState("");
  const [previewNote, setPreviewNote] = useState("");

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
    if (!draftVideo || !draftCover) {
      setError("先选成片和封面,再组织候选。");
      return;
    }
    setBusy("draft");
    setError("");
    try {
      const packId = draftPack || packs.find((p) => !p.legacy)?.id || "";
      if (!packId) {
        setError("还没有门店内容包。先在下面的「新包」里起个名字。");
        return;
      }
      const res = await session.api("POST", `stores/${storeId}/content-packs/${packId}/versions`, {
        content_kind: "video",
        video_media_id: draftVideo,
        cover_media_id: draftCover,
        title: draft.title,
        body: draft.body,
        topics: draft.topics.split(/[,，]/).map((t) => t.trim()).filter(Boolean),
        store_name: draft.store_name,
        price_note: draft.price_note,
        valid_note: draft.valid_note,
        platform_versions: [{ platform: draftPlatform, note: "", source_version: draft.title }],
        marking: { ad_required: true, ai_generated: false },
        source: "merchant_upload",
        status: "candidate",
      });
      if (!res.ok) {
        setError(failureText(res.status, res.data));
        return;
      }
      setDraft(EMPTY_DRAFT);
      setDraftVideo("");
      setDraftCover("");
      await load();
    } catch {
      setError("没有完成(没有连上服务)。候选还没有提交。");
    } finally {
      setBusy("");
    }
  }

  async function createPack(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const input = (event.currentTarget.elements.namedItem("pack-title") as HTMLInputElement | null);
    const title = (input?.value ?? "").trim();
    if (!title) return;
    setBusy("pack");
    setError("");
    try {
      const res = await session.api("POST", `stores/${storeId}/content-packs`, { title });
      if (!res.ok) {
        setError(failureText(res.status, res.data));
        return;
      }
      if (input) input.value = "";
      await load();
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
      await load();
    } catch {
      setError("没有完成(没有连上服务)。这一版还没有被采用。");
    } finally {
      setBusy("");
    }
  }

  async function block(packId: string, versionId: string) {
    const reason = window.prompt("停止分发的原因(会记进处理记录):");
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
    const mediaIDs = [version.video?.media_id, version.cover?.media_id].filter((x): x is string => Boolean(x));
    if (mediaIDs.length === 0) {
      setError("这一版没有可播放的媒体。");
      return;
    }
    setBusy(version.id + ":" + purpose);
    setError("");
    setPreviewNote("");
    try {
      const res = await session.api("POST", `public/content/${version.id}/media-access`, {
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
        setPreviewURL(first.url);
        setPreviewNote(`预览链接 ${first.expires_at} 前有效。到期重新点「预览」即可,不产生任何费用。`);
      } else {
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
      <h2 className="tk-section-title">门店内容包</h2>
      <p className="tk-note">上传真实文件、组织候选、点「采用」。采用后顾客碰一碰拿到的就是这一版;换版 = 新候选再采用,已发出的旧版不受影响。</p>
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
          {/* 真实上传:上传完成以服务器实测元数据为准。 */}
          <div className="tk-section" data-entry="real-upload">
            <h3 className="tk-label">上传真实文件(视频或封面图)</h3>
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
          </div>

          {/* 组织候选/换版:同一包再提交 = 新版本。 */}
          <form className="tk-form tk-section" data-entry="store-draft" onSubmit={(event) => void submitCandidate(event)}>
            <h3 className="tk-label">组织候选{draftPack ? "(换版:同一包新开一版)" : ""}</h3>
            {media.length === 0 ? (
              <p className="tk-note tk-unknown">还没有已核验的媒体。先在上面上传真实文件。</p>
            ) : (
              <>
                <label className="tk-label">
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
              </>
            )}
            <label className="tk-label">
              目标内容包
              <select className="tk-select" aria-label="选择内容包" value={draftPack} onChange={(event) => setDraftPack(event.target.value)}>
                <option value="">最新门店包</option>
                {packs.filter((p) => !p.legacy).map((p) => (
                  <option key={p.id} value={p.id}>{p.title}</option>
                ))}
              </select>
            </label>
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
            <label className="tk-label">
              价格说明
              <input className="tk-input" name="price_note" type="text" value={draft.price_note} onChange={(event) => setDraft({ ...draft, price_note: event.target.value })} required />
            </label>
            <label className="tk-label">
              有效期
              <input className="tk-input" name="valid_note" type="text" value={draft.valid_note} onChange={(event) => setDraft({ ...draft, valid_note: event.target.value })} required />
            </label>
            <button className="tk-button" type="submit" disabled={busy === "draft"}>提交候选</button>
          </form>

          {/* 新包(campaign 可空:门店根资产,不要求先建活动)。 */}
          <form className="tk-form" data-entry="store-pack-create" onSubmit={(event) => void createPack(event)}>
            <label className="tk-label">
              新包名称
              <input className="tk-input" name="pack-title" type="text" aria-label="新内容包名称" />
            </label>
            <button className="tk-quiet" type="submit" disabled={busy === "pack"}>新建内容包(可以不挂活动)</button>
          </form>

          {/* 包/版本/采用指针。 */}
          <ul className="tk-list" data-field="pack-list">
            {packs.length === 0 ? <p className="tk-note">这个门店还没有内容包。上传文件并提交候选后,这里会列出来。</p> : null}
            {packs.map((pack) => {
              const adoptedByVersion = new Map(pack.adoptions.map((a) => [a.version_id, a]));
              return (
                <li key={pack.id} data-pack-id={pack.id}>
                  <p className="tk-note">
                    {pack.title}
                    {pack.legacy ? " · 来自活动(旧)" : ""}
                    {pack.campaign_id ? " · 挂了活动" : " · 门店根(未挂活动)"}
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
                          <p className="tk-note">门店 {version.store_name} · 价格 {version.price_note} · 有效期 {version.valid_note}</p>
                          {version.marking?.ai_generated ? (
                            <p className="tk-unknown" data-field="ai-marking">含 AI 合成画面或配音。采用后会明确标给顾客,不隐藏。</p>
                          ) : null}
                          {isBlocked ? (
                            <p className="tk-note tk-unknown" data-field="blocked-reason">
                              已停止分发:{version.blocked_reason || "素材或授权有问题"}。已发出去的内容保留原版本。
                            </p>
                          ) : null}
                          {adoption ? (
                            <p className="tk-note tk-ok" data-field="adopted-note">
                              当前采用({adoption.platform}):第 {version.version_no} 版,顾客看到的就是这一版。
                            </p>
                          ) : null}
                          <div className="tk-row">
                            {isAdopted ? (
                              <>
                                <button
                                  className="tk-quiet"
                                  type="button"
                                  data-action="preview"
                                  disabled={busy === version.id + ":preview"}
                                  onClick={() => void openMedia(version, "preview")}
                                >
                                  预览真实播放
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
                                  预览真实播放
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
                            {!isBlocked && !isAdopted && version.status !== "retired" ? (
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
              <video className="tk-note" src={previewURL} controls preload="metadata" data-testid="preview-player"></video>
              <p className="tk-note">{previewNote}</p>
            </div>
          ) : null}
          {!previewURL && previewNote ? <p className="tk-note tk-ok" data-field="download-result">{previewNote}</p> : null}
        </>
      ) : null}
    </section>
  );
}
