"use client";

import { useCallback, useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { LoadErrorState } from "@/components/work/load-error-state";
import { surfaceLabel } from "@/lib/product-finish";

// HUI-3553 素材页三入口:上传已有内容 / 从素材库选择 / 帮我制作。
// 纪律:
//   - 商家全程不填 asset_id/version 技术字段:素材用选择器挑(选择器内部携带
//     素材 ID),来源/类型/授权声明直接展示;
//   - 「帮我制作」只在真实 handoff 能力存在时可用;本票未接制作引擎,按钮
//     如实禁用并显示原因,绝不阻塞前两个入口;
//   - 候选提交后可预览、编辑文案(= 新版本)、明确采用;采用前顾客侧不读它。

interface Campaign {
  id: string;
  title: string;
}

interface LibraryItem {
  id: string;
  purpose?: string;
  media_type?: string;
  asset_ref?: string;
  source?: string;
  grant_ref?: string;
}

interface PackVersion {
  id: string;
  version_no: number;
  status: string;
  title: string;
}

interface PackAdoption {
  platform: string;
  version_id: string;
}

interface ContentPack {
  id: string;
  title: string;
  versions: PackVersion[];
  adoptions: PackAdoption[];
}

const PLATFORMS: { id: string; label: string }[] = [
  { id: "douyin", label: "抖音" },
  { id: "shipinhao", label: "视频号" },
  { id: "xiaohongshu", label: "小红书" },
  { id: "dianping", label: "大众点评" },
];

const EMPTY_FORM = {
  campaign: "",
  title: "",
  body: "",
  topics: "",
  store_name: "",
  price_note: "",
  valid_note: "",
};

function mediaTypeLabel(mt?: string): string {
  switch (mt) {
    case "video":
      return "视频";
    case "image":
      return "图片";
    case "bgm":
      return "音乐";
    default:
      return mt || "未注明";
  }
}

function sourceLabel(source?: string): string {
  switch (source) {
    case "merchant_upload":
      return "商家上传";
    case "product_photo":
      return "产品图";
    case "aicut":
      return "AiCut 成片";
    default:
      return source || "未注明";
  }
}

function grantLabel(grantRef?: string): string {
  return grantRef ? "授权已声明" : "无授权声明";
}

function PackForm({
  entry,
  campaigns,
  library,
  form,
  setForm,
  videoId,
  coverId,
  setVideoId,
  setCoverId,
  platforms,
  togglePlatform,
  onSubmit,
  busy,
}: {
  entry: "upload" | "library";
  campaigns: Campaign[];
  library: LibraryItem[];
  form: typeof EMPTY_FORM;
  setForm: (next: typeof EMPTY_FORM) => void;
  videoId: string;
  coverId: string;
  setVideoId: (id: string) => void;
  setCoverId: (id: string) => void;
  platforms: string[];
  togglePlatform: (id: string) => void;
  onSubmit: (event: React.FormEvent) => void;
  busy: boolean;
}) {
  const videos = library.filter((item) => item.media_type === "video");
  const covers = library.filter((item) => item.media_type === "image");
  const headings = {
    upload: "上传已有内容",
    library: "从素材库选择",
  } as const;
  const intros = {
    upload: "把你已经做好的成片组织成一个候选内容包。先在上面确认成片已在素材库,再在这里选用并补全文案。",
    library: "从素材库已有的素材里挑成片和封面,补全文案后成为一个候选内容包。",
  } as const;
  return (
    <form className="tk-form tk-section" data-entry={entry} data-entry-panel={entry} onSubmit={onSubmit}>
      <h2 className="tk-section-title">{headings[entry]}</h2>
      <p className="tk-note">{intros[entry]}</p>
      <select
        className="tk-select"
        aria-label="选择活动"
        name="campaign"
        value={form.campaign}
        onChange={(event) => setForm({ ...form, campaign: event.target.value })}
        required
      >
        <option value="">选择活动</option>
        {campaigns.map((item) => (
          <option key={item.id} value={item.id}>{item.title}</option>
        ))}
      </select>
      <label className="tk-label">
        成片
        <select className="tk-select" aria-label="选择成片" value={videoId} onChange={(event) => setVideoId(event.target.value)} required>
          <option value="">选择成片</option>
          {videos.map((item) => (
            <option key={item.id} value={item.id}>
              {mediaTypeLabel(item.media_type)} · {sourceLabel(item.source)} · {grantLabel(item.grant_ref)} · {item.purpose || "未注明用途"}
            </option>
          ))}
        </select>
      </label>
      <label className="tk-label">
        封面
        <select className="tk-select" aria-label="选择封面" value={coverId} onChange={(event) => setCoverId(event.target.value)} required>
          <option value="">选择封面</option>
          {covers.map((item) => (
            <option key={item.id} value={item.id}>
              {mediaTypeLabel(item.media_type)} · {grantLabel(item.grant_ref)} · {item.purpose || "未注明用途"}
            </option>
          ))}
        </select>
      </label>
      {videos.length === 0 ? (
        <p className="tk-note tk-unknown">素材库还没有可用的成片。先在素材库登记,再回到这里。</p>
      ) : null}
      <fieldset className="tk-section">
        <legend className="tk-label">适用平台</legend>
        {PLATFORMS.map((p) => (
          <label key={p.id} className="tk-label">
            <input
              type="checkbox"
              name="platform"
              value={p.id}
              checked={platforms.includes(p.id)}
              onChange={() => togglePlatform(p.id)}
            />
            {" "}
            {p.label}
          </label>
        ))}
      </fieldset>
      <label className="tk-label">
        标题
        <input className="tk-input" name="title" type="text" value={form.title} onChange={(event) => setForm({ ...form, title: event.target.value })} required />
      </label>
      <label className="tk-label">
        正文
        <input className="tk-input" name="body" type="text" value={form.body} onChange={(event) => setForm({ ...form, body: event.target.value })} required />
      </label>
      <label className="tk-label">
        话题(逗号分隔,可空)
        <input className="tk-input" name="topics" type="text" value={form.topics} onChange={(event) => setForm({ ...form, topics: event.target.value })} />
      </label>
      <label className="tk-label">
        门店名
        <input className="tk-input" name="store_name" type="text" value={form.store_name} onChange={(event) => setForm({ ...form, store_name: event.target.value })} required />
      </label>
      <label className="tk-label">
        价格说明
        <input className="tk-input" name="price_note" type="text" value={form.price_note} onChange={(event) => setForm({ ...form, price_note: event.target.value })} required />
      </label>
      <label className="tk-label">
        有效期
        <input className="tk-input" name="valid_note" type="text" value={form.valid_note} onChange={(event) => setForm({ ...form, valid_note: event.target.value })} required />
      </label>
      <label className="tk-label">
        <input type="checkbox" name="ad_required" checked readOnly /> 内容含优惠推广信息时按平台要求标注广告
      </label>
      <button className="tk-button" type="submit" disabled={busy}>提交候选内容包</button>
    </form>
  );
}

export default function MaterialsPage() {
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "error" | "ready">("loading");
  const [error, setError] = useState("");
  const [campaigns, setCampaigns] = useState<Campaign[]>([]);
  const [library, setLibrary] = useState<LibraryItem[]>([]);
  const [libraryNote, setLibraryNote] = useState("");
  const [packs, setPacks] = useState<ContentPack[]>([]);
  // 「帮我制作」:能力如实申报;未接通 = 禁用 + 原因,不阻塞其余入口。
  const [produceAvailable, setProduceAvailable] = useState(false);
  const [produceReason, setProduceReason] = useState("正在确认制作能力…");
  // 表单状态(两个入口各自一份,互不串)
  const [uploadForm, setUploadForm] = useState(EMPTY_FORM);
  const [libraryForm, setLibraryForm] = useState(EMPTY_FORM);
  const [uploadVideo, setUploadVideo] = useState("");
  const [uploadCover, setUploadCover] = useState("");
  const [libraryVideo, setLibraryVideo] = useState("");
  const [libraryCover, setLibraryCover] = useState("");
  const [uploadPlatforms, setUploadPlatforms] = useState<string[]>(["douyin"]);
  const [libraryPlatforms, setLibraryPlatforms] = useState<string[]>(["douyin"]);
  const [busy, setBusy] = useState(false);

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
    const lib = await session.api("GET", "assets");
    if (lib.status === 404) {
      setLibrary([]);
      setLibraryNote("素材库未开通。开通后可以在这里挑选成片和封面。");
    } else if (!lib.ok) {
      setLibrary([]);
      setLibraryNote("素材库没有读到,稍后再试。已建的内容包仍列在下面。");
    } else {
      setLibrary(Array.isArray(lib.data.items) ? (lib.data.items as LibraryItem[]) : []);
      setLibraryNote("");
    }
    const packsRes = await session.api("GET", `campaigns/${items[0]?.id ?? ""}/content-packs`).catch(() => null);
    setPacks(packsRes && packsRes.ok && Array.isArray(packsRes.data.items) ? (packsRes.data.items as ContentPack[]) : []);
    // 制作能力如实申报:未接通只影响「帮我制作」按钮,不阻塞上传/选素材。
    const cap = await session.api("GET", "content-handoffs/capabilities").catch(() => null);
    if (cap && cap.ok && typeof cap.data.production_available === "boolean") {
      setProduceAvailable(cap.data.production_available as boolean);
      setProduceReason(
        cap.data.production_available
          ? "已接通制作能力。"
          : typeof cap.data.reason === "string" && cap.data.reason
            ? cap.data.reason
            : "制作引擎尚未接入。",
      );
    } else {
      setProduceAvailable(false);
      setProduceReason("制作能力没有读到:制作按钮先保持不可用。上传已有内容和从素材库选择不受影响。");
    }
    setPhase("ready");
  }, [session]);

  useEffect(() => {
    void load().catch(() => {
      setError("没有连上服务");
      setPhase("error");
    });
  }, [load]);

  async function submitEntry(entry: "upload" | "library", event: React.FormEvent) {
    event.preventDefault();
    const form = entry === "upload" ? uploadForm : libraryForm;
    const videoId = entry === "upload" ? uploadVideo : libraryVideo;
    const coverId = entry === "upload" ? uploadCover : libraryCover;
    const platforms = entry === "upload" ? uploadPlatforms : libraryPlatforms;
    setBusy(true);
    setError("");
    try {
      // 建包(活动归属+标题),再落候选版本(文案/封面/门店事实/适用平台)。
      const packRes = await session.api("POST", `campaigns/${form.campaign}/content-packs`, { title: form.title });
      let packId = "";
      if (packRes.ok) {
        packId = String(packRes.data.id ?? "");
      } else if (packRes.status === 409) {
        // 同名包已存在:复用既有包,新开一版(改文案=新版本,旧版不动)。
        const existing = packs.find((p) => p.title === form.title && p.id);
        packId = existing?.id ?? "";
      }
      if (!packId) {
        setError(failureText(packRes.status, packRes.data));
        return;
      }
      const versionRes = await session.api("POST", `content-packs/${packId}/versions`, {
        content_kind: "video",
        video_asset_id: videoId,
        cover_asset_id: coverId,
        title: form.title,
        body: form.body,
        topics: form.topics.split(/[,，]/).map((t) => t.trim()).filter(Boolean),
        store_name: form.store_name,
        price_note: form.price_note,
        valid_note: form.valid_note,
        platform_versions: platforms.map((platform) => ({ platform, note: "", source_version: form.title })),
        source: entry === "upload" ? "merchant_upload" : "asset_lib",
        status: "candidate",
      });
      if (!versionRes.ok) {
        setError(failureText(versionRes.status, versionRes.data));
        return;
      }
      setError("");
      await load();
    } catch {
      setError("没有完成(没有连上服务)。候选内容包还没有提交。");
    } finally {
      setBusy(false);
    }
  }

  async function adopt(packId: string, versionId: string) {
    setBusy(true);
    try {
      const res = await session.api("POST", `content-packs/${packId}/versions/${versionId}/adopt`, { platform: "douyin" });
      if (!res.ok) {
        setError(failureText(res.status, res.data));
        return;
      }
      setError("");
      await load();
    } finally {
      setBusy(false);
    }
  }

  return (
    <main>
      <h1 className="tk-title">素材与内容包</h1>
      <p className="tk-lead">把成片、封面和文案组织成一个候选内容包,预览后点「采用」。采用后顾客碰一碰拿到的就是这一版。</p>
      {phase === "loading" ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
      {phase === "error" ? <LoadErrorState message={error || surfaceLabel("error")} onRetry={() => void load()} /> : null}
      {libraryNote ? <p className="tk-unknown">{libraryNote}</p> : null}
      {error && phase !== "error" ? <p className="tk-danger" data-state="error">{error}</p> : null}
      {phase === "ready" ? (
        <>
          <PackForm
            entry="upload"
            campaigns={campaigns}
            library={library}
            form={uploadForm}
            setForm={setUploadForm}
            videoId={uploadVideo}
            coverId={uploadCover}
            setVideoId={setUploadVideo}
            setCoverId={setUploadCover}
            platforms={uploadPlatforms}
            togglePlatform={(id) =>
              setUploadPlatforms((cur) => (cur.includes(id) ? cur.filter((p) => p !== id) : [...cur, id]))
            }
            onSubmit={(event) => void submitEntry("upload", event)}
            busy={busy}
          />
          <PackForm
            entry="library"
            campaigns={campaigns}
            library={library}
            form={libraryForm}
            setForm={setLibraryForm}
            videoId={libraryVideo}
            coverId={libraryCover}
            setVideoId={setLibraryVideo}
            setCoverId={setLibraryCover}
            platforms={libraryPlatforms}
            togglePlatform={(id) =>
              setLibraryPlatforms((cur) => (cur.includes(id) ? cur.filter((p) => p !== id) : [...cur, id]))
            }
            onSubmit={(event) => void submitEntry("library", event)}
            busy={busy}
          />
          <section className="tk-section" data-entry="produce">
            <h2 className="tk-section-title">帮我制作</h2>
            <p className="tk-note">没有合适成片时,可以让制作能力按这个活动的目的、门店资料和选定素材帮你做。只打开制作工程不扣费;生成前沿既有报价与确认。</p>
            <p className="tk-note tk-unknown" data-field="produce-reason">{produceReason}</p>
            <button
              className="tk-button"
              type="button"
              disabled={!produceAvailable}
              onClick={() => {
                if (produceAvailable) return;
              }}
            >
              帮我制作
            </button>
          </section>
          <section className="tk-section">
            <h2 className="tk-section-title">最近的内容包</h2>
            {packs.length === 0 ? <p className="tk-note">还没有内容包。提交候选后,去活动详情页预览并采用。</p> : null}
            <ul className="tk-list">
              {packs.map((pack) => (
                <li key={pack.id} data-pack-id={pack.id}>
                  <p className="tk-note">{pack.title}</p>
                  <ul className="tk-list">
                    {pack.versions.map((version) => {
                      const adopted = pack.adoptions.some((a) => a.version_id === version.id);
                      return (
                        <li key={version.id} data-version-id={version.id}>
                          第 {version.version_no} 版 · {version.status} · {version.title}
                          {adopted ? " · 当前采用" : ""}
                          {!adopted && version.status === "candidate" ? (
                            <button
                              className="tk-quiet"
                              type="button"
                              data-action="adopt"
                              disabled={busy}
                              onClick={() => void adopt(pack.id, version.id)}
                            >
                              采用这一版
                            </button>
                          ) : null}
                        </li>
                      );
                    })}
                  </ul>
                </li>
              ))}
            </ul>
          </section>
        </>
      ) : null}
    </main>
  );
}
