"use client";

// HUI-1896 C2 内容预览与交接(/s/{storeCode}/content/{actionId})。
//
// 旅程:进门幂等开/恢复 3546 会话 → 按会话冻结版本(无会话则用门店当前
// 采用版本)预览真实媒体(3553 media-access 受控凭据)→ 已采用候选池内
// 「换一个」(零收费,只改未交接草稿)→ 经原准入交接后复制文案/保存素材
// (1670 handoff v2;基线尚未提供时给「尚未开通」诚实态)→ 返回恢复同版本。
//
// 纪律(票面/裁定书):
//   - 已开始交接的版本不被商家新采用/刷新悄悄替换:冻结(frozen)语义如实
//     文案,客户端不覆盖已交接版本;
//   - 预览/换稿/下载零顾客侧收费,不触发任何 AI 任务;
//   - 素材缺失(availability=missing)给待办文案,不给死按钮;
//   - 跨浏览器恢复凭据只经 POST 体与内存,永不进 URL/落盘;
//   - 交接 plan 未知状态绝不渲染成功绿态;未准入路线不引导发布;
//   - 发布主体是顾客自己的账号;每一步都可以取消;
//   - 无生态导航、不强制注册/留资/下载。

import { Suspense, useCallback, useEffect, useRef, useState, type ChangeEvent } from "react";
import { ActionPublishFlow } from "@/components/publish/ActionPublishFlow";
import { useParams } from "next/navigation";
import { publicVisitorColumns } from "@/lib/visitor-experience";
import {
  detectEnvironment,
  parseActionCapabilities,
  pickRoute,
  type ActionCapabilitiesView,
} from "@/lib/env-capability";
import {
  exchangeResumeCredential,
  issueCustomerResume,
  mintMediaAccess,
  sessionStatus,
  startOrResumeSession,
  switchSessionContent,
  type ActionSessionView,
  type MediaAccessItem,
} from "@/lib/store-session";

type ContentState = "loading" | "ready" | "not_found" | "undistributable" | "network_error";

interface ContentMarking {
  ad_required?: boolean;
  ai_generated?: boolean;
  incentive_disclosed?: boolean;
}

interface ContentMediaItem {
  media_id: string;
  role: string;
  order: number;
  mime_type: string;
  byte_size: number;
  sha256: string;
  duration_ms: number;
  width: number;
  height: number;
  availability: string;
}

interface ContentCandidate {
  pack_id: string;
  version_id: string;
  title: string;
  content_kind: string;
}

interface ActionContentView {
  action_id: string;
  action_revision: number;
  selected_content_pack_version: string;
  pack_id: string;
  content_kind: string;
  title: string;
  body: string;
  topics: string[];
  store_name: string;
  price_note: string;
  valid_note: string;
  marking: ContentMarking;
  media: ContentMediaItem[];
  candidates: ContentCandidate[];
}

async function getContent(actionId: string, sessionId = ""): Promise<{ state: ContentState; data: ActionContentView | null }> {
  try {
    const res = await fetch(`/api/public/actions/${encodeURIComponent(actionId)}/content${sessionId ? `?action_session_id=${encodeURIComponent(sessionId)}` : ""}`);
    if (res.status === 502 || res.status === 503 || res.status === 504) return { state: "network_error", data: null };
    const data = await res.json().catch(() => null);
    if (res.status === 409) return { state: "undistributable", data: null };
    if (!res.ok || !data || typeof data !== "object" || typeof (data as Record<string, unknown>).selected_content_pack_version !== "string") {
      return { state: "not_found", data: null };
    }
    return { state: "ready", data: data as ActionContentView };
  } catch {
    return { state: "network_error", data: null };
  }
}

export function ContentView() {
  return (
    <Suspense fallback={<main className="tk-public" data-state="loading"><p className="tk-note">正在读取内容</p></main>}>
      <ContentViewInner />
    </Suspense>
  );
}

function ContentViewInner() {
  const params = useParams<{ storeCode: string; actionId: string }>();
  const storeCode = params?.storeCode ?? "";
  const actionId = params?.actionId ?? "";

  const [content, setContent] = useState<ActionContentView | null>(null);
  const [contentState, setContentState] = useState<ContentState>("loading");
  const [session, setSession] = useState<ActionSessionView | null>(null);
  const [sessionNote, setSessionNote] = useState("");
  const [previewUrls, setPreviewUrls] = useState<Record<string, MediaAccessItem>>({});
  const [previewNote, setPreviewNote] = useState("");
  const [switchNote, setSwitchNote] = useState("");
  const [resumeToken, setResumeToken] = useState("");
  const [resumeInput, setResumeInput] = useState("");
  const [resumeNote, setResumeNote] = useState("");
  const [caps, setCaps] = useState<ActionCapabilitiesView | null>(null);
  const [reloadTick, setReloadTick] = useState(0);
  const previewSeq = useRef(0);

  const environment = detectEnvironment(typeof navigator === "undefined" ? "" : navigator.userAgent);
  const width = usePageWidth();
  const columns = publicVisitorColumns(width);

  // 进门:内容 + 能力路线(3535)并行读。
  useEffect(() => {
    if (!actionId) return;
    let alive = true;
    setContentState("loading");
    startOrResumeSession(actionId).then(async result => {
      if (!alive) return;
      const restored = "session" in result ? result.session : null;
      if (restored) {setSession(restored);setSessionNote("");} else setSessionNote("原进度暂未接上。可以先看内容，或使用下方恢复码继续。");
      return getContent(actionId, restored?.action_session_id);
    }).then((res) => {
      if (!res) return;
      if (!alive) return;
      setContent(res.data);
      setContentState(res.state);
    });
    fetch(`/api/public/actions/${encodeURIComponent(actionId)}/capabilities?environment=${encodeURIComponent(environment)}`)
      .then(async (res) => {
        if (!alive) return;
        const data = res.ok ? await res.json().catch(() => null) : null;
        if (alive) setCaps(data ? parseActionCapabilities(data) : null);
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [actionId, environment, reloadTick]);

  // 会话版本变化（换选或合法恢复）后，沿原会话读取完整冻结内容。
  useEffect(()=>{
    if(!session?.action_session_id)return;
    let alive=true;
    getContent(actionId,session.action_session_id).then(result=>{if(alive){setContent(result.data);setContentState(result.state);}});
    return()=>{alive=false;};
  },[actionId,session?.action_session_id,session?.selected_content_pack_version]);

  // 会话实时位轮询(返回平台后重进本页即恢复;resume_available 是权威实时位)。
  useEffect(() => {
    const id = session?.action_session_id;
    if (!id) return;
    let alive = true;
    const poll = () => {
      sessionStatus(id).then((res) => {
        if (!alive) return;
        if ("session" in res && res.session) setSession(res.session);
      });
    };
    const timer = window.setInterval(poll, 30000);
    return () => {
      alive = false;
      window.clearInterval(timer);
    };
  }, [session?.action_session_id]);

  const selectedVersion = session?.selected_content_pack_version || content?.selected_content_pack_version || "";

  // 预览:按当前版本索取受控媒体凭据(purpose=preview;零收费)。
  useEffect(() => {
    if (contentState !== "ready" || !selectedVersion) return;
    const ids = (content?.media ?? [])
      .filter((m) => m.availability === "ready")
      .map((m) => m.media_id);
    if (ids.length === 0) return;
    const seq = ++previewSeq.current;
    let alive = true;
    mintMediaAccess(selectedVersion, {
      actionSessionId: session?.action_session_id ?? "",
      mediaIds: ids,
      purpose: "preview",
    }).then((res) => {
      if (!alive || seq !== previewSeq.current) return;
      if ("items" in res) {
        const byId: Record<string, MediaAccessItem> = {};
        for (const item of res.items) byId[item.media_id] = item;
        setPreviewUrls(byId);
        setPreviewNote("");
        return;
      }
      if (res.error === "undistributable") setPreviewNote("素材暂不可分发(门店已撤下或授权到期)。可以先看文字说明,或请店员协助。");
      else if (res.error === "session_mismatch") setPreviewNote("这个进度和当前内容对不上。请重新进入本页,或请店员协助。");
      else if (res.error !== "bad_purpose") setPreviewNote("素材暂时读不到。稍后重试,或请店员协助。");
    });
    return () => {
      alive = false;
    };
  }, [content, contentState, selectedVersion, session?.action_session_id]);


  const video = (content?.media ?? []).find((m) => m.role === "video");
  const videoUrl = video && video.availability === "ready" ? previewUrls[video.media_id]?.url : undefined;

  // 换一个:只在已采用候选池(3553 candidates);只改未交接草稿。
  const handleSwitch = useCallback(
    async (versionId: string) => {
      const id = session?.action_session_id;
      if (!id) {
        setSwitchNote("进度还没有接上,暂时不能换选。稍后重试,或直接用当前内容。");
        return;
      }
      setSwitchNote("");
      const res = await switchSessionContent(id, versionId);
      if ("session" in res && res.session) {
        setSession(res.session);
        setSwitchNote("已换到门店选定的另一条内容。");
        return;
      }
      if ("error" in res && res.error === "frozen") {
        setSwitchNote("这一条已经开始交接,版本保持不变,不能换选。");
      } else if ("error" in res && res.error === "stale_version") {
        setSwitchNote("门店内容刚更新过。刷新后再试。");
      } else {
        setSwitchNote("换选没有完成。当前内容不受影响,可以稍后再试。");
      }
    },
    [session],
  );

  // 跨浏览器恢复:粘贴另一台浏览器取出的恢复码(POST 体;单用途)。
  const exchangeResume = useCallback(async () => {
    const token = resumeInput.trim();
    if (token === "") return;
    setResumeNote("");
    const res = await exchangeResumeCredential(token);
    if ("session" in res && res.session) {
      setSession(res.session);
      setResumeInput("");
      setResumeNote("已接上原来的进度。");
      return;
    }
    const map: Record<string, string> = {
      resume_expired: "这个恢复码已过期。请在本来的浏览器重新取出一个,或请店员协助。",
      resume_used: "这个恢复码已经用过一次。请用本来的浏览器继续,或请店员协助。",
      target_mismatch: "这个恢复码不适用于这里。请核对入口,或请店员协助。",
      not_found: "恢复码无法识别。请核对后重试。",
    };
    setResumeNote(
      "error" in res && res.error !== "network"
        ? map[res.error] ?? "恢复没有完成。请稍后再试,或请店员协助。"
        : "网络没有给出明确结果。请稍后再试。",
    );
  }, [resumeInput]);

  const issueResume = useCallback(async () => {
    const id = session?.action_session_id;
    if (!id) {
      setResumeNote("进度还没有接上,暂时取不出恢复码。");
      return;
    }
    const token = await issueCustomerResume(id);
    if (token?.token) {
      setResumeToken(token.token);
      setResumeNote("恢复码已取出,只显示这一次。请复制到另一台设备粘贴使用;它会把原进度交给那台设备，旧浏览器请勿重复办理。");
    } else {
      setResumeNote("恢复码暂时取不出来。可以直接在新设备重进本页(同一浏览器会自动恢复)。");
    }
  }, [session]);

  const retry = useCallback(() => {
    setReloadTick((t) => t + 1);
  }, []);

  const routePick = caps ? pickRoute(caps.routes, environment, "media") : null;

  return (
    <main
      data-testid="content-view"
      data-pattern="public_consumer"
      data-columns={columns}
      data-viewport={width}
      data-state={contentState}
      className="tk-public"
    >
      <div className="tk-public-card">
        <header>
          <p className="tk-public-merchant">门店内容</p>
          <h1 className="tk-public-title">{content?.title || "内容预览"}</h1>
          {content?.store_name && <p className="tk-note">来自 {content.store_name}</p>}
        </header>
        <p className="tk-note">发布用的是你自己的账号。这里不会替你发布,每一步都可以取消。</p>

        {contentState === "loading" && <p className="tk-note">正在读取…</p>}
        {contentState === "not_found" && (
          <div data-testid="content-pending">
            <p className="tk-note">门店还没准备好在线内容。可以先到店咨询,或稍后再来。</p>
            <button type="button" className="tk-quiet" onClick={retry}>重试</button>
          </div>
        )}
        {contentState === "undistributable" && (
          <div data-testid="content-blocked">
            <p className="tk-warn">这份素材暂不可分发(门店已撤下或授权到期)。请以门店现场说明为准。</p>
            <button type="button" className="tk-quiet" onClick={retry}>重试</button>
          </div>
        )}
        {contentState === "network_error" && (
          <div>
            <p className="tk-note">网络异常,内容加载失败。请检查网络后重试。</p>
            <button type="button" className="tk-quiet" onClick={retry}>重试</button>
          </div>
        )}

        {contentState === "ready" && content && (
          <>
            {session?.resume_available === true && (
              <p className="tk-note" data-testid="resume-restored">本次内容与进度已保存，离开后可以继续。</p>
            )}
            {sessionNote && <p className="tk-note" data-testid="session-note">{sessionNote}</p>}

            <section className="tk-section" data-testid="content-preview">
              {video && video.availability === "missing" && (
                <p className="tk-warn">视频素材还没有准备好。可以先看文字说明,或请店员协助。</p>
              )}
              {videoUrl ? (
                // 受控凭据 URL:短 TTL、绑定版本+用途,服务端逐层校验。
                <video controls controlsList="nodownload" preload="metadata" src={videoUrl} data-testid="content-video" className="tk-media" />
              ) : null}
              {(content.media ?? [])
                .filter((m) => (m.role === "image" || m.role === "cover") && m.availability === "ready" && previewUrls[m.media_id]?.url)
                .map((m) => (
                  // eslint-disable-next-line @next/next/no-img-element
                  <img
                    key={m.media_id}
                    src={previewUrls[m.media_id].url}
                    alt={content.title || (m.role === "cover" ? "内容封面" : `配图 ${m.order + 1}`)}
                    data-testid={m.role === "cover" ? "content-cover" : `content-image-${m.order}`}
                    className="tk-media"
                  />
                ))}
              <div data-testid="content-copy">
                <p className="tk-public-value">{content.body || "文案在交接时一并给出。"}</p>
                {(content.topics ?? []).length > 0 && <p className="tk-note">话题:{content.topics.join(" ")}</p>}
              </div>
              {content.price_note && <p className="tk-note">价格说明:{content.price_note}</p>}
              {content.valid_note && <p className="tk-note">有效期说明:{content.valid_note}</p>}
              {content.marking?.ai_generated === true && (
                <p className="tk-unknown" data-testid="content-ai-marking">含 AI 合成画面或配音。这是内容标识,不是发布成功,也不改变奖励条件。</p>
              )}
              {content.marking?.ad_required === true && (
                <p className="tk-note" data-testid="content-ad-marking">按平台要求,这条内容属于广告或推广信息。</p>
              )}
              {previewNote && <p className="tk-note" data-testid="preview-note">{previewNote}</p>}
            </section>

            {content.candidates.length > 0 && (
              <section className="tk-section" data-testid="content-candidates">
                <h2 className="tk-section-title">换一个(门店已采用的内容)</h2>
                <p className="tk-note">换选只影响还没交接的内容;已经交接的版本保持不变。这一步不产生任何费用。</p>
                <div className="tk-row">
                  {content.candidates.map((candidate) => (
                    <button
                      key={candidate.version_id}
                      type="button"
                      className={candidate.version_id === selectedVersion ? "tk-primary" : "tk-quiet"}
                      data-testid={`content-candidate-${candidate.version_id}`}
                      onClick={() => handleSwitch(candidate.version_id)}
                    >
                      {candidate.title || "换一个"}
                    </button>
                  ))}
                </div>
                {switchNote && <p className="tk-note" data-testid="switch-note">{switchNote}</p>}
              </section>
            )}

            {session?.action_session_id ? <ActionPublishFlow actionId={actionId} sessionId={session.action_session_id} actionRevision={session.action_revision ?? content.action_revision} versionId={selectedVersion} method={routePick?.kind === "ok" ? routePick.route?.method ?? "" : ""} environment={environment} storeCode={storeCode} /> : <section className="tk-section" data-testid="content-handoff"><h2 className="tk-section-title">发布交接</h2><p className="tk-warn">{routePick?.face || "当前路线尚未确认，暂不能开始交接。请刷新原进度或请门店协助。"}</p></section>}


          </>
        )}
            <section className="tk-section" data-testid="content-resume">
              <h2 className="tk-section-title">离开与恢复</h2>
              <p className="tk-note">
                关闭页面后再打开,同一台设备会自动接上原进度;换浏览器或换设备,可以用下面的恢复码接上一次。
              </p>
              <div className="tk-row tk-gap">
                <button type="button" className="tk-quiet" data-testid="resume-issue" onClick={issueResume}>取出恢复码</button>
                <a className="tk-quiet" href={`/s/${encodeURIComponent(storeCode)}`} data-testid="back-to-store">返回门店</a>
              </div>
              {resumeToken && (
                <p className="tk-note" data-testid="resume-token">
                  恢复码(只显示这一次,请复制保存):
                  {" "}
                  <code>{resumeToken}</code>
                </p>
              )}
              <label className="tk-label">
                在其他浏览器取过恢复码?粘贴到这里继续
                <input
                  className="tk-input"
                  value={resumeInput}
                  onChange={(e: ChangeEvent<HTMLInputElement>) => setResumeInput(e.target.value)}
                  placeholder="粘贴恢复码"
                  autoComplete="off"
                />
              </label>
              <button type="button" className="tk-quiet" data-testid="resume-exchange" disabled={resumeInput.trim() === ""} onClick={exchangeResume}>
                接上原进度
              </button>
              {resumeNote && <p className="tk-note" data-testid="resume-note">{resumeNote}</p>}
            </section>
      </div>
    </main>
  );
}

function usePageWidth(): number {
  const [width, setWidth] = useState(390);
  useEffect(() => {
    const apply = () => setWidth(window.innerWidth || 390);
    apply();
    window.addEventListener("resize", apply);
    return () => window.removeEventListener("resize", apply);
  }, []);
  return width;
}
